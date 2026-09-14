package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
	"drift.local/drift-next/internal/recordings"
)

type RecordingRepository struct{ store *DB }

func NewRecordingRepository(store *DB) *RecordingRepository {
	return &RecordingRepository{store: store}
}

func (r *RecordingRepository) GetSession(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID) (recordings.Session, error) {
	if err := validateRecordingRepository(ctx, r, workspace, string(id)); err != nil {
		return recordings.Session{}, err
	}
	return loadRecordingSession(ctx, r.store.db, workspace, id)
}

func (r *RecordingRepository) ListSessions(ctx context.Context, workspace organizations.WorkspaceID) ([]recordings.Session, error) {
	if err := validateRecordingRepository(ctx, r, workspace, "reader"); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, recordingSessionQuery+` WHERE workspace_id=? ORDER BY session_number, created_at, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]recordings.Session, 0)
	for rows.Next() {
		var session recordings.Session
		if err := scanRecordingSession(rows, &session); err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, classifyContext(rows.Err())
}

func (r *RecordingRepository) GetEvent(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingEventID) (recordings.InteractionEvent, error) {
	if err := validateRecordingRepository(ctx, r, workspace, string(id)); err != nil {
		return recordings.InteractionEvent{}, err
	}
	return loadRecordingEvent(ctx, r.store.db, workspace, id)
}

func (r *RecordingRepository) ListEvents(ctx context.Context, workspace organizations.WorkspaceID, sessionID recordings.RecordingSessionID) ([]recordings.InteractionEvent, error) {
	if err := validateRecordingRepository(ctx, r, workspace, string(sessionID)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, recordingEventQuery+` WHERE workspace_id=? AND recording_session_id=? ORDER BY sequence, id`, workspace, sessionID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]recordings.InteractionEvent, 0)
	for rows.Next() {
		event, err := scanRecordingEvent(rows)
		if err != nil {
			return nil, err
		}
		if err := loadRecordingEventEvidence(ctx, r.store.db, workspace, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	return result, nil
}

type RecordingService struct{ store *DB }

func NewRecordingService(store *DB) *RecordingService { return &RecordingService{store: store} }

func (s *RecordingService) Create(ctx context.Context, session recordings.Session, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, session.Workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	if session.State == "" {
		session.State = recordings.SessionRequested
	}
	if session.Cleanup == "" {
		session.Cleanup = recordings.CleanupNone
	}
	if session.Review == "" {
		session.Review = recordings.ReviewUnreviewed
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = s.store.clock.Now().UTC()
	}
	if session.Source == "" {
		return recordings.Session{}, platformerrors.New(platformerrors.CodeInvalidInput, "recording source is required")
	}
	if session.State != recordings.SessionRequested {
		return recordings.Session{}, platformerrors.New(platformerrors.CodeInvalidInput, "new recording sessions must be requested")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if session.Number == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(session_number),0)+1 FROM recording_sessions WHERE workspace_id=?`, session.Workspace).Scan(&session.Number); err != nil {
				return err
			}
		}
		if err := session.Validate(); err != nil {
			return platformerrors.Wrap(platformerrors.CodeInvalidInput, "recording session is invalid", err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO recording_sessions (id,workspace_id,device_id,state,source,started_at,finished_at,created_at,session_number,automation_id,deleted_at,cleanup_state,review_state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, session.ID, session.Workspace, nullableString(string(session.DeviceID)), session.State, session.Source, nullableTime(session.StartedAt), nullableTime(session.FinishedAt), session.CreatedAt.UTC().Format(time.RFC3339Nano), session.Number, session.AutomationID, nullableTime(session.DeletedAt), session.Cleanup, session.Review)
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(session.Workspace), "recording_session", string(session.ID), "recording.session.created", actorType, actorID)
	})
	return session, err
}

func (s *RecordingService) Start(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	var result recordings.Session
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRecordingSession(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, err = recordings.Start(current, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "recording session cannot start", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE recording_sessions SET state='recording',started_at=? WHERE workspace_id=? AND id=? AND state='requested'`, nullableTime(result.StartedAt), workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "recording session"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_session", string(id), "recording.session.started", actorType, actorID)
	})
	return result, err
}

func (s *RecordingService) Stop(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	var result recordings.Session
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRecordingSession(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, err = recordings.Stop(current, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "recording session cannot stop", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE recording_sessions SET state='completed',finished_at=? WHERE workspace_id=? AND id=? AND state='recording'`, nullableTime(result.FinishedAt), workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "recording session"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_session", string(id), "recording.session.completed", actorType, actorID)
	})
	return result, err
}

func (s *RecordingService) Discard(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	var result recordings.Session
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRecordingSession(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, err = recordings.Discard(current, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "recording session cannot be discarded", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE recording_sessions SET state='discarded',started_at=?,finished_at=?,cleanup_state='pending' WHERE workspace_id=? AND id=? AND state IN ('requested','recording','stopping')`, nullableTime(result.StartedAt), nullableTime(result.FinishedAt), workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "recording session"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_session", string(id), "recording.session.discarded", actorType, actorID)
	})
	return result, err
}

func (s *RecordingService) AppendEvent(ctx context.Context, event recordings.InteractionEvent, actorType, actorID string) error {
	if err := validateRecordingService(ctx, s, event.Workspace, actorType, actorID); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "recording event is invalid", err)
	}
	if event.Review != recordings.ReviewUnreviewed || event.ReviewerID != "" || event.ReviewedAt != nil {
		return platformerrors.New(platformerrors.CodeConflict, "recording events must be reviewed after durable capture")
	}
	beforeJSON, err := recordingCaptureJSON(event.Before)
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "recording BEFORE state is invalid", err)
	}
	actionJSON, err := recordingActionJSON(event.Action)
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "recording action is invalid", err)
	}
	afterJSON, err := recordingCaptureJSON(event.After)
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "recording AFTER state is invalid", err)
	}
	errorsJSON, err := json.Marshal(event.CaptureErrors)
	if err != nil || !safeRecordingJSON(string(errorsJSON)) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "recording capture errors are invalid or sensitive")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var state string
		var deleted sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT state,deleted_at FROM recording_sessions WHERE workspace_id=? AND id=?`, event.Workspace, event.SessionID).Scan(&state, &deleted); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "recording session not found")
		} else if err != nil {
			return err
		}
		if state != string(recordings.SessionRecording) && state != string(recordings.SessionStopping) || deleted.Valid {
			return platformerrors.New(platformerrors.CodeConflict, "recording session is not accepting events")
		}
		var expected int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM recording_events WHERE workspace_id=? AND recording_session_id=?`, event.Workspace, event.SessionID).Scan(&expected); err != nil {
			return err
		}
		if event.Sequence != expected {
			return platformerrors.New(platformerrors.CodeConflict, "recording event sequence is not the next durable sequence")
		}
		beforeObservation, afterObservation := observationID(event.Before), observationID(event.After)
		rawHash, annotatedHash, treeHash := eventHashes(event)
		_, err := tx.ExecContext(ctx, `INSERT INTO recording_events (id,workspace_id,recording_session_id,sequence,logical_action,before_observation_id,after_observation_id,raw_screenshot_hash,annotated_screenshot_hash,ui_tree_hash,redaction_state,sensitive,duration_ms,details_json,created_at,correlation_id,started_at,finished_at,before_state_json,action_json,after_state_json,before_freshness,after_freshness,capture_errors_json,review_state,reviewer_id,reviewed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.ID, event.Workspace, event.SessionID, event.Sequence, event.Action.Kind, nullableString(beforeObservation), nullableString(afterObservation), nullableString(rawHash), nullableString(annotatedHash), nullableString(treeHash), event.Redaction, boolInt(event.Sensitive), event.FinishedAt.Sub(event.StartedAt).Milliseconds(), `{}`, event.CreatedAt.UTC().Format(time.RFC3339Nano), event.CorrelationID, event.StartedAt.UTC().Format(time.RFC3339Nano), event.FinishedAt.UTC().Format(time.RFC3339Nano), beforeJSON, actionJSON, afterJSON, nullableString(freshness(event.Before)), nullableString(freshness(event.After)), string(errorsJSON), event.Review, event.ReviewerID, nullableTime(event.ReviewedAt))
		if err != nil {
			return mapConstraint(err)
		}
		for _, reference := range eventEvidence(event) {
			id, err := s.store.ids.NewID()
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate recording evidence ID", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO recording_event_evidence (id,workspace_id,recording_event_id,phase,evidence_kind,artifact_id,content_hash,media_type,schema_version,authoritative,omitted,omission_reason,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, event.Workspace, event.ID, reference.Phase, reference.Kind, nullableString(reference.ArtifactID), nullableString(reference.ContentHash), nullableString(reference.MediaType), reference.SchemaVersion, boolInt(reference.Authoritative), boolInt(reference.Omitted), reference.OmissionReason, event.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return mapConstraint(err)
			}
		}
		return s.store.recordMutation(ctx, tx, string(event.Workspace), "recording_event", string(event.ID), "recording.event.captured", actorType, actorID)
	})
}

// Save implements EventSink for a recorder connected to the durable store.
func (s *RecordingService) Save(ctx context.Context, event recordings.InteractionEvent) error {
	return s.AppendEvent(ctx, event, "recorder", "recorder")
}

type RecordingEventSink struct {
	service   *RecordingService
	actorType string
	actorID   string
}

func NewRecordingEventSink(service *RecordingService, actorType, actorID string) *RecordingEventSink {
	return &RecordingEventSink{service: service, actorType: actorType, actorID: actorID}
}

func (s *RecordingEventSink) Save(ctx context.Context, event recordings.InteractionEvent) error {
	if s == nil || s.service == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "recording event sink is required")
	}
	return s.service.AppendEvent(ctx, event, s.actorType, s.actorID)
}

func (s *RecordingService) ReviewEvent(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingEventID, approved bool, reviewer string) (recordings.InteractionEvent, error) {
	if err := validateRecordingService(ctx, s, workspace, "operator", reviewer); err != nil {
		return recordings.InteractionEvent{}, err
	}
	repository := NewRecordingRepository(s.store)
	event, err := repository.GetEvent(ctx, workspace, id)
	if err != nil {
		return recordings.InteractionEvent{}, err
	}
	updated, err := recordings.ReviewEvent(event, approved, reviewer, s.store.clock.Now())
	if err != nil {
		return event, platformerrors.Wrap(platformerrors.CodeConflict, "recording event cannot be reviewed", err)
	}
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE recording_events SET review_state=?,reviewer_id=?,reviewed_at=? WHERE workspace_id=? AND id=? AND review_state='unreviewed'`, updated.Review, updated.ReviewerID, nullableTime(updated.ReviewedAt), workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "recording event"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_event", string(id), "recording.event.reviewed", "operator", reviewer)
	})
	return updated, err
}

func (s *RecordingService) DeleteSession(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID, confirmed bool, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	var result recordings.Session
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRecordingSession(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, err = recordings.DeleteCompleted(current, confirmed, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "recording session deletion was not confirmed or is invalid", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE recording_sessions SET deleted_at=?,cleanup_state='pending' WHERE workspace_id=? AND id=? AND deleted_at IS NULL`, nullableTime(result.DeletedAt), workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "recording session"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_session", string(id), "recording.session.deletion_requested", actorType, actorID)
	})
	return result, err
}

func (s *RecordingService) MarkCleanup(ctx context.Context, workspace organizations.WorkspaceID, id recordings.RecordingSessionID, state recordings.CleanupState, actorType, actorID string) (recordings.Session, error) {
	if err := validateRecordingService(ctx, s, workspace, actorType, actorID); err != nil {
		return recordings.Session{}, err
	}
	var result recordings.Session
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRecordingSession(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, err = recordings.MarkCleanup(current, state)
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "recording cleanup transition is invalid", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE recording_sessions SET cleanup_state=? WHERE workspace_id=? AND id=? AND deleted_at IS NOT NULL`, state, workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "recording session"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "recording_session", string(id), "recording.session.cleanup_"+string(state), actorType, actorID)
	})
	return result, err
}

const recordingSessionQuery = `SELECT id,workspace_id,automation_id,device_id,session_number,state,source,started_at,finished_at,created_at,deleted_at,cleanup_state,review_state FROM recording_sessions`

const recordingEventQuery = `SELECT id,workspace_id,recording_session_id,sequence,logical_action,before_observation_id,after_observation_id,raw_screenshot_hash,annotated_screenshot_hash,ui_tree_hash,redaction_state,sensitive,duration_ms,created_at,correlation_id,started_at,finished_at,before_state_json,action_json,after_state_json,before_freshness,after_freshness,capture_errors_json,review_state,reviewer_id,reviewed_at FROM recording_events`

func validateRecordingRepository(ctx context.Context, repository *RecordingRepository, workspace organizations.WorkspaceID, id string) error {
	if ctx == nil || repository == nil || repository.store == nil || repository.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite repository are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "recording resource ID is required")
	}
	return nil
}

func validateRecordingService(ctx context.Context, service *RecordingService, workspace organizations.WorkspaceID, actorType, actorID string) error {
	if ctx == nil || service == nil || service.store == nil || service.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "recording actor fields are required")
	}
	return nil
}

func loadRecordingSession(ctx context.Context, source interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace organizations.WorkspaceID, id recordings.RecordingSessionID) (recordings.Session, error) {
	var session recordings.Session
	err := scanRecordingSession(source.QueryRowContext(ctx, recordingSessionQuery+` WHERE workspace_id=? AND id=?`, workspace, id), &session)
	if err == sql.ErrNoRows {
		return session, platformerrors.New(platformerrors.CodeNotFound, "recording session not found")
	}
	if err != nil {
		return session, classifyContext(err)
	}
	return session, nil
}

func scanRecordingSession(row interface{ Scan(...any) error }, session *recordings.Session) error {
	var automation, device, state, source, started, finished, created, deleted, cleanup, review sql.NullString
	if err := row.Scan(&session.ID, &session.Workspace, &automation, &device, &session.Number, &state, &source, &started, &finished, &created, &deleted, &cleanup, &review); err != nil {
		return err
	}
	session.AutomationID = automation.String
	session.DeviceID = devices.DeviceID(device.String)
	session.State = recordings.SessionState(state.String)
	session.Source = recordings.Source(source.String)
	session.Cleanup = recordings.CleanupState(cleanup.String)
	session.Review = recordings.ReviewState(review.String)
	session.StartedAt = parseNullableTime(started)
	session.FinishedAt = parseNullableTime(finished)
	session.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	session.DeletedAt = parseNullableTime(deleted)
	return nil
}

func scanRecordingEvent(row interface{ Scan(...any) error }) (recordings.InteractionEvent, error) {
	var event recordings.InteractionEvent
	var session, kind, beforeID, afterID, rawHash, annotatedHash, treeHash, created, correlation, started, finished, beforeJSON, actionJSON, afterJSON, beforeFreshness, afterFreshness, errorsJSON, review, reviewer, reviewed sql.NullString
	var sensitive, duration int
	if err := row.Scan(&event.ID, &event.Workspace, &session, &event.Sequence, &kind, &beforeID, &afterID, &rawHash, &annotatedHash, &treeHash, &event.Redaction, &sensitive, &duration, &created, &correlation, &started, &finished, &beforeJSON, &actionJSON, &afterJSON, &beforeFreshness, &afterFreshness, &errorsJSON, &review, &reviewer, &reviewed); err != nil {
		return event, err
	}
	event.SessionID = recordings.RecordingSessionID(session.String)
	event.Action.Kind = action.Kind(kind.String)
	event.Sensitive = sensitive != 0
	event.CorrelationID = correlation.String
	event.Review = recordings.ReviewState(review.String)
	event.ReviewerID = reviewer.String
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	event.StartedAt, _ = time.Parse(time.RFC3339Nano, started.String)
	event.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished.String)
	event.Before = recordingCaptureFromJSON(beforeJSON.String, beforeID.String, beforeFreshness.String, rawHash.String, treeHash.String, recordings.PhaseBefore)
	event.After = recordingCaptureFromJSON(afterJSON.String, afterID.String, afterFreshness.String, rawHash.String, treeHash.String, recordings.PhaseAfter)
	if actionJSON.String != "" && actionJSON.String != "{}" {
		var storedAction persistedAction
		if err := json.Unmarshal([]byte(actionJSON.String), &storedAction); err != nil {
			return event, err
		}
		event.Action = persistedActionToDomain(storedAction)
	}
	event.Action.Kind = action.Kind(kind.String)
	if err := json.Unmarshal([]byte(errorsJSON.String), &event.CaptureErrors); err != nil && errorsJSON.String != "" && errorsJSON.String != "[]" {
		return event, err
	}
	if reviewed.Valid {
		value, err := time.Parse(time.RFC3339Nano, reviewed.String)
		if err != nil {
			return event, err
		}
		event.ReviewedAt = &value
	}
	_ = duration
	return event, nil
}

type recordingQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadRecordingEvent(ctx context.Context, db recordingQueryer, workspace organizations.WorkspaceID, id recordings.RecordingEventID) (recordings.InteractionEvent, error) {
	event, err := scanRecordingEvent(db.QueryRowContext(ctx, recordingEventQuery+` WHERE workspace_id=? AND id=?`, workspace, id))
	if err == sql.ErrNoRows {
		return event, platformerrors.New(platformerrors.CodeNotFound, "recording event not found")
	}
	if err != nil {
		return event, classifyContext(err)
	}
	if err := loadRecordingEventEvidence(ctx, db, workspace, &event); err != nil {
		return event, err
	}
	return event, nil
}

func loadRecordingEventEvidence(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspace organizations.WorkspaceID, event *recordings.InteractionEvent) error {
	rows, err := db.QueryContext(ctx, `SELECT phase,evidence_kind,artifact_id,content_hash,media_type,schema_version,authoritative,omitted,omission_reason FROM recording_event_evidence WHERE workspace_id=? AND recording_event_id=? ORDER BY id`, workspace, event.ID)
	if err != nil {
		return classifyContext(err)
	}
	defer rows.Close()
	for rows.Next() {
		var phase, kind, artifact, hash, media, reason sql.NullString
		var schema, authoritative, omitted int
		if err := rows.Scan(&phase, &kind, &artifact, &hash, &media, &schema, &authoritative, &omitted, &reason); err != nil {
			return err
		}
		reference := recordings.EvidenceReference{Phase: recordings.CapturePhase(phase.String), Kind: recordings.EvidenceKind(kind.String), ArtifactID: artifact.String, ContentHash: hash.String, MediaType: media.String, SchemaVersion: schema, Authoritative: authoritative != 0, Omitted: omitted != 0, OmissionReason: reason.String}
		event.Evidence = append(event.Evidence, reference)
		switch reference.Phase {
		case recordings.PhaseBefore:
			if event.Before != nil {
				event.Before.Evidence = append(event.Before.Evidence, reference)
			}
		case recordings.PhaseAfter:
			if event.After != nil {
				event.After.Evidence = append(event.After.Evidence, reference)
			}
		}
	}
	return classifyContext(rows.Err())
}

func recordingCaptureJSON(capture *recordings.Capture) (string, error) {
	if capture == nil {
		return "{}", nil
	}
	value := persistedCapture{ObservationID: capture.ObservationID, CapturedAt: capture.CapturedAt.UTC().Format(time.RFC3339Nano), CoordinateSpace: capture.CoordinateSpace, PackageName: capture.PackageName, ActivityName: capture.ActivityName, AppVersion: capture.AppVersion, DisplayWidth: capture.DisplayWidth, DisplayHeight: capture.DisplayHeight, Orientation: capture.Orientation, ScreenshotHash: capture.ScreenshotHash, UITreeHash: capture.UITreeHash, Status: capture.Status, Sanitization: capture.Sanitization, Partial: capture.Partial, ErrorClass: capture.ErrorClass}
	if capture.Target != nil {
		target := persistedTargetFromDomain(*capture.Target)
		if !recordings.IsSensitiveTarget(*capture.Target) {
			value.Target = &target
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || !safeRecordingJSON(string(encoded)) {
		return "", fmt.Errorf("capture state is sensitive or invalid")
	}
	return string(encoded), nil
}

func recordingActionJSON(value recordings.LogicalAction) (string, error) {
	encoded, err := json.Marshal(persistedActionFromDomain(value))
	if err != nil || !safeRecordingJSON(string(encoded)) {
		return "", fmt.Errorf("action metadata is sensitive or invalid")
	}
	return string(encoded), nil
}

func safeRecordingJSON(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "credential") || strings.Contains(lower, "private_key") {
		return false
	}
	return value == string(redaction.RedactString(value))
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

type persistedTarget struct {
	ResourceID         string  `json:"resource_id,omitempty"`
	AccessibilityLabel string  `json:"accessibility_label,omitempty"`
	StableText         string  `json:"stable_text,omitempty"`
	ContextFingerprint string  `json:"context_fingerprint,omitempty"`
	ClassName          string  `json:"class_name,omitempty"`
	Bounds             [4]int  `json:"bounds,omitempty"`
	Actionable         bool    `json:"actionable"`
	Enabled            bool    `json:"enabled"`
	Editable           bool    `json:"editable"`
	Selected           bool    `json:"selected"`
	Checked            bool    `json:"checked"`
	Source             string  `json:"source,omitempty"`
	Confidence         float64 `json:"confidence"`
	CandidateCount     int     `json:"candidate_count"`
}

type persistedCoordinate struct {
	Space string `json:"space"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

type persistedGesture struct {
	Points     []persistedCoordinate `json:"points"`
	DurationMs int64                 `json:"duration_ms"`
}

type persistedAction struct {
	Kind                  action.Kind            `json:"kind"`
	Target                persistedTarget        `json:"target"`
	Coordinate            *persistedCoordinate   `json:"coordinate,omitempty"`
	DisplayValue          string                 `json:"display_value,omitempty"`
	ValueLength           int                    `json:"value_length"`
	Sensitivity           recordings.Sensitivity `json:"sensitivity"`
	Gesture               *persistedGesture      `json:"gesture,omitempty"`
	KeyCode               int                    `json:"key_code"`
	IrreversibleConfirmed bool                   `json:"irreversible_confirmed"`
	TimeoutMillis         int64                  `json:"timeout_ms"`
	Postcondition         string                 `json:"postcondition"`
}

type persistedCapture struct {
	ObservationID   string                       `json:"observation_id,omitempty"`
	CapturedAt      string                       `json:"captured_at"`
	CoordinateSpace string                       `json:"coordinate_space,omitempty"`
	PackageName     string                       `json:"package_name,omitempty"`
	ActivityName    string                       `json:"activity_name,omitempty"`
	AppVersion      string                       `json:"app_version,omitempty"`
	DisplayWidth    int                          `json:"display_width"`
	DisplayHeight   int                          `json:"display_height"`
	Orientation     string                       `json:"orientation,omitempty"`
	ScreenshotHash  string                       `json:"screenshot_hash,omitempty"`
	UITreeHash      string                       `json:"ui_tree_hash,omitempty"`
	Status          recordings.CaptureStatus     `json:"status"`
	Sanitization    recordings.SanitizationState `json:"sanitization"`
	Partial         bool                         `json:"partial"`
	ErrorClass      domain.FailureClass          `json:"error_class,omitempty"`
	Target          *persistedTarget             `json:"target,omitempty"`
}

func persistedTargetFromDomain(value recordings.TargetMetadata) persistedTarget {
	return persistedTarget{ResourceID: value.Semantic.ResourceID, AccessibilityLabel: value.Semantic.AccessibilityLabel, StableText: value.Semantic.StableText, ContextFingerprint: value.Semantic.ContextFingerprint, ClassName: value.ClassName, Bounds: value.Bounds, Actionable: value.Actionable, Enabled: value.Enabled, Editable: value.Editable, Selected: value.Selected, Checked: value.Checked, Source: value.Source, Confidence: value.Confidence, CandidateCount: value.CandidateCount}
}

func persistedTargetToDomain(value persistedTarget) recordings.TargetMetadata {
	return recordings.TargetMetadata{Semantic: action.SemanticTarget{ResourceID: value.ResourceID, AccessibilityLabel: value.AccessibilityLabel, StableText: value.StableText, ContextFingerprint: value.ContextFingerprint}, ClassName: value.ClassName, Bounds: value.Bounds, Actionable: value.Actionable, Enabled: value.Enabled, Editable: value.Editable, Selected: value.Selected, Checked: value.Checked, Source: value.Source, Confidence: value.Confidence, CandidateCount: value.CandidateCount}
}

func persistedActionFromDomain(value recordings.LogicalAction) persistedAction {
	result := persistedAction{Kind: value.Kind, Target: persistedTargetFromDomain(value.Target), DisplayValue: value.DisplayValue, ValueLength: value.ValueLength, Sensitivity: value.Sensitivity, KeyCode: value.KeyCode, IrreversibleConfirmed: value.IrreversibleConfirmed, TimeoutMillis: value.TimeoutMillis, Postcondition: value.Postcondition}
	if recordings.IsSensitiveTarget(value.Target) {
		result.Target = persistedTarget{}
	}
	if value.Coordinate != nil {
		result.Coordinate = &persistedCoordinate{Space: value.Coordinate.Space, X: value.Coordinate.X, Y: value.Coordinate.Y}
	}
	if value.Gesture != nil {
		gesture := &persistedGesture{DurationMs: value.Gesture.DurationMs, Points: make([]persistedCoordinate, len(value.Gesture.Points))}
		for index, point := range value.Gesture.Points {
			gesture.Points[index] = persistedCoordinate{Space: point.Space, X: point.X, Y: point.Y}
		}
		result.Gesture = gesture
	}
	return result
}

func persistedActionToDomain(value persistedAction) recordings.LogicalAction {
	result := recordings.LogicalAction{Kind: value.Kind, Target: persistedTargetToDomain(value.Target), DisplayValue: value.DisplayValue, ValueLength: value.ValueLength, Sensitivity: value.Sensitivity, KeyCode: value.KeyCode, IrreversibleConfirmed: value.IrreversibleConfirmed, TimeoutMillis: value.TimeoutMillis, Postcondition: value.Postcondition}
	if value.Coordinate != nil {
		result.Coordinate = &action.Coordinate{Space: value.Coordinate.Space, X: value.Coordinate.X, Y: value.Coordinate.Y}
	}
	if value.Gesture != nil {
		gesture := &recordings.Gesture{DurationMs: value.Gesture.DurationMs, Points: make([]action.Coordinate, len(value.Gesture.Points))}
		for index, point := range value.Gesture.Points {
			gesture.Points[index] = action.Coordinate{Space: point.Space, X: point.X, Y: point.Y}
		}
		result.Gesture = gesture
	}
	return result
}

func recordingCaptureFromJSON(value, observationID, freshness, screenshotHash, treeHash string, phase recordings.CapturePhase) *recordings.Capture {
	if strings.TrimSpace(value) == "" || value == "{}" {
		if observationID == "" && freshness == "" {
			return nil
		}
		return &recordings.Capture{ObservationID: observationID, FreshnessToken: freshness, ScreenshotHash: screenshotHash, UITreeHash: treeHash, Status: recordings.CapturePartial, Sanitization: recordings.Sanitized, Partial: true, ErrorClass: domain.FailureObservation}
	}
	var stored persistedCapture
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return nil
	}
	result := &recordings.Capture{ObservationID: stored.ObservationID, FreshnessToken: freshness, CapturedAt: parseTimeOrZero(stored.CapturedAt), CoordinateSpace: stored.CoordinateSpace, PackageName: stored.PackageName, ActivityName: stored.ActivityName, AppVersion: stored.AppVersion, DisplayWidth: stored.DisplayWidth, DisplayHeight: stored.DisplayHeight, Orientation: stored.Orientation, ScreenshotHash: stored.ScreenshotHash, UITreeHash: stored.UITreeHash, Status: stored.Status, Sanitization: stored.Sanitization, Partial: stored.Partial, ErrorClass: stored.ErrorClass}
	if result.ScreenshotHash == "" {
		result.ScreenshotHash = screenshotHash
	}
	if result.UITreeHash == "" {
		result.UITreeHash = treeHash
	}
	if stored.Target != nil {
		target := persistedTargetToDomain(*stored.Target)
		result.Target = &target
	}
	_ = phase
	return result
}

func observationID(capture *recordings.Capture) string {
	if capture == nil {
		return ""
	}
	return capture.ObservationID
}

func freshness(capture *recordings.Capture) string {
	if capture == nil {
		return ""
	}
	return capture.FreshnessToken
}

func eventHashes(event recordings.InteractionEvent) (raw, annotated, tree string) {
	for _, capture := range []*recordings.Capture{event.Before, event.After} {
		if capture == nil {
			continue
		}
		if raw == "" {
			raw = capture.ScreenshotHash
		}
		if tree == "" {
			tree = capture.UITreeHash
		}
		for _, reference := range capture.Evidence {
			if reference.Omitted {
				continue
			}
			hash := reference.ContentHash
			if hash == "" {
				hash = reference.ArtifactID
			}
			switch reference.Kind {
			case recordings.EvidenceRawScreenshot:
				if raw == "" {
					raw = hash
				}
			case recordings.EvidenceAnnotatedScreenshot:
				if annotated == "" {
					annotated = hash
				}
			case recordings.EvidenceUITree:
				if tree == "" {
					tree = hash
				}
			}
		}
	}
	for _, reference := range event.Evidence {
		if reference.Omitted {
			continue
		}
		hash := reference.ContentHash
		if hash == "" {
			hash = reference.ArtifactID
		}
		if reference.Kind == recordings.EvidenceAnnotatedScreenshot && annotated == "" {
			annotated = hash
		}
	}
	return raw, annotated, tree
}

func eventEvidence(event recordings.InteractionEvent) []recordings.EvidenceReference {
	result := make([]recordings.EvidenceReference, 0, len(event.Evidence)+4)
	seen := make(map[string]struct{})
	appendReference := func(reference recordings.EvidenceReference, phase recordings.CapturePhase) {
		if phase != "" {
			reference.Phase = phase
		}
		key := string(reference.Phase) + ":" + string(reference.Kind) + ":" + reference.ArtifactID + ":" + reference.ContentHash
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	for _, reference := range event.Evidence {
		appendReference(reference, reference.Phase)
	}
	if event.Before != nil {
		for _, reference := range event.Before.Evidence {
			appendReference(reference, recordings.PhaseBefore)
		}
	}
	if event.After != nil {
		for _, reference := range event.After.Evidence {
			appendReference(reference, recordings.PhaseAfter)
		}
	}
	return result
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseTimeOrZero(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
