package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"time"

	"drift.local/drift-next/internal/organizations"
)

type DiscoveryRepository struct{ store *DB }

func NewDiscoveryRepository(store *DB) *DiscoveryRepository {
	return &DiscoveryRepository{store: store}
}
func (r *DiscoveryRepository) ListCandidates(ctx context.Context, w organizations.WorkspaceID, state discovery.CandidateState) ([]discovery.ScanCandidate, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,scan_run_id,host,port,serial,fingerprint,state,discovered_at FROM scan_candidates WHERE workspace_id=? AND (?='' OR state=?) ORDER BY discovered_at,id`, w, state, state)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []discovery.ScanCandidate{}
	for rows.Next() {
		var c discovery.ScanCandidate
		var at string
		if err := rows.Scan(&c.ID, &c.Workspace, &c.ScanRunID, &c.Host, &c.Port, &c.Serial, &c.Fingerprint, &c.State, &at); err != nil {
			return nil, err
		}
		c.DiscoveredAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, c)
	}
	return out, rows.Err()
}

type AssignmentRepository struct{ store *DB }

func NewAssignmentRepository(store *DB) *AssignmentRepository {
	return &AssignmentRepository{store: store}
}
func (r *AssignmentRepository) ListBindings(ctx context.Context, w organizations.WorkspaceID, d devices.DeviceID) ([]assignments.EdgeBinding, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,edge_agent_id,state,bound_at,ended_at FROM device_bindings WHERE workspace_id=? AND device_id=? ORDER BY bound_at,id`, w, d)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []assignments.EdgeBinding{}
	for rows.Next() {
		var b assignments.EdgeBinding
		var at string
		var ended sql.NullString
		if err := rows.Scan(&b.ID, &b.Workspace, &b.DeviceID, &b.EdgeAgentID, &b.State, &at, &ended); err != nil {
			return nil, err
		}
		b.BoundAt, _ = time.Parse(time.RFC3339Nano, at)
		if ended.Valid {
			t, _ := time.Parse(time.RFC3339Nano, ended.String)
			b.EndedAt = &t
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *AssignmentRepository) ListAssignments(ctx context.Context, w organizations.WorkspaceID, d devices.DeviceID) ([]assignments.AutomationAssignment, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,automation_agent_id,profile_id,state,assigned_at,ended_at,precedence FROM automation_agent_device_assignments WHERE workspace_id=? AND device_id=? ORDER BY assigned_at,id`, w, d)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []assignments.AutomationAssignment{}
	for rows.Next() {
		var a assignments.AutomationAssignment
		var at string
		var ended sql.NullString
		if err := rows.Scan(&a.ID, &a.Workspace, &a.DeviceID, &a.AutomationAgentID, &a.ProfileID, &a.State, &at, &ended, &a.Precedence); err != nil {
			return nil, err
		}
		a.AssignedAt, _ = time.Parse(time.RFC3339Nano, at)
		if ended.Valid {
			t, _ := time.Parse(time.RFC3339Nano, ended.String)
			a.EndedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
