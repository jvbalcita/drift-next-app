// Package groups owns device grouping and historical placement.
package groups

import (
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type GroupID string
type MembershipID string

type GroupState string

const (
	GroupActive  GroupState = "active"
	GroupRetired GroupState = "retired"
)

type MembershipState string

const (
	MembershipActive MembershipState = "active"
	MembershipEnded  MembershipState = "ended"
)

type Group struct {
	ID         GroupID
	Workspace  organizations.WorkspaceID
	Name       string
	State      GroupState
	Position   uint32
	RowVersion uint64
}

type Membership struct {
	ID        MembershipID
	Workspace organizations.WorkspaceID
	GroupID   GroupID
	DeviceID  devices.DeviceID
	Position  int64
	State     MembershipState
	StartedAt time.Time
	EndedAt   *time.Time
}

func (s GroupState) Valid() bool {
	return s == GroupActive || s == GroupRetired
}

func CanTransitionGroup(from, to GroupState) bool {
	return from == GroupActive && to == GroupRetired
}

func TransitionGroup(from, to GroupState) error {
	if !CanTransitionGroup(from, to) {
		return domain.InvalidTransition("device_group", string(from), string(to))
	}
	return nil
}

func (s MembershipState) Valid() bool {
	return s == MembershipActive || s == MembershipEnded
}

func CanTransitionMembership(from, to MembershipState) bool {
	return from == MembershipActive && to == MembershipEnded
}

func TransitionMembership(from, to MembershipState) error {
	if !CanTransitionMembership(from, to) {
		return domain.InvalidTransition("group_membership", string(from), string(to))
	}
	return nil
}

// MaxActivePlacements documents the reviewed cardinality enforced by the
// partial unique index in the SQLite schema.
const MaxActivePlacements = 1
