package transportconnect

import (
	"context"
	"reflect"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
)

// The device input transport surface (card ARC-66).
//
// Three RPCs, one per typed input a device can actually be given today: tap,
// swipe and key event. Each handler authenticates the caller, validates the
// request's shape, calls one application boundary and maps the classified
// failure it returns. No handler resolves a device to a transport serial, opens
// a lease, evaluates policy, touches a database or reaches a device: those are
// the application boundary's decisions, and a handler that made them would be a
// second, unaudited place where authority is decided.
//
// Typed text and app launch have no RPC here on purpose. Both are
// contract-complete but not dispatchable from this surface: typed text has a
// resolver but no surface that can register a value with it (ARC-107), and a
// launch names its target in the intent (ARC-73) without being dispatched here.
// A route whose only possible outcome is a refusal is a control an operator
// surface would render and then find dead. They are added when the card that
// makes them dispatchable lands.

// deviceInputTimeout bounds one device input dispatch at this boundary. The
// application boundary may apply its own, shorter bound; this is the transport's
// own ceiling so a request cannot be held open indefinitely by a caller.
const deviceInputTimeout = 30 * time.Second

// DeviceInputTarget is one requested device input as the transport receives it:
// exactly what the caller supplied, and nothing the transport would have to
// invent on the caller's behalf.
//
// The device's current endpoint serial and the attempt identifier are
// deliberately absent. Resolving a device to its transport serial, and assigning
// the identity of the attempt that will run, belong to the application boundary
// that owns those facts; a transport that guessed either would be inventing a
// target the caller never named.
type DeviceInputTarget struct {
	Workspace        string
	DeviceID         string
	LeaseID          string
	FencingToken     uint64
	IdempotencyKey   string
	ObservationToken string
	ApprovalGranted  bool
	Timeout          time.Duration
	Target           action.SemanticTarget
	Payload          execution.InputPayload
}

// DeviceInputs is the application boundary a device input RPC calls. It is the
// one seam between this transport and the dispatch contract.
//
// A nil implementation means the surface was not constructed, and the route is
// therefore not mounted: an unavailable dispatcher must not be reachable as a
// route that answers everything with a refusal.
type DeviceInputs interface {
	Run(ctx context.Context, input DeviceInputTarget, actorType, actorID string) (action.Result, error)
}

// DeviceInputHandler serves the device input surface from one constructed
// application boundary.
type DeviceInputHandler struct {
	inputs DeviceInputs
}

// NewDeviceInputHandler binds the surface to a dispatcher. It returns nil for a
// dispatcher that is absent — including a non-nil interface holding a nil
// pointer, which is the shape a caller creates by passing an uninitialised
// dispatcher — so a caller cannot obtain a handler that has nothing to call.
func NewDeviceInputHandler(inputs DeviceInputs) *DeviceInputHandler {
	if isAbsentDeviceInputs(inputs) {
		return nil
	}
	return &DeviceInputHandler{inputs: inputs}
}

// isAbsentDeviceInputs reports a dispatcher this boundary has nothing to call.
//
// The plain nil check is not enough: an interface can hold a typed nil, and
// `var d *Dispatcher; NewDeviceInputHandler(d)` compiles. Without this, that
// caller would get a mounted route whose first request fails at the call site
// instead of a route that is never mounted at all — a dead control, which is the
// outcome this gate exists to prevent.
func isAbsentDeviceInputs(inputs DeviceInputs) bool {
	return isNilInterface(inputs)
}

// isNilInterface reports a value that is absent as a call target, including the
// typed-nil shape a plain nil check misses: an interface holding a nil pointer is
// not nil, and `var d *Dispatcher; NewHandler(d)` compiles.
func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Tap submits one tap.
func (h *DeviceInputHandler) Tap(ctx context.Context, request *connectrpc.Request[driftv1.TapRequest]) (*connectrpc.Response[driftv1.TapResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a tap request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	target, err := deviceInputTargetFromRequest(message.GetContext(), message.GetWorkspace(), message.GetDeviceId(), message.GetLeaseId(), message.GetFencingToken(), message.GetIdempotencyKey(), message.GetObservationToken(), message.GetApprovalGranted(), semanticTarget(message.GetTap().GetTarget()))
	if err != nil {
		return nil, err
	}
	if err := validateTapPayload(message.GetTap(), message.GetObservationToken()); err != nil {
		return nil, err
	}
	target.Payload = tapPayload(message.GetTap(), message.GetObservationToken())
	result, runErr := h.inputs.Run(ctx, target, actorType, actorID)
	if runErr != nil {
		return nil, mapDeviceInputError(runErr)
	}
	return connectrpc.NewResponse(&driftv1.TapResponse{Result: actionResultProto(result)}), nil
}

// Swipe submits one swipe.
func (h *DeviceInputHandler) Swipe(ctx context.Context, request *connectrpc.Request[driftv1.SwipeRequest]) (*connectrpc.Response[driftv1.SwipeResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a swipe request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	target, err := deviceInputTargetFromRequest(message.GetContext(), message.GetWorkspace(), message.GetDeviceId(), message.GetLeaseId(), message.GetFencingToken(), message.GetIdempotencyKey(), message.GetObservationToken(), message.GetApprovalGranted(), action.SemanticTarget{})
	if err != nil {
		return nil, err
	}
	if err := validateSwipePayload(message.GetSwipe(), message.GetObservationToken()); err != nil {
		return nil, err
	}
	target.Payload = swipePayload(message.GetSwipe())
	result, runErr := h.inputs.Run(ctx, target, actorType, actorID)
	if runErr != nil {
		return nil, mapDeviceInputError(runErr)
	}
	return connectrpc.NewResponse(&driftv1.SwipeResponse{Result: actionResultProto(result)}), nil
}

// KeyEvent submits one key event.
func (h *DeviceInputHandler) KeyEvent(ctx context.Context, request *connectrpc.Request[driftv1.KeyEventRequest]) (*connectrpc.Response[driftv1.KeyEventResponse], error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument("a key event request is required")
	}
	message := request.Msg
	actorType, actorID, err := requireActor(message.GetContext())
	if err != nil {
		return nil, err
	}
	target, err := deviceInputTargetFromRequest(message.GetContext(), message.GetWorkspace(), message.GetDeviceId(), message.GetLeaseId(), message.GetFencingToken(), message.GetIdempotencyKey(), message.GetObservationToken(), message.GetApprovalGranted(), action.SemanticTarget{})
	if err != nil {
		return nil, err
	}
	if err := validateKeyEventPayload(message.GetKeyEvent()); err != nil {
		return nil, err
	}
	target.Payload = execution.InputPayload{KeyEvent: &execution.KeyEventRequest{
		KeyCode: message.GetKeyEvent().GetKeyCode(),
		Repeat:  1,
	}}
	result, runErr := h.inputs.Run(ctx, target, actorType, actorID)
	if runErr != nil {
		return nil, mapDeviceInputError(runErr)
	}
	return connectrpc.NewResponse(&driftv1.KeyEventResponse{Result: actionResultProto(result)}), nil
}

// ready reports whether this handler was constructed with something to call.
//
// A handler with no dispatcher is never mounted, because a route that can only
// answer with a refusal is a control an operator surface would render and find
// dead. If one is invoked anyway, it answers unavailable rather than reporting
// anything that could be mistaken for an input reaching a device.
func (h *DeviceInputHandler) ready() error {
	if h == nil || h.inputs == nil {
		return connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "the device input surface is not constructed"})
	}
	return nil
}

// deviceInputTargetFromRequest validates what the caller must name and assembles
// the target. It deliberately checks only the two identifiers that say what the
// input is about.
//
// It does not check the lease, the fencing token, the observation token or the
// approval. Those are authority facts, and the kernel and the readiness probe own
// their refusal vocabulary: a handler that refused an absent lease here would
// replace `lease_missing` with a shape error, destroying the distinction ARC-62
// exists to provide. Shape is the transport's business; authority is not.
func deviceInputTargetFromRequest(
	requestContext *driftv1.RequestContext,
	workspace *driftv1.WorkspaceRef,
	deviceID, leaseID string,
	fencingToken uint64,
	idempotencyKey, observationToken string,
	approvalGranted bool,
	semantic action.SemanticTarget,
) (DeviceInputTarget, error) {
	workspaceID := ""
	if workspace != nil {
		workspaceID = strings.TrimSpace(workspace.GetWorkspaceId())
	}
	if workspaceID == "" {
		return DeviceInputTarget{}, invalidArgument("a device input requires a workspace")
	}
	if strings.TrimSpace(deviceID) == "" {
		return DeviceInputTarget{}, invalidArgument("a device input requires a device")
	}
	if strings.TrimSpace(idempotencyKey) == "" && requestContext != nil {
		idempotencyKey = requestContext.GetIdempotencyKey()
	}
	return DeviceInputTarget{
		Workspace:        workspaceID,
		DeviceID:         deviceID,
		LeaseID:          leaseID,
		FencingToken:     fencingToken,
		IdempotencyKey:   idempotencyKey,
		ObservationToken: observationToken,
		ApprovalGranted:  approvalGranted,
		Timeout:          deviceInputTimeout,
		Target:           semantic,
	}, nil
}

// tapPayload carries a validated tap into the dispatch contract. A tap that
// names a semantic target carries no coordinate; the target travels beside the
// payload, exactly as it does on the action intent.
func tapPayload(tap *driftv1.TapInput, observationToken string) execution.InputPayload {
	payload := execution.InputPayload{Tap: &execution.TapRequest{Space: execution.RenderSpace{ObservationToken: observationToken}}}
	if tap == nil {
		return payload
	}
	if point := tap.GetPoint(); point != nil {
		payload.Tap.Point = execution.Point{X: point.GetX(), Y: point.GetY()}
	}
	if space := tap.GetRenderSpace(); space != nil {
		payload.Tap.Space = execution.RenderSpace{
			Width:            space.GetRenderWidth(),
			Height:           space.GetRenderHeight(),
			ObservationToken: space.GetObservationToken(),
		}
	}
	return payload
}

// swipePayload carries a validated swipe into the dispatch contract.
func swipePayload(swipe *driftv1.SwipeInput) execution.InputPayload {
	payload := execution.InputPayload{Swipe: &execution.SwipeRequest{}}
	if swipe == nil {
		return payload
	}
	if start := swipe.GetStart(); start != nil {
		payload.Swipe.Start = execution.Point{X: start.GetX(), Y: start.GetY()}
	}
	if end := swipe.GetEnd(); end != nil {
		payload.Swipe.End = execution.Point{X: end.GetX(), Y: end.GetY()}
	}
	if space := swipe.GetRenderSpace(); space != nil {
		payload.Swipe.Space = execution.RenderSpace{
			Width:            space.GetRenderWidth(),
			Height:           space.GetRenderHeight(),
			ObservationToken: space.GetObservationToken(),
		}
	}
	payload.Swipe.DurationMS = swipe.GetDurationMs()
	return payload
}

// semanticTarget carries a semantic target from the contract into the dispatch
// contract. An absent target is the zero value: a tap that carries no semantic
// target is a coordinate tap, and the payload says so by carrying a point.
func semanticTarget(target *driftv1.SemanticTarget) action.SemanticTarget {
	if target == nil {
		return action.SemanticTarget{}
	}
	return action.SemanticTarget{
		ResourceID:         target.GetResourceId(),
		AccessibilityLabel: target.GetAccessibilityLabel(),
		StableText:         target.GetStableText(),
		ContextFingerprint: target.GetContextFingerprint(),
	}
}
