package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

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

func (s *MirrorService) StopPreview(ctx context.Context, workspace organizations.WorkspaceID, sessionID mirrors.MirrorSessionID, actorType, actorID string) (MirrorPreviewSession, error) {
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
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, loadErr := loadMirrorSessionTx(ctx, tx, workspace, sessionID)
		if loadErr != nil {
			return loadErr
		}
		if err := mirrors.TransitionSession(current.State, mirrors.SessionCompleted); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "mirror session cannot be stopped", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mirror_sessions SET state='completed', finished_at=? WHERE workspace_id=? AND id=? AND state='active'`, now, workspace, sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mirror_targets SET state='cancelled', finished_at=? WHERE workspace_id=? AND mirror_session_id=? AND state IN ('pending','leased','queued','running')`, now, workspace, sessionID); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "mirror_session", string(sessionID), "mirror.preview_stopped", actorType, actorID); err != nil {
			return err
		}
		loaded, listErr := loadMirrorSessionTx(ctx, tx, workspace, sessionID)
		if listErr != nil {
			return listErr
		}
		session = loaded
		return nil
	})
	return session, err
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
