package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
)

// unavailableError answers with a fixed sentence: a refusal an operator reads is
// never a device, transport or stack-trace diagnostic.
func unavailableError(message string) error {
	return connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: message})
}

// notFoundError answers a stream identity this service is not carrying.
func notFoundError(message string) error {
	return connectrpc.NewError(connectrpc.CodeNotFound, &safeError{message: message})
}

// The live mirror surface (card ARC-143).
//
// Four RPCs, and they are the whole of the stream's lifecycle: open a device's
// live stream, negotiate the transport with the browser, poll what the stream is
// doing, and end it. Input is not here - it is the typed device input surface,
// where the kernel authorizes it - and neither is anything a browser could use to
// reach the device around this service.
//
// A handler authenticates the caller, validates the request's shape, resolves the
// device the caller named to the transport serial that device is currently
// reachable at, and calls one application boundary. It does not open a capture, does
// not decide a transport, and does not touch a device: those belong to the
// boundary and the engine, and a handler that made them would be a second place
// where authority is decided.

// DeviceMirrorStream is one browser's stream over one device, as this surface
// needs it. *media.StreamPeer satisfies it.
type DeviceMirrorStream interface {
	// StreamKey is the per-device stream identity the browser was given.
	StreamKey() string
	// Answer completes the handshake with the browser's offer.
	Answer(ctx context.Context, offerSDP string) (string, error)
	// Stats reports what the stream has carried and how it ended.
	Stats() media.StreamStats
	// Close releases this stream, and - when it was the last viewer - the
	// device's capture with it.
	Close() error
}

// DeviceMirrors is the live stream transport this surface calls. A nil
// implementation means no transport was constructed, and the route is therefore
// not mounted: a surface whose only possible answer is a refusal is a control an
// operator renders and then finds dead.
type DeviceMirrors interface {
	// Open starts (or joins) a device's live stream and returns the stream the
	// browser will negotiate with. Opening is what subscribes a viewer, and the
	// subscription is what starts the device's capture.
	Open(ctx context.Context, deviceID, serial string) (DeviceMirrorStream, error)
	// Stream reports the live stream with this identity, or false when there is
	// none. It is how a negotiation, a poll and a stop find the stream a caller
	// named, and it confers nothing: the stream it returns is already this
	// service's own.
	Stream(streamKey string) (DeviceMirrorStream, bool)
}

// mirrorStreamProto maps one stream's own reporting onto the wire contract.
//
// The state is derived from what the stream has CARRIED rather than from a
// connection's state, because on this fleet those are two different things: an
// encoder that emits one IDR per session will happily produce a connection that is
// up and showing nothing, and reporting that as live is the silent black screen
// this card exists to close.
func mirrorStreamProto(stream DeviceMirrorStream) *driftv1.MirrorStream {
	stats := stream.Stats()
	message := &driftv1.MirrorStream{
		StreamId:  stream.StreamKey(),
		DeviceId:  stats.DeviceID,
		Transport: driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
		State:     mirrorStreamState(stats),
		Failure:   stats.Failure,
		Frames:    stats.Frames,
		KeyFrames: stats.KeyFrames,
	}
	if width, height := renderSize(stats); width > 0 && height > 0 {
		message.RenderWidth, message.RenderHeight = uint32(width), uint32(height)
	}
	return message
}

// mirrorStreamState is the state an operator surface renders, from the stream's
// own numbers.
func mirrorStreamState(stats media.StreamStats) driftv1.MirrorStreamState {
	switch {
	case stats.Failure != "":
		return driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED
	case stats.Frames == 0:
		return driftv1.MirrorStreamState_MIRROR_STREAM_STATE_STARTING
	default:
		return driftv1.MirrorStreamState_MIRROR_STREAM_STATE_LIVE
	}
}

// renderSize is the frame the stream is encoded at, as the boundary reports it.
// It is the coordinate frame a tap must be measured in.
func renderSize(stats media.StreamStats) (int, int) {
	return stats.RenderWidth, stats.RenderHeight
}

// wantedTransport maps the caller's requested transport onto the one this service
// can carry. UNSPECIFIED selects the default, and a requested transport that does
// not exist yet is refused rather than silently replaced: a caller that asked for
// TCP must not be handed a WebRTC stream it believes is TCP.
func wantedTransport(requested driftv1.MirrorTransport) (driftv1.MirrorTransport, error) {
	switch requested {
	case driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED, driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC:
		return driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC, nil
	case driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP:
		return driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED, unavailableError("the TCP transport is not implemented yet, so no stream can be opened on it")
	default:
		return driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED, invalidArgument("the requested mirror transport is not a known transport")
	}
}
