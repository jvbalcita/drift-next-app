// Package edgeagents owns the lifecycle and capabilities of local runtimes.
package edgeagents

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type EdgeAgentID string

type State string

const (
	Pending   State = "pending"
	Active    State = "active"
	Unhealthy State = "unhealthy"
	Offline   State = "offline"
	Retired   State = "retired"
)

type EdgeAgent struct {
	ID          EdgeAgentID
	Workspace   organizations.WorkspaceID
	DisplayName string
	Version     string
	State       State
	LastSeenAt  *time.Time
	RowVersion  uint64
}

func (s State) Valid() bool {
	switch s {
	case Pending, Active, Unhealthy, Offline, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Pending:
		return to == Active || to == Retired
	case Active:
		return to == Unhealthy || to == Offline || to == Retired
	case Unhealthy:
		return to == Active || to == Offline || to == Retired
	case Offline:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("edge_agent", string(from), string(to))
	}
	return nil
}
