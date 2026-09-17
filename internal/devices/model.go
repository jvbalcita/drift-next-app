// Package devices owns stable device identity and its observation history.
//
// It does NOT own a device lifecycle. The registered/active/unavailable/retired
// machine below was ruled out by ARC-116 - a device has no lifecycle beyond its
// identity and its observation history - and while the column and the vocabulary
// survive in the schema, nothing advances a device through it: the wire status is
// derived from observation facts (internal/transport/connect/device.go
// deviceStatusProto), the post-launch watcher records what it observes and leaves
// what it no longer observes, and the only remaining reader of State is a
// projection that must not turn it into a reachability claim again.
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

// CanTransition and Transition are PARKED AS UNREACHABLE, deliberately not
// deleted, and the reason is recorded here so a reader meets it beside the code
// rather than in a card:
//
// ARC-116 closed as a decision with NO CODE: a device has no lifecycle beyond its
// identity and its observation history, so there is no state for an operator to
// advance a device into and no transition to authorize. Nothing in the product
// calls these - no RPC, no handler, no page, no service - and the wire status no
// longer reads devices.State at all (internal/transport/connect/device.go
// deviceStatusProto reads observation facts instead). Their only remaining callers
// are two tests; deleting them here would remove coverage of workspace isolation
// and of the domain state-machine contract in the same change that removed the
// last reader, which is a worse trade than parking them in the open.
//
// Using these to build a device lifecycle again is not a wiring task: it re-opens
// a decision this repository recorded, and it must be raised as one.
//
// A machine that is a contract and has no producer is still the thing the ruling
// was about. Do not grow it.
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
