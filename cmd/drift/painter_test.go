package main

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// cursorUpPattern matches a cursor-up move, which is only legitimate once the
// painter has a previous frame to walk back onto.
var cursorUpPattern = regexp.MustCompile("\x1b\\[[0-9]*A")

// recordingWriter keeps every Write call separate so tests can assert how many
// payloads reached the terminal and what each one was.
type recordingWriter struct {
	mu     sync.Mutex
	writes []string
}

func (w *recordingWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, string(payload))
	return len(payload), nil
}

func (w *recordingWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.writes)
}

func (w *recordingWriter) written() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.writes, "")
}

func (w *recordingWriter) last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.writes) == 0 {
		return ""
	}
	return w.writes[len(w.writes)-1]
}

type restoreProbe struct {
	out      *recordingWriter
	painter  *painter
	restorer *terminalRestorer
	raw      int
	cleaned  int
}

func newRestoreProbe(inPlace bool) *restoreProbe {
	probe := &restoreProbe{out: &recordingWriter{}}
	probe.painter = newPainter(probe.out, inPlace)
	probe.restorer = newTerminalRestorer(probe.out, probe.painter)
	probe.restorer.raw = func() { probe.raw++ }
	probe.restorer.cleanup = func() { probe.cleaned++ }
	return probe
}

func capturePanic(body func()) (recovered any) {
	defer func() { recovered = recover() }()
	body()
	return nil
}

func TestPainterRedrawsInPlaceInsteadOfScrolling(t *testing.T) {
	out := &recordingWriter{}
	painter := newPainter(out, true)
	if err := painter.paint([]string{"row one", "row two", "Select an action: "}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if cursorUpPattern.MatchString(out.last()) {
		t.Fatalf("the first frame moved the cursor before painting: %q", out.last())
	}
	if err := painter.paint([]string{"row ONE", "row two", "Select an action: 1"}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if out.writeCount() != 2 {
		t.Fatalf("expected two frame payloads, got %d", out.writeCount())
	}
	second := out.last()
	if !strings.HasPrefix(second, "\r\x1b[2A") {
		t.Fatalf("the second frame did not move back onto the first: %q", second)
	}
	// A repaint may only touch its own rows: one frame's worth of line breaks
	// for the erase and one for the redraw, never a fresh frame appended below.
	if newlines := strings.Count(second, "\r\n"); newlines > 2*2 {
		t.Fatalf("the second frame scrolled the terminal (%d newlines): %q", newlines, second)
	}
	if count := strings.Count(second, "row ONE"); count != 1 {
		t.Fatalf("the second frame rendered %d copies of its own row: %q", count, second)
	}
	if !strings.HasSuffix(second, eraseToEndLine) {
		t.Fatalf("the cursor did not finish at the end of the last frame line: %q", second)
	}
	if !strings.Contains(stripANSI(second), "row ONE") || !strings.Contains(stripANSI(second), "Select an action: 1") {
		t.Fatalf("the second frame did not render its own content: %q", second)
	}
}

func TestPainterErasesThePreviousFrameWhenItShrinks(t *testing.T) {
	out := &recordingWriter{}
	painter := newPainter(out, true)
	if err := painter.paint([]string{"one", "two", "three", "four"}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if err := painter.paint([]string{"one"}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	payload := out.last()
	if !strings.Contains(payload, eraseToEndLine) {
		t.Fatalf("shrinking frame did not erase the rows it abandoned: %q", payload)
	}
	if newlines := strings.Count(payload, "\r\n"); newlines != 3 {
		t.Fatalf("expected the four stale rows to be cleared, got %d newlines: %q", newlines, payload)
	}
	if !strings.HasSuffix(payload, eraseToEndLine) {
		t.Fatalf("the cursor did not finish at the end of the last frame line: %q", payload)
	}
}

func TestPainterEmitsPlainTextWhenNotInPlace(t *testing.T) {
	out := &recordingWriter{}
	painter := newPainter(out, false)
	frame := []string{"Drift Next · Local Runtime", "● Control Plane ready"}
	if err := painter.paint(frame); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if err := painter.paint(frame); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if err := painter.paint([]string{"Drift Next · Local Runtime", "○ Control Plane stopped"}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if out.writeCount() != 2 {
		t.Fatalf("an unchanged plain frame should not be re-emitted, got %d writes", out.writeCount())
	}
	if strings.ContainsRune(out.written(), 0x1b) {
		t.Fatalf("plain output contained an escape sequence: %q", out.written())
	}
	if !strings.Contains(out.written(), "○ Control Plane stopped") {
		t.Fatalf("plain output did not carry the changed frame: %q", out.written())
	}
}

func TestRestoreWritesNothingWithoutInPlaceRendering(t *testing.T) {
	probe := newRestoreProbe(false)
	if err := probe.painter.paint([]string{"plain frame"}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	before := probe.out.writeCount()
	probe.restorer.restore()
	if probe.out.writeCount() != before {
		t.Fatalf("restore wrote %q into a non-interactive output", probe.out.last())
	}
	if probe.raw != 1 || probe.cleaned != 1 {
		t.Fatalf("restore skipped cleanup: raw=%d cleaned=%d", probe.raw, probe.cleaned)
	}
}

func TestRestoreEmitsTheExpectedSequenceExactlyOnce(t *testing.T) {
	probe := newRestoreProbe(true)
	if err := probe.painter.paint([]string{"row one", "prompt: "}); err != nil {
		t.Fatalf("paint: %v", err)
	}
	before := probe.out.writeCount()
	probe.restorer.restore()
	probe.restorer.restore()
	probe.restorer.restore()
	if got := probe.out.writeCount(); got != before+1 {
		t.Fatalf("restore wrote %d payloads, want exactly one", got-before)
	}
	payload := probe.out.last()
	if !strings.Contains(payload, "\r\x1b[1A") {
		t.Fatalf("restore did not return the cursor to the top of the frame: %q", payload)
	}
	if !strings.Contains(payload, eraseBelow) {
		t.Fatalf("restore did not erase the frame region: %q", payload)
	}
	if !strings.Contains(payload, sgrReset) {
		t.Fatalf("restore did not reset terminal attributes: %q", payload)
	}
	if probe.raw != 1 {
		t.Fatalf("raw terminal state was restored %d times, want once", probe.raw)
	}
	if probe.cleaned != 1 {
		t.Fatalf("non-terminal cleanup ran %d times, want once", probe.cleaned)
	}
}

func TestGuardedRestoresTerminalOnEveryExitPath(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		probe := newRestoreProbe(true)
		if err := guarded(probe.restorer.restore, func() error { return nil }); err != nil {
			t.Fatalf("guarded returned %v, want nil", err)
		}
		if probe.out.writeCount() != 1 || probe.raw != 1 || probe.cleaned != 1 {
			t.Fatalf("normal exit did not restore exactly once: writes=%d raw=%d cleaned=%d", probe.out.writeCount(), probe.raw, probe.cleaned)
		}
	})
	t.Run("error", func(t *testing.T) {
		probe := newRestoreProbe(true)
		want := errors.New("terminal read failed")
		if err := guarded(probe.restorer.restore, func() error { return want }); !errors.Is(err, want) {
			t.Fatalf("guarded returned %v, want %v", err, want)
		}
		if probe.out.writeCount() != 1 || probe.raw != 1 || probe.cleaned != 1 {
			t.Fatalf("error exit did not restore exactly once: writes=%d raw=%d cleaned=%d", probe.out.writeCount(), probe.raw, probe.cleaned)
		}
	})
	t.Run("panic", func(t *testing.T) {
		probe := newRestoreProbe(true)
		recovered := capturePanic(func() {
			_ = guarded(probe.restorer.restore, func() error { panic("boom") })
		})
		if recovered == nil {
			t.Fatal("guarded swallowed the panic instead of re-raising it")
		}
		if probe.out.writeCount() != 1 || probe.raw != 1 || probe.cleaned != 1 {
			t.Fatalf("panic exit did not restore exactly once: writes=%d raw=%d cleaned=%d", probe.out.writeCount(), probe.raw, probe.cleaned)
		}
	})
}
