package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edgeagents"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type DeviceRepository struct{ store *DB }

func NewDeviceRepository(store *DB) *DeviceRepository { return &DeviceRepository{store: store} }
func (r *DeviceRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id devices.DeviceID) (devices.Device, error) {
	var d devices.Device
	var state string
	var last sql.NullString
	var retiredAt sql.NullString
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
	if err := validateWorkspace(string(workspace)); err != nil {
		return d, err
	}
	err := r.store.db.QueryRowContext(ctx, deviceProjectionQuery+` WHERE d.workspace_id = ? AND d.id = ?`, string(workspace), string(id)).Scan(&d.ID, &d.Workspace, &d.DisplayName, &d.PlatformVersion, &state, &last, &version, &d.Retired, &retiredAt, &d.ObservedAgain)
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
	if retiredAt.Valid {
		if t, e := time.Parse(time.RFC3339Nano, retiredAt.String); e == nil {
			d.RetiredAt = &t
		}
	}
	return d, nil
}
func (r *DeviceRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]devices.Device, error) {
	return r.ListIncludingRetired(ctx, workspace, false)
}
func (r *DeviceRepository) ListIncludingRetired(ctx context.Context, workspace organizations.WorkspaceID, includeRetired bool) ([]devices.Device, error) {
	if ctx == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, deviceProjectionQuery+` WHERE d.workspace_id = ? AND (? OR retirement.action IS NULL OR retirement.action = 'restored' OR EXISTS (SELECT 1 FROM device_endpoints ep WHERE ep.workspace_id=d.workspace_id AND ep.device_id=d.id AND ep.state='current' AND ep.observed_at > retirement.decided_at)) ORDER BY d.id`, string(workspace), includeRetired)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]devices.Device, 0)
	for rows.Next() {
		var d devices.Device
		var state string
		var last sql.NullString
		var retiredAt sql.NullString
		var version int64
		if err := rows.Scan(&d.ID, &d.Workspace, &d.DisplayName, &d.PlatformVersion, &state, &last, &version, &d.Retired, &retiredAt, &d.ObservedAgain); err != nil {
			return nil, err
		}
		d.State, d.RowVersion = devices.State(state), uint64(version)
		if last.Valid {
			if t, e := time.Parse(time.RFC3339Nano, last.String); e == nil {
				d.LastSeenAt = &t
			}
		}
		if retiredAt.Valid {
			if t, e := time.Parse(time.RFC3339Nano, retiredAt.String); e == nil {
				d.RetiredAt = &t
			}
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	return result, nil
}

const deviceProjectionQuery = `SELECT d.id, d.workspace_id, d.display_name, d.platform_version, d.state, d.last_seen_at, d.row_version,
CASE WHEN retirement.action='retired' THEN 1 ELSE 0 END,
CASE WHEN retirement.action='retired' THEN retirement.decided_at ELSE NULL END,
CASE WHEN retirement.action='retired' AND EXISTS (SELECT 1 FROM device_endpoints ep WHERE ep.workspace_id=d.workspace_id AND ep.device_id=d.id AND ep.state='current' AND ep.observed_at > retirement.decided_at) THEN 1 ELSE 0 END
FROM devices d JOIN device_registry_entries registry ON registry.workspace_id=d.workspace_id AND registry.device_id=d.id
LEFT JOIN device_retirement_decisions retirement ON retirement.id=(SELECT rd.id FROM device_retirement_decisions rd WHERE rd.workspace_id=d.workspace_id AND rd.device_id=d.id ORDER BY rd.rowid DESC LIMIT 1)`

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
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_registry_entries (workspace_id, device_id, created_at) VALUES (?, ?, ?)`, d.Workspace, d.ID, now); err != nil {
			return err
		}
		return s.record(ctx, tx, string(d.Workspace), string(d.ID), "device.created", actorType, actorID)
	})
}

func (s *DeviceService) Retire(ctx context.Context, workspace organizations.WorkspaceID, id devices.DeviceID, reason, actorType, actorID string) (devices.RetirementDecision, error) {
	return s.retirementDecision(ctx, workspace, id, "retired", reason, actorType, actorID)
}

func (s *DeviceService) Restore(ctx context.Context, workspace organizations.WorkspaceID, id devices.DeviceID, reason, actorType, actorID string) (devices.RetirementDecision, error) {
	return s.retirementDecision(ctx, workspace, id, "restored", reason, actorType, actorID)
}

func (s *DeviceService) retirementDecision(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, action, reason, actorType, actorID string) (devices.RetirementDecision, error) {
	var out devices.RetirementDecision
	if ctx == nil || s == nil || s.store == nil || strings.TrimSpace(reason) == "" || strings.TrimSpace(actorID) == "" {
		return out, platformerrors.New(platformerrors.CodeInvalidInput, "device decision fields are required")
	}
	id, err := s.store.ids.NewID()
	if err != nil {
		return out, err
	}
	now := s.store.clock.Now().UTC()
	out = devices.RetirementDecision{ID: id, Workspace: workspace, DeviceID: deviceID, ActorType: actorType, ActorID: actorID, Reason: strings.TrimSpace(reason), Retired: action == "retired", DecidedAt: now}
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM device_registry_entries WHERE workspace_id=? AND device_id=?`, workspace, deviceID).Scan(&found); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "device not found")
		} else if err != nil {
			return err
		}
		var current sql.NullString
		_ = tx.QueryRowContext(ctx, `SELECT action FROM device_retirement_decisions WHERE workspace_id=? AND device_id=? ORDER BY rowid DESC LIMIT 1`, workspace, deviceID).Scan(&current)
		if (action == "retired" && current.String == "retired") || (action == "restored" && current.String != "retired") {
			return platformerrors.New(platformerrors.CodeConflict, "device expectation is already in the requested state")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_retirement_decisions (id,workspace_id,device_id,action,reason,actor_type,actor_id,decided_at) VALUES (?,?,?,?,?,?,?,?)`, id, workspace, deviceID, action, out.Reason, actorType, actorID, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return s.record(ctx, tx, string(workspace), string(deviceID), "device."+action, actorType, actorID)
	})
	return out, err
}

func (s *DeviceService) Delete(ctx context.Context, workspace organizations.WorkspaceID, deviceID devices.DeviceID, confirmation, reason, actorType, actorID string) (devices.Deletion, *devices.DeleteRefusal, error) {
	var out devices.Deletion
	if confirmation != string(deviceID) {
		return out, &devices.DeleteRefusal{Reason: devices.DeleteRefusalConfirmationMismatch}, nil
	}
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(actorID) == "" {
		return out, nil, platformerrors.New(platformerrors.CodeInvalidInput, "deletion reason and actor are required")
	}
	id, err := s.store.ids.NewID()
	if err != nil {
		return out, nil, err
	}
	now := s.store.clock.Now().UTC()
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var serial sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT d.display_name,d.hardware_serial FROM devices d JOIN device_registry_entries r ON r.workspace_id=d.workspace_id AND r.device_id=d.id WHERE d.workspace_id=? AND d.id=?`, workspace, deviceID).Scan(&out.DisplayName, &serial); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "device not found")
		} else if err != nil {
			return err
		}
		checks := []struct {
			q      string
			reason devices.DeleteRefusalReason
		}{
			{`SELECT EXISTS(SELECT 1 FROM device_endpoints WHERE workspace_id=? AND device_id=? AND state='current')`, devices.DeleteRefusalDeviceAttached},
			{`SELECT EXISTS(SELECT 1 FROM device_leases WHERE workspace_id=? AND device_id=? AND state IN ('requested','active'))`, devices.DeleteRefusalActiveLease},
			{`SELECT EXISTS(SELECT 1 FROM mirror_sessions WHERE workspace_id=? AND source_device_id=? AND state IN ('requested','active','paused','stopping') UNION SELECT 1 FROM mirror_targets WHERE workspace_id=? AND follower_device_id=? AND state IN ('pending','leased','queued','running'))`, devices.DeleteRefusalActiveMirror},
			{`SELECT EXISTS(SELECT 1 FROM run_targets WHERE workspace_id=? AND device_id=? AND state IN ('pending','leased','queued','running','verifying'))`, devices.DeleteRefusalActiveRun},
			{`SELECT EXISTS(SELECT 1 FROM device_group_memberships WHERE workspace_id=? AND device_id=? AND state='active' UNION SELECT 1 FROM automation_agent_device_assignments WHERE workspace_id=? AND device_id=? AND state='active' UNION SELECT 1 FROM device_bindings WHERE workspace_id=? AND device_id=? AND state='active')`, devices.DeleteRefusalActiveAssignment},
		}
		for _, check := range checks {
			var blocked bool
			args := make([]any, 0, strings.Count(check.q, "?"))
			for i := 0; i < strings.Count(check.q, "?")/2; i++ {
				args = append(args, workspace, deviceID)
			}
			if err := tx.QueryRowContext(ctx, check.q, args...).Scan(&blocked); err != nil {
				return err
			}
			if blocked {
				return &deleteRefusalError{reason: check.reason}
			}
		}
		out = devices.Deletion{ID: id, Workspace: workspace, DeviceID: deviceID, DisplayName: out.DisplayName, HardwareSerial: serial.String, ActorType: actorType, ActorID: actorID, Reason: strings.TrimSpace(reason), DeletedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_deletions (id,workspace_id,device_id,display_name,hardware_serial,actor_type,actor_id,reason,confirmation_device_id,deleted_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, id, workspace, deviceID, out.DisplayName, nullableString(out.HardwareSerial), actorType, actorID, out.Reason, confirmation, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_registry_entries WHERE workspace_id=? AND device_id=?`, workspace, deviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET hardware_serial=NULL, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=?`, now.Format(time.RFC3339Nano), workspace, deviceID); err != nil {
			return err
		}
		return s.record(ctx, tx, string(workspace), string(deviceID), "device.deleted", actorType, actorID)
	})
	var refusal *deleteRefusalError
	if errors.As(err, &refusal) {
		return devices.Deletion{}, &devices.DeleteRefusal{Reason: refusal.reason}, nil
	}
	return out, nil, err
}

type deleteRefusalError struct{ reason devices.DeleteRefusalReason }

func (e *deleteRefusalError) Error() string { return string(e.reason) }

// Transition moves a device through the lifecycle machine, and NOTHING CALLS IT.
//
// It is PARKED AS UNREACHABLE rather than deleted, and the reason is the decision
// this repository already recorded: a device has no lifecycle beyond its identity
// and its observation history (ARC-116, closed as a decision with no code). A
// lifecycle reading is not a weaker version of the truth about a device, it is a
// different thing that disagrees with it - offering control to a device nobody can
// reach is exactly what that disagreement produced - so the wire status no longer
// reads devices.state, and a caller of this method would re-introduce the reading
// rather than a use for it.
//
// It is left in place, reachable by the two tests that exercise it and by nothing
// else, so that workspace isolation and the domain state-machine contract keep
// their coverage instead of losing it in the change that removed the last reader.
// It is not a wiring gap to close: dispatching it needs a decision, not a call.
func (s *DeviceService) Transition(ctx context.Context, workspace organizations.WorkspaceID, id devices.DeviceID, next devices.State, expected uint64, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current, workspaceID string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id, state FROM devices WHERE workspace_id = ? AND id = ?`, workspace, id).Scan(&workspaceID, &current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "device not found")
		} else if err != nil {
			return err
		}
		if err := devices.Transition(devices.State(current), next); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE devices SET state = ?, updated_at = ?, row_version = row_version + 1 WHERE workspace_id = ? AND id = ? AND row_version = ?`, next, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "device"); err != nil {
			return err
		}
		return s.record(ctx, tx, workspaceID, string(id), "device.transitioned", actorType, actorID)
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
func (r *EdgeAgentRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id edgeagents.EdgeAgentID) (edgeagents.EdgeAgent, error) {
	var a edgeagents.EdgeAgent
	var state string
	var last sql.NullString
	var version int64
	if err := validateWorkspace(string(workspace)); err != nil {
		return a, err
	}
	err := r.store.db.QueryRowContext(ctx, `SELECT id,workspace_id,display_name,version,state,last_seen_at,row_version FROM edge_agents WHERE workspace_id = ? AND id = ?`, workspace, id).Scan(&a.ID, &a.Workspace, &a.DisplayName, &a.Version, &state, &last, &version)
	if err == sql.ErrNoRows {
		return a, platformerrors.New(platformerrors.CodeNotFound, "edge agent not found")
	}
	if err != nil {
		return a, classifyContext(err)
	}
	a.State, a.RowVersion = edgeagents.State(state), uint64(version)
	if last.Valid {
		if t, e := time.Parse(time.RFC3339Nano, last.String); e == nil {
			a.LastSeenAt = &t
		}
	}
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
		if last.Valid {
			if t, e := time.Parse(time.RFC3339Nano, last.String); e == nil {
				a.LastSeenAt = &t
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
