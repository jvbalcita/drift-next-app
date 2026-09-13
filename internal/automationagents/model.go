// Package automationagents owns logical automation-agent identity and its
// immutable, versioned profiles. It is separate from edge-agent runtime
// identity and from device assignment history.
package automationagents

import (
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type AutomationAgentID string
type ProfileID string

type AgentState string

const (
	AgentActive    AgentState = "active"
	AgentSuspended AgentState = "suspended"
	AgentRetired   AgentState = "retired"
)

type ProfileState string

const (
	ProfileDraft      ProfileState = "draft"
	ProfileValidated  ProfileState = "validated"
	ProfilePublished  ProfileState = "published"
	ProfileDeprecated ProfileState = "deprecated"
	ProfileRetired    ProfileState = "retired"
)

type MemoryScope string

const (
	MemoryNone      MemoryScope = "none"
	MemoryWorkspace MemoryScope = "workspace"
	MemoryAgent     MemoryScope = "agent"
)

type AutomationAgent struct {
	ID        AutomationAgentID
	Workspace organizations.WorkspaceID
	Name      string
	State     AgentState
}

type Profile struct {
	ID                   ProfileID
	AutomationAgentID    AutomationAgentID
	Workspace            organizations.WorkspaceID
	Version              int
	State                ProfileState
	Personality          string
	Goals                []string
	Rules                []string
	Capabilities         []string
	MemoryScope          MemoryScope
	MemoryRetentionClass string
}

func (s AgentState) Valid() bool {
	switch s {
	case AgentActive, AgentSuspended, AgentRetired:
		return true
	default:
		return false
	}
}

func CanTransitionAgent(from, to AgentState) bool {
	switch from {
	case AgentActive:
		return to == AgentSuspended || to == AgentRetired
	case AgentSuspended:
		return to == AgentActive || to == AgentRetired
	case AgentRetired:
		return false
	default:
		return false
	}
}

func TransitionAgent(from, to AgentState) error {
	if !CanTransitionAgent(from, to) {
		return domain.InvalidTransition("automation_agent", string(from), string(to))
	}
	return nil
}

func (s ProfileState) Valid() bool {
	switch s {
	case ProfileDraft, ProfileValidated, ProfilePublished, ProfileDeprecated, ProfileRetired:
		return true
	default:
		return false
	}
}

func CanTransitionProfile(from, to ProfileState) bool {
	switch from {
	case ProfileDraft:
		return to == ProfileValidated || to == ProfileRetired
	case ProfileValidated:
		return to == ProfileDraft || to == ProfilePublished || to == ProfileRetired
	case ProfilePublished:
		return to == ProfileDeprecated || to == ProfileRetired
	case ProfileDeprecated:
		return to == ProfileRetired
	case ProfileRetired:
		return false
	default:
		return false
	}
}

func TransitionProfile(from, to ProfileState) error {
	if !CanTransitionProfile(from, to) {
		return domain.InvalidTransition("automation_agent_profile", string(from), string(to))
	}
	return nil
}

const MaxActivePublishedProfilesPerAgent = 1
