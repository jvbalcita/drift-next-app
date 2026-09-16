package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *DB) recordMutation(ctx context.Context, tx *sql.Tx, workspace, resourceType, resourceID, event, actorType, actorID string) error {
	aid, err := s.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate audit ID", err)
	}
	oid, err := s.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate outbox ID", err)
	}
	corr := resourceType + ":" + resourceID + ":" + event
	if err := s.audit.AppendTx(ctx, tx, AuditEntry{ID: aid, WorkspaceID: workspace, ActorType: actorType, ActorID: actorID, EventName: event, SchemaVersion: 1, ResourceType: resourceType, ResourceID: resourceID, CorrelationID: corr, PayloadJSON: `{}`}); err != nil {
		return err
	}
	return s.outbox.AppendTx(ctx, tx, OutboxEntry{ID: oid, WorkspaceID: workspace, EventName: event, SchemaVersion: 1, CorrelationID: corr, PayloadJSON: `{}`})
}

type GroupService struct{ store *DB }

func NewGroupService(store *DB) *GroupService { return &GroupService{store: store} }

// ReplacePlacement moves a device into a group at a position and renumbers the
// placements it displaces. Position is an order, not a slot: writing a position
// that is already held would collide with the neighbour on the partial unique
// order index, so the displaced rows are staged one past the highest live
// position and then written back one step further down. Both statements are
// collision-free for any row order.
func (s *GroupService) ReplacePlacement(ctx context.Context, m groups.Membership, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(m.ID)) == "" || strings.TrimSpace(string(m.Workspace)) == "" || strings.TrimSpace(string(m.GroupID)) == "" || strings.TrimSpace(string(m.DeviceID)) == "" || m.Position < 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "membership fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	started := m.StartedAt.UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE device_group_memberships SET state='ended', ended_at=? WHERE workspace_id=? AND device_id=? AND state='active'`, now, m.Workspace, m.DeviceID); err != nil {
			return err
		}
		// The staging offset is one past the highest live position, so a staged
		// row can never collide with an unstaged one and the second statement
		// can select exactly the staged rows.
		var offset int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM device_group_memberships WHERE workspace_id=? AND group_id=? AND state='active'`, m.Workspace, m.GroupID).Scan(&offset); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_group_memberships SET position = position + ? WHERE workspace_id=? AND group_id=? AND state='active' AND position >= ?`, offset, m.Workspace, m.GroupID, m.Position); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_group_memberships SET position = position - ? + 1 WHERE workspace_id=? AND group_id=? AND state='active' AND position >= ?`, offset, m.Workspace, m.GroupID, offset); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_group_memberships (id,workspace_id,group_id,device_id,position,state,started_at) VALUES (?,?,?,?,?,'active',?)`, m.ID, m.Workspace, m.GroupID, m.DeviceID, m.Position, started); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(m.Workspace), "group_membership", string(m.ID), "group.placement_replaced", actorType, actorID)
	})
}

// RemoveDevice ends a device's active placement. Ungrouping is the absence of a
// placement, never a stored Ungrouped group: the ended row remains as history.
func (s *GroupService) RemoveDevice(ctx context.Context, w organizations.WorkspaceID, deviceID devices.DeviceID, actorType, actorID string) (groups.Membership, error) {
	if ctx == nil || s == nil || s.store == nil {
		return groups.Membership{}, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(w)) == "" || strings.TrimSpace(string(deviceID)) == "" {
		return groups.Membership{}, platformerrors.New(platformerrors.CodeInvalidInput, "workspace and device are required")
	}
	clockNow := s.store.clock.Now().UTC()
	now := clockNow.Format(time.RFC3339Nano)
	ended := groups.Membership{
		Workspace: w,
		DeviceID:  deviceID,
		State:     groups.MembershipEnded,
		EndedAt:   &clockNow,
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var id, groupID, started string
		if err := tx.QueryRowContext(ctx, `SELECT id,group_id,started_at FROM device_group_memberships WHERE workspace_id=? AND device_id=? AND state='active'`, w, deviceID).Scan(&id, &groupID, &started); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return platformerrors.New(platformerrors.CodeConflict, "device has no active group placement")
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_group_memberships SET state='ended', ended_at=? WHERE workspace_id=? AND id=? AND state='active'`, now, w, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "group placement"); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(w), "group_membership", id, "group.placement_removed", actorType, actorID); err != nil {
			return err
		}
		ended.ID = groups.MembershipID(id)
		ended.GroupID = groups.GroupID(groupID)
		ended.StartedAt, _ = parseTime(started)
		return nil
	})
	if err != nil {
		return groups.Membership{}, err
	}
	return ended, nil
}

func (s *GroupService) Create(ctx context.Context, g groups.Group, actorType, actorID string) (groups.Group, error) {
	if ctx == nil || s == nil || s.store == nil {
		return groups.Group{}, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(g.ID)) == "" || strings.TrimSpace(string(g.Workspace)) == "" || strings.TrimSpace(g.Name) == "" || !g.State.Valid() {
		return groups.Group{}, platformerrors.New(platformerrors.CodeInvalidInput, "group fields are required")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return groups.Group{}, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	created := g
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		// A new group lands at the end of the persisted operator order. Order is
		// never inferred from insertion, so it must be assigned explicitly.
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM device_groups WHERE workspace_id=?`, g.Workspace).Scan(&next); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_groups (id,workspace_id,name,state,created_at,updated_at,row_version,position) VALUES (?,?,?,?,?,?,1,?)`, g.ID, g.Workspace, g.Name, g.State, now, now, next); err != nil {
			return mapConstraint(err)
		}
		if err := s.store.recordMutation(ctx, tx, string(g.Workspace), "device_group", string(g.ID), "group.created", actorType, actorID); err != nil {
			return err
		}
		created.Position = uint32(next)
		return nil
	})
	if err != nil {
		return groups.Group{}, err
	}
	created.RowVersion = 1
	return created, nil
}

// Rename replaces PATCH /groups/:id. The caller's observed row version guards
// the write so a rename never silently overwrites a newer one.
func (s *GroupService) Rename(ctx context.Context, w organizations.WorkspaceID, id groups.GroupID, name string, expectedVersion uint64, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(w)) == "" || strings.TrimSpace(string(id)) == "" || strings.TrimSpace(name) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workspace, group, and name are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE device_groups SET name=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND state='active' AND row_version=?`, strings.TrimSpace(name), now, w, id, expectedVersion)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "device group"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(w), "device_group", string(id), "group.renamed", actorType, actorID)
	})
}

// Retire replaces DELETE /groups/:id. A group that carries placement history is
// retired in place and its current placements end, so the devices fall back
// into the computed Ungrouped view. The rows themselves are never erased:
// memberships are evidence, and the schema restricts deleting a referenced
// group.
func (s *GroupService) Retire(ctx context.Context, w organizations.WorkspaceID, id groups.GroupID, expectedVersion uint64, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(w)) == "" || strings.TrimSpace(string(id)) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workspace and group are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var state groups.GroupState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM device_groups WHERE workspace_id=? AND id=?`, w, id).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return platformerrors.New(platformerrors.CodeNotFound, "device group was not found")
			}
			return err
		}
		if err := groups.TransitionGroup(state, groups.GroupRetired); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "device group cannot be retired", err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_groups SET state='retired', updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND state='active' AND row_version=?`, now, w, id, expectedVersion)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "device group"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_group_memberships SET state='ended', ended_at=? WHERE workspace_id=? AND group_id=? AND state='active'`, now, w, id); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(w), "device_group", string(id), "group.retired", actorType, actorID)
	})
}

// Reorder replaces POST /groups/reorder. The caller names every group exactly
// once; an incomplete or duplicated order is rejected rather than guessed at.
func (s *GroupService) Reorder(ctx context.Context, w organizations.WorkspaceID, ordered []groups.GroupID, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(w)) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workspace is required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM device_groups WHERE workspace_id=?`, w)
		if err != nil {
			return err
		}
		existing := map[groups.GroupID]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			existing[groups.GroupID(id)] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(ordered) != len(existing) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "group order must name every group exactly once")
		}
		seen := map[groups.GroupID]bool{}
		for _, id := range ordered {
			if !existing[id] || seen[id] {
				return platformerrors.New(platformerrors.CodeInvalidInput, "group order must name every group exactly once")
			}
			seen[id] = true
		}
		for index, id := range ordered {
			if _, err := tx.ExecContext(ctx, `UPDATE device_groups SET position=?, updated_at=? WHERE workspace_id=? AND id=?`, index+1, now, w, id); err != nil {
				return mapConstraint(err)
			}
		}
		return s.store.recordMutation(ctx, tx, string(w), "device_group_order", string(w), "group.order_changed", actorType, actorID)
	})
}

type AssignmentService struct{ store *DB }

func NewAssignmentService(store *DB) *AssignmentService { return &AssignmentService{store: store} }
func (s *AssignmentService) Replace(ctx context.Context, a assignments.AutomationAssignment, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(a.ID)) == "" || strings.TrimSpace(string(a.Workspace)) == "" || strings.TrimSpace(string(a.DeviceID)) == "" || strings.TrimSpace(string(a.AutomationAgentID)) == "" || strings.TrimSpace(string(a.ProfileID)) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "assignment fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	at := a.AssignedAt.UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE automation_agent_device_assignments SET state='ended', ended_at=? WHERE workspace_id=? AND device_id=? AND state='active'`, now, a.Workspace, a.DeviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO automation_agent_device_assignments (id,workspace_id,automation_agent_id,profile_id,device_id,state,precedence,assigned_at) VALUES (?,?,?,?,?,'active',?,?)`, a.ID, a.Workspace, a.AutomationAgentID, a.ProfileID, a.DeviceID, a.Precedence, at); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(a.Workspace), "automation_assignment", string(a.ID), "assignment.replaced", actorType, actorID)
	})
}

type NetworkProfileService struct{ store *DB }

func NewNetworkProfileService(store *DB) *NetworkProfileService {
	return &NetworkProfileService{store: store}
}
func (s *NetworkProfileService) Create(ctx context.Context, p networkprofiles.NetworkProfile, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := p.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "network profile is invalid", err)
	}
	ports, err := json.Marshal(p.SortedPorts())
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "encode network profile ports", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if p.IsDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE network_profiles SET is_default=0,updated_at=? WHERE workspace_id=? AND is_default=1`, now, p.Workspace); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_profiles (id,workspace_id,name,address_policy,ports_json,is_default,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, p.ID, p.Workspace, p.Name, p.AddressPolicy, string(ports), boolInt(p.IsDefault), now, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(p.Workspace), "network_profile", string(p.ID), "network_profile.created", actorType, actorID)
	})
}

func (s *NetworkProfileService) Update(ctx context.Context, p networkprofiles.NetworkProfile, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := p.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "network profile is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	ports, err := json.Marshal(p.SortedPorts())
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "encode network profile ports", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if p.IsDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE network_profiles SET is_default=0, updated_at=? WHERE workspace_id=? AND is_default=1 AND id<>?`, now, p.Workspace, p.ID); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE network_profiles SET name=?, address_policy=?, ports_json=?, is_default=?, updated_at=? WHERE workspace_id=? AND id=?`, p.Name, p.AddressPolicy, string(ports), boolInt(p.IsDefault), now, p.Workspace, p.ID)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "network profile"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(p.Workspace), "network_profile", string(p.ID), "network_profile.updated", actorType, actorID)
	})
}

func (s *NetworkProfileService) Delete(ctx context.Context, workspace organizations.WorkspaceID, id networkprofiles.NetworkProfileID, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM network_profiles WHERE workspace_id=? AND id=?`, workspace, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "read affected row count", err)
		}
		// A profile that is absent from this workspace is not_found, not a
		// conflict: a conflict would confirm that an id the caller cannot see
		// exists somewhere else. The delete is workspace-scoped, so another
		// workspace's row is never touched and can never be reported on.
		if affected == 0 {
			return platformerrors.New(platformerrors.CodeNotFound, "network profile was not found")
		}
		// Scan history is immutable evidence: the run survives the profile it
		// named, and only its profile reference is cleared. scan_runs keeps
		// network_profile_id nullable with no restricting key for exactly this.
		if _, err := tx.ExecContext(ctx, `UPDATE scan_runs SET network_profile_id=NULL WHERE workspace_id=? AND network_profile_id=?`, workspace, id); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "network_profile", string(id), "network_profile.deleted", actorType, actorID)
	})
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique") {
		return platformerrors.Wrap(platformerrors.CodeConflict, "resource violates a database constraint", err)
	}
	return err
}
