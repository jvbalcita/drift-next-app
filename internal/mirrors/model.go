// Package mirrors owns supervised source/follower session state. It does not
// combine follower leases or outcomes.
package mirrors

import "drift.local/drift-next/internal/domain"

type MirrorSessionID string
type MirrorTargetID string

type SessionState string

const (
	SessionRequested SessionState = "requested"
	SessionActive    SessionState = "active"
	SessionPaused    SessionState = "paused"
	SessionStopping  SessionState = "stopping"
	SessionCompleted SessionState = "completed"
	SessionFailed    SessionState = "failed"
	SessionCancelled SessionState = "cancelled"
)

type TargetState string

const (
	TargetPending       TargetState = "pending"
	TargetLeased        TargetState = "leased"
	TargetQueued        TargetState = "queued"
	TargetRunning       TargetState = "running"
	TargetSucceeded     TargetState = "succeeded"
	TargetFailed        TargetState = "failed"
	TargetCancelled     TargetState = "cancelled"
	TargetCleanupFailed TargetState = "cleanup_failed"
)

func (s SessionState) Valid() bool {
	switch s {
	case SessionRequested, SessionActive, SessionPaused, SessionStopping, SessionCompleted, SessionFailed, SessionCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionSession(from, to SessionState) bool {
	switch from {
	case SessionRequested:
		return to == SessionActive || to == SessionCancelled || to == SessionFailed
	case SessionActive:
		return to == SessionPaused || to == SessionStopping || to == SessionCompleted || to == SessionFailed || to == SessionCancelled
	case SessionPaused:
		return to == SessionActive || to == SessionStopping || to == SessionFailed || to == SessionCancelled
	case SessionStopping:
		return to == SessionCompleted || to == SessionFailed || to == SessionCancelled
	case SessionCompleted, SessionFailed, SessionCancelled:
		return false
	default:
		return false
	}
}

func TransitionSession(from, to SessionState) error {
	if !CanTransitionSession(from, to) {
		return domain.InvalidTransition("mirror_session", string(from), string(to))
	}
	return nil
}

func (s TargetState) Valid() bool {
	switch s {
	case TargetPending, TargetLeased, TargetQueued, TargetRunning, TargetSucceeded, TargetFailed, TargetCancelled, TargetCleanupFailed:
		return true
	default:
		return false
	}
}

func CanTransitionTarget(from, to TargetState) bool {
	switch from {
	case TargetPending:
		return to == TargetLeased || to == TargetFailed || to == TargetCancelled
	case TargetLeased:
		return to == TargetQueued || to == TargetFailed || to == TargetCancelled
	case TargetQueued:
		return to == TargetRunning || to == TargetFailed || to == TargetCancelled
	case TargetRunning:
		return to == TargetSucceeded || to == TargetFailed || to == TargetCancelled || to == TargetCleanupFailed
	case TargetSucceeded:
		return to == TargetCleanupFailed
	case TargetFailed, TargetCancelled, TargetCleanupFailed:
		return false
	default:
		return false
	}
}

func TransitionTarget(from, to TargetState) error {
	if !CanTransitionTarget(from, to) {
		return domain.InvalidTransition("mirror_target", string(from), string(to))
	}
	return nil
}
