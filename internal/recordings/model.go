// Package recordings owns the local interaction-recorder session boundary.
package recordings

import "drift.local/drift-next/internal/domain"

type RecordingSessionID string
type RecordingEventID string

type SessionState string

const (
	SessionRequested SessionState = "requested"
	SessionRecording SessionState = "recording"
	SessionStopping  SessionState = "stopping"
	SessionCompleted SessionState = "completed"
	SessionDiscarded SessionState = "discarded"
	SessionFailed    SessionState = "failed"
)

type RedactionState string

const (
	RedactionNotRequired RedactionState = "not_required"
	RedactionRequired    RedactionState = "required"
	RedactionRedacted    RedactionState = "redacted"
	RedactionRejected    RedactionState = "rejected"
)

func (s SessionState) Valid() bool {
	switch s {
	case SessionRequested, SessionRecording, SessionStopping, SessionCompleted, SessionDiscarded, SessionFailed:
		return true
	default:
		return false
	}
}

func CanTransitionSession(from, to SessionState) bool {
	switch from {
	case SessionRequested:
		return to == SessionRecording || to == SessionDiscarded || to == SessionFailed
	case SessionRecording:
		return to == SessionStopping || to == SessionDiscarded || to == SessionFailed
	case SessionStopping:
		return to == SessionCompleted || to == SessionDiscarded || to == SessionFailed
	case SessionCompleted, SessionDiscarded, SessionFailed:
		return false
	default:
		return false
	}
}

func TransitionSession(from, to SessionState) error {
	if !CanTransitionSession(from, to) {
		return domain.InvalidTransition("recording_session", string(from), string(to))
	}
	return nil
}
