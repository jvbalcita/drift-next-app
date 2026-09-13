// Package networkprofiles owns bounded, operator-approved discovery policy.
package networkprofiles

import (
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type NetworkProfileID string

type State string

const (
	Draft    State = "draft"
	Active   State = "active"
	Disabled State = "disabled"
	Retired  State = "retired"
)

// NetworkProfile is deliberately bounded. CIDR/range and port validation is
// performed by the owning service before a profile can become active.
type NetworkProfile struct {
	ID            NetworkProfileID
	Workspace     organizations.WorkspaceID
	Name          string
	AddressPolicy string
	Ports         []uint16
	IsDefault     bool
	State         State
	RowVersion    uint64
}

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Disabled, Retired:
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
		return to == Disabled || to == Retired
	case Disabled:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("network_profile", string(from), string(to))
	}
	return nil
}
