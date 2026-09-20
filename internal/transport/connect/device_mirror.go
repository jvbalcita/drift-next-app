package transportconnect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/mirrors"
)

// MirrorStreamPath is the per-device stream endpoint a browser fetches when the
// operator's transport is TCP.
//
// It is a path on this service's own guarded surface: mounted with the same token
// check as every other local surface, served on the address the process already
// binds (loopback unless a deployment broadens it deliberately), and the ONLY
// thing a browser is given to reach a stream's frames - never a device address,
// an adb serial, an RTSP address or a media server's control URL.
const MirrorStreamPath = "/drift/v1/mirror/stream"

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
// live stream over the transport the operator chose, negotiate it with the
// browser when it is a peer connection, poll what the stream is doing, and end
// it. Input is not here - it is the typed device input surface,
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

// DeviceMirrorEndpointStream is a stream carried as bytes from this service's
// own stream endpoint rather than negotiated peer-to-peer: the browser fetches
// MirrorStreamPath and the response body is the container.
//
// Which transport a stream uses is what the stream IS. A stream that serves its
// bytes from the stream endpoint is the TCP transport, and the endpoint is the
// only handle a browser is given for it - there is no second fact recorded
// beside it that could disagree with the stream's own behaviour.
type DeviceMirrorEndpointStream interface {
	DeviceMirrorStream
	// Serve carries this stream to one browser until the stream ends or the
	// browser's request is cancelled, calling flush after every fragment so a
	// picture is delivered as it arrives rather than when a buffer fills.
	Serve(ctx context.Context, writer io.Writer, flush func() error) error
}

// DeviceMirrors is the live stream transport this surface calls. A nil
// implementation means no transport was constructed, and the route is therefore
// not mounted: a surface whose only possible answer is a refusal is a control an
// operator renders and then finds dead.
type DeviceMirrors interface {
	// Open starts (or joins) a device's live stream over the requested transport
	// and returns the stream the browser will be carried, spending the plane's
	// device-session capacity against the purpose the viewer is and carrying the
	// stream at the bound that purpose requires.
	//
	// Opening is what subscribes a viewer, and the subscription is what starts
	// the device's capture. The preview setting bounds an ambient viewer's
	// stream and never the operator's own frame; a caller that states none gets
	// the setting the plane is configured with.
	Open(ctx context.Context, deviceID, serial string, transport media.MirrorTransportKind, purpose media.MirrorViewerPurpose, preview media.MirrorPreview) (DeviceMirrorStream, error)
	// Stream reports the live stream with this identity, or false when there is
	// none. It is how a negotiation, a poll and a stop find the stream a caller
	// named, and it confers nothing: the stream it returns is already this
	// service's own.
	Stream(streamKey string) (DeviceMirrorStream, bool)
	// Carrying reports the identities of the streams this transport is carrying
	// right now, in a stable order. It exists for one refusal - see
	// noSuchStreamError - because "that identity is not being carried" is not
	// actionable without the identities that ARE, and the alternative an operator
	// has is reading the service's own database to find out.
	//
	// What it returns is a stream identity and nothing else: no device address, no
	// adb serial and no media-server URL is reachable through it.
	Carrying() []string
}

// MirrorCapacitySource is the plane's own device-session bound, as this surface
// reads it to answer a caller. *media.MirrorEngine satisfies it.
//
// It is a port of its own rather than a method on DeviceMirrors because it is not
// a fact about the transport: the bound belongs to the engine that spends it, and
// a deployment whose transport was built over an engine it cannot read the bound
// of has nothing truthful to publish - which is why the handler it is absent from
// answers unavailable rather than zero.
type MirrorCapacitySource interface {
	// Capacity is how many devices the plane mirrors at once.
	Capacity() int
	// OperatorReserve is how many of Capacity are kept for the operator's own
	// frame: the grid may hold at most Capacity - OperatorReserve sessions.
	OperatorReserve() int
}

// MirrorRefusalRecorder is told about a live stream this plane refused to open,
// so a frame that shows nothing can be explained from the plane's own record
// rather than only from what the operator's console happened to keep.
//
// It is called with the refusal already classified and the sentence already
// formed, because the record that matters is the one an operator reads: the
// event names the device, the capacity that was spent and the plane's own
// sentence, and a recorder that had to re-derive any of those could write a
// record that disagrees with the refusal it records. A recorder that fails
// records nothing; it never refuses the stream, because the operator's request is
// answered from the engine and not from the log.
type MirrorRefusalRecorder interface {
	RecordStreamRefusal(ctx context.Context, refusal StreamRefusal) error
}

// StreamRefusal is one live stream this plane refused to open, in the terms the
// record needs. It is the domain's own record of a refusal: what is written down
// is the plane's fact, so the surface that writes it and the surface that reads
// it cannot hold two shapes of the same refusal.
type StreamRefusal = mirrors.StreamRefusal

// MirrorRefusalRecorderFunc adapts a function to the recorder, for a deployment
// that wires one inline.
type MirrorRefusalRecorderFunc func(ctx context.Context, refusal StreamRefusal) error

func (f MirrorRefusalRecorderFunc) RecordStreamRefusal(ctx context.Context, refusal StreamRefusal) error {
	return f(ctx, refusal)
}

// mirrorCapacityProto maps the plane's bound onto the wire contract.
func mirrorCapacityProto(source MirrorCapacitySource) *driftv1.MirrorCapacity {
	if source == nil {
		return &driftv1.MirrorCapacity{}
	}
	capacity := source.Capacity()
	if capacity < 0 {
		capacity = 0
	}
	reserve := source.OperatorReserve()
	if reserve < 0 || reserve > capacity {
		reserve = capacity
	}
	return &driftv1.MirrorCapacity{
		SessionCapacity: uint32(capacity),
		OperatorReserve: uint32(reserve),
	}
}

// wantedPurpose maps the purpose a caller stated onto the one the engine spends
// its capacity against. UNSPECIFIED is the operator's own frame, not the grid:
// the operator's frame is the surface that must never be refused because a grid
// is drawing, so a caller that has not been taught the distinction is read as the
// demand that place is kept for.
func wantedPurpose(requested driftv1.MirrorViewerPurpose) media.MirrorViewerPurpose {
	switch requested {
	case driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT:
		return media.PurposeAmbient
	case driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_UNSPECIFIED,
		driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR:
		return media.PurposeOperator
	default:
		return media.PurposeOperator
	}
}

// wantedPreview maps the workspace's preview setting as a caller stated it onto
// the setting this plane will apply.
//
// A caller states nothing by leaving the level UNSPECIFIED, or by stating a level
// this plane does not know, or by stating no frame rate - and each of those is
// answered by the plane's own setting rather than by "no bound": a stream carried
// at a bound nobody chose is the defect this contract removes, so the field's
// absence is never read as an uncapped encoder. A frame rate outside the range
// this product asks for is treated the same way, because a rate no control offers
// is not a rate an operator chose.
func wantedPreview(quality driftv1.MirrorPreviewQuality, frameRate uint32) media.MirrorPreview {
	preview := media.MirrorPreview{}
	switch quality {
	case driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_LOW:
		preview.Quality = media.PreviewLow
	case driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_MEDIUM:
		preview.Quality = media.PreviewMedium
	case driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_HIGH:
		preview.Quality = media.PreviewHigh
	case driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_EXTRA:
		preview.Quality = media.PreviewExtra
	case driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_UNSPECIFIED:
		// Nothing stated: the plane's own setting applies.
	default:
		// A level this plane does not know. It is not applied and it is not an
		// error: the plane's own setting is a bound, which is what the caller
		// would have got had it stated nothing at all.
	}
	if frameRate > 0 && frameRate <= uint32(media.MaxPreviewFrameRate) {
		preview.FrameRate = int(frameRate)
	}
	return preview
}

// isCapacityRefusal reports the plane's own capacity bound as the cause of a
// refusal, so the surface can record it rather than only report it.
func isCapacityRefusal(err error) (*media.SessionCapacityError, bool) {
	var capacity *media.SessionCapacityError
	if errors.As(err, &capacity) {
		return capacity, true
	}
	return nil, false
}

// noSuchStreamError is the refusal a caller reads when the stream identity it
// named is not one this service is carrying.
//
// The sentence alone is not actionable, and it is the same sentence for three
// different facts: an identity that was never a stream, one whose session has
// ended, and one that belongs to a stream this service is carrying under a
// different name. An operator looking at a frame that shows nothing cannot tell
// those apart, and neither can the next person to read that frame - so the refusal
// names the identity it was given and the identities that ARE live, which is the
// whole diagnosis.
//
// What it names is this service's own stream identities - the handle the browser
// is given for a stream it opens - and never a device address, an adb serial or a
// media-server URL (AGENTS.md section 9). The list is bounded by the engine's own
// session bound, so the refusal is bounded with it.
func noSuchStreamError(streams DeviceMirrors, asked string) error {
	return notFoundError("no live stream with that identity is being carried (asked for " +
		strconv.Quote(asked) + "; " + carryingSentence(streams) + ")")
}

// carryingSentence names the streams this transport is carrying, or says plainly
// that it is carrying none - which is itself the diagnosis when a device's session
// has ended.
func carryingSentence(streams DeviceMirrors) string {
	live := []string(nil)
	if streams != nil {
		live = streams.Carrying()
	}
	if len(live) == 0 {
		return "this service is carrying nothing right now"
	}
	return fmt.Sprintf("this service is carrying %d live stream(s): %s", len(live), strings.Join(live, ", "))
}

// mirrorStreamProto maps one stream's own reporting onto the wire contract.
//
// The state is derived from what the stream has CARRIED rather than from a
// connection's state, because on this fleet those are two different things: an
// encoder that emits one IDR per session will happily produce a connection that is
// up and showing nothing, and reporting that as live is the silent black screen
// this card exists to close.
//
// A stream that is not live always carries the plane's own sentence, and the
// sentence is filled in here when the stream's carrier reported none. That is the
// contract this field has always promised - "a state of FAILED always carries
// one" - and it is enforced at the boundary that makes the promise rather than
// left to each carrier, because a frame that shows nothing and says only "the
// stream failed" is diagnosable from nothing.
func mirrorStreamProto(stream DeviceMirrorStream) *driftv1.MirrorStream {
	stats := stream.Stats()
	state := mirrorStreamState(stats)
	failure := stats.Failure
	switch state {
	case driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED:
		if strings.TrimSpace(failure) == "" {
			failure = stats.EndClass.Sentence()
		}
	case driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED:
		// A stream that ENDED has nothing to report as a failure, whatever a
		// carrier put beside its class. The session's own words for an idle end
		// are about why the capture stopped, and stating them in this field would
		// hand an operator a failure sentence for a stream that did not fail -
		// which is the reading this whole change exists to remove.
		failure = ""
	}
	message := &driftv1.MirrorStream{
		StreamId:  stream.StreamKey(),
		DeviceId:  stats.DeviceID,
		Transport: driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
		State:     state,
		Failure:   failure,
		Frames:    stats.Frames,
		KeyFrames: stats.KeyFrames,
	}
	// A stream that serves its own bytes from the stream endpoint is the TCP
	// transport, and what the browser is given to reach it is that endpoint and
	// nothing else. A peer-connection stream states no URL: it is negotiated with
	// NegotiateMirrorStream, and a URL it never serves from would be an address a
	// browser could find nothing at.
	if _, carried := stream.(DeviceMirrorEndpointStream); carried {
		message.Transport = driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP
		message.StreamUrl = MirrorStreamPath + "?stream_id=" + url.QueryEscape(stream.StreamKey())
	}
	if width, height := renderSize(stats); width > 0 && height > 0 {
		message.RenderWidth, message.RenderHeight = uint32(width), uint32(height)
	}
	return message
}

// mirrorStreamState is the state an operator surface renders, from the stream's
// own numbers.
//
// The class is read BEFORE the sentence, and that ordering is the whole of the
// honesty this surface owes an operator: a stream whose session ended because its
// last viewer detached is an ENDED stream, and a surface that inferred FAILED
// from the presence of a sentence reported the operator's own departure back to
// them as something going wrong. Everything else that has stopped carrying is a
// failure, and a failure always states why.
func mirrorStreamState(stats media.StreamStats) driftv1.MirrorStreamState {
	switch {
	case stats.EndClass.Valid():
		if stats.EndClass.Failed() {
			return driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED
		}
		return driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED
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
// will carry. UNSPECIFIED selects the default, which is WebRTC, and a transport
// this service does not carry is refused rather than silently replaced: a caller
// that asked for one thing must never be handed another.
func wantedTransport(requested driftv1.MirrorTransport) (media.MirrorTransportKind, error) {
	switch requested {
	case driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED, driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC:
		return media.TransportWebRTC, nil
	case driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP:
		return media.TransportTCP, nil
	default:
		return "", invalidArgument("the requested mirror transport is not a known transport")
	}
}
