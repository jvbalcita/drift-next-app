package transportconnect

import (
	"context"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

type ActionHandler struct{ db *store.DB }

func NewActionHandler(db *store.DB) *ActionHandler { return &ActionHandler{db: db} }

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
	return connectrpc.NewResponse(&driftv1.SubmitActionResponse{Result: actionResultProto(result)}), nil
}

func actionIntentFromProto(msg *driftv1.ActionIntent, requestContext *driftv1.RequestContext, actorID string) (action.Intent, error) {
	var intent action.Intent
	if msg == nil {
		return intent, invalidArgument("action intent is required")
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
		TextValue:         msg.GetTextValue(),
		ValueLength:       int(msg.GetValueLength()),
		KeyCode:           int(msg.GetKeyCode()),
		IdempotencyKey:    key,
		ObservationToken:  msg.GetObservationToken(),
		InvocationSurface: action.SurfaceManual,
		Capabilities:      append([]action.Capability(nil), spec.RequiredCapabilities...),
		ApprovalGranted:   msg.GetApprovalGranted(),
		Timeout:           30 * time.Second,
	}
	if target := msg.GetTarget(); target != nil {
		intent.Target = action.SemanticTarget{
			ResourceID:         target.GetResourceId(),
			AccessibilityLabel: target.GetAccessibilityLabel(),
			StableText:         target.GetStableText(),
			ContextFingerprint: target.GetContextFingerprint(),
		}
	}
	return intent, nil
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
