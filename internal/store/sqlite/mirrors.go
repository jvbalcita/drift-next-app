package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// mirrorSessionEnded is the event name every mirror session ending is
	// recorded under, beside the session it ended. It is the mirror surface's own
	// history row (mirror_events), which is where a reason for a session's state
	// belongs: the ledger of sessions said what state a session was in, and
	// nothing said why (see mirror_events.go).
	mirrorSessionEnded = "mirror.session_ended"

	// endingWriteTimeout bounds the write that finalises a session whose own
	// request was aborted mid-flight. The ending still lands, and it lands
	// bounded: a record is not worth a process that hangs to repair it.
	endingWriteTimeout = 5 * time.Second
)

// PreviewEndCause names what ended a mirror session, in the plane's own words.
//
// It is stated beside the class because two very different stories share a
// class: engine_stopped describes both a session the plane ended on purpose and
// one a process that died left behind, and a reader asking whether an operator
// walked away or the machine restarted is answered by this name and not by
// inferring it from the class. The vocabulary is closed and the values are
// stable, because a name written into a record is read later by somebody who was
// not there.
type PreviewEndCause string

const (
	// PreviewEndOperatorStop: the operator asked for the session to end, which is
	// what StopPreview is.
	PreviewEndOperatorStop PreviewEndCause = "mirror.preview_stopped"
	// PreviewEndOwnWork: the session's own work ended it - a follower fan-out that
	// failed or gave up, which is the ending a supervising worker records.
	PreviewEndOwnWork PreviewEndCause = "mirror.preview_ended"
	// PreviewEndPreviousProcess: the session was left open by a process that is no
	// longer running, and this one found it at startup.
	PreviewEndPreviousProcess PreviewEndCause = "mirror.preview_recovered"
)

// Valid reports whether this cause is one the plane states.
func (c PreviewEndCause) Valid() bool {
	switch c {
	case PreviewEndOperatorStop, PreviewEndOwnWork, PreviewEndPreviousProcess:
		return true
	default:
		return false
	}
}

// PreviewEnd is how a mirror session ended: the state its record is left in,
// beside the class the plane's own closed vocabulary states for that end.
//
// It exists because what the record lacked was not a reason but an ENDING.
// StopPreview was the only path that finalised a session, so a session whose work
// failed, was aborted, or was left open by a process that died stayed 'active'
// with no finished_at: a row reporting running work that is not running, which is
// a record an operator and a later reader cannot trust. Every way a session ends
// now states itself in this one shape.
//
// The class is not decoration and the state is not free: a state and a class that
// disagree are two readings of one event and a reader cannot tell which to
// believe, so Valid refuses the pair rather than storing it.
type PreviewEnd struct {
	// State is the state the session's record is left in: completed, failed or
	// cancelled. Any other state is refused, because a session is ended here and
	// not paused or parked.
	State mirrors.SessionState
	// Class is how the end is classified, in the plane's own closed vocabulary
	// (internal/media, ARC-230). An end with no class is refused: a record saying
	// a session ended and not saying why is the reading this shape exists to
	// prevent, and a class this vocabulary does not name is refused for the same
	// reason.
	Class media.MirrorEndClass
	// Cause names what ended the session.
	Cause PreviewEndCause
}

// Valid reports whether this end is one the plane can state.
//
// The rule that makes it worth having is the last one: the class answers whether
// this end was a failure, and the state has to say the same thing. A session that
// ended - the operator stopping it, the engine going away - did not complete as
// 'failed', and a session whose work failed did not end as an ending.
func (e PreviewEnd) Valid() bool {
	switch e.State {
	case mirrors.SessionCompleted, mirrors.SessionFailed, mirrors.SessionCancelled:
	default:
		return false
	}
	if !e.Class.Valid() || !e.Cause.Valid() {
		return false
	}
	return e.Class.Failed() == (e.State == mirrors.SessionFailed)
}

// Sentence is the plane's own sentence for this end, so a reader holding the
// record still has the words it was ended with: it is the class's own sentence
// rather than a second copy of it.
func (e PreviewEnd) Sentence() string { return e.Class.Sentence() }

// PreviousProcessPreviewEnd is the end a mirror session a previous process left
// open is given at startup.
//
// It is stated once, here, because the startup line that names the repaired
// sessions and the record that repairs them have to state the same end: a report
// that described the repair differently from the row would be a second vocabulary
// for one event, which is what this shape exists to prevent.
//
// The class is the engine being stopped - the plane's own word for the thing that
// was carrying the session going away - and the state is failed because that is
// what the class says: ARC-230's Failed reports every class but the viewer's own
// departure as a failure, so a record that left this session 'cancelled' would be
// contradicting the reason written beside it. The cause is what keeps the story
// honest for a reader: the class says a plane stopped carrying it, and the cause
// says this one found it that way rather than having ended it.
func PreviousProcessPreviewEnd() PreviewEnd {
	return PreviewEnd{State: mirrors.SessionFailed, Class: media.MirrorEndEngineStopped, Cause: PreviewEndPreviousProcess}
}

// StrandedPreview is one mirror session this process found still open: the row
// carries no finished_at, so nothing has ended it, and the process that opened it
// was not this one.
type StrandedPreview struct {
	ID               mirrors.MirrorSessionID
	Workspace        organizations.WorkspaceID
	ControlSessionID string
	SourceDeviceID   devices.DeviceID
	State            mirrors.SessionState
	CreatedAt        string
	// UnfinishedTargets is how many of the session's follower targets are still
	// in a state that reports work in progress.
	UnfinishedTargets int
}

type MirrorPreviewTarget struct {
	ID           string
	DeviceID     devices.DeviceID
	State        mirrors.TargetState
	FailureClass string
	Detail       string
}

type MirrorPreviewSession struct {
	ID               mirrors.MirrorSessionID
	Workspace        organizations.WorkspaceID
	ControlSessionID string
	SourceDeviceID   devices.DeviceID
	State            mirrors.SessionState
	FailurePolicy    string
	CreatedAt        string
	FinishedAt       string
	Targets          []MirrorPreviewTarget
}

type MirrorService struct{ store *DB }

func NewMirrorService(store *DB) *MirrorService { return &MirrorService{store: store} }

func (s *MirrorService) StartPreview(ctx context.Context, workspace organizations.WorkspaceID, sourceID devices.DeviceID, followerIDs []devices.DeviceID, actorType, actorID string) (MirrorPreviewSession, error) {
	var session MirrorPreviewSession
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return session, err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "mirror actor fields are required")
	}
	sourceID = devices.DeviceID(strings.TrimSpace(string(sourceID)))
	if sourceID == "" {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "source device is required")
	}
	seen := map[devices.DeviceID]struct{}{}
	uniqueFollowers := make([]devices.DeviceID, 0, len(followerIDs))
	for _, id := range followerIDs {
		trimmed := devices.DeviceID(strings.TrimSpace(string(id)))
		if trimmed == "" || trimmed == sourceID {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		uniqueFollowers = append(uniqueFollowers, trimmed)
	}
	if len(uniqueFollowers) == 0 {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "preview needs one source and at least one follower")
	}
	control, openErr := NewSessionService(s.store, 0).Open(ctx, workspace, actorID, actorType, actorID)
	if openErr != nil {
		return session, openErr
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	sessionID, idErr := s.store.ids.NewID()
	if idErr != nil {
		_, _ = NewSessionService(s.store, 0).Close(ctx, workspace, control.ID, actorType, actorID)
		return session, platformerrors.Wrap(platformerrors.CodeInternal, "generate mirror session ID", idErr)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		source, sourceErr := loadDeviceTx(ctx, tx, workspace, sourceID)
		if sourceErr != nil {
			return sourceErr
		}
		if source.State != devices.Active {
			return platformerrors.New(platformerrors.CodePreconditionFailed, "preview rejected: source is not an active connected device")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mirror_sessions (id, workspace_id, control_session_id, source_device_id, state, failure_policy, created_at) VALUES (?, ?, ?, ?, 'active', 'continue', ?)`, sessionID, workspace, control.ID, sourceID, now); err != nil {
			return mapConstraint(err)
		}
		targets := make([]MirrorPreviewTarget, 0, len(uniqueFollowers))
		for _, followerID := range uniqueFollowers {
			targetID, targetIDErr := s.store.ids.NewID()
			if targetIDErr != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate mirror target ID", targetIDErr)
			}
			follower, followerErr := loadDeviceTx(ctx, tx, workspace, followerID)
			if followerErr != nil {
				return followerErr
			}
			target := MirrorPreviewTarget{ID: targetID, DeviceID: followerID, State: mirrors.TargetPending, Detail: "Preview admitted; no command sent."}
			if follower.State != devices.Active {
				target.State = mirrors.TargetFailed
				target.FailureClass = previewFailureClass(follower.State)
				target.Detail = "Preview withheld: follower is not an active connected device."
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO mirror_targets (id, workspace_id, mirror_session_id, follower_device_id, state, failure_class, created_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, target.ID, workspace, sessionID, followerID, target.State, nullIfEmpty(target.FailureClass), now, finishedAt(target.State, now)); err != nil {
				return mapConstraint(err)
			}
			targets = append(targets, target)
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "mirror_session", sessionID, "mirror.preview_started", actorType, actorID); err != nil {
			return err
		}
		session = MirrorPreviewSession{
			ID:               mirrors.MirrorSessionID(sessionID),
			Workspace:        workspace,
			ControlSessionID: string(control.ID),
			SourceDeviceID:   sourceID,
			State:            mirrors.SessionActive,
			FailurePolicy:    "continue",
			CreatedAt:        now,
			Targets:          targets,
		}
		return nil
	})
	if err != nil {
		_, _ = NewSessionService(s.store, 0).Close(ctx, workspace, control.ID, actorType, actorID)
	}
	return session, err
}

// StopPreview ends a preview session at the operator's request.
//
// It is one ending among several and not the only one: it states its end in the
// same shape every other ending does (a completed session, classified as the
// operator's own departure, which is an ENDING and not a failure), and a request
// that was aborted mid-flight still leaves the session ended rather than active
// forever.
func (s *MirrorService) StopPreview(ctx context.Context, workspace organizations.WorkspaceID, sessionID mirrors.MirrorSessionID, actorType, actorID string) (MirrorPreviewSession, error) {
	end := PreviewEnd{State: mirrors.SessionCompleted, Class: media.MirrorEndViewerDetached, Cause: PreviewEndOperatorStop}
	session, err := s.EndPreview(ctx, workspace, sessionID, end, actorType, actorID)
	if err == nil || ctx == nil || ctx.Err() == nil {
		return session, err
	}
	// The request that asked for this stop was aborted while it was being
	// written - a console that navigated away, a process shutting down - so the
	// transaction above rolled back and the row is still active. An ending that
	// is lost to an abort is exactly the record this path exists to keep honest,
	// so it is written again on a context the abort cannot cancel, bounded so the
	// repair cannot outlive its welcome. A caller that is still there is told the
	// session ended, because it did.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endingWriteTimeout)
	defer cancel()
	ended, retryErr := s.EndPreview(recordCtx, workspace, sessionID, end, actorType, actorID)
	if retryErr == nil {
		return ended, nil
	}
	return ended, err
}

// EndPreview finalises one mirror session, stating how it ended.
//
// This is the one path every ending goes through, which is the point: an ending
// that only one caller knows how to write is an ending every other caller forgets,
// and the sessions a plane forgets are the ones that report running work it is not
// running. The end is refused unless it is complete and consistent - a stated
// state, a class the plane's vocabulary names, the cause, and a state that agrees
// with the class about whether this end was a failure.
func (s *MirrorService) EndPreview(ctx context.Context, workspace organizations.WorkspaceID, sessionID mirrors.MirrorSessionID, end PreviewEnd, actorType, actorID string) (MirrorPreviewSession, error) {
	var session MirrorPreviewSession
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return session, err
	}
	if strings.TrimSpace(string(sessionID)) == "" {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "mirror session ID is required")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "mirror actor fields are required")
	}
	if !end.Valid() {
		return session, platformerrors.New(platformerrors.CodeInvalidInput, "a mirror session ends as completed, failed or cancelled, with the class and the cause of that end: the state, the class and the cause must be stated and must agree")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		ended, endErr := s.endPreviewTx(ctx, tx, workspace, sessionID, end, actorType, actorID)
		if endErr != nil {
			return endErr
		}
		session = ended
		return nil
	})
	return session, err
}

// endPreviewTx ends one session inside the caller's transaction.
//
// The followers are cancelled in every ending and never marked failed: what ended
// is the session, no command was sent to a follower, and a follower blamed for the
// session's end is a device reported as failing work it was never given. The class
// in the record says what happened to the session; the target rows say, truthfully,
// that nothing was sent.
func (s *MirrorService) endPreviewTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, sessionID mirrors.MirrorSessionID, end PreviewEnd, actorType, actorID string) (MirrorPreviewSession, error) {
	current, loadErr := loadMirrorSessionTx(ctx, tx, workspace, sessionID)
	if loadErr != nil {
		return MirrorPreviewSession{}, loadErr
	}
	if transitionErr := mirrors.TransitionSession(current.State, end.State); transitionErr != nil {
		return MirrorPreviewSession{}, platformerrors.Wrap(platformerrors.CodeConflict, "mirror session cannot be ended", transitionErr)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE mirror_sessions SET state=?, finished_at=? WHERE workspace_id=? AND id=? AND state=?`, end.State, now, workspace, sessionID, current.State); err != nil {
		return MirrorPreviewSession{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mirror_targets SET state='cancelled', finished_at=? WHERE workspace_id=? AND mirror_session_id=? AND state IN ('pending','leased','queued','running')`, now, workspace, sessionID); err != nil {
		return MirrorPreviewSession{}, err
	}
	if err := s.recordSessionEndTx(ctx, tx, workspace, current.SourceDeviceID, sessionID, end, actorType, actorID); err != nil {
		return MirrorPreviewSession{}, err
	}
	if err := s.store.recordMutation(ctx, tx, string(workspace), "mirror_session", string(sessionID), string(end.Cause), actorType, actorID); err != nil {
		return MirrorPreviewSession{}, err
	}
	return loadMirrorSessionTx(ctx, tx, workspace, sessionID)
}

// recordSessionEndTx appends the plane's record of how a session ended: the
// class, the class's own sentence, and the cause, beside the session.
//
// It is history and nothing reads it to decide anything (AGENTS.md section 2), so
// a reader who finds a session that is not active can find WHY in the row beside
// it - which is what was missing when sessions sat unfinished with no reason
// recorded anywhere.
func (s *MirrorService) recordSessionEndTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, source devices.DeviceID, sessionID mirrors.MirrorSessionID, end PreviewEnd, actorType, actorID string) error {
	payload, err := json.Marshal(map[string]any{
		"state":  string(end.State),
		"class":  string(end.Class),
		"reason": end.Sentence(),
		"cause":  string(end.Cause),
	})
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "encode the mirror session end payload", err)
	}
	eventID, idErr := s.store.ids.NewID()
	if idErr != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate mirror event ID", idErr)
	}
	occurredAt := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO mirror_events (id, workspace_id, mirror_session_id, mirror_target_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, NULL, ?, ?, ?, ?, NULL, ?, ?, ?, ?)`,
		eventID, workspace, sessionID, source, mirrorSessionEnded, mirrorEventSchemaVersion, sessionID, actorID, mirrorEventSource, string(payload), occurredAt); err != nil {
		return mapConstraint(err)
	}
	return nil
}

// ListStrandedPreviews reports the mirror sessions nothing has ended: no
// finished_at, so the only thing that could have ended them was the process that
// opened them. It is a read, and it changes nothing.
func (s *MirrorService) ListStrandedPreviews(ctx context.Context, workspace organizations.WorkspaceID) ([]StrandedPreview, error) {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT s.id, s.control_session_id, s.source_device_id, s.state, s.created_at, (SELECT count(*) FROM mirror_targets t WHERE t.workspace_id=s.workspace_id AND t.mirror_session_id=s.id AND t.state IN ('pending','leased','queued','running')) FROM mirror_sessions s WHERE s.workspace_id=? AND s.finished_at IS NULL ORDER BY s.created_at, s.id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	stranded := make([]StrandedPreview, 0)
	for rows.Next() {
		var row StrandedPreview
		if err := rows.Scan(&row.ID, &row.ControlSessionID, &row.SourceDeviceID, &row.State, &row.CreatedAt, &row.UnfinishedTargets); err != nil {
			return nil, err
		}
		row.Workspace = workspace
		stranded = append(stranded, row)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	return stranded, nil
}

// FinalizeStrandedPreviews ends the mirror sessions a PREVIOUS process left open,
// and reports each one it ended.
//
// A session with no finished_at that this process did not open is one no process
// is carrying: this plane is the only writer to this store and it opened none of
// them yet. They are ended with the plane's own class for the engine that was
// carrying them going away (ARC-230), and the state is whatever that class says -
// not a second opinion about it, because a state that disagreed with the reason
// written beside it is a record a reader cannot resolve.
//
// The bound is what makes the sweep safe to run at startup: only sessions opened
// BEFORE this process started can be a previous process's leftovers, so a session
// this process opened while the sweep ran is never touched. A row whose created_at
// cannot be read is left exactly as it is and reported, because a sweep that
// cannot tell when a session started cannot claim it was somebody else's - and
// nothing here is deleted: these rows are history, and repair is a state, not a
// deletion.
func (s *MirrorService) FinalizeStrandedPreviews(ctx context.Context, workspace organizations.WorkspaceID, openedBefore time.Time, actorType, actorID string) ([]MirrorPreviewSession, []StrandedPreview, error) {
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidInput, "mirror actor fields are required")
	}
	stranded, listErr := s.ListStrandedPreviews(ctx, workspace)
	if listErr != nil {
		return nil, nil, listErr
	}
	end := PreviousProcessPreviewEnd()
	ended := make([]MirrorPreviewSession, 0, len(stranded))
	leftAlone := make([]StrandedPreview, 0)
	for _, row := range stranded {
		openedAt, parseErr := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if parseErr != nil || !openedAt.Before(openedBefore) {
			leftAlone = append(leftAlone, row)
			continue
		}
		session, endErr := s.EndPreview(ctx, workspace, row.ID, end, actorType, actorID)
		if endErr != nil {
			// Each session is ended on its own: one row this plane cannot repair is
			// not a reason to leave every other row reporting work that is not
			// running, and what was ended before the failure is reported as ended.
			return ended, leftAlone, endErr
		}
		ended = append(ended, session)
	}
	return ended, leftAlone, nil
}

func (s *MirrorService) List(ctx context.Context, workspace organizations.WorkspaceID) ([]MirrorPreviewSession, error) {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id FROM mirror_sessions WHERE workspace_id=? ORDER BY created_at DESC, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	ids := make([]mirrors.MirrorSessionID, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, mirrors.MirrorSessionID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	result := make([]MirrorPreviewSession, 0, len(ids))
	for _, id := range ids {
		session, loadErr := loadMirrorSessionTx(ctx, s.store.db, workspace, id)
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, session)
	}
	return result, nil
}

func loadDeviceTx(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id devices.DeviceID) (devices.Device, error) {
	var device devices.Device
	var state string
	err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, display_name, state FROM devices WHERE workspace_id=? AND id=?`, workspace, id).Scan(&device.ID, &device.Workspace, &device.DisplayName, &state)
	if err == sql.ErrNoRows {
		return device, platformerrors.New(platformerrors.CodeNotFound, "device not found")
	}
	if err != nil {
		return device, classifyContext(err)
	}
	device.State = devices.State(state)
	return device, nil
}

func loadMirrorSessionTx(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id mirrors.MirrorSessionID) (MirrorPreviewSession, error) {
	var session MirrorPreviewSession
	var finished sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, control_session_id, source_device_id, state, failure_policy, created_at, finished_at FROM mirror_sessions WHERE workspace_id=? AND id=?`, workspace, id).Scan(&session.ID, &session.Workspace, &session.ControlSessionID, &session.SourceDeviceID, &session.State, &session.FailurePolicy, &session.CreatedAt, &finished)
	if err == sql.ErrNoRows {
		return session, platformerrors.New(platformerrors.CodeNotFound, "mirror session not found")
	}
	if err != nil {
		return session, classifyContext(err)
	}
	if finished.Valid {
		session.FinishedAt = finished.String
	}
	rows, queryErr := queryer.QueryContext(ctx, `SELECT id, follower_device_id, state, failure_class FROM mirror_targets WHERE workspace_id=? AND mirror_session_id=? ORDER BY created_at, id`, workspace, id)
	if queryErr != nil {
		return session, classifyContext(queryErr)
	}
	defer rows.Close()
	for rows.Next() {
		var target MirrorPreviewTarget
		var failure sql.NullString
		if err := rows.Scan(&target.ID, &target.DeviceID, &target.State, &failure); err != nil {
			return session, err
		}
		if failure.Valid {
			target.FailureClass = failure.String
		}
		if target.State == mirrors.TargetFailed {
			target.Detail = "Preview withheld: follower is not an active connected device."
		} else if target.State == mirrors.TargetCancelled {
			target.Detail = "Preview stopped; no command was sent."
		} else {
			target.Detail = "Preview admitted; no command sent."
		}
		session.Targets = append(session.Targets, target)
	}
	return session, classifyContext(rows.Err())
}

func previewFailureClass(state devices.State) string {
	if state == devices.Unavailable {
		return "device_offline"
	}
	return "target_incompatible"
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func finishedAt(state mirrors.TargetState, now string) any {
	if state == mirrors.TargetFailed || state == mirrors.TargetCancelled || state == mirrors.TargetSucceeded || state == mirrors.TargetCleanupFailed {
		return now
	}
	return nil
}
