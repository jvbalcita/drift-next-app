package policies

import (
	"encoding/json"
	"strings"

	"drift.local/drift-next/internal/action"
)

type ReasonCode string

const (
	ReasonAllowed                 ReasonCode = "allowed"
	ReasonEmergencyStop           ReasonCode = "emergency_stop"
	ReasonInvocationNotAllowed    ReasonCode = "invocation_surface_not_allowed"
	ReasonCapabilityUnavailable   ReasonCode = "capability_unavailable"
	ReasonApprovalRequired        ReasonCode = "approval_required"
	ReasonPolicyDefinitionBlocked ReasonCode = "policy_definition_blocked"
)

type ActionEvaluation struct {
	Specification    action.Specification
	Invocation       action.InvocationSurface
	Capabilities     []action.Capability
	ApprovalGranted  bool
	EmergencyStopped bool
	PolicyRuleJSON   string
}

type DecisionResult struct {
	Decision Decision
	Reason   ReasonCode
	Message  string
}

// Evaluate is pure and fail-closed. Persistence and audit of the result are
// owned by the application adapter.
func Evaluate(input ActionEvaluation) DecisionResult {
	if input.EmergencyStopped {
		return DecisionResult{Decision: Deny, Reason: ReasonEmergencyStop, Message: "emergency stop is active"}
	}
	if !action.ContainsSurface(input.Specification.AllowedSurfaces, input.Invocation) {
		return DecisionResult{Decision: Deny, Reason: ReasonInvocationNotAllowed, Message: "invocation surface is not permitted"}
	}
	if !action.Supports(input.Capabilities, input.Specification.RequiredCapabilities) {
		return DecisionResult{Decision: Deny, Reason: ReasonCapabilityUnavailable, Message: "required capability is unavailable"}
	}
	if (input.Specification.Risk == action.RiskHigh || input.Specification.Risk == action.RiskIrreversible) && !input.ApprovalGranted {
		return DecisionResult{Decision: Deny, Reason: ReasonApprovalRequired, Message: "explicit approval is required for this action risk"}
	}
	if strings.TrimSpace(input.PolicyRuleJSON) != "" {
		var rule struct {
			Allow *bool `json:"allow"`
		}
		if err := json.Unmarshal([]byte(input.PolicyRuleJSON), &rule); err != nil || (rule.Allow != nil && !*rule.Allow) {
			return DecisionResult{Decision: Deny, Reason: ReasonPolicyDefinitionBlocked, Message: "active policy denies this action"}
		}
	}
	return DecisionResult{Decision: Allow, Reason: ReasonAllowed, Message: "action is allowed"}
}
