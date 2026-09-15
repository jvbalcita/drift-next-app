package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"encoding/json"
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_group_memberships (id,workspace_id,group_id,device_id,position,state,started_at) VALUES (?,?,?,?,?,'active',?)`, m.ID, m.Workspace, m.GroupID, m.DeviceID, m.Position, started); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(m.Workspace), "group_membership", string(m.ID), "group.placement_replaced", actorType, actorID)
	})
}

func (s *GroupService) Create(ctx context.Context, g groups.Group, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(string(g.ID)) == "" || strings.TrimSpace(string(g.Workspace)) == "" || strings.TrimSpace(g.Name) == "" || !g.State.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "group fields are required")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_groups (id,workspace_id,name,state,created_at,updated_at,row_version) VALUES (?,?,?,?,?,?,1)`, g.ID, g.Workspace, g.Name, g.State, now, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(g.Workspace), "device_group", string(g.ID), "group.created", actorType, actorID)
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
		return platformerrors.New(platformerrors.CodeInvalidInput, "context, SQLite store, and row version are required")
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
		if err := RequireAffected(result, "network profile"); err != nil {
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
