package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type DeviceRepository struct{ store *DB }

func NewDeviceRepository(store *DB) *DeviceRepository { return &DeviceRepository{store: store} }
func (r *DeviceRepository) Get(ctx context.Context, id devices.DeviceID) (devices.Device, error) {
	var d devices.Device
	var state string
	var last sql.NullString
	var version int64
	if ctx == nil {
		return d, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return d, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if strings.TrimSpace(string(id)) == "" {
		return d, platformerrors.New(platformerrors.CodeInvalidInput, "device ID is required")
	}
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, display_name, platform_version, state, last_seen_at, row_version FROM devices WHERE id = ?`, string(id)).Scan(&d.ID, &d.Workspace, &d.DisplayName, &d.PlatformVersion, &state, &last, &version)
	if err == sql.ErrNoRows {
		return d, platformerrors.New(platformerrors.CodeNotFound, "device not found")
	}
	if err != nil {
		return d, classifyContext(err)
	}
	d.State, d.RowVersion = devices.State(state), uint64(version)
	if last.Valid {
		if t, e := time.Parse(time.RFC3339Nano, last.String); e == nil {
			d.LastSeenAt = &t
		}
	}
	return d, nil
}
func (r *DeviceRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]devices.Device, error) {
	if ctx == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, display_name, platform_version, state, last_seen_at, row_version FROM devices WHERE workspace_id = ? ORDER BY id`, string(workspace))
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]devices.Device, 0)
	for rows.Next() {
		var d devices.Device
		var state string
		var last sql.NullString
		var version int64
		if err := rows.Scan(&d.ID, &d.Workspace, &d.DisplayName, &d.PlatformVersion, &state, &last, &version); err != nil {
			return nil, err
		}
		d.State, d.RowVersion = devices.State(state), uint64(version)
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	return result, nil
}

type DeviceService struct{ store *DB }

func NewDeviceService(store *DB) *DeviceService { return &DeviceService{store: store} }
func (s *DeviceService) Create(ctx context.Context, d devices.Device, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(d.ID)) == "" || strings.TrimSpace(string(d.Workspace)) == "" || strings.TrimSpace(d.DisplayName) == "" || !d.State.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "device fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, d.ID, d.Workspace, d.DisplayName, d.PlatformVersion, d.State, now, now)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, string(d.Workspace), string(d.ID), "device.created", actorType, actorID)
	})
}
func (s *DeviceService) Transition(ctx context.Context, id devices.DeviceID, next devices.State, expected uint64, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current, workspace string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id, state FROM devices WHERE id = ?`, id).Scan(&workspace, &current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "device not found")
		} else if err != nil {
			return err
		}
		if err := devices.Transition(devices.State(current), next); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE devices SET state = ?, updated_at = ?, row_version = row_version + 1 WHERE id = ? AND row_version = ?`, next, s.store.clock.Now().UTC().Format(time.RFC3339Nano), id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "device"); err != nil {
			return err
		}
		return s.record(ctx, tx, workspace, string(id), "device.transitioned", actorType, actorID)
	})
}
func (s *DeviceService) record(ctx context.Context, tx *sql.Tx, workspace, id, event, actorType, actorID string) error {
	corr := "device:" + id + ":" + event
	auditID, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate audit ID", err)
	}
	outboxID, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate outbox ID", err)
	}
	if err := s.store.audit.AppendTx(ctx, tx, AuditEntry{ID: auditID, WorkspaceID: workspace, ActorType: actorType, ActorID: actorID, EventName: event, SchemaVersion: 1, ResourceType: "device", ResourceID: id, CorrelationID: corr, PayloadJSON: `{}`}); err != nil {
		return err
	}
	return s.store.outbox.AppendTx(ctx, tx, OutboxEntry{ID: outboxID, WorkspaceID: workspace, EventName: event, SchemaVersion: 1, CorrelationID: corr, PayloadJSON: `{}`})
}

type EdgeAgentRepository struct{ store *DB }

func NewEdgeAgentRepository(store *DB) *EdgeAgentRepository {
	return &EdgeAgentRepository{store: store}
}
func (r *EdgeAgentRepository) Get(ctx context.Context, id edgeagents.EdgeAgentID) (edgeagents.EdgeAgent, error) {
	var a edgeagents.EdgeAgent
	var state string
	var last sql.NullString
	var version int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id,workspace_id,display_name,version,state,last_seen_at,row_version FROM edge_agents WHERE id = ?`, id).Scan(&a.ID, &a.Workspace, &a.DisplayName, &a.Version, &state, &last, &version)
	if err == sql.ErrNoRows {
		return a, platformerrors.New(platformerrors.CodeNotFound, "edge agent not found")
	}
	if err != nil {
		return a, classifyContext(err)
	}
	a.State, a.RowVersion = edgeagents.State(state), uint64(version)
	return a, nil
}
func (r *EdgeAgentRepository) List(ctx context.Context, w organizations.WorkspaceID) ([]edgeagents.EdgeAgent, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,display_name,version,state,last_seen_at,row_version FROM edge_agents WHERE workspace_id = ? ORDER BY id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []edgeagents.EdgeAgent{}
	for rows.Next() {
		var a edgeagents.EdgeAgent
		var state string
		var last sql.NullString
		var v int64
		if err := rows.Scan(&a.ID, &a.Workspace, &a.DisplayName, &a.Version, &state, &last, &v); err != nil {
			return nil, err
		}
		a.State, a.RowVersion = edgeagents.State(state), uint64(v)
		out = append(out, a)
	}
	return out, rows.Err()
}
