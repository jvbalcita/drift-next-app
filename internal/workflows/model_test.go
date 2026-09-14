package workflows

import (
	"testing"

	"drift.local/drift-next/internal/action"
)

func validVersion() Version {
	return Version{
		ID:         "version-1",
		Workspace:  "workspace-1",
		WorkflowID: "workflow-1",
		Version:    1,
		State:      StateDraft,
		Steps: []Step{
			{ID: "step-1", Sequence: 0, Action: action.Observe, Risk: action.RiskLow, Retry: action.RetrySafe, Definition: StepDefinition{TimeoutMillis: 1000, EvidenceRequired: true}},
			{ID: "step-2", Sequence: 1, Action: action.Tap, Risk: action.RiskMedium, Retry: action.RetryAfterObservation, Definition: StepDefinition{Target: action.SemanticTarget{ResourceID: "continue"}, TimeoutMillis: 1000, RequiresObservation: true, EvidenceRequired: true, Postcondition: "screen_changed"}},
		},
	}
}

func TestVersionValidateRequiresTypedContiguousSteps(t *testing.T) {
	version := validVersion()
	if err := version.Validate(); err != nil {
		t.Fatalf("valid version rejected: %v", err)
	}

	version.Steps[1].Sequence = 2
	if err := version.Validate(); err == nil {
		t.Fatal("non-contiguous workflow steps were accepted")
	}

	version = validVersion()
	version.Steps[1].Risk = action.RiskLow
	if err := version.Validate(); err == nil {
		t.Fatal("step risk mismatch was accepted")
	}
}

func TestVersionValidateRejectsUnsafeOrIncompleteStepDefinitions(t *testing.T) {
	version := validVersion()
	version.Steps[1].Definition.Target = action.SemanticTarget{}
	if err := version.Validate(); err == nil {
		t.Fatal("target-requiring step without a target was accepted")
	}

	version = validVersion()
	version.Steps[0].Definition.EvidenceRequired = false
	if err := version.Validate(); err == nil {
		t.Fatal("step without required evidence was accepted")
	}

	version = validVersion()
	version.Steps[1].Definition.Postcondition = `{"token":"secret"}`
	if err := version.Validate(); err == nil {
		t.Fatal("credential-shaped step definition was accepted")
	}
}

func TestWorkflowStateTransitionsAreForwardOnly(t *testing.T) {
	if err := Transition(StateDraft, StateValidated); err != nil {
		t.Fatal(err)
	}
	if err := Transition(StatePublished, StateDraft); err == nil {
		t.Fatal("published workflow reverted to draft")
	}
	if err := Transition(StatePublished, StateDeprecated); err != nil {
		t.Fatal(err)
	}
}
