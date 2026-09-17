package product_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/product"
)

// enumerationStep is one poll's scripted answer: the attached transports the
// adapter enumeration reports, or the failure it returns instead of a view.
type enumerationStep struct {
	devices []discovery.RuntimeDevice
	err     error
}

func view(devices ...discovery.RuntimeDevice) enumerationStep {
	return enumerationStep{devices: devices}
}

func failedPoll(err error) enumerationStep {
	return enumerationStep{err: err}
}

func transport(serial, transportID string) discovery.RuntimeDevice {
	return discovery.RuntimeDevice{Serial: serial, TransportID: transportID}
}

// fakeTransportEnumerator is the adapter enumeration seam under the watcher's
// control. It serves the view each poll is scripted to see, repeats the last
// scripted answer once the script is exhausted, and records what each poll was
// bounded by. It spawns no process, opens no socket, and reaches no device.
type fakeTransportEnumerator struct {
	mu    sync.Mutex
	steps []enumerationStep
	last  enumerationStep
	calls int

	block bool

	boundedCalls int
	sawDeadline  bool
	deadlineAt   time.Time
}

func (e *fakeTransportEnumerator) script(steps ...enumerationStep) *fakeTransportEnumerator {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps = append(e.steps, steps...)
	return e
}

// blockPolls makes every poll wait for its bound to expire instead of
// answering, which is how a poll that stops answering is exercised.
func (e *fakeTransportEnumerator) blockPolls() *fakeTransportEnumerator {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.block = true
	return e
}

func (e *fakeTransportEnumerator) Enumerate(ctx context.Context) ([]discovery.RuntimeDevice, error) {
	e.mu.Lock()
	step := e.last
	if len(e.steps) > 0 {
		step, e.steps = e.steps[0], e.steps[1:]
		e.last = step
	}
	e.calls++
	block := e.block
	if deadline, ok := ctx.Deadline(); ok {
		e.boundedCalls++
		if !e.sawDeadline {
			// The first poll's bound is the one asserted: later polls carry
			// their own, so a bound that outlives its poll would otherwise be
			// overwritten before it could be checked.
			e.sawDeadline, e.deadlineAt = true, deadline
		}
	}
	e.mu.Unlock()

	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	return append([]discovery.RuntimeDevice(nil), step.devices...), nil
}

func (e *fakeTransportEnumerator) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *fakeTransportEnumerator) bound() (bool, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sawDeadline, e.deadlineAt
}

// bounded reports how many polls carried a bound, so "every poll is bounded"
// can be asserted rather than assumed from one sample.
func (e *fakeTransportEnumerator) bounded() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.boundedCalls
}

// logCapture stands in for the watcher's log seam so a change or a failure can
// be asserted as reported rather than merely counted.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

func (l *logCapture) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// watcherTestNow is the fixed clock the watcher stamps an observation with, so a
// reported change can be asserted down to its timestamp.
var watcherTestNow = time.Date(2026, time.September, 17, 10, 30, 0, 0, time.UTC)

func newTestWatcher(t *testing.T, enumerator discovery.RuntimeEnumerator, cfg product.TransportWatcherConfig) (*product.TransportWatcher, *logCapture) {
	t.Helper()
	logs := &logCapture{}
	cfg.Enumerator = enumerator
	cfg.Logf = logs.logf
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return watcherTestNow }
	}
	return product.NewTransportWatcher(cfg), logs
}

// startWatcher runs the watcher on its own shutdown context and returns a stop
// function that cancels it and yields the outcome it reported.
func startWatcher(t *testing.T, watcher *product.TransportWatcher) func() product.TransportWatchOutcome {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan product.TransportWatchOutcome, 1)
	go func() { done <- watcher.Run(ctx) }()

	var (
		stopped bool
		outcome product.TransportWatchOutcome
	)
	stop := func() product.TransportWatchOutcome {
		t.Helper()
		cancel()
		if stopped {
			return outcome
		}
		select {
		case outcome = <-done:
			stopped = true
			return outcome
		case <-time.After(5 * time.Second):
			t.Fatal("watcher did not stop when its shutdown context was cancelled")
			return product.TransportWatchOutcome{}
		}
	}
	t.Cleanup(func() { stop() })
	return stop
}

// waitFor blocks until cond holds, and fails rather than hanging when the
// watcher never gets there.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTransportWatcherReportsArrivalsAfterLaunch(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		view(transport("SERIAL-1", "1"), transport("SERIAL-2", "2")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("transport arrived") }, "the arrival to be reported")
	outcome := stop()

	if outcome.State != product.TransportWatchStopped {
		t.Fatalf("outcome state = %q (err = %v), want stopped", outcome.State, outcome.Err)
	}
	if outcome.Arrivals != 1 {
		t.Fatalf("arrivals = %d, want 1", outcome.Arrivals)
	}
	if outcome.Departures != 0 {
		t.Fatalf("departures = %d, want 0", outcome.Departures)
	}
	if outcome.PollErrors != 0 {
		t.Fatalf("poll errors = %d, want 0", outcome.PollErrors)
	}
	if !logs.contains("transport arrived: serial=SERIAL-2 transport_id=2") {
		t.Fatalf("arrival was not reported with its transport identity:\n%s", logs.joined())
	}
	if !logs.contains("at " + watcherTestNow.Format(time.RFC3339)) {
		t.Fatalf("reported change carried no observation time:\n%s", logs.joined())
	}
	if report := outcome.Report(); !strings.Contains(report, "1 arrival(s)") {
		t.Fatalf("report = %q, want the arrival count", report)
	}
	if outcome.Attached != 2 {
		t.Fatalf("attached = %d, want 2 transports at the last poll", outcome.Attached)
	}
	if report := outcome.Report(); !strings.Contains(report, "2 transport(s) attached at the last poll") {
		t.Fatalf("report = %q, want what the last poll saw attached", report)
	}
}

func TestTransportWatcherFirstPollIsTheBaselineNotAnArrival(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(view(transport("SERIAL-1", "1")))
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 3 }, "the watcher to poll repeatedly")
	outcome := stop()

	if outcome.Polls < 3 {
		t.Fatalf("polls = %d, want the watcher to keep polling", outcome.Polls)
	}
	if outcome.Arrivals != 0 || outcome.Departures != 0 {
		t.Fatalf("arrivals = %d, departures = %d, want none for a device that was attached all along", outcome.Arrivals, outcome.Departures)
	}
	if logs.contains("transport arrived: serial=SERIAL-1") {
		t.Fatalf("a device attached every poll was reported as an arrival:\n%s", logs.joined())
	}
}

func TestTransportWatcherReportsDeparturesAfterLaunch(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1"), transport("SERIAL-2", "2")),
		view(transport("SERIAL-1", "1")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("transport departed") }, "the departure to be reported")
	outcome := stop()

	if outcome.Departures != 1 || outcome.Arrivals != 0 {
		t.Fatalf("arrivals = %d, departures = %d, want one departure and no arrival", outcome.Arrivals, outcome.Departures)
	}
	if !logs.contains("transport departed: serial=SERIAL-2 transport_id=2") {
		t.Fatalf("departure was not reported with its transport identity:\n%s", logs.joined())
	}
}

func TestTransportWatcherReportsOneChangePerTransportAcrossPolls(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		view(transport("SERIAL-1", "1"), transport("SERIAL-2", "2")),
	)
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 5 }, "the arrival to be re-polled")
	outcome := stop()

	if outcome.Arrivals != 1 {
		t.Fatalf("arrivals = %d, want 1 for one transport arriving once", outcome.Arrivals)
	}
}

func TestTransportWatcherKeepsOneSerialOnTwoTransportsAsTwoTransportChanges(t *testing.T) {
	// The same device answering on a second transport is a second attached
	// transport: that is the arrival this watcher reports. Keeping it ONE device
	// identity is the serial-keyed registry path's decision, not this watcher's.
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		view(transport("SERIAL-1", "1"), transport("SERIAL-1", "7")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("transport arrived: serial=SERIAL-1 transport_id=7") }, "the second transport to be reported")
	outcome := stop()

	if outcome.Arrivals != 1 || outcome.Departures != 0 {
		t.Fatalf("arrivals = %d, departures = %d, want one arrival and no departure", outcome.Arrivals, outcome.Departures)
	}
}

func TestTransportWatcherSkipsAnUnidentifiableTransport(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		view(transport("SERIAL-1", "1"), discovery.RuntimeDevice{}),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 4 }, "the second view to be polled")
	outcome := stop()

	if outcome.Arrivals != 0 || outcome.Departures != 0 {
		t.Fatalf("arrivals = %d, departures = %d, want none for a transport with nothing to identify it", outcome.Arrivals, outcome.Departures)
	}
	if logs.contains("observed transport") {
		t.Fatalf("an unidentifiable transport was reported as a change:\n%s", logs.joined())
	}
}

func TestTransportWatcherRecordsAPollFailureAndKeepsWatching(t *testing.T) {
	// A poll that fails says nothing about what is attached. The watcher records
	// the failure, keeps the view it already had, and keeps polling.
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		failedPoll(errors.New("enumeration unreachable")),
		view(transport("SERIAL-1", "1")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("poll failed") }, "the poll failure to be reported")
	waitFor(t, func() bool { return enumerator.callCount() >= 4 }, "the watcher to keep polling after a failed poll")
	outcome := stop()

	if outcome.PollErrors < 1 {
		t.Fatalf("poll errors = %d, want the failed poll recorded", outcome.PollErrors)
	}
	if outcome.LastError == nil {
		t.Fatal("last error = nil, want the recorded poll failure")
	}
	if outcome.Arrivals != 0 || outcome.Departures != 0 {
		t.Fatalf("arrivals = %d, departures = %d, want none: a failed poll is not a fleet-wide departure", outcome.Arrivals, outcome.Departures)
	}
	if report := outcome.Report(); !strings.Contains(report, "failed poll(s)") || !strings.Contains(report, "enumeration unreachable") {
		t.Fatalf("report = %q, want the failure count and the failure itself", report)
	}
}

func TestTransportWatcherBoundsEachPoll(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(view(transport("SERIAL-1", "1"))).blockPolls()
	timeout := 150 * time.Millisecond
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{
		Interval:    5 * time.Millisecond,
		PollTimeout: timeout,
	})
	before := time.Now()

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("poll failed") }, "the blocked poll to hit its bound")
	waitFor(t, func() bool { return enumerator.callCount() >= 3 }, "the watcher to keep polling past a bounded poll")
	outcome := stop()

	sawDeadline, deadlineAt := enumerator.bound()
	if !sawDeadline {
		t.Fatal("poll context carried no deadline; polling work must be bounded")
	}
	if deadlineAt.After(before.Add(timeout + 100*time.Millisecond)) {
		t.Fatalf("poll deadline = %s, want no later than %s", deadlineAt, before.Add(timeout))
	}
	if boundedCalls := enumerator.bounded(); boundedCalls != enumerator.callCount() {
		t.Fatalf("bounded polls = %d of %d calls, want every poll bounded", boundedCalls, enumerator.callCount())
	}
	if outcome.PollErrors < 2 || outcome.LastError == nil {
		t.Fatalf("outcome = %#v, want each bounded-out poll recorded rather than swallowed", outcome)
	}
	if !logs.contains("poll failed") {
		t.Fatal("a poll that exceeded its bound was not reported")
	}
}

func TestTransportWatcherStopsWithProcessShutdownAndReportsWhatItSaw(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(
		view(transport("SERIAL-1", "1")),
		view(transport("SERIAL-1", "1"), transport("SERIAL-2", "2")),
	)
	watcher, logs := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return logs.contains("transport arrived") }, "the arrival to be reported")
	outcome := stop()

	if outcome.State != product.TransportWatchStopped {
		t.Fatalf("outcome state = %q, want stopped", outcome.State)
	}
	if outcome.Err == nil {
		t.Fatal("outcome error = nil, want the cancellation that ended the watch")
	}
	if outcome.Polls == 0 {
		t.Fatal("outcome polls = 0, want the polls the watch performed before shutdown")
	}
	if report := outcome.Report(); !strings.Contains(report, "discovery watcher stopped") {
		t.Fatalf("report = %q, want a stop record", report)
	}
}

func TestTransportWatcherDoesNotPollWhenShutdownContextIsAlreadyCancelled(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(view(transport("SERIAL-1", "1")))
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := watcher.Run(ctx)

	if outcome.State != product.TransportWatchStopped || outcome.Err == nil {
		t.Fatalf("outcome = %#v, want a stop record carrying the cancellation", outcome)
	}
	if enumerator.callCount() != 0 {
		t.Fatalf("enumeration calls = %d, want 0: shutdown began before the first poll", enumerator.callCount())
	}
	if outcome.Polls != 0 || outcome.Attached != 0 {
		t.Fatalf("outcome = %#v, want no poll and nothing attached", outcome)
	}
	if report := outcome.Report(); strings.Contains(report, "attached at the last poll") {
		t.Fatalf("report = %q, want no claim about what was attached when nothing was polled", report)
	}
}

func TestTransportWatcherRunsAtMostOneWatchPerWatcher(t *testing.T) {
	enumerator := (&fakeTransportEnumerator{}).script(view(transport("SERIAL-1", "1")))
	watcher, _ := newTestWatcher(t, enumerator, product.TransportWatcherConfig{Interval: 5 * time.Millisecond})

	stop := startWatcher(t, watcher)
	waitFor(t, func() bool { return enumerator.callCount() >= 2 }, "the watcher to poll")
	first := stop()
	calls := enumerator.callCount()

	second := watcher.Run(context.Background())

	if enumerator.callCount() != calls {
		t.Fatalf("enumeration calls = %d after a second Run, want %d: one watcher polls once", enumerator.callCount(), calls)
	}
	if second.State != first.State || second.Polls != first.Polls {
		t.Fatalf("second outcome = %#v, want the first outcome %#v", second, first)
	}
}

func TestTransportWatcherWithoutAnEnumerationFailsClosed(t *testing.T) {
	watcher := product.NewTransportWatcher(product.TransportWatcherConfig{})

	outcome := watcher.Run(context.Background())

	if outcome.State != product.TransportWatchFailed {
		t.Fatalf("outcome state = %q, want failed", outcome.State)
	}
	if outcome.Err == nil {
		t.Fatal("outcome error = nil, want the reason the watcher cannot poll")
	}
	if outcome.Polls != 0 {
		t.Fatalf("polls = %d, want 0", outcome.Polls)
	}
	if report := outcome.Report(); !strings.Contains(report, "discovery watcher failed") {
		t.Fatalf("report = %q, want the failure recorded rather than swallowed", report)
	}

	var unconfigured *product.TransportWatcher
	if outcome := unconfigured.Run(context.Background()); outcome.State != product.TransportWatchFailed {
		t.Fatalf("nil watcher outcome = %#v, want failed", outcome)
	}
}
