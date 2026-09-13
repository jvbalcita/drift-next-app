// Package devices owns stable device identity and its current lifecycle.
package devices

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type DeviceID string

type State string

const (
	Registered  State = "registered"
	Active      State = "active"
	Unavailable State = "unavailable"
	Retired     State = "retired"
)

type Device struct {
	ID              DeviceID
	Workspace       organizations.WorkspaceID
	DisplayName     string
	PlatformVersion string
	State           State
	LastSeenAt      *time.Time
	RowVersion      uint64
}

func (s State) Valid() bool {
	switch s {
	case Registered, Active, Unavailable, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Registered:
		return to == Active || to == Retired
	case Active:
		return to == Unavailable || to == Retired
	case Unavailable:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("device", string(from), string(to))
	}
	return nil
}
