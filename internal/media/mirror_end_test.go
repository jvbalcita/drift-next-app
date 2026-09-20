package media

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Every way a session ends, and what each one has to say for itself.
//
// This file is the answer to the defect it was written for: the engine recorded a
// sentence and no CLASS, and the surface derived FAILED from the mere presence of
// a sentence - so five very different ends reached an operator as one word, and an
// operator who simply closed the frame was told their own departure had failed.
// Each test below pins one end path's class, and the sentence beside it, because a
// class without words is as undiagnosable as words without a class.

// endedSession runs a session to its end and returns the session the test drove.
func endedSession(t *testing.T, dialer *fakeDialer, config MirrorEngineConfig, deviceID string) MirrorSession {
	t.Helper()
	engine := newEngine(t, dialer, config)
	session, _, err := engine.Start(context.Background(), deviceID, "SERIAL-"+deviceID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("the session for %s never ended", deviceID)
	}
	return session
}

// assertEnded asserts the whole of what an end path owes a reader: a class the
// plane states, the answer to whether it was a failure, and a sentence that names
// it rather than only reporting that something stopped.
func assertEnded(t *testing.T, session MirrorSession, want MirrorEndClass, wantFailure bool, sentence string) {
	t.Helper()
	class := session.EndClass()
	if !class.Valid() {
		t.Fatalf("the session ended with class %q, which is not one the plane states", class)
	}
	if class != want {
		t.Fatalf("the session ended as %q, want %q", class, want)
	}
	if got := class.Failed(); got != wantFailure {
		t.Fatalf("class %q reports Failed=%v, want %v", class, got, wantFailure)
	}
	failure := session.Fails()
	if failure == nil {
		t.Fatalf("the session ended as %q and reported no reason at all", class)
	}
	if strings.TrimSpace(failure.Error()) == "" {
		t.Fatalf("the session ended as %q and reported an empty sentence", class)
	}
	if sentence != "" && !strings.Contains(failure.Error(), sentence) {
		t.Fatalf("the session ended as %q with %q, which does not carry %q", class, failure.Error(), sentence)
	}
	// A class with no words of its own is a class a surface holding only the
	// class cannot render, and the wire contract promises a sentence for every
	// stream that is not live.
	if class.Sentence() == "" {
		t.Fatalf("class %q has no sentence of its own, so a surface holding only the class can say nothing", class)
	}
}

// TestTheDeviceStreamEndingIsClassifiedWithTheTransportsOwnError: the device's
// stream ending is the most common end there is, and the transport's error is the
// whole diagnosis. The class says which end it was; the sentence carries what the
// transport said.
func TestTheDeviceStreamEndingIsClassifiedWithTheTransportsOwnError(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	stream.fail(errors.New("fake: the device went away"))

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end when its stream ended")
	}
	assertEnded(t, session, MirrorEndDeviceStreamEnded, true, "the device went away")
}

// TestAStreamWhoseDeviceServerDiedNamesThatFact: the device-side server dying has
// no symptom of its own - the sockets simply stop carrying - so a reader told only
// "the stream ended" is left with the one thing nothing named. The class is stated
// at the adapter, where the device's own process is, and it survives every hop to
// the engine's record.
func TestAStreamWhoseDeviceServerDiedNamesThatFact(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	stream.fail(ClassifyMirrorEnd(errors.New("scrcpy: the device-side server exited"), MirrorEndDeviceServerFailed))

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end when the device-side server died")
	}
	assertEnded(t, session, MirrorEndDeviceServerFailed, true, "the device-side server exited")
	if class := session.EndClass(); class == MirrorEndDeviceStreamEnded {
		t.Fatal("a dead device-side server was recorded as a device stream ending, which is the fact the class exists to state")
	}
}

// TestTheAdapterAndTunnelFailingToBeEstablishedIsItsOwnClass: there was never a
// stream, because there was never a path to one. It is told apart from a device
// that streamed and stopped, and it is told apart from the device-side server,
// because the three have three different fixes.
func TestTheAdapterAndTunnelFailingToBeEstablishedIsItsOwnClass(t *testing.T) {
	dialer := newFakeDialer()
	dialer.err = errors.New("mirror: the reverse tunnel could not be opened")
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		// The dial can fail before the viewer attaches, in which case Start
		// itself reports it. Either way the class is stated on what a reader
		// gets, so the test accepts both and asserts the class below.
		if class, ok := MirrorEndClassOf(err); !ok || class != MirrorEndTransportUnavailable {
			t.Fatalf("the refusal was reported as %v with no transport class on it", err)
		}
		return
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a device that could not be dialed produced a session that never ended")
	}
	assertEnded(t, session, MirrorEndTransportUnavailable, true, "the reverse tunnel could not be opened")
}

// TestADeviceServerThatWillNotStartIsClassifiedAtTheAdapter: the device-side
// server failing to start is the other half of the same class, and it is stated
// where the launch happened rather than guessed at from a generic failure.
func TestADeviceServerThatWillNotStartIsClassifiedAtTheAdapter(t *testing.T) {
	dialer := newFakeDialer()
	dialer.err = ClassifyMirrorEnd(errors.New("scrcpy: the device-side server could not be started"), MirrorEndDeviceServerFailed)
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		if class, ok := MirrorEndClassOf(err); !ok || class != MirrorEndDeviceServerFailed {
			t.Fatalf("the refusal was reported as %v with no device-server class on it", err)
		}
		return
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a device whose server would not start produced a session that never ended")
	}
	assertEnded(t, session, MirrorEndDeviceServerFailed, true, "the device-side server could not be started")
}

// TestADeviceReportingNoStreamableScreenIsItsOwnClass: a screen this plane cannot
// stream is not a stream that ended, because none ever started, and it is not the
// transport either: the device answered, and what it answered was unusable.
func TestADeviceReportingNoStreamableScreenIsItsOwnClass(t *testing.T) {
	dialer := newFakeDialer()
	// The size is the first thing the worker reads, so it has to be there before
	// the dial rather than injected after it.
	dialer.sizeErr = errors.New("mirror: device-1 reported no streamable screen size")
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a device that reported no streamable screen produced a session that never ended")
	}
	assertEnded(t, session, MirrorEndNoStreamableScreen, true, "no streamable screen")
}

// TestAnIdleEndIsAnEndingAndNotAFailure is the card's second requirement, stated
// as the engine must state it: the last viewer detaching is a stream that ENDED.
// An operator who closed the frame is told the capture stopped, not that something
// went wrong - and the class is what says so, because the sentence alone says
// neither.
func TestAnIdleEndIsAnEndingAndNotAFailure(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: 10 * time.Millisecond})
	session, viewer, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)

	viewer.Close()

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a device nobody is watching was still being mirrored")
	}
	assertEnded(t, session, MirrorEndViewerDetached, false, "no viewer is watching")
	if got := session.EndClass().Failed(); got {
		t.Fatal("the last viewer detaching was reported as a failure, which tells an operator their own departure went wrong")
	}
	waitFor(t, "the unwatched device's stream to be closed", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
}

// TestTheEngineBeingStoppedIsItsOwnClass: the process's shutdown is not a device
// failure and must not read as one. It is the class that lets a later reader tell
// "the plane went away" from "the device went away".
func TestTheEngineBeingStoppedIsItsOwnClass(t *testing.T) {
	dialer := newFakeDialer()
	engine, err := NewMirrorEngine(MirrorEngineConfig{Dialer: dialer})
	if err != nil {
		t.Fatalf("NewMirrorEngine: %v", err)
	}
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, session)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stopErr := engine.Stop(stopCtx); stopErr != nil {
		t.Fatalf("Stop: %v", stopErr)
	}
	assertEnded(t, session, MirrorEndEngineStopped, true, "the mirror engine was stopped")
}

// TestTheEnginesOwnRecordStatesHowItsCapturesEnded: an end nobody was watching
// ends no request and reports to no surface, so the engine's own numbers are the
// only place its class can be read from. The audit therefore carries the classes,
// not only the count of ends.
func TestTheEnginesOwnRecordStatesHowItsCapturesEnded(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: 10 * time.Millisecond})

	// One session that ends because nobody is watching it.
	session, viewer, err := engine.Start(context.Background(), "device-idle", "SERIAL-idle")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionReady(t, session)
	viewer.Close()
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the unwatched session never ended")
	}

	// One session whose device went away.
	failing, _, err := engine.Start(context.Background(), "device-gone", "SERIAL-gone")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	gone := dialer.streamFor(t, "device-gone")
	sessionReady(t, failing)
	gone.fail(errors.New("fake: the device went away"))
	select {
	case <-failing.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the failing session never ended")
	}

	audit := engine.Audit()
	if audit.SessionsEnded != 2 {
		t.Fatalf("the audit reports %d session(s) ended, want 2", audit.SessionsEnded)
	}
	counts := map[MirrorEndClass]int{}
	for _, end := range audit.EndsByClass {
		counts[end.Class] = end.Count
	}
	if counts[MirrorEndViewerDetached] != 1 {
		t.Fatalf("the audit reports %d idle end(s), want 1: %+v", counts[MirrorEndViewerDetached], audit.EndsByClass)
	}
	if counts[MirrorEndDeviceStreamEnded] != 1 {
		t.Fatalf("the audit reports %d device stream end(s), want 1: %+v", counts[MirrorEndDeviceStreamEnded], audit.EndsByClass)
	}
	if audit.EndedWithoutAFailure() {
		t.Fatal("the audit claims every capture ended without a failure, and one of them was the device going away")
	}
	if !strings.Contains(audit.Report(), string(MirrorEndViewerDetached)) {
		t.Fatalf("the audit's own line does not state how its captures ended: %s", audit.Report())
	}
}

// TestAnUnclassifiedClassIsNeverRenderedAsAFailure: the vocabulary is closed, and
// a class it does not name is refused rather than attached. The zero class is not a
// failure either - a session that has not ended has nothing to be one.
func TestAnUnclassifiedClassIsNeverRenderedAsAFailure(t *testing.T) {
	if got := MirrorEndClass("something-nobody-defined").Failed(); !got {
		t.Fatal("an unnamed class does not read as a failure, so an end path nobody taught the vocabulary would render as an ordinary end")
	}
	if MirrorEndClass("something-nobody-defined").Valid() {
		t.Fatal("the vocabulary accepts a class it does not name")
	}
	if MirrorEndClass("").Failed() {
		t.Fatal("a session that has not ended reads as a failure")
	}
	if MirrorEndClass("").Sentence() != "" {
		t.Fatal("a session that has not ended states a sentence")
	}
	// ClassifyMirrorEnd refuses an unnamed class rather than attaching it, so an
	// error is never tagged with a class no surface can render.
	plain := errors.New("fake: something happened")
	if got := ClassifyMirrorEnd(plain, MirrorEndClass("nonsense")); got != plain {
		t.Fatalf("an unnamed class was attached to %v", got)
	}
	// And a class a nearer layer stated is never overwritten by a caller that
	// assumed one.
	classified := ClassifyMirrorEnd(plain, MirrorEndDeviceServerFailed)
	if got := ClassifyMirrorEnd(classified, MirrorEndTransportUnavailable); got != classified {
		t.Fatal("a class stated at the adapter was replaced by a class the caller assumed")
	}
	if class, ok := MirrorEndClassOf(classified); !ok || class != MirrorEndDeviceServerFailed {
		t.Fatalf("the class the adapter stated reads back as %q (found=%v)", class, ok)
	}
	// It survives a wrap, which is what every hop between the adapter and the
	// engine does to it.
	wrapped := ClassifyMirrorEnd(wrapErr(classified), MirrorEndTransportUnavailable)
	if class, _ := MirrorEndClassOf(wrapped); class != MirrorEndDeviceServerFailed {
		t.Fatalf("the class did not survive a wrap: %q", class)
	}
}

// wrapErr wraps an error the way every layer between the adapter and the engine
// does, so the class has to be readable through it.
func wrapErr(err error) error {
	return &wrappedError{err: err}
}

type wrappedError struct{ err error }

func (w *wrappedError) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrappedError) Unwrap() error { return w.err }
