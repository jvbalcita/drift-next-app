// Package settings owns typed, explicitly scoped configuration projections.
package settings

import (
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type SettingID string

type Scope string

const (
	ScopeWorkspace          Scope = "workspace"
	ScopeControlPlane       Scope = "control_plane"
	ScopeEdgeHost           Scope = "edge_host"
	ScopeDevice             Scope = "device"
	ScopeAutomationAgent    Scope = "automation_agent"
	ScopeOperatorPreference Scope = "operator_preference"
)

type State string

const (
	Draft      State = "draft"
	Active     State = "active"
	Superseded State = "superseded"
	Retired    State = "retired"
)

type Setting struct {
	ID        SettingID
	Workspace organizations.WorkspaceID
	Scope     Scope
	TargetID  string
	Key       string
	ValueJSON string
	State     State
}

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Superseded, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Draft:
		return to == Active || to == Retired
	case Active:
		return to == Superseded || to == Retired
	case Superseded:
		return to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("setting", string(from), string(to))
	}
	return nil
}
