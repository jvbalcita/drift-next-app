package transportconnect_test

import (
	"context"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
)

// What the plane states to an operator surface about how a stream ended.
//
// The wire contract has three obligations here and the surface used to keep none
// of them: a stream that ENDED must be reported as ended rather than as a failure,
// a stream that FAILED must say why, and neither may be read out of the other -
// the state must come from the plane's own class, never from the presence of a
// sentence.

// TestAnIdleEndIsReportedAsEndedRatherThanFailed is the card's second requirement
// at the boundary an operator actually reads: the last viewer detaching stops the
// capture, and the plane reports a stream that ENDED. The carrier reports a
// sentence beside it - the session's own words for why it stopped - and the state
// must not be inferred from it.
func TestAnIdleEndIsReportedAsEndedRatherThanFailed(t *testing.T) {
	idle := &fakeMirrorStream{
		key:      mirrorStreamID,
		deviceID: mirrorDevice,
		serial:   mirrorSerial,
		frames:   40,
		// The session's own sentence, which is non-empty for an idle end exactly
		// as it is for a failure. That is the whole trap: the old derivation read
		// FAILED out of this string.
		failure:  "media: no viewer is watching this device, so the capture was stopped",
		endClass: media.MirrorEndViewerDetached,
	}
	handler := mirrorHandler(t, newFakeMirrors(idle))

	response, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID}))
	if err != nil {
		t.Fatalf("poll the stream: %v", err)
	}
	stream := response.Msg.GetStream()
	if state := stream.GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED {
		t.Fatalf("an idle end reports %v, want ENDED", state)
	}
	if failure := stream.GetFailure(); failure != "" {
		t.Fatalf("an idle end carries %q as a failure, and a stream that ended has nothing to report as one", failure)
	}
	if stream.GetFrames() != 40 {
		t.Fatalf("the ended stream reports %d picture(s), want the 40 it carried", stream.GetFrames())
	}
}

// TestEveryFailedEndStatesWhyItFailed: the other half of the contract, and the one
// a frame that shows nothing depends on. Each class states the sentence the plane
// classified it with, and the boundary fills one in from the class when no carrier
// had words of its own - a FAILED stream with an empty failure field is a frame
// nothing can be diagnosed from.
func TestEveryFailedEndStatesWhyItFailed(t *testing.T) {
	failing := []media.MirrorEndClass{
		media.MirrorEndDeviceStreamEnded,
		media.MirrorEndTransportUnavailable,
		media.MirrorEndDeviceServerFailed,
		media.MirrorEndNoStreamableScreen,
		media.MirrorEndEngineStopped,
	}
	for _, class := range failing {
		t.Run(string(class), func(t *testing.T) {
			stream := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, serial: mirrorSerial, endClass: class}
			handler := mirrorHandler(t, newFakeMirrors(stream))

			response, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID}))
			if err != nil {
				t.Fatalf("poll the stream: %v", err)
			}
			message := response.Msg.GetStream()
			if state := message.GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED {
				t.Fatalf("class %q reports %v, want FAILED", class, state)
			}
			if failure := strings.TrimSpace(message.GetFailure()); failure == "" {
				t.Fatalf("class %q reports FAILED with an empty failure field, which is the frame that cannot be diagnosed", class)
			}
			if got, want := message.GetFailure(), class.Sentence(); got != want {
				t.Fatalf("class %q states %q, want the plane's own sentence for it %q", class, got, want)
			}
		})
	}
}

// TestAFailedEndCarriesTheCarriersOwnSentenceRatherThanTheClasss: the class's
// sentence is the floor and not the ceiling. Where a carrier had the transport's
// own error - which is the whole diagnosis - that is what the operator reads.
func TestAFailedEndCarriesTheCarriersOwnSentenceRatherThanTheClasss(t *testing.T) {
	stream := &fakeMirrorStream{
		key:      mirrorStreamID,
		deviceID: mirrorDevice,
		serial:   mirrorSerial,
		endClass: media.MirrorEndDeviceServerFailed,
		failure:  "media: the stream from device-alpha ended: scrcpy: the device-side server exited",
	}
	handler := mirrorHandler(t, newFakeMirrors(stream))

	response, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID}))
	if err != nil {
		t.Fatalf("poll the stream: %v", err)
	}
	if failure := response.Msg.GetStream().GetFailure(); !strings.Contains(failure, "the device-side server exited") {
		t.Fatalf("the failure is %q, which does not name the device-side server", failure)
	}
}

// TestALiveStreamStatesNeitherAClassNorASentence: a stream that is carrying
// pictures is live, and nothing else about it is stated. A class here would tell an
// operator a live frame had ended.
func TestALiveStreamStatesNeitherAClassNorASentence(t *testing.T) {
	stream := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, serial: mirrorSerial, frames: 12, keyFrames: 1}
	handler := mirrorHandler(t, newFakeMirrors(stream))

	response, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID}))
	if err != nil {
		t.Fatalf("poll the stream: %v", err)
	}
	message := response.Msg.GetStream()
	if state := message.GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_LIVE {
		t.Fatalf("a stream carrying pictures reports %v, want LIVE", state)
	}
	if failure := message.GetFailure(); failure != "" {
		t.Fatalf("a live stream carries %q as a failure", failure)
	}
}

// TestAnOperatorStopStatesTheEndingOverWhatTheStreamCarried: the operator asked for
// the stop, so the state is ENDED and what the stream carried is kept beside it. A
// stream that had already failed keeps its failure, because the ending did not fix
// it.
func TestAnOperatorStopStatesTheEndingOverWhatTheStreamCarried(t *testing.T) {
	live := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, serial: mirrorSerial, frames: 7}
	handler := mirrorHandler(t, newFakeMirrors(live))

	response, err := handler.StopMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{Context: mirrorRequestContext(), StreamId: mirrorStreamID}))
	if err != nil {
		t.Fatalf("stop the stream: %v", err)
	}
	message := response.Msg.GetStream()
	if state := message.GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED {
		t.Fatalf("an operator stop reports %v, want ENDED", state)
	}
	if message.GetFrames() != 7 {
		t.Fatalf("the stopped stream reports %d picture(s), want the 7 it carried", message.GetFrames())
	}
	if live.closes != 1 {
		t.Fatalf("the stream was closed %d time(s), want once", live.closes)
	}

	// The same ending over a stream that had already failed keeps the failure.
	failed := &fakeMirrorStream{
		key:      mirrorStreamID,
		deviceID: mirrorDevice,
		serial:   mirrorSerial,
		endClass: media.MirrorEndDeviceServerFailed,
		failure:  "media: the device-side server exited",
	}
	failedHandler := mirrorHandler(t, newFakeMirrors(failed))
	failedResponse, err := failedHandler.StopMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{Context: mirrorRequestContext(), StreamId: mirrorStreamID}))
	if err != nil {
		t.Fatalf("stop the failed stream: %v", err)
	}
	if state := failedResponse.Msg.GetStream().GetState(); state != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED {
		t.Fatalf("stopping an already-failed stream reports %v, want FAILED", state)
	}
	if failure := failedResponse.Msg.GetStream().GetFailure(); !strings.Contains(failure, "device-side server") {
		t.Fatalf("stopping a failed stream dropped its failure: %q", failure)
	}
}
