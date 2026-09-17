package media

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests cover the composition root's half of the live mirror: the host
// that owns the engine for the life of the process. What they must prove is that
// the process cannot end while a capture is still running without saying so -
// and that a mirror which was never armed says why instead of looking like a
// mirror that is showing nothing.

// TestMirrorHostSaysWhyTheMirrorIsNotArmed pins the startup diagnosis. A
// deployment without the inputs the mirror needs must produce a reason, not an
// empty frame: an engine that exists and cannot dial is the failure an operator
// cannot tell from a black screen.
func TestMirrorHostSaysWhyTheMirrorIsNotArmed(t *testing.T) {
	reason := errors.New("the live mirror needs the host path of the scrcpy server (DRIFT_MIRROR_SCRCPY_SERVER)")

	var missing *MirrorHost
	if state := missing.State(); !strings.Contains(state, "not started") || !strings.Contains(state, "not constructed") {
		t.Fatalf("a mirror host that was never built reported %q", state)
	}
	missingOutcome := missing.Run(context.Background())
	if missingOutcome.State != MirrorNotConfigured {
		t.Fatalf("a mirror host that was never built reported state %q", missingOutcome.State)
	}
	if !strings.Contains(missingOutcome.Report(), "not started") {
		t.Fatalf("the outcome did not report the mirror as not started: %q", missingOutcome.Report())
	}

	host := NewMirrorHost(MirrorHostConfig{Reason: reason})
	if state := host.State(); !strings.Contains(state, reason.Error()) {
		t.Fatalf("the startup line %q does not carry the reason the mirror is not armed", state)
	}
	outcome := host.Run(context.Background())
	if outcome.State != MirrorNotConfigured {
		t.Fatalf("state = %q, want %q", outcome.State, MirrorNotConfigured)
	}
	if outcome.Err != nil {
		t.Fatalf("a mirror that was never armed reported a stop error: %v", outcome.Err)
	}
	if !strings.Contains(outcome.Report(), reason.Error()) {
		t.Fatalf("the outcome %q does not carry the reason", outcome.Report())
	}
}

// TestMirrorHostArmedCapturesNothingUntilAViewerSubscribes is acceptance
// criterion 2's first half at the process level: a running process with no
// viewer is not capturing any device, and its own audit says so.
func TestMirrorHostArmedCapturesNothingUntilAViewerSubscribes(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	host := NewMirrorHost(MirrorHostConfig{Engine: engine})

	state := host.State()
	if !strings.Contains(state, "armed") || !strings.Contains(state, "only while a viewer is subscribed") {
		t.Fatalf("the startup line does not say what an armed mirror does: %q", state)
	}
	if dialed := dialer.dialed(); len(dialed) != 0 {
		t.Fatalf("an armed mirror dialed %v from the startup line alone", dialed)
	}
	audit := engine.Audit()
	if audit.SessionsStarted != 0 || audit.StreamsDialed != 0 {
		t.Fatalf("an armed mirror reported work it never started: %s", audit.Report())
	}
}

// TestMirrorHostStopsTheEngineAndAuditsWhatItOwned is the whole point of the
// host: the engine is cancelled by the process's shutdown, stopped with a
// viewer's session still live, and audited - so "no capture outlived this
// process" is a number rather than an assumption.
func TestMirrorHostStopsTheEngineAndAuditsWhatItOwned(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, session)
	stream := dialer.streamFor(t, "device-1")
	defer viewer.Close()

	host := NewMirrorHost(MirrorHostConfig{Engine: engine})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes := make(chan MirrorOutcome, 1)
	go func() { outcomes <- host.Run(ctx) }()

	// The host owns the engine for the LIFE of the process: while the process is
	// running it must not stop a session an operator is watching. The wait is
	// short and it is bounded on the other side too - Run is what must return
	// when the context ends, so a host that stopped early would answer here.
	select {
	case outcome := <-outcomes:
		t.Fatalf("the host stopped the engine while the process was still running: %+v", outcome)
	case <-time.After(50 * time.Millisecond):
	}
	if len(engine.Sessions()) != 1 {
		t.Fatalf("the engine reports %d sessions while a viewer is watching, want 1", len(engine.Sessions()))
	}

	cancel()
	var outcome MirrorOutcome
	select {
	case outcome = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("the host did not return after the shutdown context ended")
	}
	if outcome.State != MirrorStopped {
		t.Fatalf("state = %q (%v), want %q", outcome.State, outcome.Err, MirrorStopped)
	}
	if !outcome.Audit.Clean() {
		t.Fatalf("the audit reported work left running: %s", outcome.Audit.Report())
	}
	if outcome.Audit.SessionsStarted != 1 || outcome.Audit.SessionsEnded != 1 ||
		outcome.Audit.StreamsDialed != 1 || outcome.Audit.StreamsClosed != 1 {
		t.Fatalf("audit = %s, want one session and one stream started and stopped", outcome.Audit.Report())
	}
	if _, closes, _ := stream.state(); closes != 1 {
		t.Fatalf("the device's stream was closed %d times, want exactly one", closes)
	}
	if len(engine.Sessions()) != 0 {
		t.Fatalf("the engine still holds %d session(s) after the host stopped it", len(engine.Sessions()))
	}
	if _, _, startErr := engine.Start(context.Background(), "device-2", "SERIAL-2"); startErr == nil {
		t.Fatal("a stopped engine started a new capture after the process's shutdown")
	}
	if !strings.Contains(outcome.Report(), "nothing of the engine's own was left running") {
		t.Fatalf("the shutdown line does not state what the audit found: %q", outcome.Report())
	}
}

// TestMirrorHostReportsASessionThatDidNotStopWithinTheBound is the bound's own
// test: a device that stopped answering must not hold the process open, and it
// must be reported by name rather than passed over as a clean shutdown.
func TestMirrorHostReportsASessionThatDidNotStopWithinTheBound(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, viewer, err := engine.Start(context.Background(), "device-stuck", "SERIAL-STUCK")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, session)
	defer viewer.Close()
	release := dialer.streamFor(t, "device-stuck").holdClose()
	defer release()

	host := NewMirrorHost(MirrorHostConfig{Engine: engine, StopTimeout: 100 * time.Millisecond, Logf: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	outcome := host.Run(ctx)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the host waited %s for a stream that will not close, and the bound was 100ms", elapsed)
	}
	if outcome.State != MirrorStoppedOutstanding {
		t.Fatalf("state = %q (%v), want %q", outcome.State, outcome.Err, MirrorStoppedOutstanding)
	}
	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "device-stuck") {
		t.Fatalf("the stop error does not name the device that did not stop: %v", outcome.Err)
	}
	if outcome.Audit.Clean() {
		t.Fatalf("the audit called the shutdown clean while a stream was still open: %s", outcome.Audit.Report())
	}
	if outcome.Audit.StreamsDialed != 1 || outcome.Audit.StreamsClosed != 0 {
		t.Fatalf("audit = %s, want one stream dialed and none closed", outcome.Audit.Report())
	}
	if report := outcome.Report(); !strings.Contains(report, "work outstanding") || !strings.Contains(report, "device-stuck") {
		t.Fatalf("the shutdown line does not report what was left behind: %q", report)
	}
}
