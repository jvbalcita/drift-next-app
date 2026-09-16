package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/policies"
	store "drift.local/drift-next/internal/store/sqlite"
)

type PolicyHandler struct{ db *store.DB }

func NewPolicyHandler(db *store.DB) *PolicyHandler { return &PolicyHandler{db: db} }

func (h *PolicyHandler) ListPolicies(ctx context.Context, request *connectrpc.Request[driftv1.ListPoliciesRequest]) (*connectrpc.Response[driftv1.ListPoliciesResponse], error) {
	if request == nil {
		return nil, invalidArgument("list policies request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewPolicyRepository(h.db).List(ctx, workspace)
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.Policy, 0, len(page))
	for _, policy := range page {
		out = append(out, policyProto(policy))
	}
	return connectrpc.NewResponse(&driftv1.ListPoliciesResponse{Policies: out, Page: pageResponse(next)}), nil
}

func (h *PolicyHandler) CreatePolicyVersion(ctx context.Context, request *connectrpc.Request[driftv1.CreatePolicyVersionRequest]) (*connectrpc.Response[driftv1.CreatePolicyVersionResponse], error) {
	if request == nil {
		return nil, invalidArgument("create policy version request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetBasePolicy())
	if err != nil {
		return nil, err
	}
	stored, createErr := store.NewPolicyService(h.db).CreateNextVersion(ctx, workspace, policies.PolicyID(id), request.Msg.GetRuleJson(), actorType, actorID)
	if createErr != nil {
		return nil, MapError(createErr)
	}
	return connectrpc.NewResponse(&driftv1.CreatePolicyVersionResponse{Policy: policyProto(stored)}), nil
}

func (h *PolicyHandler) ActivatePolicy(ctx context.Context, request *connectrpc.Request[driftv1.ActivatePolicyRequest]) (*connectrpc.Response[driftv1.ActivatePolicyResponse], error) {
	if request == nil {
		return nil, invalidArgument("activate policy request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetPolicy())
	if err != nil {
		return nil, err
	}
	if expected := request.Msg.GetExpectedRowVersion(); expected != 0 {
		current, getErr := store.NewPolicyRepository(h.db).Get(ctx, workspace, policies.PolicyID(id))
		if getErr != nil {
			return nil, MapError(getErr)
		}
		if current.RowVersion != expected {
			return nil, MapError(platformerrors.New(platformerrors.CodeConflict, "policy changed before activation"))
		}
	}
	stored, activateErr := store.NewPolicyService(h.db).Activate(ctx, workspace, policies.PolicyID(id), actorType, actorID)
	if activateErr != nil {
		return nil, MapError(activateErr)
	}
	return connectrpc.NewResponse(&driftv1.ActivatePolicyResponse{Policy: policyProto(stored)}), nil
}

func (h *PolicyHandler) RetirePolicy(ctx context.Context, request *connectrpc.Request[driftv1.RetirePolicyRequest]) (*connectrpc.Response[driftv1.RetirePolicyResponse], error) {
	if request == nil {
		return nil, invalidArgument("retire policy request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, id, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetPolicy())
	if err != nil {
		return nil, err
	}
	stored, retireErr := store.NewPolicyService(h.db).Retire(ctx, workspace, policies.PolicyID(id), request.Msg.GetExpectedRowVersion(), actorType, actorID)
	if retireErr != nil {
		return nil, MapError(retireErr)
	}
	return connectrpc.NewResponse(&driftv1.RetirePolicyResponse{Policy: policyProto(stored)}), nil
}

func (h *PolicyHandler) ListPolicyDecisions(ctx context.Context, request *connectrpc.Request[driftv1.ListPolicyDecisionsRequest]) (*connectrpc.Response[driftv1.ListPolicyDecisionsResponse], error) {
	if request == nil {
		return nil, invalidArgument("list policy decisions request is required")
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	offset, limit, err := parsePage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	listed, listErr := store.NewPolicyRepository(h.db).ListDecisions(ctx, workspace, request.Msg.GetResourceType())
	if listErr != nil {
		return nil, MapError(listErr)
	}
	page, next := applyPage(listed, offset, limit)
	out := make([]*driftv1.PolicyDecisionRecord, 0, len(page))
	for _, decision := range page {
		out = append(out, policyDecisionProto(decision))
	}
	return connectrpc.NewResponse(&driftv1.ListPolicyDecisionsResponse{Decisions: out, Page: pageResponse(next)}), nil
}

func policyProto(policy policies.Policy) *driftv1.Policy {
	return &driftv1.Policy{
		Id:          string(policy.ID),
		Workspace:   workspaceRef(policy.Workspace),
		DisplayName: policy.Name,
		Version:     uint32(policy.Version),
		State:       string(policy.State),
		RuleJson:    policy.RuleJSON,
		RowVersion:  policy.RowVersion,
		CreatedAt:   formatTime(policy.CreatedAt),
		UpdatedAt:   formatTime(policy.UpdatedAt),
	}
}

func policyDecisionProto(decision policies.PolicyDecision) *driftv1.PolicyDecisionRecord {
	value := driftv1.PolicyDecision_POLICY_DECISION_UNSPECIFIED
	switch decision.Decision {
	case policies.Allow:
		value = driftv1.PolicyDecision_POLICY_DECISION_ALLOW
	case policies.Deny:
		value = driftv1.PolicyDecision_POLICY_DECISION_DENY
	case policies.Inconclusive:
		value = driftv1.PolicyDecision_POLICY_DECISION_INCONCLUSIVE
	}
	return &driftv1.PolicyDecisionRecord{
		Id:            string(decision.ID),
		PolicyId:      string(decision.PolicyID),
		ResourceType:  decision.ResourceType,
		ResourceId:    decision.ResourceID,
		Action:        decision.Action,
		Decision:      value,
		ReasonCode:    decision.ReasonCode,
		CorrelationId: decision.CorrelationID,
		DecidedAt:     formatTime(decision.DecidedAt),
		ActorId:       decision.ActorID,
	}
}
