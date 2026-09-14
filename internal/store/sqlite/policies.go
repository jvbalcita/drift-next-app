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
	err := scanPolicy(r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version FROM policies WHERE workspace_id=? AND id=?`, workspace, id), &policy)
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
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version FROM policies WHERE workspace_id=? ORDER BY name, version, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]policies.Policy, 0)
	for rows.Next() {
		var policy policies.Policy
		if err := scanPolicy(rows, &policy); err != nil {
			return nil, err
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

func (r *PolicyRepository) ListDecisions(ctx context.Context, workspace organizations.WorkspaceID, resourceType string) ([]policies.PolicyDecision, error) {
	if ctx == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, policy_id, resource_type, resource_id, action, decision, reason_code, correlation_id, actor_id, decided_at FROM policy_decisions WHERE workspace_id=? AND (?='' OR resource_type=?) ORDER BY decided_at, id`, workspace, resourceType, resourceType)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]policies.PolicyDecision, 0)
	for rows.Next() {
		var decision policies.PolicyDecision
		var decided string
		if err := rows.Scan(&decision.ID, &decision.Workspace, &decision.PolicyID, &decision.ResourceType, &decision.ResourceID, &decision.Action, &decision.Decision, &decision.ReasonCode, &decision.CorrelationID, &decision.ActorID, &decided); err != nil {
			return nil, err
		}
		decision.DecidedAt = parseTimeOrZero(decided)
		result = append(result, decision)
	}
	return result, classifyContext(rows.Err())
}

func scanPolicy(row interface{ Scan(...any) error }, policy *policies.Policy) error {
	var created, updated string
	var rowVersion int64
	if err := row.Scan(&policy.ID, &policy.Workspace, &policy.Name, &policy.Version, &policy.RuleJSON, &policy.State, &created, &updated, &rowVersion); err != nil {
		return err
	}
	policy.CreatedAt = parseTimeOrZero(created)
	policy.UpdatedAt = parseTimeOrZero(updated)
	policy.RowVersion = uint64(rowVersion)
	return nil
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
	nowValue := s.store.clock.Now().UTC()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = nowValue
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = policy.CreatedAt
	}
	if policy.RowVersion == 0 {
		policy.RowVersion = 1
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if policy.State == policies.Active {
			if _, err := tx.ExecContext(ctx, `UPDATE policies SET state='superseded', updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND name=? AND state='active'`, nowValue.Format(time.RFC3339Nano), policy.Workspace, policy.Name); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO policies (id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, policy.ID, policy.Workspace, policy.Name, policy.Version, policy.RuleJSON, policy.State, policy.CreatedAt.UTC().Format(time.RFC3339Nano), policy.UpdatedAt.UTC().Format(time.RFC3339Nano), policy.RowVersion); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(policy.Workspace), "policy", string(policy.ID), "policy.created", actorType, actorID)
	})
}

// CreateNextVersion preserves policy definitions as immutable version rows.
// The new version starts as a draft and must be activated explicitly.
func (s *PolicyService) CreateNextVersion(ctx context.Context, workspace organizations.WorkspaceID, baseID policies.PolicyID, ruleJSON, actorType, actorID string) (policies.Policy, error) {
	var next policies.Policy
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return next, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return next, err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return next, platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var base policies.Policy
		if err := loadPolicyTx(ctx, tx, workspace, baseID, &base); err != nil {
			return err
		}
		if base.State == policies.Retired {
			return platformerrors.New(platformerrors.CodePolicyDenied, "retired policy cannot receive a new version")
		}
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM policies WHERE workspace_id=? AND name=?`, workspace, base.Name).Scan(&version); err != nil {
			return err
		}
		id, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate policy version ID", err)
		}
		now := s.store.clock.Now().UTC()
		next = policies.Policy{ID: policies.PolicyID(id), Workspace: workspace, Name: base.Name, Version: version, RuleJSON: ruleJSON, State: policies.Draft, CreatedAt: now, UpdatedAt: now, RowVersion: 1}
		if err := next.Validate(); err != nil {
			return platformerrors.Wrap(platformerrors.CodeInvalidInput, "policy version is invalid", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO policies (id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, next.ID, next.Workspace, next.Name, next.Version, next.RuleJSON, next.State, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), next.RowVersion); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "policy", string(next.ID), "policy.version_created", actorType, actorID)
	})
	return next, err
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
		nowValue := s.store.clock.Now().UTC()
		now := nowValue.Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE policies SET state='superseded', updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND name=? AND state='active' AND id<>?`, now, workspace, policy.Name, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE policies SET state='active', updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND state='draft' AND row_version=?`, now, workspace, id, policy.RowVersion)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "policy"); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "policy changed before activation", err)
		}
		policy.State, policy.UpdatedAt, policy.RowVersion = policies.Active, nowValue, policy.RowVersion+1
		return s.store.recordMutation(ctx, tx, string(workspace), "policy", string(id), "policy.activated", actorType, actorID)
	})
	return policy, err
}

func (s *PolicyService) Retire(ctx context.Context, workspace organizations.WorkspaceID, id policies.PolicyID, expected uint64, actorType, actorID string) (policies.Policy, error) {
	var policy policies.Policy
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return policy, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return policy, err
	}
	if expected == 0 || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return policy, platformerrors.New(platformerrors.CodeInvalidInput, "policy retirement fields are invalid")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := loadPolicyTx(ctx, tx, workspace, id, &policy); err != nil {
			return err
		}
		if err := policies.Transition(policy.State, policies.Retired); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "policy cannot be retired", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE policies SET state='retired', updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, now.Format(time.RFC3339Nano), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "policy"); err != nil {
			return err
		}
		policy.State, policy.UpdatedAt, policy.RowVersion = policies.Retired, now, expected+1
		return s.store.recordMutation(ctx, tx, string(workspace), "policy", string(id), "policy.retired", actorType, actorID)
	})
	return policy, err
}

func loadPolicyTx(ctx context.Context, row interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspace organizations.WorkspaceID, id policies.PolicyID, policy *policies.Policy) error {
	err := scanPolicy(row.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version FROM policies WHERE workspace_id=? AND id=?`, workspace, id), policy)
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
	nowValue := store.clock.Now().UTC()
	now := nowValue.Format(time.RFC3339Nano)
	policy = policies.Policy{ID: policies.PolicyID(id), Workspace: workspace, Name: "default", Version: 1, RuleJSON: `{}`, State: policies.Active, CreatedAt: nowValue, UpdatedAt: nowValue, RowVersion: 1}
	if _, err := tx.ExecContext(ctx, `INSERT INTO policies (id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version) VALUES (?, ?, 'default', 1, '{}', 'active', ?, ?, 1)`, id, workspace, now, now); err != nil {
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
	err := scanPolicy(row.QueryRowContext(ctx, `SELECT id, workspace_id, name, version, rule_json, state, created_at, updated_at, row_version FROM policies WHERE workspace_id=? AND state='active' ORDER BY version DESC, id LIMIT 1`, workspace), policy)
	if err == sql.ErrNoRows {
		return platformerrors.New(platformerrors.CodeNotFound, "active policy not found")
	}
	return err
}
