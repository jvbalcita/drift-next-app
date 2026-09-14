package policies

import "testing"

func TestPolicyValidationRejectsCredentialMaterial(t *testing.T) {
	policy := Policy{ID: "policy-1", Workspace: "workspace-1", Name: "Safety", Version: 1, RuleJSON: `{"allow":true}`, State: Draft}
	if err := policy.Validate(); err != nil {
		t.Fatalf("safe policy rejected: %v", err)
	}
	policy.RuleJSON = `{"token":"not-persisted"}`
	if err := policy.Validate(); err == nil {
		t.Fatal("policy containing credential material was accepted")
	}
}
