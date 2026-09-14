package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/policies"
)

type PolicyRepository struct{ store *DB }

func NewPolicyRepository(store *DB) *PolicyRepository { return &PolicyRepository{store: store} }

func (r *PolicyRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id policies.PolicyID) (policies.Policy, error) {
	var policy policies.Policy
	if err := validateWorkspace(string(workspace)); err != nil {
		return policy, err
	}
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state FROM policies WHERE workspace_id=? AND id=?`, workspace, id).Scan(&policy.ID, &policy.Workspace, &policy.Name, &policy.Version, &policy.RuleJSON, &policy.State)
	if err == sql.ErrNoRows {
		return policy, platformerrors.New(platformerrors.CodeNotFound, "policy not found")
	}
	if err != nil {
		return policy, classifyContext(err)
	}
	return policy, nil
}

func (r *PolicyRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]policies.Policy, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state FROM policies WHERE workspace_id=? ORDER BY name, version, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]policies.Policy, 0)
	for rows.Next() {
		var policy policies.Policy
		if err := rows.Scan(&policy.ID, &policy.Workspace, &policy.Name, &policy.Version, &policy.RuleJSON, &policy.State); err != nil {
			return nil, err
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

type PolicyService struct{ store *DB }

func NewPolicyService(store *DB) *PolicyService { return &PolicyService{store: store} }

func (s *PolicyService) Create(ctx context.Context, policy policies.Policy, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := policy.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "policy is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if policy.State == policies.Active {
			if _, err := tx.ExecContext(ctx, `UPDATE policies SET state='superseded' WHERE workspace_id=? AND name=? AND state='active'`, policy.Workspace, policy.Name); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO policies (id, workspace_id, name, version, rule_json, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, policy.ID, policy.Workspace, policy.Name, policy.Version, policy.RuleJSON, policy.State, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(policy.Workspace), "policy", string(policy.ID), "policy.created", actorType, actorID)
	})
}

func (s *PolicyService) Activate(ctx context.Context, workspace organizations.WorkspaceID, id policies.PolicyID, actorType, actorID string) (policies.Policy, error) {
	var policy policies.Policy
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return policy, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return policy, err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return policy, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadPolicyTx(ctx, tx, workspace, id, &policy); err != nil {
			return err
		}
		if err := policies.Transition(policy.State, policies.Active); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "policy cannot be activated", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE policies SET state='superseded' WHERE workspace_id=? AND name=? AND state='active' AND id<>?`, workspace, policy.Name, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE policies SET state='active' WHERE workspace_id=? AND id=? AND state='draft'`, workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "policy"); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "policy changed before activation", err)
		}
		policy.State = policies.Active
		return s.store.recordMutation(ctx, tx, string(workspace), "policy", string(id), "policy.activated", actorType, actorID)
	})
	return policy, err
}

func loadPolicyTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace organizations.WorkspaceID, id policies.PolicyID, policy *policies.Policy) error {
	err := row.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state FROM policies WHERE workspace_id=? AND id=?`, workspace, id).Scan(&policy.ID, &policy.Workspace, &policy.Name, &policy.Version, &policy.RuleJSON, &policy.State)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "policy not found")
	}
	return err
}

func ensureDefaultPolicyTx(ctx context.Context, tx *sql.Tx, store *DB, workspace organizations.WorkspaceID, actorType, actorID string) (policies.Policy, error) {
	var policy policies.Policy
	err := loadActivePolicyTx(ctx, tx, workspace, &policy)
	if err == nil {
		return policy, nil
	}
	if platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		return policy, err
	}
	id, err := store.ids.NewID()
	if err != nil {
		return policy, platformerrors.Wrap(platformerrors.CodeInternal, "generate default policy ID", err)
	}
	now := store.clock.Now().UTC().Format(time.RFC3339Nano)
	policy = policies.Policy{ID: policies.PolicyID(id), Workspace: workspace, Name: "default", Version: 1, RuleJSON: `{}`, State: policies.Active}
	if _, err := tx.ExecContext(ctx, `INSERT INTO policies (id, workspace_id, name, version, rule_json, state, created_at) VALUES (?, ?, 'default', 1, '{}', 'active', ?)`, id, workspace, now); err != nil {
		return policies.Policy{}, mapConstraint(err)
	}
	if err := store.recordMutation(ctx, tx, string(workspace), "policy", id, "policy.defaulted", actorType, actorID); err != nil {
		return policies.Policy{}, err
	}
	return policy, nil
}

func loadActivePolicyTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace organizations.WorkspaceID, policy *policies.Policy) error {
	err := row.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state FROM policies WHERE workspace_id=? AND state='active' ORDER BY version DESC, id LIMIT 1`, workspace).Scan(&policy.ID, &policy.Workspace, &policy.Name, &policy.Version, &policy.RuleJSON, &policy.State)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "active policy not found")
	}
	return err
}
