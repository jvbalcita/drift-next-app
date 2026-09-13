package sqlite

import (
	"context"
	"database/sql"
	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/groups"
	"drift.local/drift-next/internal/networkprofiles"
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
	if strings.TrimSpace(string(p.ID)) == "" || strings.TrimSpace(string(p.Workspace)) == "" || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.AddressPolicy) == "" || !p.State.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "network profile fields are required")
	}
	ports, err := json.Marshal(p.Ports)
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "encode network profile ports", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if p.IsDefault && p.State != networkprofiles.Active {
			return platformerrors.New(platformerrors.CodeInvalidInput, "only active profiles may be default")
		}
		if p.IsDefault {
			if _, err := tx.ExecContext(ctx, `UPDATE network_profiles SET is_default=0,updated_at=? WHERE workspace_id=? AND state='active' AND is_default=1`, now, p.Workspace); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO network_profiles (id,workspace_id,name,address_policy,ports_json,is_default,state,created_at,updated_at,row_version) VALUES (?,?,?,?,?,?,?,?,?,1)`, p.ID, p.Workspace, p.Name, p.AddressPolicy, string(ports), boolInt(p.IsDefault), p.State, now, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(p.Workspace), "network_profile", string(p.ID), "network_profile.created", actorType, actorID)
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
