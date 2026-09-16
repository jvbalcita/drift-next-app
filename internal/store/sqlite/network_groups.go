package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"encoding/json"
	"errors"
	"time"
)

type NetworkProfileRepository struct{ store *DB }

func NewNetworkProfileRepository(store *DB) *NetworkProfileRepository {
	return &NetworkProfileRepository{store: store}
}

func (r *NetworkProfileRepository) Get(ctx context.Context, w organizations.WorkspaceID, id networkprofiles.NetworkProfileID) (networkprofiles.NetworkProfile, error) {
	if r == nil || r.store == nil {
		return networkprofiles.NetworkProfile{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	return r.store.GetNetworkProfile(ctx, w, id)
}
func (r *NetworkProfileRepository) List(ctx context.Context, w organizations.WorkspaceID) ([]networkprofiles.NetworkProfile, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,name,address_policy,ports_json,is_default FROM network_profiles WHERE workspace_id=? ORDER BY id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []networkprofiles.NetworkProfile{}
	for rows.Next() {
		var p networkprofiles.NetworkProfile
		var ports string
		var d int
		if err := rows.Scan(&p.ID, &p.Workspace, &p.Name, &p.AddressPolicy, &ports, &d); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(ports), &p.Ports)
		p.IsDefault = d != 0

		out = append(out, p)
	}
	return out, rows.Err()
}

type GroupRepository struct{ store *DB }

func NewGroupRepository(store *DB) *GroupRepository { return &GroupRepository{store: store} }

func (r *GroupRepository) Get(ctx context.Context, w organizations.WorkspaceID, id groups.GroupID) (groups.Group, error) {
	if r == nil || r.store == nil {
		return groups.Group{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(w)); err != nil {
		return groups.Group{}, err
	}
	var g groups.Group
	var version int64
	var position int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id,workspace_id,name,state,position,row_version FROM device_groups WHERE workspace_id=? AND id=?`, w, id).Scan(&g.ID, &g.Workspace, &g.Name, &g.State, &position, &version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return groups.Group{}, platformerrors.New(platformerrors.CodeNotFound, "device group was not found")
		}
		return groups.Group{}, classifyContext(err)
	}
	if position < 0 {
		return groups.Group{}, platformerrors.New(platformerrors.CodeInternal, "device group order is invalid")
	}
	g.Position = uint32(position)
	g.RowVersion = uint64(version)
	return g, nil
}

func (r *GroupRepository) List(ctx context.Context, w organizations.WorkspaceID) ([]groups.Group, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,name,state,position,row_version FROM device_groups WHERE workspace_id=? ORDER BY position,id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []groups.Group{}
	for rows.Next() {
		var g groups.Group
		var v int64
		var position int64
		if err := rows.Scan(&g.ID, &g.Workspace, &g.Name, &g.State, &position, &v); err != nil {
			return nil, err
		}
		if position < 0 {
			return nil, platformerrors.New(platformerrors.CodeInternal, "device group order is invalid")
		}
		g.Position = uint32(position)
		g.RowVersion = uint64(v)
		out = append(out, g)
	}
	return out, rows.Err()
}
func (r *GroupRepository) ListMemberships(ctx context.Context, w organizations.WorkspaceID, gid groups.GroupID) ([]groups.Membership, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,group_id,device_id,position,state,started_at,ended_at FROM device_group_memberships WHERE workspace_id=? AND group_id=? ORDER BY position,id`, w, gid)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []groups.Membership{}
	for rows.Next() {
		var m groups.Membership
		var started string
		var ended sql.NullString
		if err := rows.Scan(&m.ID, &m.Workspace, &m.GroupID, &m.DeviceID, &m.Position, &m.State, &started, &ended); err != nil {
			return nil, err
		}
		m.StartedAt, _ = parseTime(started)
		if ended.Valid {
			t, _ := parseTime(ended.String)
			m.EndedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *GroupRepository) ListAllMemberships(ctx context.Context, w organizations.WorkspaceID) ([]groups.Membership, error) {
	if r == nil || r.store == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(w)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,group_id,device_id,position,state,started_at,ended_at FROM device_group_memberships WHERE workspace_id=? ORDER BY group_id,position,id`, w)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := []groups.Membership{}
	for rows.Next() {
		var m groups.Membership
		var started string
		var ended sql.NullString
		if err := rows.Scan(&m.ID, &m.Workspace, &m.GroupID, &m.DeviceID, &m.Position, &m.State, &started, &ended); err != nil {
			return nil, err
		}
		m.StartedAt, _ = parseTime(started)
		if ended.Valid {
			t, _ := parseTime(ended.String)
			m.EndedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func parseTime(s string) (t time.Time, err error) { return time.Parse(time.RFC3339Nano, s) }
