package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/health"
	"drift.local/drift-next/internal/inventory"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type InventoryRepository struct{ store *DB }

func NewInventoryRepository(store *DB) *InventoryRepository {
	return &InventoryRepository{store: store}
}

func (r *InventoryRepository) Current(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) (inventory.Record, error) {
	var record inventory.Record
	if err := validateWorkspace(string(workspace)); err != nil {
		return record, err
	}
	var observed string
	var version int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, device_id, COALESCE(source_observation_id,''), inventory_json, observed_at, row_version FROM device_inventory WHERE workspace_id=? AND device_id=?`, workspace, deviceID).Scan(&record.ID, &record.Workspace, &record.DeviceID, &record.SourceObservationID, &record.InventoryJSON, &observed, &version)
	if err == sql.ErrNoRows {
		return record, platformerrors.New(platformerrors.CodeNotFound, "current inventory not found")
	}
	if err != nil {
		return record, classifyContext(err)
	}
	record.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
	record.RowVersion = uint64(version)
	return record, nil
}

func (r *InventoryRepository) ListSnapshots(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) ([]inventory.Snapshot, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, device_id, COALESCE(source_observation_id,''), inventory_json, observed_at FROM inventory_snapshots WHERE workspace_id=? AND (?='' OR device_id=?) ORDER BY observed_at DESC, id DESC`, workspace, deviceID, deviceID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]inventory.Snapshot, 0)
	for rows.Next() {
		var snapshot inventory.Snapshot
		var observed string
		if err := rows.Scan(&snapshot.ID, &snapshot.Workspace, &snapshot.DeviceID, &snapshot.SourceObservationID, &snapshot.InventoryJSON, &observed); err != nil {
			return nil, err
		}
		snapshot.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

type InventoryService struct{ store *DB }

func NewInventoryService(store *DB) *InventoryService { return &InventoryService{store: store} }

func (s *InventoryService) Record(ctx context.Context, record inventory.Record, actorType, actorID string) (inventory.Record, error) {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return record, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := record.Validate(); err != nil {
		return record, platformerrors.Wrap(platformerrors.CodeInvalidInput, "inventory is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return record, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var source any
		if record.SourceObservationID != "" {
			var sourceDevice string
			if err := tx.QueryRowContext(ctx, `SELECT device_id FROM observation_snapshots WHERE workspace_id=? AND id=?`, record.Workspace, record.SourceObservationID).Scan(&sourceDevice); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "source observation not found")
			} else if err != nil {
				return err
			} else if sourceDevice != string(record.DeviceID) {
				return platformerrors.New(platformerrors.CodeInvalidInput, "inventory source observation belongs to another device")
			}
			source = record.SourceObservationID
		}
		snapshotID, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate inventory snapshot ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_snapshots (id, workspace_id, device_id, source_observation_id, inventory_json, observed_at) VALUES (?, ?, ?, ?, ?, ?)`, snapshotID, record.Workspace, record.DeviceID, source, record.InventoryJSON, record.ObservedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_inventory (id, workspace_id, device_id, source_observation_id, inventory_json, observed_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, 1) ON CONFLICT(workspace_id, device_id) DO UPDATE SET source_observation_id=excluded.source_observation_id, inventory_json=excluded.inventory_json, observed_at=excluded.observed_at, updated_at=excluded.updated_at, row_version=device_inventory.row_version+1`, record.ID, record.Workspace, record.DeviceID, source, record.InventoryJSON, record.ObservedAt.UTC().Format(time.RFC3339Nano), now); err != nil {
			return mapConstraint(err)
		}
		if err := s.store.recordMutation(ctx, tx, string(record.Workspace), "inventory", string(record.ID), "inventory.recorded", actorType, actorID); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		current, currentErr := NewInventoryRepository(s.store).Current(ctx, record.Workspace, string(record.DeviceID))
		if currentErr == nil {
			return current, nil
		}
		return record, currentErr
	}
	return record, err
}

type HealthRepository struct{ store *DB }

func NewHealthRepository(store *DB) *HealthRepository { return &HealthRepository{store: store} }

func (r *HealthRepository) Current(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) (health.Current, error) {
	var current health.Current
	var sampled, updated string
	var version int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, device_id, source_sample_id, status, sampled_at, updated_at, row_version FROM device_health_current WHERE workspace_id=? AND device_id=?`, workspace, deviceID).Scan(&current.ID, &current.Workspace, &current.DeviceID, &current.SourceSample, &current.Status, &sampled, &updated, &version)
	if err == sql.ErrNoRows {
		return current, platformerrors.New(platformerrors.CodeNotFound, "current health not found")
	}
	if err != nil {
		return current, classifyContext(err)
	}
	current.SampledAt, _ = time.Parse(time.RFC3339Nano, sampled)
	current.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	current.RowVersion = uint64(version)
	return current, nil
}

func (r *HealthRepository) List(ctx context.Context, workspace organizations.WorkspaceID, deviceID string) ([]health.Sample, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, device_id, COALESCE(observation_id,''), status, battery_percent, sampled_at, details_json FROM health_samples WHERE workspace_id=? AND (?='' OR device_id=?) ORDER BY sampled_at DESC, id DESC`, workspace, deviceID, deviceID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]health.Sample, 0)
	for rows.Next() {
		var sample health.Sample
		var sampled string
		var battery sql.NullInt64
		if err := rows.Scan(&sample.ID, &sample.Workspace, &sample.DeviceID, &sample.ObservationID, &sample.Status, &battery, &sampled, &sample.DetailsJSON); err != nil {
			return nil, err
		}
		if battery.Valid {
			value := int(battery.Int64)
			sample.Battery = &value
		}
		sample.SampledAt, _ = time.Parse(time.RFC3339Nano, sampled)
		result = append(result, sample)
	}
	return result, rows.Err()
}

type HealthService struct{ store *DB }

func NewHealthService(store *DB) *HealthService { return &HealthService{store: store} }

func (s *HealthService) Record(ctx context.Context, sample health.Sample, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := sample.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "health sample is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	var observation any
	if sample.ObservationID != "" {
		observation = sample.ObservationID
	}
	var battery any
	if sample.Battery != nil {
		battery = *sample.Battery
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if sample.ObservationID != "" {
			var observationDevice string
			if err := tx.QueryRowContext(ctx, `SELECT device_id FROM observation_snapshots WHERE workspace_id=? AND id=?`, sample.Workspace, sample.ObservationID).Scan(&observationDevice); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "health observation not found")
			} else if err != nil {
				return err
			} else if observationDevice != string(sample.DeviceID) {
				return platformerrors.New(platformerrors.CodeInvalidInput, "health observation belongs to another device")
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO health_samples (id, workspace_id, device_id, observation_id, status, battery_percent, sampled_at, details_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sample.ID, sample.Workspace, sample.DeviceID, observation, sample.Status, battery, sample.SampledAt.UTC().Format(time.RFC3339Nano), sample.DetailsJSON); err != nil {
			return mapConstraint(err)
		}
		currentID, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate health projection ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_health_current (id, workspace_id, device_id, source_sample_id, status, sampled_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, 1) ON CONFLICT(workspace_id, device_id) DO UPDATE SET source_sample_id=excluded.source_sample_id, status=excluded.status, sampled_at=excluded.sampled_at, updated_at=excluded.updated_at, row_version=device_health_current.row_version+1`, currentID, sample.Workspace, sample.DeviceID, sample.ID, sample.Status, sample.SampledAt.UTC().Format(time.RFC3339Nano), now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(sample.Workspace), "health_sample", string(sample.ID), "health.recorded", actorType, actorID)
	})
}
