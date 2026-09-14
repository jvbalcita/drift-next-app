package recordings

import (
	"fmt"
	"strings"
	"time"
)

// Start moves a requested session into the active recording state.
func Start(session Session, at time.Time) (Session, error) {
	if at.IsZero() {
		return session, fmt.Errorf("recording start time is required")
	}
	if err := TransitionSession(session.State, SessionRecording); err != nil {
		return session, err
	}
	if session.StartedAt != nil || session.FinishedAt != nil || session.DeletedAt != nil {
		return session, fmt.Errorf("recording session has already started or finished")
	}
	at = at.UTC()
	session.State = SessionRecording
	session.StartedAt = &at
	return session, nil
}

// Stop moves a recording through the stopping boundary. The worker must flush
// its queue before calling this function's completed result.
func Stop(session Session, at time.Time) (Session, error) {
	if at.IsZero() {
		return session, fmt.Errorf("recording finish time is required")
	}
	if err := TransitionSession(session.State, SessionStopping); err != nil {
		return session, err
	}
	if session.StartedAt == nil || at.Before(*session.StartedAt) {
		return session, fmt.Errorf("recording finish must follow start")
	}
	at = at.UTC()
	session.State = SessionCompleted
	session.FinishedAt = &at
	return session, nil
}

// Fail records a terminal failure after preserving any events already saved.
func Fail(session Session, at time.Time) (Session, error) {
	if at.IsZero() {
		return session, fmt.Errorf("recording failure time is required")
	}
	if !CanTransitionSession(session.State, SessionFailed) {
		return session, fmt.Errorf("recording session cannot fail from %q", session.State)
	}
	at = at.UTC()
	if session.StartedAt == nil {
		session.StartedAt = &at
	}
	session.State = SessionFailed
	session.FinishedAt = &at
	return session, nil
}

// Discard closes a session without making its events executable. Cleanup is a
// separate explicit operation so artifact deletion cannot be hidden in a UI
// callback or a database cascade.
func Discard(session Session, at time.Time) (Session, error) {
	if at.IsZero() {
		return session, fmt.Errorf("discard time is required")
	}
	if !CanTransitionSession(session.State, SessionDiscarded) {
		return session, fmt.Errorf("recording session cannot be discarded from %q", session.State)
	}
	at = at.UTC()
	if session.StartedAt == nil {
		session.StartedAt = &at
	}
	session.State = SessionDiscarded
	session.FinishedAt = &at
	session.Cleanup = CleanupPending
	return session, nil
}

// DeleteCompleted requires an explicit confirmation and leaves a tombstone so
// source provenance and audit history remain explainable. It is intentionally
// separate from Discard.
func DeleteCompleted(session Session, confirmed bool, at time.Time) (Session, error) {
	if !confirmed {
		return session, fmt.Errorf("completed-session deletion requires explicit confirmation")
	}
	if at.IsZero() || (session.State != SessionCompleted && session.State != SessionDiscarded) || session.DeletedAt != nil {
		return session, fmt.Errorf("only an undeleted completed or discarded session can be deleted")
	}
	at = at.UTC()
	session.DeletedAt = &at
	session.Cleanup = CleanupPending
	return session, nil
}

func MarkCleanup(session Session, state CleanupState) (Session, error) {
	if !state.Valid() || session.DeletedAt == nil {
		return session, fmt.Errorf("deleted session and valid cleanup state are required")
	}
	if state == CleanupNone || state == CleanupPending {
		return session, fmt.Errorf("cleanup must finish as done or failed")
	}
	session.Cleanup = state
	return session, nil
}

func ReviewEvent(event InteractionEvent, approved bool, reviewer string, at time.Time) (InteractionEvent, error) {
	if event.Review != ReviewUnreviewed {
		return event, fmt.Errorf("recording event has already been reviewed")
	}
	if strings.TrimSpace(reviewer) == "" || at.IsZero() {
		return event, fmt.Errorf("reviewer and review time are required")
	}
	if approved {
		event.Review = ReviewApproved
	} else {
		event.Review = ReviewRejected
	}
	event.ReviewerID = reviewer
	at = at.UTC()
	event.ReviewedAt = &at
	return event, nil
}
