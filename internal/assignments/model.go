// Package assignments owns historical relationships between devices and
// runtimes or logical automation agents.
package assignments

import (
	"time"

	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/organizations"
)

type EdgeBindingID string
type AutomationAssignmentID string

type State string

const (
	Active State = "active"
	Ended  State = "ended"
)

type EdgeBinding struct {
	ID          EdgeBindingID
	Workspace   organizations.WorkspaceID
	DeviceID    devices.DeviceID
	EdgeAgentID edgeagents.EdgeAgentID
	State       State
	BoundAt     time.Time
	EndedAt     *time.Time
}

type AutomationAssignment struct {
	ID                AutomationAssignmentID
	Workspace         organizations.WorkspaceID
	DeviceID          devices.DeviceID
	AutomationAgentID automationagents.AutomationAgentID
	ProfileID         automationagents.ProfileID
	State             State
	AssignedAt        time.Time
	EndedAt           *time.Time
	Precedence        int
}

func (s State) Valid() bool { return s == Active || s == Ended }

func CanTransition(from, to State) bool {
	return from == Active && to == Ended
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("assignment", string(from), string(to))
	}
	return nil
}

const MaxActiveAssignmentsPerDevice = 1
const MaxActiveBindingsPerDevice = 1
