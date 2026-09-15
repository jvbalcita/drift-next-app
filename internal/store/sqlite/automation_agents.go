package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"drift.local/drift-next/internal/assignments"
	"drift.local/drift-next/internal/automationagents"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type AutomationAgentRepository struct{ store *DB }

func NewAutomationAgentRepository(store *DB) *AutomationAgentRepository {
	return &AutomationAgentRepository{store: store}
}

func (r *AutomationAgentRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]automationagents.AutomationAgent, error) {
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, name, state FROM automation_agents WHERE workspace_id=? ORDER BY id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := make([]automationagents.AutomationAgent, 0)
	for rows.Next() {
		var agent automationagents.AutomationAgent
		if err := rows.Scan(&agent.ID, &agent.Workspace, &agent.Name, &agent.State); err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, classifyContext(rows.Err())
}

func (r *AutomationAgentRepository) ListProfiles(ctx context.Context, workspace organizations.WorkspaceID) ([]automationagents.Profile, error) {
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, automation_agent_id, version, state, personality_json, capabilities_json, memory_scope FROM automation_agent_profiles WHERE workspace_id=? ORDER BY automation_agent_id, version DESC, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := make([]automationagents.Profile, 0)
	for rows.Next() {
		var profile automationagents.Profile
		var capabilitiesJSON string
		if err := rows.Scan(&profile.ID, &profile.Workspace, &profile.AutomationAgentID, &profile.Version, &profile.State, &profile.Personality, &capabilitiesJSON, &profile.MemoryScope); err != nil {
			return nil, err
		}
		if capabilitiesJSON != "" && capabilitiesJSON != "[]" {
			_ = json.Unmarshal([]byte(capabilitiesJSON), &profile.Capabilities)
		}
		out = append(out, profile)
	}
	return out, classifyContext(rows.Err())
}

func (r *AutomationAgentRepository) LatestProfile(ctx context.Context, workspace organizations.WorkspaceID, agentID automationagents.AutomationAgentID) (automationagents.Profile, error) {
	var profile automationagents.Profile
	if r == nil || r.store == nil || r.store.db == nil {
		return profile, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return profile, err
	}
	if strings.TrimSpace(string(agentID)) == "" {
		return profile, platformerrors.New(platformerrors.CodeInvalidInput, "automation agent ID is required")
	}
	var capabilitiesJSON string
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, automation_agent_id, version, state, personality_json, capabilities_json, memory_scope FROM automation_agent_profiles WHERE workspace_id=? AND automation_agent_id=? ORDER BY version DESC, id LIMIT 1`, workspace, agentID).Scan(&profile.ID, &profile.Workspace, &profile.AutomationAgentID, &profile.Version, &profile.State, &profile.Personality, &capabilitiesJSON, &profile.MemoryScope)
	if err == sql.ErrNoRows {
		return profile, platformerrors.New(platformerrors.CodeNotFound, "automation agent profile not found")
	}
	if err != nil {
		return profile, classifyContext(err)
	}
	if capabilitiesJSON != "" && capabilitiesJSON != "[]" {
		_ = json.Unmarshal([]byte(capabilitiesJSON), &profile.Capabilities)
	}
	return profile, nil
}

type AutomationAgentService struct{ store *DB }

func NewAutomationAgentService(store *DB) *AutomationAgentService {
	return &AutomationAgentService{store: store}
}

func (s *AutomationAgentService) Create(ctx context.Context, agent automationagents.AutomationAgent, actorType, actorID string) (automationagents.AutomationAgent, automationagents.Profile, error) {
	var profile automationagents.Profile
	if ctx == nil || s == nil || s.store == nil {
		return agent, profile, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return agent, profile, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	if strings.TrimSpace(string(agent.Workspace)) == "" || strings.TrimSpace(agent.Name) == "" {
		return agent, profile, platformerrors.New(platformerrors.CodeInvalidInput, "automation agent name is required")
	}
	if agent.State == "" {
		agent.State = automationagents.AgentActive
	}
	if !agent.State.Valid() {
		return agent, profile, platformerrors.New(platformerrors.CodeInvalidInput, "automation agent state is invalid")
	}
	if strings.TrimSpace(string(agent.ID)) == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return agent, profile, platformerrors.Wrap(platformerrors.CodeInternal, "generate automation agent ID", err)
		}
		agent.ID = automationagents.AutomationAgentID(id)
	}
	profileID, err := s.store.ids.NewID()
	if err != nil {
		return agent, profile, platformerrors.Wrap(platformerrors.CodeInternal, "generate automation agent profile ID", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	profile = automationagents.Profile{
		ID:                   automationagents.ProfileID(profileID),
		AutomationAgentID:    agent.ID,
		Workspace:            agent.Workspace,
		Version:              1,
		State:                automationagents.ProfileDraft,
		Personality:          "{}",
		Capabilities:         []string{},
		MemoryScope:          automationagents.MemoryNone,
		MemoryRetentionClass: "none",
	}
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO automation_agents (id,workspace_id,name,state,created_at,updated_at,row_version) VALUES (?,?,?,?,?,?,1)`, agent.ID, agent.Workspace, agent.Name, agent.State, now, now); err != nil {
			return mapConstraint(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO automation_agent_profiles (id,workspace_id,automation_agent_id,version,state,personality_json,goals_json,rules_json,capabilities_json,memory_scope,memory_retention_class,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, profile.ID, agent.Workspace, agent.ID, profile.Version, profile.State, "{}", "[]", "[]", "[]", profile.MemoryScope, profile.MemoryRetentionClass, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(agent.Workspace), "automation_agent", string(agent.ID), "automation_agent.created", actorType, actorID)
	})
	return agent, profile, err
}

func (r *AssignmentRepository) ListAllAssignments(ctx context.Context, workspace organizations.WorkspaceID) ([]assignments.AutomationAssignment, error) {
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,device_id,automation_agent_id,profile_id,state,assigned_at,ended_at,precedence FROM automation_agent_device_assignments WHERE workspace_id=? ORDER BY assigned_at,id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	out := make([]assignments.AutomationAssignment, 0)
	for rows.Next() {
		var assignment assignments.AutomationAssignment
		var assignedAt string
		var ended sql.NullString
		if err := rows.Scan(&assignment.ID, &assignment.Workspace, &assignment.DeviceID, &assignment.AutomationAgentID, &assignment.ProfileID, &assignment.State, &assignedAt, &ended, &assignment.Precedence); err != nil {
			return nil, err
		}
		assignment.AssignedAt, _ = time.Parse(time.RFC3339Nano, assignedAt)
		if ended.Valid {
			parsed, _ := time.Parse(time.RFC3339Nano, ended.String)
			assignment.EndedAt = &parsed
		}
		out = append(out, assignment)
	}
	return out, classifyContext(rows.Err())
}