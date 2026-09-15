package sqlite

import (
	"context"

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
