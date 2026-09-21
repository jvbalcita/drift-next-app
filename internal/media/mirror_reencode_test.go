package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ARC-260: a second viewing of one device must not cancel the device's own work,
// and a cancellation THIS PLANE performed must never be reported as the transport
// failing.
//
// Both cases are driven through the seam the engine already schedules on - the
// dialer - and the device's server push NEVER settles on its own: a fake that
// answers at once, which is every fake the identity tests used, can no more catch
// a cancellation than it can catch a hang (AGENTS.md section 10). That is why the
// two-viewing defect was invisible to the whole suite: the re-encode cancels the
// context it was given, and a port that returns immediately never sees it.
//
// The measurement this file exists for, on one lab device (192.168.1.124:5555,
// the screen awake at 1080x2280, adb reporting `device`): with a tile's viewing
// held open and the operator's own frame opened against the same device, the push
// of the server died four seconds in - `adb mirror-push-server failed
// (operator_cancelled) ... cause=adb invocation canceled: context canceled` - the
// session ended, and BOTH viewings received no picture at all. The server is
// pushed on every open, so a cancelled push is a device that never streams.

// heldDialer is a dialer whose device-side work - the push of the server, the
// reverse tunnel, the launch and the handshake, as one operation - does not settle
// until the test lets it. It records every open it was asked for and every one
// whose context was cancelled under it, which is how a case can say what a viewer
// change did to the device's own work.
type heldDialer struct {
	inner *fakeDialer
	// settle is closed by the test to let the held opens finish.
	settle chan struct{}

	mu        sync.Mutex
	asks      []MirrorViewerPurpose
	cancelled []MirrorViewerPurpose
	opened    int
}

func newHeldDialer() *heldDialer {
	return &heldDialer{inner: newFakeDialer(), settle: make(chan struct{})}
}

// Dial holds the open until it is released, and reports the cancellation if the
// caller abandons it first - which is exactly what the real adapter does: the adb
// invocation is killed, the push dies in flight, and the device is left with no
// server and no stream.
func (d *heldDialer) Dial(ctx context.Context, deviceID, serial string, purpose MirrorViewerPurpose, preview MirrorPreview) (MirrorStream, error) {
	purpose = purposeOrDefault(purpose)
	d.mu.Lock()
	d.asks = append(d.asks, purpose)
	d.mu.Unlock()
	select {
	case <-d.settle:
	case <-ctx.Done():
		d.mu.Lock()
		d.cancelled = append(d.cancelled, purpose)
		d.mu.Unlock()
		return nil, fmt.Errorf("held: the push of the server to %s was cancelled: %w", serial, ctx.Err())
	}
	stream, err := d.inner.Dial(ctx, deviceID, serial, purpose, preview)
	if err == nil {
		d.mu.Lock()
		d.opened++
		d.mu.Unlock()
	}
	return stream, err
}

// release lets every held open finish, as a device that answered does.
func (d *heldDialer) release() { close(d.settle) }

// asked reports the purposes the device was opened for, in the order it was
// asked, which is the profile each open carried.
func (d *heldDialer) asked() []MirrorViewerPurpose {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]MirrorViewerPurpose(nil), d.asks...)
}

// cancelledOpens reports the opens this dialer's caller abandoned: the device's
// own work that a cancellation reached.
func (d *heldDialer) cancelledOpens() []MirrorViewerPurpose {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]MirrorViewerPurpose(nil), d.cancelled...)
}

// streamsOpened reports how many device streams the device has produced, which is
// how a case tells a settled open from one still in flight.
func (d *heldDialer) streamsOpened() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opened
}

// streamFor reports the stream the device produced last, which is the one the
// session is reading once both opens have settled.
func (d *heldDialer) streamFor(t *testing.T) *fakeStream {
	t.Helper()
	return d.inner.streamFor(t, "device-1")
}

// heldFor reports whether a condition became true within a bounded window. It is
// how a case asserts that something did NOT happen, and the window is the whole
// point of it: a viewer change that cancelled the device's own work does so at
// once - measured, four seconds into a push - so the difference between "it did
// not happen" and "the case looked too early" is the wait.
func heldFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// TestAViewerChangeDoesNotCancelTheDeviceWorkThatOpensTheSession is the card's
// first acceptance: an open whose push never settles proves that a second viewing
// does not cancel it.
//
// It fails on the tree this was found on, and it fails the way the fleet did: the
// re-encode cancels the context the open was given, the held push returns the
// cancellation, and the session ends - `transport_unavailable` - with both
// viewings empty. What must happen instead is that the device's own work finishes,
// and the session ASKS THE DEVICE AGAIN at the profile the stronger viewer needs.
func TestAViewerChangeDoesNotCancelTheDeviceWorkThatOpensTheSession(t *testing.T) {
	dialer := newHeldDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})

	// The console's grid opens a tile of one device. Its push is in flight and
	// nothing of it has settled: this is the state the operator arrives into.
	tile, tileViewer, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeAmbient, MirrorPreview{})
	if err != nil {
		t.Fatalf("opening the tile's stream: %v", err)
	}
	waitFor(t, "the tile's open to reach the device", func() bool { return len(dialer.asked()) == 1 })

	// The operator opens the same device into the big frame. That is a stronger
	// encode profile, and it is what used to re-encode the session.
	frame, frameViewer, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("opening the operator's frame: %v", err)
	}
	_ = frame

	if heldFor(500*time.Millisecond, func() bool {
		return len(dialer.cancelledOpens()) > 0 || tile.EndClass() != ""
	}) {
		t.Fatalf("the device's own work was cancelled by a viewer change: the opens cancelled are %v and the session ended as %q (%v)",
			dialer.cancelledOpens(), tile.EndClass(), tile.Fails())
	}
	if class := tile.EndClass(); class != "" {
		t.Fatalf("the session ended (%q) while its device was still being opened: %v", class, tile.Fails())
	}

	// The device answers at last. The open that was already running is allowed to
	// finish, and the session then opens the device again at the profile the
	// operator's frame needs: the re-encode follows the device's own work instead
	// of tearing it down.
	dialer.release()
	waitFor(t, "the device to be opened again for the operator's own frame", func() bool {
		asks := dialer.asked()
		return len(asks) == 2 && asks[1] == PurposeOperator
	})
	waitFor(t, "both opens to settle", func() bool { return dialer.streamsOpened() == 2 })
	if asks := dialer.asked(); asks[0] != PurposeAmbient {
		t.Fatalf("the tile's open was made at the %s profile, want the ambient one", asks[0])
	}
	if class := tile.EndClass(); class != "" {
		t.Fatalf("the session ended (%q) while it was re-encoding for a stronger viewer: %v", class, tile.Fails())
	}

	// Both viewings are carried the re-encoded device's pictures. The tile's
	// viewing never lost its identity, and the operator's frame is served by the
	// same session: one capture of one device, at the stronger bound.
	reencoded := dialer.streamFor(t)
	reencoded.push(StreamFrame{Config: true, Data: configUnit})
	reencoded.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	if frame := next(t, tileViewer); !frame.Key {
		t.Error("the tile's viewing was carried a non-key frame from the re-encoded device")
	}
	if frame := next(t, frameViewer); !frame.Key {
		t.Error("the operator's own frame was carried a non-key frame from the re-encoded device")
	}
	if sessions := len(engine.Sessions()); sessions != 1 {
		t.Errorf("one device has %d sessions, want one capture serving both viewings", sessions)
	}
}

// TestASessionThatEndsUnderItsOwnPushNamesThePlaneAndNotTheTransport is the other
// half of the same fact, on the end path: when this plane cancels its own
// device-side work, the end it reports names the plane.
//
// A session ending while its push is in flight is the one cancellation of a push
// that remains in this engine, because a viewer change no longer makes one. What
// an operator must never be told is that the transport to the device could not be
// established: the device answered adb, the path was there, and this plane is what
// stopped.
func TestASessionThatEndsUnderItsOwnPushNamesThePlaneAndNotTheTransport(t *testing.T) {
	dialer := newHeldDialer()
	engine, err := NewMirrorEngine(MirrorEngineConfig{Dialer: dialer})
	if err != nil {
		t.Fatalf("NewMirrorEngine: %v", err)
	}
	session, _, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the open to reach the device", func() bool { return len(dialer.asked()) == 1 })

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stopErr := engine.Stop(stopCtx); stopErr != nil {
		t.Fatalf("Stop: %v", stopErr)
	}

	// The push was cancelled by this plane's own shutdown, and that is a fact the
	// dialer saw: the record of the end is still the session's own class, which
	// names the plane, and never the transport failing.
	if cancelled := dialer.cancelledOpens(); len(cancelled) != 1 {
		t.Fatalf("the plane cancelled %d open(s) while ending its session, want the one in flight: %v", len(cancelled), cancelled)
	}
	if class := session.EndClass(); class == MirrorEndTransportUnavailable {
		t.Fatalf("a push this plane cancelled was reported as %q: %v", class, session.Fails())
	}
	assertEnded(t, session, MirrorEndEngineStopped, true, "the mirror engine was stopped")
	if got := session.EndClass().Sentence(); !strings.Contains(got, "engine") {
		t.Fatalf("the end's own sentence is %q, which does not name the plane as what stopped the capture", got)
	}
}

// TestAPushThisPlaneCancelledIsItsOwnClass pins the classification itself, and it
// is stated here rather than only on an end because the class has to be right
// wherever a cancelled open is reported: the fix above means a viewer change no
// longer reaches a push at all, so what remains is the class of the failure, and a
// failure a nearer layer classified is never overwritten.
func TestAPushThisPlaneCancelledIsItsOwnClass(t *testing.T) {
	session := newMirrorSession("device-1", "SERIAL-1", PurposeAmbient, DefaultPreview(), nil)

	// The adb invocation a cancelled push dies on, as the adapter reports it.
	push := errors.New("scrcpy: pushing the server to SERIAL-1: adb mirror-push-server failed (operator_cancelled) serial=SERIAL-1 cause=adb invocation canceled: context canceled")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	class, reason := session.dialFailure(cancelled, push)
	if class != MirrorEndPushCancelled {
		t.Fatalf("a push this plane cancelled was classified %q, want %q", class, MirrorEndPushCancelled)
	}
	if !class.Valid() {
		t.Fatal("the class of a cancelled push is not one the plane states")
	}
	if !class.Failed() {
		t.Fatal("a cancelled push reads as a stream that ENDED rather than as a failure")
	}
	if sentence := class.Sentence(); sentence == "" || !strings.Contains(sentence, "cancelled") {
		t.Fatalf("the class's own sentence is %q, which does not name the cancellation", sentence)
	}
	if reason == nil || !strings.Contains(reason.Error(), "this plane") {
		t.Fatalf("the sentence beside it is %v, which does not name the plane as what cancelled the push", reason)
	}
	if got := class.Sentence() + " " + reason.Error(); strings.Contains(got, MirrorEndTransportUnavailable.Sentence()) {
		t.Fatalf("a cancellation this plane performed is reported with the transport's own sentence: %s", got)
	}
	// A push nobody cancelled is the transport, and that is what it stays: an
	// open that failed on a path that was not there.
	if class, _ := session.dialFailure(context.Background(), errors.New("mirror: the reverse tunnel could not be opened")); class != MirrorEndTransportUnavailable {
		t.Fatalf("an open that failed with its context intact was classified %q, want %q", class, MirrorEndTransportUnavailable)
	}
	// And a class stated where the device's own process is — the adapter - is
	// never replaced by the class a caller assumed.
	stated := ClassifyMirrorEnd(errors.New("scrcpy: the device-side server exited"), MirrorEndDeviceServerFailed)
	if class, _ := session.dialFailure(cancelled, stated); class != MirrorEndDeviceServerFailed {
		t.Fatalf("the class the adapter stated was replaced by %q", class)
	}
}

// TestAReadThisSessionCancelledIsNotTheDevicesStreamEnding is the read half of the
// same rule: the context a read is bounded by is the session's own, so a read that
// returns because the session ended it is not the device's stream ending. It is
// answered by re-encoding the device, never by blaming the device or the transport
// for a cancellation this plane made - on the fleet, a device reported as having
// ended its own stream is a device an operator would go and look at.
func TestAReadThisSessionCancelledIsNotTheDevicesStreamEnding(t *testing.T) {
	dialer := newHeldDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	dialer.release()

	tile, tileViewer, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeAmbient, MirrorPreview{})
	if err != nil {
		t.Fatalf("opening the tile's stream: %v", err)
	}
	waitFor(t, "the tile's stream to be carried", func() bool { return dialer.streamsOpened() == 1 })
	first := dialer.streamFor(t)

	// The operator opens the same device, which needs a stronger profile: the
	// session ends its own read of the stream it is carrying so the re-encode
	// does not wait for the device's next frame.
	_, _, err = engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("opening the operator's frame: %v", err)
	}
	waitFor(t, "the device to be opened again at the operator profile", func() bool {
		asks := dialer.asked()
		return len(asks) == 2 && asks[1] == PurposeOperator
	})
	waitFor(t, "the re-encoded stream to be carried", func() bool { return dialer.streamsOpened() == 2 })

	if class := tile.EndClass(); class != "" {
		t.Fatalf("a read this session cancelled ended the session as %q (%v), want the session re-encoded", class, tile.Fails())
	}
	// The stream it replaced was released on the way: one device is captured once.
	if _, closes, _ := first.state(); closes == 0 {
		t.Error("the stream the session replaced was not released")
	}
	reencoded := dialer.streamFor(t)
	reencoded.push(StreamFrame{Config: true, Data: configUnit})
	reencoded.push(StreamFrame{Key: true, PTSUS: 100_000, Data: idrUnit})
	if frame := next(t, tileViewer); !frame.Key {
		t.Error("the viewing that was already attached was carried a non-key frame after the re-encode")
	}
}
