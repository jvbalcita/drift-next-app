package automationagents_test

import (
	"errors"
	"testing"

	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/domain"
)

func TestPublishedProfileCannotBeRewritten(t *testing.T) {
	legal := [][2]automationagents.ProfileState{
		{automationagents.ProfileDraft, automationagents.ProfileValidated},
		{automationagents.ProfileValidated, automationagents.ProfilePublished},
		{automationagents.ProfilePublished, automationagents.ProfileDeprecated},
		{automationagents.ProfileDeprecated, automationagents.ProfileRetired},
	}
	for _, edge := range legal {
		if err := automationagents.TransitionProfile(edge[0], edge[1]); err != nil {
			t.Fatalf("TransitionProfile(%q, %q) = %v, want nil", edge[0], edge[1], err)
		}
	}

	for _, edge := range [][2]automationagents.ProfileState{
		{automationagents.ProfilePublished, automationagents.ProfileDraft},
		{automationagents.ProfilePublished, automationagents.ProfileValidated},
		{automationagents.ProfileRetired, automationagents.ProfilePublished},
	} {
		err := automationagents.TransitionProfile(edge[0], edge[1])
		var transitionErr *domain.TransitionError
		if !errors.As(err, &transitionErr) {
			t.Fatalf("TransitionProfile(%q, %q) = %v, want TransitionError", edge[0], edge[1], err)
		}
	}
}

func TestAutomationAgentAssignmentCardinality(t *testing.T) {
	if automationagents.MaxActivePublishedProfilesPerAgent != 1 {
		t.Fatalf("MaxActivePublishedProfilesPerAgent = %d, want 1", automationagents.MaxActivePublishedProfilesPerAgent)
	}
	// Assignment cardinality is enforced by the assignments domain and the
	// SQLite partial unique index; one agent may still own many devices.
	if automationagents.AgentActive == automationagents.AgentRetired {
		t.Fatal("active and retired states unexpectedly share a value")
	}
}
