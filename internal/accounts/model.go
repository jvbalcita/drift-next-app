// Package accounts owns non-secret account references only.
package accounts

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type AccountID string
type AccountSourceID string

type State string

const (
	Draft    State = "draft"
	Active   State = "active"
	Inactive State = "inactive"
	Retired  State = "retired"
)

type ServiceState string

const (
	ServiceUnknown  ServiceState = "unknown"
	ServiceHealthy  ServiceState = "healthy"
	ServiceDegraded ServiceState = "degraded"
	ServiceFailed   ServiceState = "failed"
	ServiceDisabled ServiceState = "disabled"
)

type Account struct {
	ID          AccountID
	Workspace   organizations.WorkspaceID
	SourceID    AccountSourceID
	ExternalRef string
	Label       string
	State       State
	CreatedAt   time.Time
}

func (s State) Valid() bool {
	switch s {
	case Draft, Active, Inactive, Retired:
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
		return to == Inactive || to == Retired
	case Inactive:
		return to == Active || to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("account", string(from), string(to))
	}
	return nil
}
