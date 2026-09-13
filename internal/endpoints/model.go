// Package endpoints owns mutable transport observations and endpoint history.
package endpoints

import (
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type EndpointID string

type State string

const (
	Observed   State = "observed"
	Current    State = "current"
	Superseded State = "superseded"
	Retired    State = "retired"
)

type Endpoint struct {
	ID         EndpointID
	Workspace  organizations.WorkspaceID
	DeviceID   devices.DeviceID
	Serial     string
	Host       string
	Port       uint16
	State      State
	ObservedAt time.Time
}

func (s State) Valid() bool {
	switch s {
	case Observed, Current, Superseded, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Observed:
		return to == Current || to == Superseded || to == Retired
	case Current:
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
		return domain.InvalidTransition("endpoint", string(from), string(to))
	}
	return nil
}
