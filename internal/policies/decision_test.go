package policies

import (
	"testing"

	"drift.local/drift-next/internal/action"
)

func tapEvaluation() ActionEvaluation {
	spec, _ := action.Lookup(action.Tap)
	return ActionEvaluation{Specification: spec, Invocation: action.SurfaceManual, Capabilities: []action.Capability{action.CapabilityTap}, ApprovalGranted: true}
}

func TestEvaluateAllowsSupportedApprovedAction(t *testing.T) {
	result := Evaluate(tapEvaluation())
	if result.Decision != Allow || result.Reason != ReasonAllowed {
		t.Fatalf("decision = %#v, want allow", result)
	}
}

func TestEvaluateRejectsEmergencyStopCapabilityAndApprovalFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ActionEvaluation)
		want   ReasonCode
	}{
		{name: "emergency", mutate: func(input *ActionEvaluation) { input.EmergencyStopped = true }, want: ReasonEmergencyStop},
		{name: "capability", mutate: func(input *ActionEvaluation) { input.Capabilities = nil }, want: ReasonCapabilityUnavailable},
		{name: "surface", mutate: func(input *ActionEvaluation) { input.Invocation = action.SurfaceAISuggestion }, want: ReasonInvocationNotAllowed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := tapEvaluation()
			test.mutate(&input)
			if got := Evaluate(input); got.Decision != Deny || got.Reason != test.want {
				t.Fatalf("decision = %#v, want deny/%q", got, test.want)
			}
		})
	}
	input := tapEvaluation()
	spec, _ := action.Lookup(action.TextInput)
	input.Specification, input.ApprovalGranted = spec, false
	input.Capabilities = []action.Capability{action.CapabilityTextInput}
	if got := Evaluate(input); got.Decision != Deny || got.Reason != ReasonApprovalRequired {
		t.Fatalf("high-risk decision = %#v, want approval_required", got)
	}
}

func TestEvaluateHonorsTypedPolicyRuleWithoutExecutingAnything(t *testing.T) {
	input := tapEvaluation()
	input.PolicyRuleJSON = `{"allow":false}`
	if got := Evaluate(input); got.Decision != Deny || got.Reason != ReasonPolicyDefinitionBlocked {
		t.Fatalf("policy decision = %#v, want policy_definition_blocked", got)
	}
}
