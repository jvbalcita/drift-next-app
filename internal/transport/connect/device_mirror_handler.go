package transportconnect

import (
	"context"
	"log"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// DeviceMirrorHandler serves the live mirror surface from one constructed stream
// transport and the registry that resolves a device to the transport it is
// currently reachable at.
type DeviceMirrorHandler struct {
	streams  DeviceMirrors
	serials  DeviceSerialResolver
	capacity MirrorCapacitySource
	refusals MirrorRefusalRecorder
}

// NewDeviceMirrorHandler binds the surface to the stream transport and the
// device-to-serial resolver. It returns nil when either is absent - including a
// non-nil interface holding a nil pointer - so a caller cannot obtain a handler
// that has nothing to call, and the route is never mounted (AGENTS.md section 6).
//
// capacity and refusals are not gates on the mount: a deployment whose mirror
// engine was not constructed has no transport either, so a route that exists at
// all always has both facts to answer with. They are separate arguments because
// they are separate facts - how much room the plane has, and where a refusal is
// written down - and a handler given neither still serves streams; it just cannot
// answer what the plane's bound is, and answers that it cannot rather than
// claiming a bound of zero.
func NewDeviceMirrorHandler(streams DeviceMirrors, serials DeviceSerialResolver, capacity MirrorCapacitySource, refusals MirrorRefusalRecorder) *DeviceMirrorHandler {
	if isAbsentDeviceMirrors(streams) || serials == nil {
		return nil
	}
	if isNilInterface(capacity) {
		capacity = nil
	}
	if isNilInterface(refusals) {
		refusals = nil
	}
	return &DeviceMirrorHandler{streams: streams, serials: serials, capacity: capacity, refusals: refusals}
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
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
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
	purpose := wantedPurpose(message.GetPurpose())
	// The workspace's preview setting travels with the viewer that stated it and
	// bounds an AMBIENT stream only. It is read here, at the boundary, and never
	// re-derived below: the engine applies it to the grid's tiles and the
	// operator's own frame keeps its own profile whatever this says.
	preview := wantedPreview(message.GetPreviewQuality(), message.GetFrameRate())
	serial, err := h.serials.CurrentSerial(ctx, workspaceID, deviceID)
	if err != nil {
		return nil, MapError(err)
	}
	stream, openErr := h.streams.Open(ctx, deviceID, serial, transport, purpose, preview)
	if openErr != nil {
		// A refusal the plane's own capacity caused is recorded before it is
		// answered, and it is answered in the plane's OWN sentence rather than
		// through the shared generic mapping: the mapper renders an unclassified
		// error as a fixed internal message, which would put "the stream failed"
		// in front of an operator whose capacity was spent and leave the reason
		// nowhere but the log. That is the whole of what an operator could not
		// read, and it is why a refusal is carried here instead.
		if capacity, refused := isCapacityRefusal(openErr); refused {
			h.recordRefusal(ctx, workspaceID, deviceID, actorType, actorID, purpose, capacity)
			return nil, MapError(platformerrors.Wrap(platformerrors.CodeUnavailable, capacity.Error(), openErr))
		}
		return nil, MapError(openErr)
	}
	return connectrpc.NewResponse(&driftv1.StartMirrorStreamResponse{Stream: mirrorStreamProto(stream)}), nil
}

// recordRefusal writes the plane's own record of a stream it refused.
//
// Nothing here changes what the caller is answered: the refusal is reported from
// the engine, and a record that could not be written is logged rather than
// turned into a second, different refusal. A refusal recorded nowhere is a defect
// in the deployment's wiring, not in the operator's request.
func (h *DeviceMirrorHandler) recordRefusal(ctx context.Context, workspaceID, deviceID, actorType, actorID string, purpose media.MirrorViewerPurpose, capacity *media.SessionCapacityError) {
	if h.refusals == nil {
		log.Printf("live mirror refused %s for %s and no mirror event recorder is constructed: %v", purpose, deviceID, capacity)
		return
	}
	refusal := StreamRefusal{
		WorkspaceID:     workspaceID,
		DeviceID:        deviceID,
		ActorType:       actorType,
		ActorID:         actorID,
		ViewerPurpose:   string(purpose),
		Reason:          capacity.Error(),
		Capacity:        capacity.Capacity,
		OperatorReserve: h.reserveFor(capacity),
	}
	if err := h.refusals.RecordStreamRefusal(ctx, refusal); err != nil {
		log.Printf("live mirror refused %s for %s and the record of it could not be written: %v", purpose, deviceID, err)
	}
}

// reserveFor states the place kept for the operator's own frame beside the
// refusal. The capacity error carries the share the refused viewer may hold, so
// the reserve is derived from the plane's own answer rather than from a second
// reading of the engine - which could have moved between the refusal and the
// record.
func (h *DeviceMirrorHandler) reserveFor(capacity *media.SessionCapacityError) int {
	if capacity.Purpose != media.PurposeAmbient {
		return 0
	}
	reserve := capacity.Capacity - capacity.AmbientShare
	if reserve < 0 {
		return 0
	}
	return reserve
}

// GetMirrorCapacity answers with the plane's own device-session bound.
//
// It exists because the console's grid must not carry a copy of a number the
// plane owns: the tile allocation used to be a constant that happened to hold the
// same value as the engine's bound, so the two agreed by luck. A handler with no
// capacity source answers unavailable rather than zero, because zero is not a
// bound this plane stated and a console would read it as one.
//
// The workspace the caller names is validated rather than used: the bound belongs
// to the plane and is the same for every workspace on it, so there is no
// per-workspace capacity to read. It is still required, because a surface a
// console reads its own allocation from should say which workspace it is reading
// for - the same demand every other call from that console makes - and a request
// that named none would be a request this surface could not account for.
func (h *DeviceMirrorHandler) GetMirrorCapacity(ctx context.Context, request *connectrpc.Request[driftv1.GetMirrorCapacityRequest]) (*connectrpc.Response[driftv1.GetMirrorCapacityResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if h.capacity == nil {
		return nil, unavailableError("this control plane cannot state its device session capacity")
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a get mirror capacity request is required")
	}
	if err := validateWorkspace(request.Msg.GetWorkspace()); err != nil {
		return nil, err
	}
	return connectrpc.NewResponse(&driftv1.GetMirrorCapacityResponse{Capacity: mirrorCapacityProto(h.capacity)}), nil
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

// StopMirrorStream ends ONE VIEWING of a device's live stream and returns that
// stream as it ended.
//
// A stop is a VIEWER DETACH and never a capture-wide operation, and the identity
// is what makes it one. One device is captured once and its session carries the
// same frames to every viewer of it, while each viewing holds an identity of its
// own - so what this resolves is the caller's own stream, and releasing it
// leaves every other viewer of that device exactly as it was. The device's
// capture ends when its LAST viewer has detached for the idle bound, which is
// the end an unsubscribed device already had; an answer of ENDED therefore says
// that THIS viewing is over, which is a state and not a fault.
//
// What the stream carried is read before it is released, and the ending is then
// stated over it: an operator asking a stream to stop is told what it delivered
// as well as that it stopped. A stream that had already failed keeps its failure
// - the ending did not fix it.
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
// The identity names one VIEWING, so the pictures the answer reports are what
// THAT browser was carried and never what another viewer of the same device was:
// a surface polling its own identity cannot be told a stream is delivering
// pictures to somebody else.
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
