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

// StreamRefusal is one live stream this plane refused to open, in the terms an
// event row needs.
//
// It exists because a refusal has to be explainable from the plane's own record
// and not only from whatever the console that asked kept: a frame that shows
// nothing is the failure this whole surface exists to avoid, and a later reader of
// the database - the next operator, an incident review, the person reading this
// repo - has only the rows. What it carries is therefore the whole of the
// refusal: which device, what asked for it, what the plane's own sentence was, and
// the bound that was spent.
//
// It carries no claim about authority and no content: a live mirror is a viewer,
// viewing confers no authority to act on a device, and nothing here names a
// control session, a lease or a credential.
type StreamRefusal struct {
	WorkspaceID string
	DeviceID    string
	// ActorType and ActorID are who asked for the stream.
	ActorType string
	ActorID   string
	// ViewerPurpose is what the refused viewer was, in the plane's own
	// vocabulary: "operator" for the operator's own big frame, "ambient" for one
	// of the console's grid tiles. It is what the bound was spent against, so a
	// refusal read later says whether the grid spent the operator's place or the
	// plane was simply full.
	ViewerPurpose string
	// Reason is the plane's own refusal sentence, verbatim.
	Reason string
	// Capacity is the device-session capacity the plane is carrying.
	Capacity int
	// OperatorReserve is how many of Capacity are kept for the operator's own
	// frame. It is zero for an operator request, which is refused only when there
	// is genuinely no place at all.
	OperatorReserve int
}
