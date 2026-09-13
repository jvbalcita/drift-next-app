// Package organizations owns local workspace identity and lifecycle.
package organizations

import "drift.local/drift-next/internal/domain"

// WorkspaceID is the stable identity of one local installation scope.
type WorkspaceID string

// WorkspaceState is the lifecycle of a workspace.
type WorkspaceState string

const (
	WorkspaceActive    WorkspaceState = "active"
	WorkspaceSuspended WorkspaceState = "suspended"
	WorkspaceRetired   WorkspaceState = "retired"
)

// Workspace is the current projection for a local workspace.
type Workspace struct {
	ID         WorkspaceID
	Name       string
	State      WorkspaceState
	RowVersion uint64
}

// Valid reports whether the state is known.
func (s WorkspaceState) Valid() bool {
	switch s {
	case WorkspaceActive, WorkspaceSuspended, WorkspaceRetired:
		return true
	default:
		return false
	}
}

// CanTransition reports whether from may move to to.
func CanTransition(from, to WorkspaceState) bool {
	switch from {
	case WorkspaceActive:
		return to == WorkspaceSuspended || to == WorkspaceRetired
	case WorkspaceSuspended:
		return to == WorkspaceActive || to == WorkspaceRetired
	case WorkspaceRetired:
		return false
	default:
		return false
	}
}

func Transition(from, to WorkspaceState) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("workspace", string(from), string(to))
	}
	return nil
}
