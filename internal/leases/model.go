// Package leases owns supervised control sessions and per-device fencing.
package leases

import (
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type ControlSessionID string
type DeviceLeaseID string

type ControlSessionState string

const (
	SessionRequested ControlSessionState = "requested"
	SessionActive    ControlSessionState = "active"
	SessionClosing   ControlSessionState = "closing"
	SessionClosed    ControlSessionState = "closed"
	SessionExpired   ControlSessionState = "expired"
	SessionRevoked   ControlSessionState = "revoked"
)

type DeviceLeaseState string

const (
	LeaseRequested DeviceLeaseState = "requested"
	LeaseActive    DeviceLeaseState = "active"
	LeaseReleased  DeviceLeaseState = "released"
	LeaseExpired   DeviceLeaseState = "expired"
	LeaseRevoked   DeviceLeaseState = "revoked"
)

type ControlSession struct {
	ID        ControlSessionID
	Workspace organizations.WorkspaceID
	HolderID  string
	State     ControlSessionState
	CreatedAt time.Time
	ExpiresAt time.Time
}

type DeviceLease struct {
	ID           DeviceLeaseID
	Workspace    organizations.WorkspaceID
	DeviceID     devices.DeviceID
	SessionID    ControlSessionID
	HolderID     string
	FencingToken uint64
	State        DeviceLeaseState
	AcquiredAt   time.Time
	ExpiresAt    time.Time
}

func (s ControlSessionState) Valid() bool {
	switch s {
	case SessionRequested, SessionActive, SessionClosing, SessionClosed, SessionExpired, SessionRevoked:
		return true
	default:
		return false
	}
}

func CanTransitionSession(from, to ControlSessionState) bool {
	switch from {
	case SessionRequested:
		return to == SessionActive || to == SessionClosed || to == SessionExpired || to == SessionRevoked
	case SessionActive:
		return to == SessionClosing || to == SessionExpired || to == SessionRevoked
	case SessionClosing:
		return to == SessionClosed || to == SessionExpired || to == SessionRevoked
	case SessionClosed, SessionExpired, SessionRevoked:
		return false
	default:
		return false
	}
}

func TransitionSession(from, to ControlSessionState) error {
	if !CanTransitionSession(from, to) {
		return domain.InvalidTransition("control_session", string(from), string(to))
	}
	return nil
}

func (s DeviceLeaseState) Valid() bool {
	switch s {
	case LeaseRequested, LeaseActive, LeaseReleased, LeaseExpired, LeaseRevoked:
		return true
	default:
		return false
	}
}

func CanTransitionLease(from, to DeviceLeaseState) bool {
	switch from {
	case LeaseRequested:
		return to == LeaseActive || to == LeaseReleased || to == LeaseExpired || to == LeaseRevoked
	case LeaseActive:
		return to == LeaseReleased || to == LeaseExpired || to == LeaseRevoked
	case LeaseReleased, LeaseExpired, LeaseRevoked:
		return false
	default:
		return false
	}
}

func TransitionLease(from, to DeviceLeaseState) error {
	if !CanTransitionLease(from, to) {
		return domain.InvalidTransition("device_lease", string(from), string(to))
	}
	return nil
}

const MaxActiveLeasesPerDevice = 1
