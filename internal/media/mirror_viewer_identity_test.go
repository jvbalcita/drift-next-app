package media

import (
	"context"
	"testing"
	"time"
)

// One identity per VIEWING, and never one identity per device.
//
// These are the cases card ARC-253 asks for, and this is why they are the
// difference between a mirror that holds and one that "never held": the operator's
// own frame and a grid tile are two viewers of ONE device, and while both were
// handed the SAME stream identity, a stop on that identity was not "this viewer is
// done" - it was a capture-wide operation. A tile unmounting ended the operator's
// frame; opening a device into the frame ended the tiles. The capture is still
// started once and shared; what is not shared is the identity, and that is what
// makes a stop a viewer detach.
//
// The measurement the card records, against the running plane (lab fleet, device
// 0003e69f-…): an AMBIENT stream (what a grid tile opens) and then the OPERATOR's
// stream for the SAME device both returned drift-0003e69f-…-5026af10.

// TestTwoViewersOfOneDeviceAreTwoIdentities is that measurement asserted where it
// is decided: the second viewer still JOINS the session the first one started -
// the device is captured once - and the identity it is handed is its own.
func TestTwoViewersOfOneDeviceAreTwoIdentities(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})

	tileSession, tile, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeAmbient, MirrorPreview{})
	if err != nil {
		t.Fatalf("the grid's tile could not open a stream: %v", err)
	}
	frameSession, frame, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("the operator's own frame could not open a stream: %v", err)
	}

	// One device is one capture, however many surfaces are watching it.
	if frameSession != tileSession {
		t.Fatal("the operator's frame opened a second capture of a device the grid was already watching")
	}
	if len(engine.Sessions()) != 1 {
		t.Fatalf("one device holds %d session(s), want 1", len(engine.Sessions()))
	}

	// And one capture is two identities, because it is two viewings.
	if tile.StreamKey() == "" || frame.StreamKey() == "" {
		t.Fatal("a viewer was handed no stream identity")
	}
	if tile.StreamKey() == frame.StreamKey() {
		t.Fatalf("both viewers of one device hold the identity %q: a stop on it is then a stop for every viewer of that device",
			tile.StreamKey())
	}

	// The session reports the identities it has handed out, one per viewing, and
	// that is what an input's declared observation is reconciled against: both
	// viewers are watching the one stream, so both identities are observations of
	// it - and an identity this session never handed out is not.
	identities := tileSession.ViewerIdentities()
	if len(identities) != 2 {
		t.Fatalf("the session reports %d identity/identities (%v), want one per viewing", len(identities), identities)
	}
	if identities[0] == identities[1] {
		t.Fatalf("the session reports the same identity twice (%v): it is not one per viewing", identities)
	}
	seen := map[string]bool{identities[0]: true, identities[1]: true}
	if !seen[tile.StreamKey()] || !seen[frame.StreamKey()] {
		t.Fatalf("the session reports %v, want the identities it handed to its two viewers (%q, %q)",
			identities, tile.StreamKey(), frame.StreamKey())
	}
}

// TestStoppingOneViewerLeavesTheOtherViewingCarried is the first case the card
// asks for: two viewers of one device, one of them stops, and the other is still
// live and still being handed the device's pictures.
//
// The two viewers here are both the operator's own frame, so that nothing about
// a profile change is being asserted by accident - what is under test is one
// viewer detaching and the other's picture, not the encoder the device chose.
func TestStoppingOneViewerLeavesTheOtherViewingCarried(t *testing.T) {
	const idle = 20 * time.Millisecond
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: idle})

	session, departing, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("the first viewer could not open a stream: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)
	_, staying, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("the second viewer could not open a stream: %v", err)
	}
	if departing.StreamKey() == staying.StreamKey() {
		t.Fatalf("both viewers of one device hold the identity %q", departing.StreamKey())
	}

	// Both viewings are carried by the one capture, so both are handed the same
	// picture.
	stream.push(StreamFrame{Key: true, PTSUS: 1000, Data: idrUnit})
	if frame := next(t, departing); !frame.Key {
		t.Fatal("the first viewer was not handed the device's key frame")
	}
	if frame := next(t, staying); !frame.Key {
		t.Fatal("the second viewer was not handed the device's key frame")
	}

	// One viewer detaches: a tile unmounting, a frame re-mounting, a surface
	// closing the stream it was given.
	departing.Close()

	// The device is still being captured, and the viewer still watching is still
	// being handed its pictures.
	stream.push(StreamFrame{PTSUS: 2000, Data: sliceUnit})
	if frame := next(t, staying); frame.PTSUS != 2000 {
		t.Fatalf("the viewer still watching was handed frame %d, want 2000", frame.PTSUS)
	}
	// The idle bound is 20ms, so a capture that had wrongly been ended by the
	// departing viewer would have ended many times over by now.
	time.Sleep(10 * idle)
	if failure := session.Fails(); failure != nil {
		t.Fatalf("one viewer's departure stopped a capture another viewer was still watching: %v", failure)
	}
	if _, live := engine.Session("device-1"); !live {
		t.Fatal("a device with a viewer still watching it stopped being mirrored")
	}
	if _, closes, _ := stream.state(); closes != 0 {
		t.Fatal("one viewer's departure released the device's stream while another viewer was watching it")
	}
}

// TestStoppingTheLastViewerEndsTheCaptureWithTheIdleClass is the second case the
// card asks for, and it is the other half of the same rule: the LAST viewer
// detaching does end the device's capture, and it ends it as an ENDING - the
// idle class the engine already models - rather than as a failure.
func TestStoppingTheLastViewerEndsTheCaptureWithTheIdleClass(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: 20 * time.Millisecond})

	session, first, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("the first viewer could not open a stream: %v", err)
	}
	stream := dialer.streamFor(t, "device-1")
	sessionReady(t, session)
	_, second, err := engine.Start(context.Background(), "device-1", "SERIAL-1")
	if err != nil {
		t.Fatalf("the second viewer could not open a stream: %v", err)
	}

	// One departure is not the end of the capture.
	first.Close()
	if _, live := engine.Session("device-1"); !live {
		t.Fatal("one viewer's departure ended the capture while another viewer was watching it")
	}
	select {
	case <-session.Done():
		t.Fatal("the device's capture ended while a viewer was still watching it")
	default:
	}

	// The last one is.
	second.Close()
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the capture of a device whose last viewer detached was still running")
	}
	if class := session.EndClass(); class != MirrorEndViewerDetached {
		t.Fatalf("the capture ended with class %q, want %q - the last viewer detached, which is an ending and not a failure",
			class, MirrorEndViewerDetached)
	}
	if class := session.EndClass(); class.Failed() {
		t.Fatalf("the last viewer's departure was classified as the failure %q", class)
	}
	if reason := session.Fails(); reason == nil {
		t.Fatal("the capture ended without stating why it stopped")
	}
	if _, live := engine.Session("device-1"); live {
		t.Fatal("a device nobody is watching is still reported as being mirrored")
	}
	waitFor(t, "the device's stream to be released", func() bool {
		_, closes, _ := stream.state()
		return closes > 0
	})
}
