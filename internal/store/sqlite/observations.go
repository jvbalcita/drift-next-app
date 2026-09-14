package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type ObservationRepository struct{ store *DB }

func NewObservationRepository(store *DB) *ObservationRepository {
	return &ObservationRepository{store: store}
}

func (r *ObservationRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id observations.ObservationID) (observations.ObservationSnapshot, error) {
	var observation observations.ObservationSnapshot
	if err := validateWorkspace(string(workspace)); err != nil {
		return observation, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return observation, platformerrors.New(platformerrors.CodeInvalidInput, "observation ID is required")
	}
	err := scanObservation(r.store.db.QueryRowContext(ctx, observationQuery+` WHERE workspace_id=? AND id=?`, workspace, id), &observation)
	if err == sql.ErrNoRows {
		return observation, platformerrors.New(platformerrors.CodeNotFound, "observation not found")
	}
	if err != nil {
		return observation, classifyContext(err)
	}
	return observation, nil
}

func (r *ObservationRepository) List(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) ([]observations.ObservationSnapshot, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, observationQuery+` WHERE workspace_id=? AND (?='' OR device_id=?) ORDER BY captured_at DESC, id DESC`, workspace, deviceID, deviceID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]observations.ObservationSnapshot, 0)
	for rows.Next() {
		var observation observations.ObservationSnapshot
		if err := scanObservation(rows, &observation); err != nil {
			return nil, err
		}
		result = append(result, observation)
	}
	return result, rows.Err()
}

func (r *ObservationRepository) Current(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) (observations.ObservationSnapshot, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return observations.ObservationSnapshot{}, err
	}
	var observation observations.ObservationSnapshot
	err := scanObservation(r.store.db.QueryRowContext(ctx, observationQuery+` WHERE workspace_id=? AND device_id=? AND state='recorded' ORDER BY captured_at DESC, id DESC LIMIT 1`, workspace, deviceID), &observation)
	if err == sql.ErrNoRows {
		return observation, platformerrors.New(platformerrors.CodeNotFound, "current observation not found")
	}
	return observation, classifyContext(err)
}

type ObservationService struct{ store *DB }

func NewObservationService(store *DB) *ObservationService { return &ObservationService{store: store} }

func (s *ObservationService) Record(ctx context.Context, observation observations.ObservationSnapshot, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := observation.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "observation is invalid", err)
	}
	if observation.State != observations.Recorded {
		return platformerrors.New(platformerrors.CodeInvalidInput, "new observations must be recorded")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	var errorClass any
	if observation.ErrorClass != "" {
		errorClass = string(observation.ErrorClass)
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE observation_snapshots SET state='superseded' WHERE workspace_id=? AND device_id=? AND state='recorded'`, observation.Workspace, observation.DeviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO observation_snapshots (id, workspace_id, device_id, captured_at, capture_correlation_id, coordinate_space, package_name, activity_name, orientation, display_width, display_height, ui_tree_hash, screenshot_hash, source, protocol_version, capture_status, error_class, state, created_at, model_version, truncated, freshness_token, capture_skew_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, observation.ID, observation.Workspace, observation.DeviceID, observation.CapturedAt.UTC().Format(time.RFC3339Nano), observation.CaptureCorrelationID, observation.CoordinateSpace, nullableString(observation.PackageName), nullableString(observation.ActivityName), nullableString(observation.Orientation), observation.DisplayWidth, observation.DisplayHeight, nullableString(observation.UITreeHash), nullableString(observation.ScreenshotHash), observation.Source, observation.ProtocolVersion, observation.CaptureStatus, errorClass, observations.Recorded, now, observation.ModelVersion, boolInt(observation.Truncated), observation.FreshnessToken, observation.CaptureSkewMillis); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(observation.Workspace), "observation", string(observation.ID), "observation.recorded", actorType, actorID)
	})
}

const observationQuery = `SELECT id, workspace_id, device_id, captured_at, capture_correlation_id, coordinate_space, package_name, activity_name, orientation, display_width, display_height, ui_tree_hash, screenshot_hash, source, protocol_version, capture_status, error_class, state, model_version, truncated, freshness_token, capture_skew_ms FROM observation_snapshots`

func scanObservation(row interface{ Scan(...any) error }, observation *observations.ObservationSnapshot) error {
	var captured, state, model, freshness string
	var packageName, activity, orientation, uiTree, screenshot, errorClass sql.NullString
	var truncated int
	err := row.Scan(&observation.ID, &observation.Workspace, &observation.DeviceID, &captured, &observation.CaptureCorrelationID, &observation.CoordinateSpace, &packageName, &activity, &orientation, &observation.DisplayWidth, &observation.DisplayHeight, &uiTree, &screenshot, &observation.Source, &observation.ProtocolVersion, &observation.CaptureStatus, &errorClass, &state, &model, &truncated, &freshness, &observation.CaptureSkewMillis)
	if err != nil {
		return err
	}
	observation.CapturedAt, _ = time.Parse(time.RFC3339Nano, captured)
	if packageName.Valid {
		observation.PackageName = packageName.String
	}
	if activity.Valid {
		observation.ActivityName = activity.String
	}
	if orientation.Valid {
		observation.Orientation = orientation.String
	}
	if uiTree.Valid {
		observation.UITreeHash = uiTree.String
	}
	if screenshot.Valid {
		observation.ScreenshotHash = screenshot.String
	}
	if errorClass.Valid {
		observation.ErrorClass = domain.FailureClass(errorClass.String)
	}
	observation.State = observations.State(state)
	observation.ModelVersion, observation.Truncated, observation.FreshnessToken = model, truncated != 0, freshness
	return nil
}
