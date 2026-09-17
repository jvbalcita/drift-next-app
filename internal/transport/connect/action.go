package transportconnect

import (
	"context"
	"fmt"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type ActionExecutor interface {
	Run(ctx context.Context, intent action.Intent, actorType, actorID string) (action.Result, error)
}

type ActionHandler struct {
	db       *store.DB
	executor ActionExecutor
}

func NewActionHandler(db *store.DB) *ActionHandler { return &ActionHandler{db: db} }

func (h *ActionHandler) SetExecutor(executor ActionExecutor) {
	if h == nil {
		return
	}
	h.executor = executor
}

func (h *ActionHandler) SubmitAction(ctx context.Context, request *connectrpc.Request[driftv1.SubmitActionRequest]) (*connectrpc.Response[driftv1.SubmitActionResponse], error) {
	if request == nil {
		return nil, invalidArgument("submit action request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	intent, err := actionIntentFromProto(request.Msg.GetIntent(), request.Msg.GetContext(), actorID)
	if err != nil {
		return nil, err
	}
	if intent.ID == "" {
		id, idErr := newID(h.db)
		if idErr != nil {
			return nil, idErr
		}
		intent.ID = id
	}
	if _, err := lookupWorkspace(ctx, h.db, workspaceRef(organizations.WorkspaceID(intent.Workspace))); err != nil {
		return nil, err
	}
	result, authorizeErr := store.NewActionService(h.db).Authorize(ctx, intent, actorType, actorID)
	if authorizeErr != nil {
		return nil, MapError(authorizeErr)
	}
	if h.executor == nil || (result.IdempotentReplay && result.Attempt.State != action.AttemptAuthorized) {
		return connectrpc.NewResponse(&driftv1.SubmitActionResponse{Result: actionResultProto(result)}), nil
	}
	executed, executeErr := h.executor.Run(ctx, intent, actorType, actorID)
	if executeErr != nil {
		return nil, MapError(executeErr)
	}
	return connectrpc.NewResponse(&driftv1.SubmitActionResponse{Result: actionResultProto(executed)}), nil
}

func (h *ActionHandler) GetHalt(ctx context.Context, request *connectrpc.Request[driftv1.GetHaltRequest]) (*connectrpc.Response[driftv1.GetHaltResponse], error) {
	if request == nil || request.Msg.GetWorkspace() == nil {
		return nil, invalidArgument("workspace is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	halt, err := store.NewHaltService(h.db).Get(ctx, workspace)
	if err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.GetHaltResponse{Halt: haltStatusProto(halt)}), nil
}

func (h *ActionHandler) SetHalt(ctx context.Context, request *connectrpc.Request[driftv1.SetHaltRequest]) (*connectrpc.Response[driftv1.SetHaltResponse], error) {
	if request == nil || request.Msg.GetWorkspace() == nil {
		return nil, invalidArgument("workspace is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	state, err := haltStateFromProto(request.Msg.GetState())
	if err != nil {
		return nil, err
	}
	halt, err := store.NewHaltService(h.db).Set(ctx, workspace, state, request.Msg.GetReason(), actorType, actorID)
	if err != nil {
		return nil, MapError(err)
	}
	return connectrpc.NewResponse(&driftv1.SetHaltResponse{Halt: haltStatusProto(halt)}), nil
}

func haltStateFromProto(state driftv1.HaltState) (store.HaltState, error) {
	switch state {
	case driftv1.HaltState_HALT_STATE_CLEAR:
		return store.HaltClear, nil
	case driftv1.HaltState_HALT_STATE_EMERGENCY_STOP:
		return store.HaltEmergencyStop, nil
	default:
		return "", invalidArgument("halt state is required")
	}
}

func haltStatusProto(halt store.Halt) *driftv1.HaltStatus {
	state := driftv1.HaltState_HALT_STATE_CLEAR
	if halt.State == store.HaltEmergencyStop {
		state = driftv1.HaltState_HALT_STATE_EMERGENCY_STOP
	}
	return &driftv1.HaltStatus{Halt: &driftv1.ResourceRef{Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(halt.Workspace)}, ResourceId: halt.ID}, State: state, Reason: halt.Reason, UpdatedAt: halt.UpdatedAt.UTC().Format(time.RFC3339Nano), RowVersion: halt.RowVersion, LastActorType: halt.LastActorType, LastActorId: halt.LastActorID}
}

func actionIntentFromProto(msg *driftv1.ActionIntent, requestContext *driftv1.RequestContext, actorID string) (action.Intent, error) {
	var intent action.Intent
	if err := ValidateDeviceInputIntent(msg); err != nil {
		return intent, err
	}
	if msg.GetWorkspace() == nil || msg.GetWorkspace().GetWorkspaceId() == "" {
		return intent, invalidArgument("workspace ID is required")
	}
	key := msg.GetIdempotencyKey()
	if key == "" && requestContext != nil {
		key = requestContext.GetIdempotencyKey()
	}
	kind := actionKindFromProto(msg.GetKind())
	spec, ok := action.Lookup(kind)
	if !ok {
		return intent, invalidArgument("action kind is required")
	}
	intent = action.Intent{
		ID:                msg.GetId(),
		Workspace:         msg.GetWorkspace().GetWorkspaceId(),
		DeviceID:          msg.GetDeviceId(),
		LeaseID:           msg.GetLeaseId(),
		HolderID:          actorID,
		FencingToken:      msg.GetFencingToken(),
		Kind:              kind,
		ValueLength:       typedValueLength(msg),
		KeyCode:           typedKeyCode(msg),
		Launch:            typedLaunch(msg),
		IdempotencyKey:    key,
		ObservationToken:  msg.GetObservationToken(),
		InvocationSurface: action.SurfaceManual,
		Capabilities:      append([]action.Capability(nil), spec.RequiredCapabilities...),
		ApprovalGranted:   msg.GetApprovalGranted(),
		Timeout:           30 * time.Second,
	}
	// TextValue is deliberately never mapped: the published plaintext field is
	// deprecated, and typed text travels as an opaque SensitiveTextReference that
	// is resolved at dispatch through the boundary that owns the value.
	if target := msg.GetTarget(); target != nil {
		intent.Target = action.SemanticTarget{
			ResourceID:         target.GetResourceId(),
			AccessibilityLabel: target.GetAccessibilityLabel(),
			StableText:         target.GetStableText(),
			ContextFingerprint: target.GetContextFingerprint(),
		}
	}
	if swipe := msg.GetSwipe(); swipe != nil {
		intent.Gesture = &action.GesturePath{
			Points:     []action.Coordinate{renderCoordinate(swipe.GetStart(), swipe.GetRenderSpace()), renderCoordinate(swipe.GetEnd(), swipe.GetRenderSpace())},
			DurationMs: int64(swipe.GetDurationMs()),
		}
	}
	if tap := msg.GetTap(); tap != nil && tap.GetPoint() != nil {
		intent.CoordinateFallback = &action.CoordinateFallback{
			Start:     renderCoordinate(tap.GetPoint(), tap.GetRenderSpace()),
			Confirmed: true,
		}
	}
	return intent, nil
}

// typedValueLength and typedKeyCode read the typed payloads. The published flat
// value_length and key_code fields are not accepted for the typed input kinds:
// the contract requires the payload that belongs to the kind.
func typedValueLength(msg *driftv1.ActionIntent) int {
	if typed := msg.GetTypeText(); typed != nil {
		return int(typed.GetText().GetValueLength())
	}
	return 0
}

func typedKeyCode(msg *driftv1.ActionIntent) int {
	if keyEvent := msg.GetKeyEvent(); keyEvent != nil {
		return int(keyEvent.GetKeyCode())
	}
	return 0
}

// typedLaunch reads the typed launch payload. The target is carried into the
// intent so the request hash covers what the launch actually names: without it
// two launches of different packages hash alike, and the second is answered as
// a duplicate of the first.
func typedLaunch(msg *driftv1.ActionIntent) *action.LaunchTarget {
	launch := msg.GetLaunchApp()
	if launch == nil {
		return nil
	}
	return &action.LaunchTarget{PackageName: launch.GetPackageName(), ActivityName: launch.GetActivityName()}
}

// renderCoordinate places a point in the render space that travelled with it.
// The label follows the existing observation convention (`display:1080x1920`)
// and names the render frame, never a physical or downscaled frame.
func renderCoordinate(point *driftv1.DevicePoint, space *driftv1.DeviceRenderSpace) action.Coordinate {
	if point == nil || space == nil {
		return action.Coordinate{}
	}
	return action.Coordinate{
		Space: fmt.Sprintf("display:%dx%d", space.GetRenderWidth(), space.GetRenderHeight()),
		X:     int(point.GetX()),
		Y:     int(point.GetY()),
	}
}

func actionKindFromProto(kind driftv1.ActionKind) action.Kind {
	switch kind {
	case driftv1.ActionKind_ACTION_KIND_OBSERVE:
		return action.Observe
	case driftv1.ActionKind_ACTION_KIND_HEALTH_CHECK:
		return action.HealthCheck
	case driftv1.ActionKind_ACTION_KIND_CAPTURE:
		return action.Capture
	case driftv1.ActionKind_ACTION_KIND_TAP:
		return action.Tap
	case driftv1.ActionKind_ACTION_KIND_TEXT_INPUT:
		return action.TextInput
	case driftv1.ActionKind_ACTION_KIND_BACK:
		return action.Back
	case driftv1.ActionKind_ACTION_KIND_DOUBLE_TAP:
		return action.DoubleTap
	case driftv1.ActionKind_ACTION_KIND_LONG_PRESS:
		return action.LongPress
	case driftv1.ActionKind_ACTION_KIND_TEXT_DELETE:
		return action.TextDelete
	case driftv1.ActionKind_ACTION_KIND_CLEAR:
		return action.Clear
	case driftv1.ActionKind_ACTION_KIND_SWIPE:
		return action.Swipe
	case driftv1.ActionKind_ACTION_KIND_SCROLL:
		return action.Scroll
	case driftv1.ActionKind_ACTION_KIND_DRAG:
		return action.Drag
	case driftv1.ActionKind_ACTION_KIND_HOME:
		return action.Home
	case driftv1.ActionKind_ACTION_KIND_ENTER:
		return action.Enter
	case driftv1.ActionKind_ACTION_KIND_KEY_EVENT:
		return action.KeyEvent
	case driftv1.ActionKind_ACTION_KIND_LAUNCH_APP:
		return action.LaunchApp
	case driftv1.ActionKind_ACTION_KIND_UI_CHANGE:
		return action.UIChange
	case driftv1.ActionKind_ACTION_KIND_STATE_CHANGE:
		return action.StateChange
	default:
		return ""
	}
}

func actionResultProto(result action.Result) *driftv1.ActionResult {
	outcome := driftv1.ActionOutcome_ACTION_OUTCOME_UNSPECIFIED
	switch result.Outcome {
	case action.OutcomePending:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_PENDING
	case action.OutcomeVerified:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_VERIFIED
	case action.OutcomeFailed:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_FAILED
	case action.OutcomeCancelled:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_CANCELLED
	case action.OutcomeTimedOut:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_TIMED_OUT
	case action.OutcomeIndeterminate:
		outcome = driftv1.ActionOutcome_ACTION_OUTCOME_INDETERMINATE
	}
	return &driftv1.ActionResult{ActionId: result.Attempt.ID, Outcome: outcome}
}
