package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
)

// DeviceMirrorHandler serves the live mirror surface from one constructed stream
// transport and the registry that resolves a device to the transport it is
// currently reachable at.
type DeviceMirrorHandler struct {
	streams DeviceMirrors
	serials DeviceSerialResolver
}

// NewDeviceMirrorHandler binds the surface to the stream transport and the
// device-to-serial resolver. It returns nil when either is absent - including a
// non-nil interface holding a nil pointer - so a caller cannot obtain a handler
// that has nothing to call, and the route is never mounted (AGENTS.md section 6).
func NewDeviceMirrorHandler(streams DeviceMirrors, serials DeviceSerialResolver) *DeviceMirrorHandler {
	if isAbsentDeviceMirrors(streams) || serials == nil {
		return nil
	}
	return &DeviceMirrorHandler{streams: streams, serials: serials}
}

// isAbsentDeviceMirrors reports a stream transport this boundary has nothing to
// call, including the typed-nil shape that a plain nil check misses.
func isAbsentDeviceMirrors(streams DeviceMirrors) bool {
	return isNilInterface(streams)
}

// StartMirrorStream opens one device's live stream and returns the stream the
// browser is about to negotiate.
//
// It resolves the device the caller named to the transport that device is
// currently reachable at. The caller never supplies a serial and never receives
// one: what a browser is given is this service's own stream identity, and every
// byte it receives comes back through this surface.
func (h *DeviceMirrorHandler) StartMirrorStream(ctx context.Context, request *connectrpc.Request[driftv1.StartMirrorStreamRequest]) (*connectrpc.Response[driftv1.StartMirrorStreamResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a start mirror stream request is required")
	}
	message := request.Msg
	if _, _, err := requireActor(message.GetContext()); err != nil {
		return nil, err
	}
	workspaceID := ""
	if workspace := message.GetWorkspace(); workspace != nil {
		workspaceID = strings.TrimSpace(workspace.GetWorkspaceId())
	}
	if workspaceID == "" {
		return nil, invalidArgument("a live stream requires a workspace")
	}
	deviceID := strings.TrimSpace(message.GetDeviceId())
	if deviceID == "" {
		return nil, invalidArgument("a live stream requires a device")
	}
	transport, err := wantedTransport(message.GetTransport())
	if err != nil {
		return nil, err
	}
	serial, err := h.serials.CurrentSerial(ctx, workspaceID, deviceID)
	if err != nil {
		return nil, MapError(err)
	}
	stream, openErr := h.streams.Open(ctx, deviceID, serial, transport)
	if openErr != nil {
		return nil, MapError(openErr)
	}
	return connectrpc.NewResponse(&driftv1.StartMirrorStreamResponse{Stream: mirrorStreamProto(stream)}), nil
}

// NegotiateMirrorStream completes the handshake with a browser's offer and
// returns the answer.
//
// The offer is only ever negotiated against the stream a caller named, and a
// stream this service is not carrying answers not-found rather than opening one:
// negotiation never starts a capture.
func (h *DeviceMirrorHandler) NegotiateMirrorStream(ctx context.Context, request *connectrpc.Request[driftv1.NegotiateMirrorStreamRequest]) (*connectrpc.Response[driftv1.NegotiateMirrorStreamResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a negotiate mirror stream request is required")
	}
	message := request.Msg
	if _, _, err := requireActor(message.GetContext()); err != nil {
		return nil, err
	}
	stream, err := h.stream(message.GetStreamId())
	if err != nil {
		return nil, err
	}
	answer, answerErr := stream.Answer(ctx, message.GetOfferSdp())
	if answerErr != nil {
		return nil, MapError(answerErr)
	}
	return connectrpc.NewResponse(&driftv1.NegotiateMirrorStreamResponse{
		AnswerSdp: answer,
		Stream:    mirrorStreamProto(stream),
	}), nil
}

// StopMirrorStream ends one stream and returns it as it ended.
//
// Ending releases the browser's subscription, and a device whose last viewer has
// gone stops being captured: this is the operator's own stop, and the state it
// answers with reports what the stream carried rather than claiming a clean end.
func (h *DeviceMirrorHandler) StopMirrorStream(ctx context.Context, request *connectrpc.Request[driftv1.StopMirrorStreamRequest]) (*connectrpc.Response[driftv1.StopMirrorStreamResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a stop mirror stream request is required")
	}
	message := request.Msg
	if _, _, err := requireActor(message.GetContext()); err != nil {
		return nil, err
	}
	stream, err := h.stream(message.GetStreamId())
	if err != nil {
		return nil, err
	}
	// What the stream carried is read before it is released, and the ending is
	// then stated over it: an operator asking a stream to stop is told what it
	// delivered as well as that it stopped. A stream that had already failed keeps
	// its failure - the ending did not fix it.
	ended := mirrorStreamProto(stream)
	if closeErr := stream.Close(); closeErr != nil {
		return nil, MapError(closeErr)
	}
	ended.State = driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED
	if ended.GetFailure() != "" {
		ended.State = driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED
	}
	return connectrpc.NewResponse(&driftv1.StopMirrorStreamResponse{Stream: ended}), nil
}

// GetMirrorStream polls one stream's state while the console is showing it.
//
// It reads; it never opens, negotiates or ends anything. A stream this service is
// not carrying answers not-found, so a console whose stream ended learns that
// rather than being told a stream is fine.
func (h *DeviceMirrorHandler) GetMirrorStream(ctx context.Context, request *connectrpc.Request[driftv1.GetMirrorStreamRequest]) (*connectrpc.Response[driftv1.GetMirrorStreamResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a get mirror stream request is required")
	}
	stream, err := h.stream(request.Msg.GetStreamId())
	if err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.GetMirrorStreamResponse{Stream: mirrorStreamProto(stream)}), nil
}

// stream finds the stream a caller named. An absent or unknown identity is a
// refusal rather than a guess: this surface never opens a stream to answer a
// question about one, and the refusal names both the identity it was given and
// the identities this service IS carrying (see noSuchStreamError).
func (h *DeviceMirrorHandler) stream(streamID string) (DeviceMirrorStream, error) {
	trimmed := strings.TrimSpace(streamID)
	if trimmed == "" {
		return nil, invalidArgument("a live stream requires the stream identity it was given")
	}
	stream, live := h.streams.Stream(trimmed)
	if !live {
		return nil, noSuchStreamError(h.streams, trimmed)
	}
	return stream, nil
}

// ready reports whether this handler was constructed with something to call. A
// handler with no transport is never mounted; if one is invoked anyway it answers
// unavailable rather than reporting anything that could be mistaken for a stream.
func (h *DeviceMirrorHandler) ready() error {
	if h == nil || h.streams == nil || h.serials == nil {
		return unavailableError("the live mirror surface is not constructed")
	}
	return nil
}
