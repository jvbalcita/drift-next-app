package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drift.local/drift-next/internal/discovery"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// failingEnumerator is the adapter's own transport failing the same way, every
// time: the shape of the condition this card is about. Its error is the one the
// lab runtime actually returns when the adb server stops answering.
type failingEnumerator struct {
	calls atomic.Int64
	stop  func()
	limit int64
	err   error
}

func (e *failingEnumerator) Enumerate(context.Context) ([]discovery.RuntimeDevice, error) {
	if e.calls.Add(1) >= e.limit {
		e.stop()
	}
	return nil, e.err
}

// ARC-231 acceptance 3. A repeated adapter transport failure must be BOUNDED in log
// volume and surfaced as a CLASSIFIED READING rather than only counted in a log
// file.
//
// The condition is repeated 500 times - a failed poll every five seconds for about
// forty minutes, which is what this host actually did for hours. The assertions are
// the two halves of the requirement: the number of lines is logarithmic in the
// number of occurrences (so it cannot grow without bound), and what the record says
// is a reading with a classification and the one thing that fixes it, not a total.
func TestARepeatedAdapterTransportFailureIsBoundedAndClassified(t *testing.T) {
	const occurrences = 500

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	lines := make([]string, 0, 32)
	enumerator := &failingEnumerator{
		limit: occurrences,
		err:   platformerrors.New(platformerrors.CodeDeadlineExceeded, "lab adapter could not enumerate devices"),
	}
	enumerator.stop = cancel

	watcher := NewTransportWatcher(TransportWatcherConfig{
		Enumerator:  enumerator,
		Interval:    time.Millisecond,
		PollTimeout: 50 * time.Millisecond,
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
		},
	})
	outcome := watcher.Run(ctx)

	mu.Lock()
	written := append([]string(nil), lines...)
	mu.Unlock()

	if outcome.PollErrors != occurrences {
		t.Fatalf("PollErrors = %d, want %d", outcome.PollErrors, occurrences)
	}

	// BOUNDED: the lines are logarithmic in the occurrences. A condition that
	// repeats 500 times must not produce 500 lines - and the bound has to hold for
	// any count, which is why the assertion is on the growth and not on a constant.
	if len(written) > 24 {
		t.Fatalf("a condition repeated %d times produced %d log line(s); the volume must be bounded: %v",
			occurrences, len(written), written)
	}
	if len(written) < 2 {
		t.Fatalf("a repeated condition produced %d log line(s); it must be reported rather than swallowed", len(written))
	}

	// CLASSIFIED READING, not a count: the outcome carries what the condition IS,
	// how long it has repeated, and the one thing that fixes it.
	reading := outcome.Condition
	if reading.Condition != TransportConditionTimedOut {
		t.Fatalf("Condition = %q, want %q", reading.Condition, TransportConditionTimedOut)
	}
	if reading.Count != occurrences {
		t.Fatalf("Condition.Count = %d, want %d: the lines are bounded, so the number behind them must be carried", reading.Count, occurrences)
	}
	if strings.TrimSpace(reading.Remedy) == "" {
		t.Fatal("the reading names no remedy: a condition an operator cannot act on is a count with words")
	}
	if !strings.Contains(reading.Remedy, "adb kill-server") {
		t.Fatalf("the remedy does not name the one thing that fixes it: %q", reading.Remedy)
	}
	if reading.FirstSeen.IsZero() || reading.LastSeen.IsZero() || reading.LastSeen.Before(reading.FirstSeen) {
		t.Fatalf("the reading does not bound the condition: first=%v last=%v", reading.FirstSeen, reading.LastSeen)
	}

	// The closing record an operator reads carries the reading, not only the count.
	report := outcome.Report()
	if !strings.Contains(report, string(TransportConditionTimedOut)) {
		t.Fatalf("the closing record does not carry the classified condition: %s", report)
	}
	if !strings.Contains(report, "the one thing that fixes it") {
		t.Fatalf("the closing record does not carry the remedy: %s", report)
	}

	// And the line an operator sees while it is happening says the same thing.
	if !strings.Contains(written[0], string(TransportConditionTimedOut)) {
		t.Fatalf("the first line is not the classified reading: %s", written[0])
	}
	if !strings.Contains(written[0], "the one thing that fixes it") {
		t.Fatalf("the first line does not carry the remedy: %s", written[0])
	}
}

// A condition that CHANGES is a new reading and is always reported in full, even
// though the volume bound would otherwise have skipped it: a bound that hid the
// second failure mode would be worse than no bound at all.
func TestAChangedConditionIsAlwaysReported(t *testing.T) {
	var log transportConditionLog
	at := time.Unix(1_700_000_000, 0).UTC()

	timedOut, write := log.observe(at, platformerrors.New(platformerrors.CodeDeadlineExceeded, "no answer"))
	if !write || timedOut.Condition != TransportConditionTimedOut {
		t.Fatalf("first occurrence = %+v write=%v, want a timed-out reading", timedOut, write)
	}
	// The next three occurrences land on counts 2, 3 and 4: the doublings are
	// written, and the one in between is counted without a line of its own.
	written := 0
	for index := 0; index < 3; index++ {
		if _, write := log.observe(at, platformerrors.New(platformerrors.CodeDeadlineExceeded, "no answer")); write {
			written++
		}
	}
	if written != 2 {
		t.Fatalf("%d of 3 repeats were written, want exactly the 2 doublings (counts 2 and 4)", written)
	}
	if log.current().Count != 4 {
		t.Fatalf("count after 3 repeats = %d, want 4", log.current().Count)
	}
	changed, write := log.observe(at, platformerrors.New(platformerrors.CodePolicyDenied, "no permissions"))
	if !write {
		t.Fatal("a condition that CHANGED was not reported")
	}
	if changed.Condition != TransportConditionRefused {
		t.Fatalf("changed condition = %q, want %q", changed.Condition, TransportConditionRefused)
	}
	if changed.Count != 1 {
		t.Fatalf("a new condition started at count %d, want 1", changed.Count)
	}
	if !strings.Contains(changed.Remedy, "host-side rule") {
		t.Fatalf("the new reading does not name its own remedy: %q", changed.Remedy)
	}
}

// The reading is bounded in SIZE as well as in volume: a transport failure that
// arrives with a page of output must not become a page of record.
func TestAReadingIsBoundedInSize(t *testing.T) {
	var log transportConditionLog
	at := time.Unix(1_700_000_000, 0).UTC()
	reading, _ := log.observe(at, errors.New(strings.Repeat("x", 4096)))
	if len(reading.Detail) > maxConditionDetailBytes+8 {
		t.Fatalf("a reading carried %d bytes of detail, want it bounded near %d", len(reading.Detail), maxConditionDetailBytes)
	}
}

// Recovery is a fact too: when the adapter's transport answers again, the reading
// is cleared and the episode is reported once, so the record says the condition
// ended rather than leaving the last failure standing as the current state.
func TestRecoveryEndsTheReading(t *testing.T) {
	var log transportConditionLog
	at := time.Unix(1_700_000_000, 0).UTC()
	log.observe(at, platformerrors.New(platformerrors.CodeDeadlineExceeded, "no answer"))
	previous, write := log.clear()
	if !write {
		t.Fatal("recovery was not reported")
	}
	if previous.Count != 1 || previous.Condition != TransportConditionTimedOut {
		t.Fatalf("recovery reported %+v, want the episode that ended", previous)
	}
	if log.current().Failed() {
		t.Fatalf("the reading was not cleared: %+v", log.current())
	}
	if _, write := log.clear(); write {
		t.Fatal("a second recovery was reported with no condition in force")
	}
}
