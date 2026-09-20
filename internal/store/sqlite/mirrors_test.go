package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/mirrors"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A mirror session that outlives its session is a record an operator and a later
// reader cannot trust: it reports running work that is not running. StopPreview
// was the only path that ended one, so a session whose work failed, was aborted,
// or was left open by a process that died stayed 'active' with no finished_at and
// no reason recorded anywhere. These are the endings, and the reason each one
// leaves behind.

// storedSessionState reads the row itself, so an assertion cannot pass on what
// the service returned while the table says something else.
func storedSessionState(t *testing.T, db *store.DB, sessionID string) (string, sql.NullString) {
	t.Helper()
	var state string
	var finished sql.NullString
	if err := store.SQLForTest(db).QueryRow(
		`SELECT state, finished_at FROM mirror_sessions WHERE workspace_id=? AND id=?`,
		mirrorEventWorkspace, sessionID).Scan(&state, &finished); err != nil {
		t.Fatalf("read mirror session %s: %v", sessionID, err)
	}
	return state, finished
}

// sessionEndEvent reads the ending recorded beside a session, which is where the
// reason belongs: the sessions table states what a session is, and the mirror
// event beside it states why.
func sessionEndEvent(t *testing.T, db *store.DB, sessionID string) map[string]any {
	t.Helper()
	var payload string
	err := store.SQLForTest(db).QueryRow(
		`SELECT payload_json FROM mirror_events WHERE workspace_id=? AND mirror_session_id=? AND event_name=? ORDER BY occurred_at, id LIMIT 1`,
		mirrorEventWorkspace, sessionID, "mirror.session_ended").Scan(&payload)
	if err == sql.ErrNoRows {
		t.Fatalf("session %s ended with no reason recorded beside it", sessionID)
	}
	if err != nil {
		t.Fatalf("read the session's ending: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("the ending's payload is not readable: %v", err)
	}
	return decoded
}

func unfinishedTargets(t *testing.T, db *store.DB, sessionID string) int {
	t.Helper()
	var open int
	if err := store.SQLForTest(db).QueryRow(
		`SELECT count(*) FROM mirror_targets WHERE workspace_id=? AND mirror_session_id=? AND state IN ('pending','leased','queued','running')`,
		mirrorEventWorkspace, sessionID).Scan(&open); err != nil {
		t.Fatalf("count unfinished targets: %v", err)
	}
	return open
}

// TestTheOperatorsOwnStopEndsASessionWithThePlaneOwnClass: the stop is one ending
// among several, and it now states the class of that end - the operator's own
// departure, which is an ENDING and not a failure - beside its sentence.
func TestTheOperatorsOwnStopEndsASessionWithThePlaneOwnClass(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)

	session, err := store.NewMirrorService(db).StopPreview(context.Background(), mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID), "operator", "op-1")
	if err != nil {
		t.Fatalf("StopPreview: %v", err)
	}
	if session.State != mirrors.SessionCompleted || session.FinishedAt == "" {
		t.Fatalf("stopping left %#v, want a completed session with a finish", session)
	}
	state, finished := storedSessionState(t, db, sessionID)
	if state != "completed" || !finished.Valid {
		t.Fatalf("the row says %q finished=%v, want the completed session the caller was told", state, finished.Valid)
	}
	ending := sessionEndEvent(t, db, sessionID)
	if got, _ := ending["class"].(string); got != string(media.MirrorEndViewerDetached) {
		t.Fatalf("the ending states class %q, want the operator's own departure", got)
	}
	if got, _ := ending["reason"].(string); got != media.MirrorEndViewerDetached.Sentence() {
		t.Fatalf("the ending states %q, want the class's own sentence", got)
	}
	if got, _ := ending["cause"].(string); got != string(store.PreviewEndOperatorStop) {
		t.Fatalf("the ending states cause %q, want the operator's stop", got)
	}
	if open := unfinishedTargets(t, db, sessionID); open != 0 {
		t.Fatalf("%d follower target(s) are still unfinished after the session ended", open)
	}
}

// TestASessionCanEndAsFailedWithTheClassOfItsFailure: the ending StopPreview never
// had. A session whose work fails is left failed, and the record says which
// failure it was rather than leaving the reader to guess from a generic sentence.
func TestASessionCanEndAsFailedWithTheClassOfItsFailure(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)

	session, err := store.NewMirrorService(db).EndPreview(context.Background(), mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID),
		store.PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndDeviceServerFailed, Cause: store.PreviewEndOwnWork}, "operator", "op-1")
	if err != nil {
		t.Fatalf("EndPreview: %v", err)
	}
	if session.State != mirrors.SessionFailed || session.FinishedAt == "" {
		t.Fatalf("the failed ending left %#v, want a failed session with a finish", session)
	}
	ending := sessionEndEvent(t, db, sessionID)
	if got, _ := ending["class"].(string); got != string(media.MirrorEndDeviceServerFailed) {
		t.Fatalf("the ending states class %q, want the device-side server's own failure", got)
	}
	if got, _ := ending["state"].(string); got != string(mirrors.SessionFailed) {
		t.Fatalf("the ending states state %q, want the state the row is left in", got)
	}
	// The rows are history: an ended session is a state, never a deletion.
	var sessions int
	if err := store.SQLForTest(db).QueryRow(`SELECT count(*) FROM mirror_sessions WHERE workspace_id=?`, mirrorEventWorkspace).Scan(&sessions); err != nil {
		t.Fatalf("count mirror sessions: %v", err)
	}
	if sessions != 1 {
		t.Fatalf("%d mirror session row(s) exist, want the ended one kept", sessions)
	}
}

// TestAnEndCannotContradictItsOwnClass: a state and a class that disagree are two
// readings of one event and a reader cannot tell which to believe, so the pair is
// refused and the session is left exactly as it was.
func TestAnEndCannotContradictItsOwnClass(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)
	service := store.NewMirrorService(db)

	cases := []struct {
		name string
		end  store.PreviewEnd
	}{
		{name: "a failing class on a completed session", end: store.PreviewEnd{State: mirrors.SessionCompleted, Class: media.MirrorEndDeviceServerFailed, Cause: store.PreviewEndOperatorStop}},
		{name: "an ending on a failed session", end: store.PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndViewerDetached, Cause: store.PreviewEndOwnWork}},
		{name: "a class the vocabulary does not name", end: store.PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndClass("the_stream_looked_odd"), Cause: store.PreviewEndOwnWork}},
		{name: "no class at all", end: store.PreviewEnd{State: mirrors.SessionFailed, Cause: store.PreviewEndOwnWork}},
		{name: "no cause", end: store.PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndDeviceServerFailed}},
		{name: "a state that is not an ending", end: store.PreviewEnd{State: mirrors.SessionActive, Class: media.MirrorEndDeviceServerFailed, Cause: store.PreviewEndOwnWork}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.EndPreview(context.Background(), mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID), test.end, "operator", "op-1")
			if code := platformerrors.CodeOf(err); code != platformerrors.CodeInvalidInput {
				t.Fatalf("ending with %s was answered with %v, want an invalid input refusal", test.name, err)
			}
			if state, finished := storedSessionState(t, db, sessionID); state != "active" || finished.Valid {
				t.Fatalf("a refused ending changed the row to %q finished=%v", state, finished.Valid)
			}
		})
	}
}

// TestAnAbortedStopStillEndsTheSession: the request that asked for the ending was
// aborted while it was being written, so the write rolled back. An ending lost to
// an abort is the record this path exists to keep honest, so it is written again
// on a context the abort cannot cancel - and the row is not left reporting work
// that is not running.
func TestAnAbortedStopStillEndsTheSession(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)

	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	session, err := store.NewMirrorService(db).StopPreview(aborted, mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID), "operator", "op-1")
	if err != nil {
		t.Fatalf("StopPreview on an aborted request: %v", err)
	}
	if session.State != mirrors.SessionCompleted {
		t.Fatalf("the aborted stop left the session %q, want the ending it asked for", session.State)
	}
	state, finished := storedSessionState(t, db, sessionID)
	if state != "completed" || !finished.Valid {
		t.Fatalf("an aborted stop left the row %q finished=%v, want the ending recorded", state, finished.Valid)
	}
	if got, _ := sessionEndEvent(t, db, sessionID)["cause"].(string); got != string(store.PreviewEndOperatorStop) {
		t.Fatalf("the ending states cause %q, want the operator's stop", got)
	}
}

// TestASessionCannotBeEndedTwice: an ending is final, so a second one is refused
// rather than rewriting the reason the first one recorded.
func TestASessionCannotBeEndedTwice(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)
	service := store.NewMirrorService(db)

	if _, err := service.StopPreview(context.Background(), mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID), "operator", "op-1"); err != nil {
		t.Fatalf("first StopPreview: %v", err)
	}
	_, err := service.EndPreview(context.Background(), mirrorEventWorkspace, mirrors.MirrorSessionID(sessionID),
		store.PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndTransportUnavailable, Cause: store.PreviewEndOwnWork}, "operator", "op-1")
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeConflict {
		t.Fatalf("ending an ended session was answered with %v, want a conflict", err)
	}
	ending := sessionEndEvent(t, db, sessionID)
	if got, _ := ending["class"].(string); got != string(media.MirrorEndViewerDetached) {
		t.Fatalf("the first ending was rewritten to %q by an ending that was refused", got)
	}
}

// TestStartupEndsTheSessionsAPreviousProcessLeftOpen: the rows a dead process left
// behind are the whole point. They are found by what they are - open, and opened
// before this process started - ended with the plane's own class for the engine
// that was carrying them going away, named in the result, and never deleted.
func TestStartupEndsTheSessionsAPreviousProcessLeftOpen(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	first := startMirrorSession(t, db)
	second := startMirrorSession(t, db)
	service := store.NewMirrorService(db)

	stranded, err := service.ListStrandedPreviews(context.Background(), mirrorEventWorkspace)
	if err != nil {
		t.Fatalf("ListStrandedPreviews: %v", err)
	}
	if len(stranded) != 2 {
		t.Fatalf("the host holds %d open session(s), want the two a previous process left", len(stranded))
	}
	if stranded[0].SourceDeviceID != mirrorEventSource || stranded[0].UnfinishedTargets != 1 {
		t.Fatalf("the stranded row reads %#v, want its source device and its unfinished follower", stranded[0])
	}

	ended, leftAlone, err := service.FinalizeStrandedPreviews(context.Background(), mirrorEventWorkspace, time.Now().Add(time.Second).UTC(), "system", "control-plane")
	if err != nil {
		t.Fatalf("FinalizeStrandedPreviews: %v", err)
	}
	if len(ended) != 2 || len(leftAlone) != 0 {
		t.Fatalf("the sweep ended %d session(s) and left %d alone, want both ended", len(ended), len(leftAlone))
	}
	named := map[string]bool{}
	for _, session := range ended {
		named[string(session.ID)] = true
		if session.State != mirrors.SessionFailed || session.FinishedAt == "" {
			t.Fatalf("the sweep left %#v, want the session ended with a finish", session)
		}
	}
	if !named[first] || !named[second] {
		t.Fatalf("the sweep reported %v, want both session identities named", named)
	}
	for _, sessionID := range []string{first, second} {
		ending := sessionEndEvent(t, db, sessionID)
		if got, _ := ending["class"].(string); got != string(media.MirrorEndEngineStopped) {
			t.Fatalf("session %s states class %q, want the engine that was carrying it going away", sessionID, got)
		}
		if got, _ := ending["state"].(string); got != string(mirrors.SessionFailed) {
			t.Fatalf("session %s states state %q, want the state the class itself reports", sessionID, got)
		}
		if state, _ := storedSessionState(t, db, sessionID); state != "failed" {
			t.Fatalf("session %s reads %q in the table, want the failed session the reason says", sessionID, state)
		}
		if got, _ := ending["cause"].(string); got != string(store.PreviewEndPreviousProcess) {
			t.Fatalf("session %s states cause %q, want the previous process's departure", sessionID, got)
		}
		if open := unfinishedTargets(t, db, sessionID); open != 0 {
			t.Fatalf("session %s still reports %d unfinished follower(s) after the sweep", sessionID, open)
		}
	}
	var sessions int
	if err := store.SQLForTest(db).QueryRow(`SELECT count(*) FROM mirror_sessions WHERE workspace_id=?`, mirrorEventWorkspace).Scan(&sessions); err != nil {
		t.Fatalf("count mirror sessions: %v", err)
	}
	if sessions != 2 {
		t.Fatalf("the sweep left %d row(s), want both kept: these rows are history", sessions)
	}
	stillOpen, err := service.ListStrandedPreviews(context.Background(), mirrorEventWorkspace)
	if err != nil {
		t.Fatalf("ListStrandedPreviews after the sweep: %v", err)
	}
	if len(stillOpen) != 0 {
		t.Fatalf("%d session(s) are still open after the sweep", len(stillOpen))
	}
}

// TestStartupLeavesASessionOpenedAfterTheProcessStarted: the sweep's bound is what
// makes it safe. Only a session opened BEFORE this process started can be a
// previous process's leftover, so one opened after it is left exactly as it is and
// reported as left.
func TestStartupLeavesASessionOpenedAfterTheProcessStarted(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)

	ended, leftAlone, err := store.NewMirrorService(db).FinalizeStrandedPreviews(context.Background(), mirrorEventWorkspace, time.Now().Add(-time.Hour).UTC(), "system", "control-plane")
	if err != nil {
		t.Fatalf("FinalizeStrandedPreviews: %v", err)
	}
	if len(ended) != 0 || len(leftAlone) != 1 {
		t.Fatalf("the sweep ended %d and left %d, want the newer session left alone", len(ended), len(leftAlone))
	}
	if string(leftAlone[0].ID) != sessionID {
		t.Fatalf("the sweep left %s alone, want %s", leftAlone[0].ID, sessionID)
	}
	if state, finished := storedSessionState(t, db, sessionID); state != "active" || finished.Valid {
		t.Fatalf("a session opened after this process started reads %q finished=%v, want it untouched", state, finished.Valid)
	}
}
