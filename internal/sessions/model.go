// Package sessions exposes the supervised control-session vocabulary.
package sessions

import "drift.local/drift-next/internal/leases"

type ID = leases.ControlSessionID
type State = leases.ControlSessionState
type ControlSession = leases.ControlSession

const (
	Requested State = leases.SessionRequested
	Active    State = leases.SessionActive
	Closing   State = leases.SessionClosing
	Closed    State = leases.SessionClosed
	Expired   State = leases.SessionExpired
	Revoked   State = leases.SessionRevoked
)

func CanAuthorize(state State, expiresAtUnix int64, nowUnix int64) bool {
	return state == Active && expiresAtUnix > nowUnix
}
