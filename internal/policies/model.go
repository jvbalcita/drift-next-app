// Package policies owns typed safety policy versions and their decisions.
package policies

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type PolicyID string
type PolicyDecisionID string

type State string

const (
	Draft      State = "draft"
	Active     State = "active"
	Superseded State = "superseded"
	Retired    State = "retired"
)

type Decision string

const (
	Allow        Decision = "allow"
	Deny         Decision = "deny"
	Inconclusive Decision = "inconclusive"
)

type Policy struct {
	ID        PolicyID
	Workspace organizations.WorkspaceID
	Name      string
	Version   int
	RuleJSON  string
	State     State
}

type PolicyDecision struct {
	ID           PolicyDecisionID
	Workspace    organizations.WorkspaceID
	PolicyID     PolicyID
	ResourceType string
	ResourceID   string
	Action       string
	Decision     Decision
	ReasonCode   string
	DecidedAt    time.Time
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
		return domain.InvalidTransition("policy", string(from), string(to))
	}
	return nil
}
