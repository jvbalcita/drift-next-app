package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
	"drift.local/drift-next/internal/runs"
	"drift.local/drift-next/internal/workflows"
)

type sqlContextQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// WorkflowRepository exposes immutable workflow projections.
type WorkflowRepository struct{ store *DB }

func NewWorkflowRepository(store *DB) *WorkflowRepository { return &WorkflowRepository{store: store} }

func (r *WorkflowRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id workflows.ID) (workflows.Workflow, error) {
	var workflow workflows.Workflow
	if err := validateWorkflowReader(ctx, r, workspace, string(id)); err != nil {
		return workflow, err
	}
	var state string
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, state FROM workflows WHERE workspace_id=? AND id=?`, workspace, id).Scan(&workflow.ID, &workflow.Workspace, &workflow.Name, &state)
	if err == sql.ErrNoRows {
		return workflow, platformerrors.New(platformerrors.CodeNotFound, "workflow not found")
	}
	if err != nil {
		return workflow, classifyContext(err)
	}
	workflow.State = workflows.State(state)
	return workflow, nil
}

func (r *WorkflowRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]workflows.Workflow, error) {
	if err := validateWorkflowReader(ctx, r, workspace, "workspace"); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, name, state FROM workflows WHERE workspace_id=? ORDER BY id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]workflows.Workflow, 0)
	for rows.Next() {
		var workflow workflows.Workflow
		var state string
		if err := rows.Scan(&workflow.ID, &workflow.Workspace, &workflow.Name, &state); err != nil {
			return nil, err
		}
		workflow.State = workflows.State(state)
		result = append(result, workflow)
	}
	return result, classifyContext(rows.Err())
}

func (r *WorkflowRepository) GetVersion(ctx context.Context, workspace organizations.WorkspaceID, id workflows.VersionID) (workflows.Version, error) {
	if err := validateWorkflowReader(ctx, r, workspace, string(id)); err != nil {
		return workflows.Version{}, err
	}
	return loadWorkflowVersion(ctx, r.store.db, workspace, id)
}

func (r *WorkflowRepository) PublishedVersion(ctx context.Context, workspace organizations.WorkspaceID, workflowID workflows.ID) (workflows.Version, error) {
	if err := validateWorkflowReader(ctx, r, workspace, string(workflowID)); err != nil {
		return workflows.Version{}, err
	}
	return loadWorkflowVersion(ctx, r.store.db, workspace, "", workflowID)
}

func (r *WorkflowRepository) LatestVersion(ctx context.Context, workspace organizations.WorkspaceID, workflowID workflows.ID) (workflows.Version, error) {
	if err := validateWorkflowReader(ctx, r, workspace, string(workflowID)); err != nil {
		return workflows.Version{}, err
	}
	var version workflows.Version
	var state string
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, workflow_id, version, state FROM workflow_versions WHERE workspace_id=? AND workflow_id=? ORDER BY version DESC LIMIT 1`, workspace, workflowID).Scan(&version.ID, &version.Workspace, &version.WorkflowID, &version.Version, &state)
	if err == sql.ErrNoRows {
		return version, platformerrors.New(platformerrors.CodeNotFound, "workflow version not found")
	}
	if err != nil {
		return version, classifyContext(err)
	}
	version.State = workflows.State(state)
	return version, nil
}

func validateWorkflowReader(ctx context.Context, reader *WorkflowRepository, workspace organizations.WorkspaceID, id string) error {
	if ctx == nil || reader == nil || reader.store == nil || reader.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite repository are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workflow ID is required")
	}
	return nil
}

// WorkflowService owns workflow definition admission and immutable version
// publication. A published version cannot be edited in place.
type WorkflowService struct{ store *DB }

func NewWorkflowService(store *DB) *WorkflowService { return &WorkflowService{store: store} }

func (s *WorkflowService) Create(ctx context.Context, workflow workflows.Workflow, actorType, actorID string) error {
	if err := validateWorkflowServiceInput(ctx, s, workflow.Workspace, actorType, actorID); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "workflow is invalid", err)
	}
	if workflow.State != workflows.StateDraft {
		return platformerrors.New(platformerrors.CodeInvalidInput, "new workflows must be drafts")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflows (id, workspace_id, name, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, 1)`, workflow.ID, workflow.Workspace, workflow.Name, workflow.State, now, now); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(workflow.Workspace), "workflow", string(workflow.ID), "workflow.created", actorType, actorID)
	})
}

func (s *WorkflowService) CreateVersion(ctx context.Context, workspace organizations.WorkspaceID, version workflows.Version, actorType, actorID string) (workflows.Version, error) {
	if err := validateWorkflowServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return version, err
	}
	version.Workspace = workspace
	if version.State != workflows.StateDraft {
		return version, platformerrors.New(platformerrors.CodeInvalidInput, "new workflow versions must be drafts")
	}
	if strings.TrimSpace(string(version.ID)) == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return version, platformerrors.Wrap(platformerrors.CodeInternal, "generate workflow version ID", err)
		}
		version.ID = workflows.VersionID(id)
	}
	for index := range version.Steps {
		if strings.TrimSpace(string(version.Steps[index].ID)) == "" {
			id, err := s.store.ids.NewID()
			if err != nil {
				return version, platformerrors.Wrap(platformerrors.CodeInternal, "generate workflow step ID", err)
			}
			version.Steps[index].ID = workflows.StepID(id)
		}
	}
	if err := version.Validate(); err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInvalidInput, "workflow version is invalid", err)
	}
	definition, err := version.DefinitionJSON()
	if err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInvalidInput, "workflow definition is invalid", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var workflowState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM workflows WHERE workspace_id=? AND id=?`, workspace, version.WorkflowID).Scan(&workflowState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "workflow not found")
		} else if err != nil {
			return err
		}
		if workflowState == string(workflows.StateRetired) {
			return platformerrors.New(platformerrors.CodeConflict, "retired workflow cannot receive a version")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_versions (id, workspace_id, workflow_id, version, state, definition_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, version.ID, workspace, version.WorkflowID, version.Version, version.State, definition, now); err != nil {
			return mapConstraint(err)
		}
		for _, step := range version.Steps {
			stepDefinition, err := json.Marshal(step.Definition)
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "encode workflow step", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_steps (id, workspace_id, workflow_version_id, sequence, action_kind, risk_class, retry_class, definition_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, step.ID, workspace, version.ID, step.Sequence, step.Action, step.Risk, step.Retry, string(stepDefinition)); err != nil {
				return mapConstraint(err)
			}
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "workflow_version", string(version.ID), "workflow.version_created", actorType, actorID)
	})
	if err != nil {
		return version, err
	}
	return version, nil
}

func (s *WorkflowService) TransitionVersion(ctx context.Context, workspace organizations.WorkspaceID, id workflows.VersionID, next workflows.State, actorType, actorID string) (workflows.Version, error) {
	var version workflows.Version
	if err := validateWorkflowServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return version, err
	}
	if !next.Valid() || strings.TrimSpace(string(id)) == "" {
		return version, platformerrors.New(platformerrors.CodeInvalidInput, "workflow version transition fields are invalid")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadWorkflowVersion(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		if err := workflows.Transition(current.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "workflow version transition is invalid", err)
		}
		if next == workflows.StateValidated || next == workflows.StatePublished {
			if err := current.Validate(); err != nil {
				return platformerrors.Wrap(platformerrors.CodeInvalidInput, "workflow version cannot be published", err)
			}
		}
		if next == workflows.StatePublished {
			if _, err := tx.ExecContext(ctx, `UPDATE workflow_versions SET state='deprecated' WHERE workspace_id=? AND workflow_id=? AND state='published' AND id<>?`, workspace, current.WorkflowID, id); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE workflow_versions SET state=? WHERE workspace_id=? AND id=? AND state=?`, next, workspace, id, current.State)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "workflow version"); err != nil {
			return err
		}
		if next == workflows.StatePublished {
			if _, err := tx.ExecContext(ctx, `UPDATE workflows SET state='published', updated_at=? WHERE workspace_id=? AND id=?`, now, workspace, current.WorkflowID); err != nil {
				return err
			}
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "workflow_version", string(id), "workflow.version_"+string(next), actorType, actorID)
	})
	if err != nil {
		return version, err
	}
	return loadWorkflowVersion(ctx, s.store.db, workspace, id)
}

func validateWorkflowServiceInput(ctx context.Context, service *WorkflowService, workspace organizations.WorkspaceID, actorType, actorID string) error {
	if ctx == nil || service == nil || service.store == nil || service.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workflow actor fields are required")
	}
	return nil
}

func loadWorkflowVersion(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id workflows.VersionID, workflowIDs ...workflows.ID) (workflows.Version, error) {
	var version workflows.Version
	if len(workflowIDs) > 0 {
		if err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, workflow_id, version, state FROM workflow_versions WHERE workspace_id=? AND workflow_id=? AND state='published' ORDER BY version DESC LIMIT 1`, workspace, workflowIDs[0]).Scan(&version.ID, &version.Workspace, &version.WorkflowID, &version.Version, &version.State); err == sql.ErrNoRows {
			return version, platformerrors.New(platformerrors.CodeNotFound, "published workflow version not found")
		} else if err != nil {
			return version, classifyContext(err)
		}
	} else {
		var state string
		var versionNumber int
		if err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, workflow_id, version, state FROM workflow_versions WHERE workspace_id=? AND id=?`, workspace, id).Scan(&version.ID, &version.Workspace, &version.WorkflowID, &versionNumber, &state); err == sql.ErrNoRows {
			return version, platformerrors.New(platformerrors.CodeNotFound, "workflow version not found")
		} else if err != nil {
			return version, classifyContext(err)
		}
		version.Version, version.State = versionNumber, workflows.State(state)
	}
	rows, err := queryer.QueryContext(ctx, `SELECT id, sequence, action_kind, risk_class, retry_class, definition_json FROM workflow_steps WHERE workspace_id=? AND workflow_version_id=? ORDER BY sequence`, workspace, version.ID)
	if err != nil {
		return version, classifyContext(err)
	}
	defer rows.Close()
	version.Steps = make([]workflows.Step, 0)
	for rows.Next() {
		var step workflows.Step
		var stepID, actionKind, riskClass, retryClass, definition string
		if err := rows.Scan(&stepID, &step.Sequence, &actionKind, &riskClass, &retryClass, &definition); err != nil {
			return version, err
		}
		if err := json.Unmarshal([]byte(definition), &step.Definition); err != nil {
			return version, platformerrors.Wrap(platformerrors.CodeInternal, "decode workflow step", err)
		}
		step.ID, step.Action, step.Risk, step.Retry = workflows.StepID(stepID), action.Kind(actionKind), action.RiskClass(riskClass), action.RetryClass(retryClass)
		version.Steps = append(version.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return version, classifyContext(err)
	}
	return version, nil
}

type CreateRunRequest struct {
	Workspace         organizations.WorkspaceID
	WorkflowVersionID workflows.VersionID
	Selector          runs.TargetSelector
	ConcurrencyLimit  int
	RetryBudget       int
	Approval          runs.ApprovalState
}

type RunService struct{ store *DB }

func NewRunService(store *DB) *RunService { return &RunService{store: store} }

func (s *RunService) Create(ctx context.Context, request CreateRunRequest, actorType, actorID string) (runs.ParentRun, error) {
	var result runs.ParentRun
	if err := validateRunServiceInput(ctx, s, request.Workspace, actorType, actorID); err != nil {
		return result, err
	}
	if strings.TrimSpace(string(request.WorkflowVersionID)) == "" {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "workflow version ID is required")
	}
	if request.ConcurrencyLimit == 0 {
		request.ConcurrencyLimit = 1
	}
	if err := runs.ValidateConcurrencyLimit(request.ConcurrencyLimit); err != nil || request.RetryBudget < 0 || request.RetryBudget > 1024 {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "run concurrency or retry budget is invalid")
	}
	if request.Approval == "" {
		request.Approval = runs.ApprovalPending
	}
	if !request.Approval.Valid() {
		return result, platformerrors.New(platformerrors.CodeInvalidInput, "run approval state is invalid")
	}
	request.Selector.Normalize()
	if err := request.Selector.Validate(); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInvalidInput, "run target selector is invalid", err)
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		version, err := loadWorkflowVersion(ctx, tx, request.Workspace, request.WorkflowVersionID)
		if err != nil {
			return err
		}
		if version.State != workflows.StatePublished {
			return platformerrors.New(platformerrors.CodeConflict, "only a published workflow version can run")
		}
		deviceIDs, err := resolveTargetIDsTx(ctx, tx, request.Workspace, request.Selector)
		if err != nil {
			return err
		}
		if len(deviceIDs) == 0 {
			return platformerrors.New(platformerrors.CodeUnavailable, "target selector resolved to no eligible devices")
		}
		selectorJSON, err := json.Marshal(struct {
			Selector  runs.TargetSelector `json:"selector"`
			DeviceIDs []devices.DeviceID  `json:"resolved_device_ids"`
		}{Selector: request.Selector, DeviceIDs: deviceIDs})
		if err != nil || len(selectorJSON) > 65536 || redaction.RedactString(string(selectorJSON)) != string(selectorJSON) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "target snapshot is invalid or sensitive")
		}
		if err := s.store.idsForRun(&result, &version, &deviceIDs, now, request); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO runs (id, workspace_id, workflow_version_id, state, concurrency_limit, created_at, approval_state, retry_budget, recovery_count) VALUES (?, ?, ?, 'requested', ?, ?, ?, ?, 0)`, result.ID, request.Workspace, request.WorkflowVersionID, request.ConcurrencyLimit, now.Format(time.RFC3339Nano), request.Approval, request.RetryBudget); err != nil {
			return mapConstraint(err)
		}
		snapshotID := resultSnapshotID(result.ID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO target_set_snapshots (id, workspace_id, run_id, selector_type, selector_json, resolved_at) VALUES (?, ?, ?, ?, ?, ?)`, snapshotID, request.Workspace, result.ID, request.Selector.Type, string(selectorJSON), now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		for _, deviceID := range deviceIDs {
			targetID, err := s.store.ids.NewID()
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate run target ID", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO run_targets (id, workspace_id, run_id, target_snapshot_id, device_id, state, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?)`, targetID, request.Workspace, result.ID, snapshotID, deviceID, now.Format(time.RFC3339Nano)); err != nil {
				return mapConstraint(err)
			}
			for _, step := range version.Steps {
				stepRunID, err := s.store.ids.NewID()
				if err != nil {
					return platformerrors.Wrap(platformerrors.CodeInternal, "generate target workflow step ID", err)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO target_run_steps (id, workspace_id, run_target_id, workflow_step_id, state, attempt_count, created_at) VALUES (?, ?, ?, ?, 'pending', 0, ?)`, stepRunID, request.Workspace, targetID, step.ID, now.Format(time.RFC3339Nano)); err != nil {
					return mapConstraint(err)
				}
			}
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(request.Workspace), string(result.ID), "", "run.requested", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(request.Workspace), "run", string(result.ID), "run.requested", actorType, actorID)
	})
	if err != nil {
		return runs.ParentRun{}, err
	}
	return result, nil
}

// idsForRun allocates the parent and snapshot IDs together. Keeping this
// helper on DB makes the allocation explicit and keeps the transaction body
// free of hidden global state.
func (s *DB) idsForRun(result *runs.ParentRun, version *workflows.Version, deviceIDs *[]devices.DeviceID, now time.Time, request CreateRunRequest) error {
	runID, err := s.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate run ID", err)
	}
	result.ID = runs.RunID(runID)
	result.Workspace = request.Workspace
	result.WorkflowVersion = runs.WorkflowVersionID(version.ID)
	result.State = runs.RunRequested
	result.Approval = request.Approval
	result.ConcurrencyLimit = request.ConcurrencyLimit
	result.RetryBudget = request.RetryBudget
	result.CreatedAt = now
	return nil
}

// resultSnapshotID deterministically derives only a local child key from the
// newly allocated parent key. It is not exposed as a general ID generator and
// therefore cannot collide across runs.
func resultSnapshotID(runID runs.RunID) string { return string(runID) + ":targets" }

func validateRunServiceInput(ctx context.Context, service *RunService, workspace organizations.WorkspaceID, actorType, actorID string) error {
	if ctx == nil || service == nil || service.store == nil || service.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "run actor fields are required")
	}
	return nil
}

func resolveTargetIDsTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, selector runs.TargetSelector) ([]devices.DeviceID, error) {
	var query string
	var args []any
	switch selector.Type {
	case runs.SelectorExplicitDevices:
		result := make([]devices.DeviceID, 0, len(selector.DeviceIDs))
		for _, deviceID := range selector.DeviceIDs {
			var state string
			if err := tx.QueryRowContext(ctx, `SELECT state FROM devices WHERE workspace_id=? AND id=?`, workspace, deviceID).Scan(&state); err == sql.ErrNoRows {
				return nil, platformerrors.New(platformerrors.CodeNotFound, "explicit run target device not found")
			} else if err != nil {
				return nil, err
			}
			if state == string(devices.Retired) {
				return nil, platformerrors.New(platformerrors.CodeUnavailable, "retired device cannot be selected for a run")
			}
			result = append(result, deviceID)
		}
		return result, nil
	case runs.SelectorGroup:
		query = `SELECT d.id FROM device_group_memberships m JOIN device_groups g ON g.workspace_id=m.workspace_id AND g.id=m.group_id JOIN devices d ON d.workspace_id=m.workspace_id AND d.id=m.device_id WHERE m.workspace_id=? AND m.group_id=? AND m.state='active' AND g.state='active' AND d.state<>'retired' ORDER BY m.position,d.id`
		args = []any{workspace, selector.GroupID}
	case runs.SelectorAutomationAgent:
		query = `SELECT d.id FROM automation_agent_device_assignments a JOIN automation_agents ag ON ag.workspace_id=a.workspace_id AND ag.id=a.automation_agent_id JOIN devices d ON d.workspace_id=a.workspace_id AND d.id=a.device_id WHERE a.workspace_id=? AND a.automation_agent_id=? AND a.state='active' AND ag.state='active' AND d.state<>'retired' ORDER BY d.id`
		args = []any{workspace, selector.AutomationAgentID}
	case runs.SelectorCapability:
		query = `SELECT c.device_id FROM device_capabilities c JOIN devices d ON d.workspace_id=c.workspace_id AND d.id=c.device_id WHERE c.workspace_id=? AND c.capability=? AND c.state='available' AND d.state<>'retired' ORDER BY c.device_id`
		args = []any{workspace, selector.Capability}
	default:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "target selector type is invalid")
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]devices.DeviceID, 0)
	seen := make(map[devices.DeviceID]struct{})
	for rows.Next() {
		var id devices.DeviceID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

type RunEvent struct {
	ID          string
	Workspace   organizations.WorkspaceID
	RunID       runs.RunID
	RunTargetID runs.RunTargetID
	EventName   string
	Schema      int
	Correlation string
	Causation   string
	ActorID     string
	Source      string
	PayloadJSON string
	OccurredAt  time.Time
}

func (s *RunService) Get(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID) (runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return runs.ParentRun{}, err
	}
	return loadParentRun(ctx, s.store.db, workspace, id)
}

func (s *RunService) List(ctx context.Context, workspace organizations.WorkspaceID) ([]runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id FROM runs WHERE workspace_id=? ORDER BY created_at,id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	ids := make([]runs.RunID, 0)
	for rows.Next() {
		var id runs.RunID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	result := make([]runs.ParentRun, 0, len(ids))
	for _, id := range ids {
		run, err := loadParentRun(ctx, s.store.db, workspace, id)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, nil
}

func (s *RunService) GetTargetSnapshot(ctx context.Context, workspace organizations.WorkspaceID, id runs.TargetSetSnapshotID) (runs.TargetSetSnapshot, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return runs.TargetSetSnapshot{}, err
	}
	var snapshot runs.TargetSetSnapshot
	var selectorType, selectorJSON, resolved string
	err := s.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, run_id, selector_type, selector_json, resolved_at FROM target_set_snapshots WHERE workspace_id=? AND id=?`, workspace, id).Scan(&snapshot.ID, &snapshot.Workspace, &snapshot.RunID, &selectorType, &selectorJSON, &resolved)
	if err == sql.ErrNoRows {
		return snapshot, platformerrors.New(platformerrors.CodeNotFound, "target snapshot not found")
	}
	if err != nil {
		return snapshot, classifyContext(err)
	}
	var payload struct {
		Selector  runs.TargetSelector `json:"selector"`
		DeviceIDs []devices.DeviceID  `json:"resolved_device_ids"`
	}
	if err := json.Unmarshal([]byte(selectorJSON), &payload); err != nil {
		return snapshot, platformerrors.Wrap(platformerrors.CodeInternal, "decode target snapshot", err)
	}
	payload.Selector.Type = runs.SelectorType(selectorType)
	snapshot.Selector, snapshot.DeviceIDs = payload.Selector, payload.DeviceIDs
	snapshot.ResolvedAt, _ = time.Parse(time.RFC3339Nano, resolved)
	if err := snapshot.Validate(); err != nil {
		return snapshot, platformerrors.Wrap(platformerrors.CodeInternal, "stored target snapshot is invalid", err)
	}
	return snapshot, nil
}

func (s *RunService) ListTargets(ctx context.Context, workspace organizations.WorkspaceID, runID runs.RunID) ([]runs.RunTarget, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(runID)) == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "run ID is required")
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, run_id, target_snapshot_id, device_id, state, failure_class, lease_id, current_observation_id, created_at, finished_at FROM run_targets WHERE workspace_id=? AND run_id=? ORDER BY device_id,id`, workspace, runID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]runs.RunTarget, 0)
	for rows.Next() {
		var target runs.RunTarget
		var state, created string
		var failureValue, leaseValue, observationValue, finishedValue sql.NullString
		if err := rows.Scan(&target.ID, &target.Workspace, &target.RunID, &target.SnapshotID, &target.DeviceID, &state, &failureValue, &leaseValue, &observationValue, &created, &finishedValue); err != nil {
			return nil, err
		}
		target.State = runs.TargetState(state)
		if failureValue.Valid {
			target.Failure = domain.FailureClass(failureValue.String)
		}
		if leaseValue.Valid {
			target.LeaseID = leaseValue.String
		}
		if observationValue.Valid {
			target.CurrentObservation = observationValue.String
		}
		target.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if finishedValue.Valid {
			finishedAt, parseErr := time.Parse(time.RFC3339Nano, finishedValue.String)
			if parseErr == nil {
				target.FinishedAt = &finishedAt
			}
		}
		result = append(result, target)
	}
	return result, classifyContext(rows.Err())
}

func (s *RunService) ListAllTargets(ctx context.Context, workspace organizations.WorkspaceID) ([]runs.RunTarget, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, run_id, target_snapshot_id, device_id, state, failure_class, lease_id, current_observation_id, created_at, finished_at FROM run_targets WHERE workspace_id=? ORDER BY run_id, device_id, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]runs.RunTarget, 0)
	for rows.Next() {
		var target runs.RunTarget
		var state, created string
		var failureValue, leaseValue, observationValue, finishedValue sql.NullString
		if err := rows.Scan(&target.ID, &target.Workspace, &target.RunID, &target.SnapshotID, &target.DeviceID, &state, &failureValue, &leaseValue, &observationValue, &created, &finishedValue); err != nil {
			return nil, err
		}
		target.State = runs.TargetState(state)
		if failureValue.Valid {
			target.Failure = domain.FailureClass(failureValue.String)
		}
		if leaseValue.Valid {
			target.LeaseID = leaseValue.String
		}
		if observationValue.Valid {
			target.CurrentObservation = observationValue.String
		}
		target.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if finishedValue.Valid {
			finishedAt, parseErr := time.Parse(time.RFC3339Nano, finishedValue.String)
			if parseErr == nil {
				target.FinishedAt = &finishedAt
			}
		}
		result = append(result, target)
	}
	return result, classifyContext(rows.Err())
}

func (s *RunService) ListTargetSteps(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID) ([]runs.TargetRunStep, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT t.id, t.workspace_id, t.run_target_id, t.workflow_step_id, t.state, t.attempt_count, t.created_at FROM target_run_steps t JOIN workflow_steps w ON w.workspace_id=t.workspace_id AND w.id=t.workflow_step_id WHERE t.workspace_id=? AND t.run_target_id=? ORDER BY w.sequence`, workspace, targetID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]runs.TargetRunStep, 0)
	for rows.Next() {
		var step runs.TargetRunStep
		var state, created string
		if err := rows.Scan(&step.ID, &step.Workspace, &step.RunTargetID, &step.WorkflowStepID, &state, &step.AttemptCount, &created); err != nil {
			return nil, err
		}
		step.State = runs.TargetRunStepState(state)
		step.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, step)
	}
	return result, classifyContext(rows.Err())
}

func loadParentRun(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id runs.RunID) (runs.ParentRun, error) {
	var run runs.ParentRun
	var state, approval, created string
	var pausedAt, startedAt, finishedAt, failure sql.NullString
	var concurrency, retry, recovery int
	err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, workflow_version_id, state, approval_state, concurrency_limit, retry_budget, recovery_count, paused_at, pause_reason, created_at, started_at, finished_at, failure_class FROM runs WHERE workspace_id=? AND id=?`, workspace, id).Scan(&run.ID, &run.Workspace, &run.WorkflowVersion, &state, &approval, &concurrency, &retry, &recovery, &pausedAt, &run.PauseReason, &created, &startedAt, &finishedAt, &failure)
	if err == sql.ErrNoRows {
		return run, platformerrors.New(platformerrors.CodeNotFound, "run not found")
	}
	if err != nil {
		return run, classifyContext(err)
	}
	run.State, run.Approval, run.ConcurrencyLimit, run.RetryBudget = runs.RunState(state), runs.ApprovalState(approval), concurrency, retry
	if pausedAt.Valid {
		run.State = runs.RunPaused
	}
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if startedAt.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, startedAt.String)
		if parseErr == nil {
			run.StartedAt = &value
		}
	}
	if finishedAt.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, finishedAt.String)
		if parseErr == nil {
			run.FinishedAt = &value
		}
	}
	if failure.Valid {
		run.Failure = domain.FailureClass(failure.String)
	}
	_ = recovery
	return run, nil
}

func (s *RunService) Approve(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID, actorType, actorID string) (runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.ParentRun{}, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return runs.ParentRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "run ID is required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var approval string
		if err := tx.QueryRowContext(ctx, `SELECT approval_state FROM runs WHERE workspace_id=? AND id=?`, workspace, id).Scan(&approval); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "run not found")
		} else if err != nil {
			return err
		}
		if runs.ApprovalState(approval) != runs.ApprovalPending {
			return platformerrors.New(platformerrors.CodeConflict, "run is not awaiting approval")
		}
		result, err := tx.ExecContext(ctx, `UPDATE runs SET approval_state='approved' WHERE workspace_id=? AND id=? AND approval_state='pending'`, workspace, id)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "run approval"); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(id), "", "run.approved", `{}`, actorID, "operator"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "run", string(id), "run.approved", actorType, actorID)
	})
	if err != nil {
		return runs.ParentRun{}, err
	}
	return loadParentRun(ctx, s.store.db, workspace, id)
}

func (s *RunService) Transition(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID, next runs.RunState, actorType, actorID string) (runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.ParentRun{}, err
	}
	if !next.Valid() {
		return runs.ParentRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "run state is invalid")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadParentRun(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		if next == runs.RunValidating && current.Approval != runs.ApprovalApproved {
			return platformerrors.New(platformerrors.CodePolicyDenied, "run requires explicit approval before validation")
		}
		if err := runs.TransitionRun(current.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "run transition is invalid", err)
		}
		rawFrom := string(current.State)
		if current.State == runs.RunPaused {
			rawFrom = string(runs.RunRunning)
		}
		rawNext := string(next)
		pausedAt := any(nil)
		pauseReason := ""
		if next == runs.RunPaused {
			rawNext = string(runs.RunRunning)
			pausedAt = now.Format(time.RFC3339Nano)
			pauseReason = "operator requested pause"
		}
		if next == runs.RunQueued && current.State == runs.RunPaused {
			pausedAt = nil
		}
		var started any
		if next == runs.RunRunning {
			started = now.Format(time.RFC3339Nano)
		}
		var finished any
		if next == runs.RunCompleted || next == runs.RunFailed || next == runs.RunCancelled {
			finished = now.Format(time.RFC3339Nano)
		}
		result, err := tx.ExecContext(ctx, `UPDATE runs SET state=?, paused_at=?, pause_reason=?, started_at=COALESCE(started_at,?), finished_at=COALESCE(finished_at,?) WHERE workspace_id=? AND id=? AND state=?`, rawNext, pausedAt, pauseReason, started, finished, workspace, id, rawFrom)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "run"); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(id), "", "run."+string(next), `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "run", string(id), "run."+string(next), actorType, actorID)
	})
	if err != nil {
		return runs.ParentRun{}, err
	}
	return loadParentRun(ctx, s.store.db, workspace, id)
}

func (s *RunService) Pause(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID, reason, actorType, actorID string) (runs.ParentRun, error) {
	if len(reason) > 1024 || (reason != "" && redaction.RedactString(reason) != reason) {
		return runs.ParentRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "pause reason is invalid or sensitive")
	}
	run, err := s.Transition(ctx, workspace, id, runs.RunPaused, actorType, actorID)
	if err != nil || reason == "" {
		return run, err
	}
	if err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET pause_reason=? WHERE workspace_id=? AND id=? AND paused_at IS NOT NULL`, reason, workspace, id); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return runs.ParentRun{}, err
	}
	run.PauseReason = reason
	return run, nil
}

func (s *RunService) Cancel(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID, actorType, actorID string) (runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.ParentRun{}, err
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadParentRun(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		if err := runs.TransitionRun(current.State, runs.RunCancelled); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "run cancellation is invalid", err)
		}
		rawFrom := string(current.State)
		if current.State == runs.RunPaused {
			rawFrom = string(runs.RunRunning)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state='cancelled', finished_at=?, paused_at=NULL, pause_reason='' WHERE workspace_id=? AND id=? AND state=?`, now, workspace, id, rawFrom); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE run_targets SET state='cancelled', failure_class=?, finished_at=? WHERE workspace_id=? AND run_id=? AND state IN ('pending','leased','queued')`, domain.FailureOperatorCancelled, now, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state='cancelled', outcome_state='cancelled', failure_class=?, finished_at=? WHERE workspace_id=? AND run_target_id IN (SELECT id FROM run_targets WHERE workspace_id=? AND run_id=?) AND state='authorized'`, domain.FailureOperatorCancelled, now, workspace, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state='dispatched', outcome_state='indeterminate', postcondition_state='unknown', failure_class=?, finished_at=? WHERE workspace_id=? AND run_target_id IN (SELECT id FROM run_targets WHERE workspace_id=? AND run_id=?) AND state IN ('dispatched','acknowledged')`, domain.FailureIndeterminate, now, workspace, workspace, id); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(id), "", "run.cancelled", `{}`, actorID, "operator"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "run", string(id), "run.cancelled", actorType, actorID)
	})
	if err != nil {
		return runs.ParentRun{}, err
	}
	return loadParentRun(ctx, s.store.db, workspace, id)
}

// Recover fences the in-flight portion of a run after a process restart. It
// pauses the parent and marks dispatched attempts indeterminate; no target is
// resumed automatically.
func (s *RunService) Recover(ctx context.Context, workspace organizations.WorkspaceID, id runs.RunID, actorType, actorID string) (runs.ParentRun, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.ParentRun{}, err
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadParentRun(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		if current.State != runs.RunRunning && current.State != runs.RunCompleting && current.State != runs.RunPaused {
			return platformerrors.New(platformerrors.CodeConflict, "only an active run can be recovered")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state='running', paused_at=?, pause_reason='recovered after restart', recovery_count=recovery_count+1 WHERE workspace_id=? AND id=? AND state IN ('running','completing')`, now, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state='dispatched', outcome_state='indeterminate', postcondition_state='unknown', failure_class=?, finished_at=? WHERE workspace_id=? AND run_target_id IN (SELECT id FROM run_targets WHERE workspace_id=? AND run_id=?) AND state IN ('dispatched','acknowledged')`, domain.FailureIndeterminate, now, workspace, workspace, id); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(id), "", "run.recovered", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "run", string(id), "run.recovered", actorType, actorID)
	})
	if err != nil {
		return runs.ParentRun{}, err
	}
	return loadParentRun(ctx, s.store.db, workspace, id)
}

func (s *RunService) LeaseTarget(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID, leaseID, actorType, actorID string) (runs.RunTarget, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.RunTarget{}, err
	}
	if strings.TrimSpace(leaseID) == "" || strings.TrimSpace(string(targetID)) == "" {
		return runs.RunTarget{}, platformerrors.New(platformerrors.CodeInvalidInput, "run target and lease IDs are required")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var deviceID, runID, leaseDevice, leaseState string
		if err := tx.QueryRowContext(ctx, `SELECT device_id, run_id FROM run_targets WHERE workspace_id=? AND id=?`, workspace, targetID).Scan(&deviceID, &runID); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "run target not found")
		} else if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT device_id, state FROM device_leases WHERE workspace_id=? AND id=?`, workspace, leaseID).Scan(&leaseDevice, &leaseState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "device lease not found")
		} else if err != nil {
			return err
		}
		if leaseDevice != deviceID || leaseState != "active" {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "lease does not own the run target")
		}
		result, err := tx.ExecContext(ctx, `UPDATE run_targets SET state='leased', lease_id=? WHERE workspace_id=? AND id=? AND state='pending'`, leaseID, workspace, targetID)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "run target"); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), runID, string(targetID), "run_target.leased", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "run_target", string(targetID), "run_target.leased", actorType, actorID)
	})
	if err != nil {
		return runs.RunTarget{}, err
	}
	return loadRunTarget(ctx, s.store.db, workspace, targetID)
}

func (s *RunService) TransitionTarget(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID, next runs.TargetState, failure domain.FailureClass, actorType, actorID string) (runs.RunTarget, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return runs.RunTarget{}, err
	}
	if !next.Valid() || strings.TrimSpace(string(targetID)) == "" {
		return runs.RunTarget{}, platformerrors.New(platformerrors.CodeInvalidInput, "run target transition fields are invalid")
	}
	if (next == runs.TargetFailed || next == runs.TargetCleanupFailed) && !failure.Valid() {
		return runs.RunTarget{}, platformerrors.New(platformerrors.CodeInvalidInput, "failed target requires a known failure class")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadRunTarget(ctx, tx, workspace, targetID)
		if err != nil {
			return err
		}
		if err := runs.TransitionTarget(current.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "run target transition is invalid", err)
		}
		if next == runs.TargetVerifying || next == runs.TargetSucceeded {
			if err := ensureTargetStepsSucceededTx(ctx, tx, workspace, targetID); err != nil {
				return err
			}
		}
		var finished any
		if next == runs.TargetSucceeded || next == runs.TargetFailed || next == runs.TargetCancelled || next == runs.TargetCleanupFailed {
			finished = now.Format(time.RFC3339Nano)
		}
		result, err := tx.ExecContext(ctx, `UPDATE run_targets SET state=?, failure_class=?, finished_at=? WHERE workspace_id=? AND id=? AND state=?`, next, nullableString(string(failure)), finished, workspace, targetID, current.State)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "run target"); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(current.RunID), string(targetID), "run_target."+string(next), `{}`, actorID, "service"); err != nil {
			return err
		}
		if err := s.store.recordMutation(ctx, tx, string(workspace), "run_target", string(targetID), "run_target."+string(next), actorType, actorID); err != nil {
			return err
		}
		return finalizeParentRunTx(ctx, tx, s.store, workspace, current.RunID, failure, actorID)
	})
	if err != nil {
		return runs.RunTarget{}, err
	}
	return loadRunTarget(ctx, s.store.db, workspace, targetID)
}

func ensureTargetStepsSucceededTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, targetID runs.RunTargetID) error {
	var total, incomplete int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), SUM(CASE WHEN state<>'succeeded' THEN 1 ELSE 0 END) FROM target_run_steps WHERE workspace_id=? AND run_target_id=?`, workspace, targetID).Scan(&total, &incomplete); err != nil {
		return err
	}
	if total == 0 || incomplete > 0 {
		return platformerrors.New(platformerrors.CodeConflict, "run target cannot succeed before every workflow step succeeds")
	}
	return nil
}

func loadRunTarget(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, targetID runs.RunTargetID) (runs.RunTarget, error) {
	var target runs.RunTarget
	var state, created string
	var failure, leaseID, observationID, finished sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT id, workspace_id, run_id, target_snapshot_id, device_id, state, failure_class, lease_id, current_observation_id, created_at, finished_at FROM run_targets WHERE workspace_id=? AND id=?`, workspace, targetID).Scan(&target.ID, &target.Workspace, &target.RunID, &target.SnapshotID, &target.DeviceID, &state, &failure, &leaseID, &observationID, &created, &finished)
	if err == sql.ErrNoRows {
		return target, platformerrors.New(platformerrors.CodeNotFound, "run target not found")
	}
	if err != nil {
		return target, classifyContext(err)
	}
	target.State = runs.TargetState(state)
	if failure.Valid {
		target.Failure = domain.FailureClass(failure.String)
	}
	if leaseID.Valid {
		target.LeaseID = leaseID.String
	}
	if observationID.Valid {
		target.CurrentObservation = observationID.String
	}
	target.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if finished.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, finished.String)
		if parseErr == nil {
			target.FinishedAt = &value
		}
	}
	return target, nil
}

func finalizeParentRunTx(ctx context.Context, tx *sql.Tx, store *DB, workspace organizations.WorkspaceID, runID runs.RunID, targetFailure domain.FailureClass, actorID string) error {
	var total, failures, succeeded, cancelled int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), SUM(CASE WHEN state IN ('failed','cleanup_failed') THEN 1 ELSE 0 END), SUM(CASE WHEN state='succeeded' THEN 1 ELSE 0 END), SUM(CASE WHEN state='cancelled' THEN 1 ELSE 0 END) FROM run_targets WHERE workspace_id=? AND run_id=?`, workspace, runID).Scan(&total, &failures, &succeeded, &cancelled); err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM runs WHERE workspace_id=? AND id=?`, workspace, runID).Scan(&state); err != nil {
		return err
	}
	now := store.clock.Now().UTC().Format(time.RFC3339Nano)
	if failures > 0 && state != string(runs.RunCompleted) && state != string(runs.RunFailed) && state != string(runs.RunCancelled) {
		failure := targetFailure
		if !failure.Valid() {
			failure = domain.FailureInfrastructure
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state='failed', failure_class=?, finished_at=?, paused_at=NULL, pause_reason='' WHERE workspace_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`, failure, now, workspace, runID); err != nil {
			return err
		}
		return appendRunEventTx(ctx, tx, store, string(workspace), string(runID), "", "run.failed", `{}`, actorID, "service")
	}
	if succeeded == total && failures == 0 && cancelled == 0 && (state == string(runs.RunRunning) || state == string(runs.RunCompleting)) {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state='completing' WHERE workspace_id=? AND id=? AND state='running'`, workspace, runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state='completed', finished_at=?, paused_at=NULL, pause_reason='' WHERE workspace_id=? AND id=? AND state='completing'`, now, workspace, runID); err != nil {
			return err
		}
		return appendRunEventTx(ctx, tx, store, string(workspace), string(runID), "", "run.completed", `{}`, actorID, "service")
	}
	return nil
}

type AuthorizeAttemptRequest struct {
	Workspace         organizations.WorkspaceID
	RunTargetID       runs.RunTargetID
	TargetRunStepID   runs.TargetRunStepID
	Action            action.Kind
	InvocationSurface action.InvocationSurface
	IdempotencyKey    string
	RequestHash       string
	ObservationToken  string
	EvidenceJSON      string
}

type CompleteAttemptRequest struct {
	Workspace        organizations.WorkspaceID
	AttemptID        runs.ActionAttemptID
	Outcome          action.Outcome
	Postcondition    action.PostconditionState
	ObservationToken string
	EvidenceJSON     string
	Failure          domain.FailureClass
}

type ReconcileAttemptRequest struct {
	Workspace         organizations.WorkspaceID
	AttemptID         runs.ActionAttemptID
	ObservationToken  string
	OperatorConfirmed bool
	Succeeded         bool
	Postcondition     action.PostconditionState
	EvidenceJSON      string
	Failure           domain.FailureClass
}

func (s *RunService) AuthorizeAttempt(ctx context.Context, request AuthorizeAttemptRequest, actorType, actorID string) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	if err := validateRunServiceInput(ctx, s, request.Workspace, actorType, actorID); err != nil {
		return attempt, err
	}
	if strings.TrimSpace(string(request.RunTargetID)) == "" || strings.TrimSpace(string(request.TargetRunStepID)) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action attempt identity is required")
	}
	if len(request.IdempotencyKey) > 256 || len(request.ObservationToken) > 256 {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action attempt metadata is unbounded")
	}
	spec, ok := action.Lookup(request.Action)
	if !ok {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action kind is not allow-listed")
	}
	if spec.RequiresObservation && strings.TrimSpace(request.ObservationToken) == "" {
		return attempt, platformerrors.New(platformerrors.CodeStaleObservation, "mutating attempt requires a fresh observation")
	}
	if request.InvocationSurface == "" {
		request.InvocationSurface = action.SurfaceReplay
	}
	expectedRequestHash := runs.AttemptRequestHash(string(request.Workspace), string(request.RunTargetID), string(request.TargetRunStepID), runs.ActionKind(request.Action), request.InvocationSurface, request.IdempotencyKey, request.ObservationToken)
	if request.RequestHash == "" {
		request.RequestHash = expectedRequestHash
	} else if request.RequestHash != expectedRequestHash {
		return attempt, platformerrors.New(platformerrors.CodeConflict, "action request hash does not match typed run attempt")
	}
	if len(request.RequestHash) > 128 {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action request hash is unbounded")
	}
	if !action.ContainsSurface(spec.AllowedSurfaces, request.InvocationSurface) {
		return attempt, platformerrors.New(platformerrors.CodePolicyDenied, "workflow action surface is not permitted")
	}
	evidence := request.EvidenceJSON
	if evidence == "" {
		evidence = `{}`
	}
	if err := validateRunEvidenceJSON(evidence); err != nil {
		return attempt, err
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if existing, found, err := findRunAttemptByKeyTx(ctx, tx, request.Workspace, request.IdempotencyKey); err != nil {
			return err
		} else if found {
			if existing.RequestHash != request.RequestHash {
				return platformerrors.New(platformerrors.CodeConflict, "action idempotency key was reused with a different request")
			}
			attempt = existing
			return nil
		}
		var targetState, targetRunID string
		var deviceID devices.DeviceID
		var leaseID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT run_id, device_id, state, lease_id FROM run_targets WHERE workspace_id=? AND id=?`, request.Workspace, request.RunTargetID).Scan(&targetRunID, &deviceID, &targetState, &leaseID); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "run target not found")
		} else if err != nil {
			return err
		}
		if targetState != string(runs.TargetRunning) {
			return platformerrors.New(platformerrors.CodeConflict, "action attempts require a running target")
		}
		parent, err := loadParentRun(ctx, tx, request.Workspace, runs.RunID(targetRunID))
		if err != nil {
			return err
		}
		if parent.State != runs.RunRunning || parent.Approval != runs.ApprovalApproved {
			return platformerrors.New(platformerrors.CodeConflict, "action attempts require an approved running parent run")
		}
		if !leaseID.Valid || strings.TrimSpace(leaseID.String) == "" {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "action attempts require a leased target")
		}
		var fencingToken int64
		var leaseState, leaseHolder string
		if err := tx.QueryRowContext(ctx, `SELECT fencing_token, state, holder_id FROM device_leases WHERE workspace_id=? AND id=? AND device_id=?`, request.Workspace, leaseID.String, deviceID).Scan(&fencingToken, &leaseState, &leaseHolder); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "run target lease not found")
		} else if err != nil {
			return err
		}
		if fencingToken <= 0 || leaseState != "active" {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "run target lease is not active")
		}
		if err := validateLeaseTupleTx(ctx, tx, string(request.Workspace), string(deviceID), leaseID.String, leaseHolder, uint64(fencingToken), now); err != nil {
			return err
		}
		var workflowAction, definitionJSON string
		if err := tx.QueryRowContext(ctx, `SELECT w.action_kind, w.definition_json FROM target_run_steps t JOIN workflow_steps w ON w.workspace_id=t.workspace_id AND w.id=t.workflow_step_id WHERE t.workspace_id=? AND t.id=? AND t.run_target_id=?`, request.Workspace, request.TargetRunStepID, request.RunTargetID).Scan(&workflowAction, &definitionJSON); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "target workflow step not found")
		} else if err != nil {
			return err
		}
		if workflowAction != string(request.Action) {
			return platformerrors.New(platformerrors.CodeConflict, "attempt action does not match the versioned workflow step")
		}
		var definition workflows.StepDefinition
		if err := json.Unmarshal([]byte(definitionJSON), &definition); err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "decode stored workflow step", err)
		}
		if err := definition.Validate(spec); err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "stored workflow step is invalid", err)
		}
		if spec.RequiresObservation {
			if err := requireFreshObservationTx(ctx, tx, request.Workspace, deviceID, request.ObservationToken); err != nil {
				return err
			}
		}
		targetJSON, err := json.Marshal(definition.Target)
		if err != nil || len(targetJSON) > 4096 || redaction.RedactString(string(targetJSON)) != string(targetJSON) {
			return platformerrors.New(platformerrors.CodeInternal, "stored workflow target is invalid or sensitive")
		}
		var sequence int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), -1)+1 FROM action_attempts WHERE workspace_id=? AND run_target_id=?`, request.Workspace, request.RunTargetID).Scan(&sequence); err != nil {
			return err
		}
		id, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate action attempt ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO action_attempts (id, workspace_id, run_target_id, target_run_step_id, action_kind, state, idempotency_key, fencing_token, postcondition_state, failure_class, created_at, sequence, invocation_surface, request_hash, observation_token, evidence_json, outcome_state, target_json, timeout_ms, expected_postcondition) VALUES (?, ?, ?, ?, ?, 'authorized', ?, ?, 'pending', NULL, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`, id, request.Workspace, request.RunTargetID, request.TargetRunStepID, request.Action, request.IdempotencyKey, fencingToken, now.Format(time.RFC3339Nano), sequence, request.InvocationSurface, request.RequestHash, nullableString(request.ObservationToken), evidence, string(targetJSON), definition.TimeoutMillis, definition.Postcondition); err != nil {
			return mapConstraint(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE target_run_steps SET state='running', attempt_count=attempt_count+1 WHERE workspace_id=? AND id=? AND run_target_id=? AND state='pending'`, request.Workspace, request.TargetRunStepID, request.RunTargetID); err != nil {
			return err
		}
		attempt = runs.ActionAttempt{ID: runs.ActionAttemptID(id), Workspace: request.Workspace, RunTargetID: request.RunTargetID, TargetRunStepID: request.TargetRunStepID, Sequence: sequence, Action: runs.ActionKind(request.Action), InvocationSurface: request.InvocationSurface, State: runs.ActionAuthorized, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, FencingToken: uint64(fencingToken), ObservationToken: request.ObservationToken, Target: definition.Target, TimeoutMillis: definition.TimeoutMillis, ExpectedPostcondition: definition.Postcondition, Postcondition: action.PostconditionPending, EvidenceJSON: evidence, CreatedAt: now}
		if err := appendRunEventTx(ctx, tx, s.store, string(request.Workspace), targetRunID, string(request.RunTargetID), "action.authorized", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(request.Workspace), "action_attempt", id, "action.authorized", actorType, actorID)
	})
	if err != nil {
		return runs.ActionAttempt{}, err
	}
	return attempt, nil
}

func (s *RunService) DispatchAttempt(ctx context.Context, workspace organizations.WorkspaceID, attemptID runs.ActionAttemptID, holderID string, fencingToken uint64, actorType, actorID string) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return attempt, err
	}
	if strings.TrimSpace(string(attemptID)) == "" || strings.TrimSpace(holderID) == "" || fencingToken == 0 {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "attempt holder and fencing fields are required")
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var err error
		attempt, err = loadRunActionAttempt(ctx, tx, workspace, attemptID)
		if err != nil {
			return err
		}
		if attempt.State != runs.ActionAuthorized {
			return platformerrors.New(platformerrors.CodeConflict, "only an authorized attempt can be dispatched")
		}
		target, err := loadRunTarget(ctx, tx, workspace, attempt.RunTargetID)
		if err != nil {
			return err
		}
		if target.LeaseID == "" {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "run target has no device lease")
		}
		if attempt.FencingToken != fencingToken {
			return platformerrors.New(platformerrors.CodeLeaseConflict, "action attempt fencing token is stale")
		}
		if err := validateLeaseTupleTx(ctx, tx, string(workspace), string(target.DeviceID), target.LeaseID, holderID, fencingToken, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state='dispatched' WHERE workspace_id=? AND id=? AND state='authorized'`, workspace, attemptID)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "action attempt"); err != nil {
			return err
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(target.RunID), string(target.ID), "action.dispatched", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "action_attempt", string(attemptID), "action.dispatched", actorType, actorID)
	})
	if err != nil {
		return runs.ActionAttempt{}, err
	}
	attempt.State = runs.ActionDispatched
	return attempt, nil
}

func (s *RunService) MarkAttemptIndeterminate(ctx context.Context, workspace organizations.WorkspaceID, attemptID runs.ActionAttemptID, failure domain.FailureClass, actorType, actorID string) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return attempt, err
	}
	if !failure.Valid() {
		failure = domain.FailureIndeterminate
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var err error
		attempt, err = loadRunActionAttempt(ctx, tx, workspace, attemptID)
		if err != nil {
			return err
		}
		if attempt.State == runs.ActionIndeterminate {
			return nil
		}
		if attempt.State != runs.ActionDispatched && attempt.State != runs.ActionAcknowledged {
			return platformerrors.New(platformerrors.CodeConflict, "only a dispatched attempt can become indeterminate")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state='dispatched', outcome_state='indeterminate', postcondition_state='unknown', failure_class=?, finished_at=? WHERE workspace_id=? AND id=? AND state IN ('dispatched','acknowledged')`, failure, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, attemptID); err != nil {
			return err
		}
		target, err := loadRunTarget(ctx, tx, workspace, attempt.RunTargetID)
		if err != nil {
			return err
		}
		attempt.State = runs.ActionIndeterminate
		attempt.Postcondition = action.PostconditionUnknown
		attempt.Failure = failure
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), string(target.RunID), string(attempt.RunTargetID), "action.indeterminate", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "action_attempt", string(attemptID), "action.indeterminate", actorType, actorID)
	})
	if err != nil {
		return runs.ActionAttempt{}, err
	}
	return attempt, nil
}

func (s *RunService) CompleteAttempt(ctx context.Context, request CompleteAttemptRequest, actorType, actorID string) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	if err := validateRunServiceInput(ctx, s, request.Workspace, actorType, actorID); err != nil {
		return attempt, err
	}
	if strings.TrimSpace(string(request.AttemptID)) == "" {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action attempt ID is required")
	}
	if request.EvidenceJSON == "" {
		request.EvidenceJSON = `{}`
	}
	if err := validateRunEvidenceJSON(request.EvidenceJSON); err != nil {
		return attempt, err
	}
	if request.Postcondition == "" {
		request.Postcondition = action.PostconditionUnknown
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var err error
		attempt, err = loadRunActionAttempt(ctx, tx, request.Workspace, request.AttemptID)
		if err != nil {
			return err
		}
		if attempt.State != runs.ActionDispatched && attempt.State != runs.ActionAcknowledged {
			return platformerrors.New(platformerrors.CodeConflict, "only a dispatched attempt can be completed")
		}
		spec, ok := action.Lookup(action.Kind(attempt.Action))
		if !ok {
			return platformerrors.New(platformerrors.CodeInternal, "stored action kind is not allow-listed")
		}
		if request.Outcome == action.OutcomeVerified && request.Postcondition != action.PostconditionPassed {
			return platformerrors.New(platformerrors.CodeConflict, "a failed or unknown postcondition cannot become successful")
		}
		if request.Outcome == action.OutcomeVerified && spec.EvidenceRequired && !hasRunEvidence(request.EvidenceJSON) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "verified action requires evidence")
		}
		if request.Outcome == action.OutcomeVerified && spec.RequiresObservation {
			if strings.TrimSpace(request.ObservationToken) == "" || request.ObservationToken == attempt.ObservationToken {
				return platformerrors.New(platformerrors.CodeStaleObservation, "verified mutating action requires a fresh completion observation")
			}
			target, err := loadRunTarget(ctx, tx, request.Workspace, attempt.RunTargetID)
			if err != nil {
				return err
			}
			if err := requireFreshObservationTx(ctx, tx, request.Workspace, target.DeviceID, request.ObservationToken); err != nil {
				return err
			}
		}
		if request.Outcome != action.OutcomeVerified && request.Postcondition == action.PostconditionPassed {
			return platformerrors.New(platformerrors.CodeConflict, "a non-successful action cannot carry a passed postcondition")
		}
		rawState, outcome, failure := completionStates(request.Outcome, request.Postcondition, request.Failure)
		if rawState == "" {
			return platformerrors.New(platformerrors.CodeInvalidInput, "action completion outcome is invalid")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state=?, outcome_state=?, postcondition_state=?, observation_token=?, evidence_json=?, failure_class=?, finished_at=? WHERE workspace_id=? AND id=? AND state IN ('dispatched','acknowledged')`, rawState, outcome, request.Postcondition, nullableString(request.ObservationToken), request.EvidenceJSON, nullableString(string(failure)), now.Format(time.RFC3339Nano), request.Workspace, request.AttemptID); err != nil {
			return err
		}
		if attempt.TargetRunStepID != "" {
			stepState := string(runs.TargetStepFailed)
			if outcome == string(action.OutcomeVerified) {
				stepState = string(runs.TargetStepSucceeded)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE target_run_steps SET state=? WHERE workspace_id=? AND id=? AND state='running'`, stepState, request.Workspace, attempt.TargetRunStepID); err != nil {
				return err
			}
		}
		target, err := loadRunTarget(ctx, tx, request.Workspace, attempt.RunTargetID)
		if err != nil {
			return err
		}
		attempt.State = runs.ActionAttemptState(rawState)
		attempt.Postcondition = request.Postcondition
		attempt.ObservationToken = request.ObservationToken
		attempt.EvidenceJSON = request.EvidenceJSON
		attempt.Failure = failure
		attempt.FinishedAt = &now
		if err := appendRunEventTx(ctx, tx, s.store, string(request.Workspace), string(target.RunID), string(target.ID), "action."+outcome, `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(request.Workspace), "action_attempt", string(request.AttemptID), "action."+outcome, actorType, actorID)
	})
	if err != nil {
		return runs.ActionAttempt{}, err
	}
	return attempt, nil
}

func (s *RunService) ReconcileAttempt(ctx context.Context, request ReconcileAttemptRequest, actorType, actorID string) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	if err := validateRunServiceInput(ctx, s, request.Workspace, actorType, actorID); err != nil {
		return attempt, err
	}
	if strings.TrimSpace(string(request.AttemptID)) == "" {
		return attempt, platformerrors.New(platformerrors.CodeInvalidInput, "action attempt ID is required")
	}
	if request.EvidenceJSON == "" {
		request.EvidenceJSON = `{}`
	}
	if err := validateRunEvidenceJSON(request.EvidenceJSON); err != nil {
		return attempt, err
	}
	if request.Postcondition == "" {
		request.Postcondition = action.PostconditionUnknown
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var err error
		attempt, err = loadRunActionAttempt(ctx, tx, request.Workspace, request.AttemptID)
		if err != nil {
			return err
		}
		if attempt.State != runs.ActionIndeterminate {
			return platformerrors.New(platformerrors.CodeConflict, "only an indeterminate attempt can be reconciled")
		}
		if !request.OperatorConfirmed && strings.TrimSpace(request.ObservationToken) == "" {
			return platformerrors.New(platformerrors.CodeStaleObservation, "reconciliation requires a fresh observation or explicit operator confirmation")
		}
		target, err := loadRunTarget(ctx, tx, request.Workspace, attempt.RunTargetID)
		if err != nil {
			return err
		}
		if request.ObservationToken != "" && request.ObservationToken == attempt.ObservationToken {
			return platformerrors.New(platformerrors.CodeStaleObservation, "reconciliation observation must be newer than the dispatch observation")
		}
		if request.ObservationToken != "" {
			if err := requireFreshObservationTx(ctx, tx, request.Workspace, target.DeviceID, request.ObservationToken); err != nil {
				return err
			}
		}
		if request.Succeeded && request.Postcondition != action.PostconditionPassed {
			return platformerrors.New(platformerrors.CodeConflict, "reconciled success requires a passed postcondition")
		}
		spec, ok := action.Lookup(action.Kind(attempt.Action))
		if !ok {
			return platformerrors.New(platformerrors.CodeInternal, "stored action kind is not allow-listed")
		}
		if request.Succeeded && spec.EvidenceRequired && !hasRunEvidence(request.EvidenceJSON) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "reconciled success requires evidence")
		}
		failure := request.Failure
		if !request.Succeeded && !failure.Valid() {
			failure = domain.FailurePostcondition
		}
		rawState, outcome := string(runs.ActionFailed), string(action.OutcomeFailed)
		if request.Succeeded {
			rawState, outcome = string(runs.ActionVerified), string(action.OutcomeVerified)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_attempts SET state=?, outcome_state=?, postcondition_state=?, observation_token=?, evidence_json=?, failure_class=?, finished_at=? WHERE workspace_id=? AND id=? AND outcome_state='indeterminate'`, rawState, outcome, request.Postcondition, nullableString(request.ObservationToken), request.EvidenceJSON, nullableString(string(failure)), now.Format(time.RFC3339Nano), request.Workspace, request.AttemptID); err != nil {
			return err
		}
		if attempt.TargetRunStepID != "" {
			stepState := string(runs.TargetStepFailed)
			if request.Succeeded {
				stepState = string(runs.TargetStepSucceeded)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE target_run_steps SET state=? WHERE workspace_id=? AND id=?`, stepState, request.Workspace, attempt.TargetRunStepID); err != nil {
				return err
			}
		}
		attempt.State = runs.ActionAttemptState(rawState)
		attempt.Postcondition = request.Postcondition
		attempt.ObservationToken = request.ObservationToken
		attempt.EvidenceJSON = request.EvidenceJSON
		attempt.Failure = failure
		attempt.FinishedAt = &now
		if err := appendRunEventTx(ctx, tx, s.store, string(request.Workspace), string(target.RunID), string(target.ID), "action.reconciled", `{}`, actorID, "operator"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(request.Workspace), "action_attempt", string(request.AttemptID), "action.reconciled", actorType, actorID)
	})
	if err != nil {
		return runs.ActionAttempt{}, err
	}
	return attempt, nil
}

type ReplayEvidence struct {
	ID              string
	Workspace       organizations.WorkspaceID
	RunTargetID     runs.RunTargetID
	Action          action.Kind
	ActionAttemptID runs.ActionAttemptID
	ObservationID   observations.ObservationID
	Metadata        runs.ReplayMetadata
	CreatedAt       time.Time
}

type RecordReplayEvidenceRequest struct {
	Workspace       organizations.WorkspaceID
	RunTargetID     runs.RunTargetID
	Action          action.Kind
	ActionAttemptID runs.ActionAttemptID
	ObservationID   observations.ObservationID
	Metadata        runs.ReplayMetadata
}

func (s *RunService) RecordReplayEvidence(ctx context.Context, request RecordReplayEvidenceRequest, actorType, actorID string) (ReplayEvidence, error) {
	var evidence ReplayEvidence
	if err := validateRunServiceInput(ctx, s, request.Workspace, actorType, actorID); err != nil {
		return evidence, err
	}
	if strings.TrimSpace(string(request.RunTargetID)) == "" {
		return evidence, platformerrors.New(platformerrors.CodeInvalidInput, "run target ID is required")
	}
	if err := request.Metadata.ValidateFor(request.Action); err != nil {
		return evidence, platformerrors.Wrap(platformerrors.CodeInvalidInput, "replay evidence is invalid", err)
	}
	targetJSON, err := json.Marshal(request.Metadata.Target)
	if err != nil || len(targetJSON) > 4096 || redaction.RedactString(string(targetJSON)) != string(targetJSON) {
		return evidence, platformerrors.New(platformerrors.CodeInvalidInput, "replay target evidence is invalid or sensitive")
	}
	now := s.store.clock.Now().UTC()
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		target, err := loadRunTarget(ctx, tx, request.Workspace, request.RunTargetID)
		if err != nil {
			return err
		}
		if request.ActionAttemptID != "" {
			var attemptTarget, attemptKind string
			if err := tx.QueryRowContext(ctx, `SELECT run_target_id, action_kind FROM action_attempts WHERE workspace_id=? AND id=?`, request.Workspace, request.ActionAttemptID).Scan(&attemptTarget, &attemptKind); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "action attempt not found")
			} else if err != nil {
				return err
			}
			if attemptTarget != string(request.RunTargetID) {
				return platformerrors.New(platformerrors.CodeConflict, "replay evidence attempt is outside the target")
			}
			if attemptKind != string(request.Action) {
				return platformerrors.New(platformerrors.CodeConflict, "replay evidence action does not match the attempt")
			}
		}
		spec, ok := action.Lookup(request.Action)
		if !ok {
			return platformerrors.New(platformerrors.CodeInvalidInput, "replay action is not allow-listed")
		}
		if spec.RequiresObservation && request.ObservationID == "" {
			return platformerrors.New(platformerrors.CodeStaleObservation, "replay evidence requires a fresh observation")
		}
		if request.ObservationID != "" {
			var observationDevice, captureStatus string
			var packageName, activityName, appVersion sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT device_id, package_name, activity_name, app_version, capture_status FROM observation_snapshots WHERE workspace_id=? AND id=? AND state='recorded'`, request.Workspace, request.ObservationID).Scan(&observationDevice, &packageName, &activityName, &appVersion, &captureStatus); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "replay observation not found")
			} else if err != nil {
				return err
			}
			if observationDevice != string(target.DeviceID) || captureStatus != string(observations.CaptureComplete) {
				return platformerrors.New(platformerrors.CodeStaleObservation, "replay evidence observation is not current for the target")
			}
			if !packageName.Valid || !activityName.Valid || !appVersion.Valid {
				return platformerrors.New(platformerrors.CodeStaleObservation, "replay observation lacks application identity")
			}
			if err := request.Metadata.CompatibleWith(packageName.String, activityName.String, appVersion.String); err != nil {
				return platformerrors.Wrap(platformerrors.CodeStaleObservation, "replay application compatibility failed", err)
			}
		}
		id, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate replay evidence ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO replay_evidence (id, workspace_id, run_target_id, action_kind, action_attempt_id, observation_id, package_name, activity_name, app_version, coordinate_space, target_json, permission_dialog_mode, irreversible_confirmed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, request.Workspace, request.RunTargetID, request.Action, nullableString(string(request.ActionAttemptID)), nullableString(string(request.ObservationID)), request.Metadata.PackageName, request.Metadata.ActivityName, request.Metadata.AppVersion, request.Metadata.CoordinateSpace, string(targetJSON), request.Metadata.PermissionDialogMode, boolInt(request.Metadata.IrreversibleConfirmed), now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		evidence = ReplayEvidence{ID: id, Workspace: request.Workspace, RunTargetID: request.RunTargetID, Action: request.Action, ActionAttemptID: request.ActionAttemptID, ObservationID: request.ObservationID, Metadata: request.Metadata, CreatedAt: now}
		if err := appendRunEventTx(ctx, tx, s.store, string(request.Workspace), string(target.RunID), string(target.ID), "replay.evidence_recorded", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(request.Workspace), "replay_evidence", id, "replay.evidence_recorded", actorType, actorID)
	})
	if err != nil {
		return ReplayEvidence{}, err
	}
	return evidence, nil
}

func (s *RunService) ListReplayEvidence(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID) ([]ReplayEvidence, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, run_target_id, action_kind, action_attempt_id, observation_id, package_name, activity_name, app_version, coordinate_space, target_json, permission_dialog_mode, irreversible_confirmed, created_at FROM replay_evidence WHERE workspace_id=? AND run_target_id=? ORDER BY created_at,id`, workspace, targetID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]ReplayEvidence, 0)
	for rows.Next() {
		var item ReplayEvidence
		var attemptID, observationID sql.NullString
		var targetJSON, permissionMode, created string
		var confirmed int
		var kind string
		if err := rows.Scan(&item.ID, &item.Workspace, &item.RunTargetID, &kind, &attemptID, &observationID, &item.Metadata.PackageName, &item.Metadata.ActivityName, &item.Metadata.AppVersion, &item.Metadata.CoordinateSpace, &targetJSON, &permissionMode, &confirmed, &created); err != nil {
			return nil, err
		}
		item.Action = action.Kind(kind)
		if attemptID.Valid {
			item.ActionAttemptID = runs.ActionAttemptID(attemptID.String)
		}
		if observationID.Valid {
			item.ObservationID = observations.ObservationID(observationID.String)
		}
		if err := json.Unmarshal([]byte(targetJSON), &item.Metadata.Target); err != nil {
			return nil, err
		}
		item.Metadata.PermissionDialogMode = runs.ReplayPermissionMode(permissionMode)
		item.Metadata.IrreversibleConfirmed = confirmed != 0
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, item)
	}
	return result, classifyContext(rows.Err())
}

func (s *RunService) RecordAICandidate(ctx context.Context, workspace organizations.WorkspaceID, candidate runs.AICandidate, actorType, actorID string) (runs.AICandidate, error) {
	if err := validateRunServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return candidate, err
	}
	if err := candidate.Validate(s.store.clock.Now().UTC()); err != nil {
		return candidate, platformerrors.Wrap(platformerrors.CodeInvalidInput, "AI candidate is invalid", err)
	}
	if strings.TrimSpace(string(candidate.RunTargetID)) == "" {
		return candidate, platformerrors.New(platformerrors.CodeInvalidInput, "AI candidate must be attached to a run target")
	}
	evidenceJSON, err := json.Marshal(candidate.Evidence)
	if err != nil {
		return candidate, platformerrors.Wrap(platformerrors.CodeInternal, "encode AI candidate evidence", err)
	}
	now := s.store.clock.Now().UTC()
	err = WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var runID string
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM run_targets WHERE workspace_id=? AND id=?`, workspace, candidate.RunTargetID).Scan(&runID); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "run target not found")
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_ai_candidates (id, workspace_id, run_target_id, provider, model, prompt_template_version, proposal_json, confidence, uncertainty, evidence_json, expires_at, disposition, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.ID, workspace, candidate.RunTargetID, candidate.Provider, candidate.Model, candidate.PromptTemplateVersion, candidate.ProposalJSON, candidate.Confidence, candidate.Uncertainty, string(evidenceJSON), candidate.ExpiresAt.UTC().Format(time.RFC3339Nano), candidate.Disposition, now.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		if err := appendRunEventTx(ctx, tx, s.store, string(workspace), runID, string(candidate.RunTargetID), "ai.candidate_recorded", `{}`, actorID, "service"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "ai_candidate", candidate.ID, "ai.candidate_recorded", actorType, actorID)
	})
	if err != nil {
		return candidate, err
	}
	return candidate, nil
}

func (s *RunService) ListAICandidates(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID) ([]runs.AICandidate, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, run_target_id, provider, model, prompt_template_version, proposal_json, confidence, uncertainty, evidence_json, expires_at, disposition FROM run_ai_candidates WHERE workspace_id=? AND run_target_id=? ORDER BY created_at,id`, workspace, targetID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]runs.AICandidate, 0)
	for rows.Next() {
		var candidate runs.AICandidate
		var evidenceJSON, expires, disposition string
		if err := rows.Scan(&candidate.ID, &candidate.RunTargetID, &candidate.Provider, &candidate.Model, &candidate.PromptTemplateVersion, &candidate.ProposalJSON, &candidate.Confidence, &candidate.Uncertainty, &evidenceJSON, &expires, &disposition); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &candidate.Evidence); err != nil {
			return nil, err
		}
		candidate.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		candidate.Disposition = runs.CandidateDisposition(disposition)
		result = append(result, candidate)
	}
	return result, classifyContext(rows.Err())
}

func completionStates(outcome action.Outcome, postcondition action.PostconditionState, failure domain.FailureClass) (string, string, domain.FailureClass) {
	switch outcome {
	case action.OutcomeVerified:
		if postcondition != action.PostconditionPassed {
			return "", "", failure
		}
		return string(runs.ActionVerified), string(action.OutcomeVerified), ""
	case action.OutcomeFailed:
		if !failure.Valid() {
			failure = domain.FailurePostcondition
		}
		return string(runs.ActionFailed), string(action.OutcomeFailed), failure
	case action.OutcomeTimedOut:
		if !failure.Valid() {
			failure = domain.FailureTimeout
		}
		return string(runs.ActionTimedOut), string(action.OutcomeTimedOut), failure
	case action.OutcomeCancelled:
		if !failure.Valid() {
			failure = domain.FailureOperatorCancelled
		}
		return string(runs.ActionCancelled), string(action.OutcomeCancelled), failure
	default:
		return "", "", failure
	}
}

func requireFreshObservationTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, deviceID devices.DeviceID, token string) error {
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_snapshots WHERE workspace_id=? AND device_id=? AND freshness_token=? AND state='recorded' AND capture_status='complete'`, workspace, deviceID, token).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		return platformerrors.New(platformerrors.CodeStaleObservation, "completion observation is stale or missing")
	}
	return nil
}

func validateRunEvidenceJSON(value string) error {
	if len(value) > 65536 || !json.Valid([]byte(value)) || redaction.RedactString(value) != value || strings.Contains(strings.ToLower(value), "token") {
		return platformerrors.New(platformerrors.CodeInvalidInput, "run evidence must be bounded JSON without sensitive fields")
	}
	return nil
}

func hasRunEvidence(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return false
	default:
		return true
	}
}

func appendRunEventTx(ctx context.Context, tx *sql.Tx, store *DB, workspace, runID, targetID, eventName, payload, actorID, source string) error {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(eventName) == "" || strings.TrimSpace(actorID) == "" || strings.TrimSpace(source) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "run event identity is required")
	}
	if err := validateRunEvidenceJSON(payload); err != nil {
		return err
	}
	id, err := store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate run event ID", err)
	}
	correlation := "run:" + runID + ":" + eventName
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_events (id, workspace_id, run_id, run_target_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, 1, ?, NULL, ?, ?, ?, ?)`, id, workspace, runID, nullableString(targetID), eventName, correlation, actorID, source, payload, store.clock.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return mapConstraint(err)
	}
	return nil
}

func (s *RunService) ListActionAttempts(ctx context.Context, workspace organizations.WorkspaceID, targetID runs.RunTargetID) ([]runs.ActionAttempt, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, run_target_id, target_run_step_id, sequence, action_kind, invocation_surface, state, idempotency_key, request_hash, fencing_token, observation_token, postcondition_state, evidence_json, outcome_state, target_json, timeout_ms, expected_postcondition, failure_class, created_at, finished_at FROM action_attempts WHERE workspace_id=? AND run_target_id=? ORDER BY sequence,id`, workspace, targetID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]runs.ActionAttempt, 0)
	for rows.Next() {
		attempt, err := scanRunActionAttempt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, attempt)
	}
	return result, classifyContext(rows.Err())
}

func (s *RunService) ListEvents(ctx context.Context, workspace organizations.WorkspaceID, runID runs.RunID) ([]RunEvent, error) {
	if err := validateRunServiceInput(ctx, s, workspace, "reader", "reader"); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, workspace_id, run_id, run_target_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at FROM run_events WHERE workspace_id=? AND run_id=? ORDER BY occurred_at,id`, workspace, runID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]RunEvent, 0)
	for rows.Next() {
		var event RunEvent
		var targetID, causation, occurred sql.NullString
		if err := rows.Scan(&event.ID, &event.Workspace, &event.RunID, &targetID, &event.EventName, &event.Schema, &event.Correlation, &causation, &event.ActorID, &event.Source, &event.PayloadJSON, &occurred); err != nil {
			return nil, err
		}
		if targetID.Valid {
			event.RunTargetID = runs.RunTargetID(targetID.String)
		}
		if causation.Valid {
			event.Causation = causation.String
		}
		if occurred.Valid {
			event.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurred.String)
		}
		result = append(result, event)
	}
	return result, classifyContext(rows.Err())
}

func scanRunActionAttempt(row interface{ Scan(...any) error }) (runs.ActionAttempt, error) {
	var attempt runs.ActionAttempt
	var targetStep, state, kind, surface, rawPost, outcome, targetJSON, expectedPostcondition, created string
	var targetStepValue sql.NullString
	var observation, failure, finished sql.NullString
	var fencing, timeoutMillis int64
	err := row.Scan(&attempt.ID, &attempt.Workspace, &attempt.RunTargetID, &targetStepValue, &attempt.Sequence, &kind, &surface, &state, &attempt.IdempotencyKey, &attempt.RequestHash, &fencing, &observation, &rawPost, &attempt.EvidenceJSON, &outcome, &targetJSON, &timeoutMillis, &expectedPostcondition, &failure, &created, &finished)
	if err != nil {
		return attempt, err
	}
	if targetStepValue.Valid {
		targetStep = targetStepValue.String
	}
	attempt.TargetRunStepID, attempt.Action, attempt.InvocationSurface, attempt.State, attempt.FencingToken = runs.TargetRunStepID(targetStep), runs.ActionKind(kind), action.InvocationSurface(surface), runs.ActionAttemptState(state), uint64(fencing)
	if err := json.Unmarshal([]byte(targetJSON), &attempt.Target); err != nil {
		return attempt, err
	}
	attempt.TimeoutMillis, attempt.ExpectedPostcondition = timeoutMillis, expectedPostcondition
	if outcome == string(action.OutcomeIndeterminate) {
		attempt.State = runs.ActionIndeterminate
	}
	attempt.Postcondition = action.PostconditionState(rawPost)
	attempt.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if observation.Valid {
		attempt.ObservationToken = observation.String
	}
	if failure.Valid {
		attempt.Failure = domain.FailureClass(failure.String)
	}
	if finished.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, finished.String)
		if parseErr == nil {
			attempt.FinishedAt = &value
		}
	}
	return attempt, nil
}

func findRunAttemptByKeyTx(ctx context.Context, tx *sql.Tx, workspace organizations.WorkspaceID, key string) (runs.ActionAttempt, bool, error) {
	attempt, err := loadRunActionAttemptByQuery(ctx, tx, workspace, "", key)
	if err == nil {
		return attempt, true, nil
	}
	if platformerrors.CodeOf(err) == platformerrors.CodeNotFound {
		return runs.ActionAttempt{}, false, nil
	}
	return runs.ActionAttempt{}, false, err
}

func loadRunActionAttempt(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id runs.ActionAttemptID) (runs.ActionAttempt, error) {
	return loadRunActionAttemptByQuery(ctx, queryer, workspace, id, "")
}

func loadRunActionAttemptByQuery(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id runs.ActionAttemptID, idempotencyKey string) (runs.ActionAttempt, error) {
	var query string
	var args []any
	if idempotencyKey != "" {
		query = `SELECT id, workspace_id, run_target_id, target_run_step_id, sequence, action_kind, invocation_surface, state, idempotency_key, request_hash, fencing_token, observation_token, postcondition_state, evidence_json, outcome_state, target_json, timeout_ms, expected_postcondition, failure_class, created_at, finished_at FROM action_attempts WHERE workspace_id=? AND idempotency_key=?`
		args = []any{workspace, idempotencyKey}
	} else {
		query = `SELECT id, workspace_id, run_target_id, target_run_step_id, sequence, action_kind, invocation_surface, state, idempotency_key, request_hash, fencing_token, observation_token, postcondition_state, evidence_json, outcome_state, target_json, timeout_ms, expected_postcondition, failure_class, created_at, finished_at FROM action_attempts WHERE workspace_id=? AND id=?`
		args = []any{workspace, id}
	}
	attempt, err := scanRunActionAttempt(queryer.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return attempt, platformerrors.New(platformerrors.CodeNotFound, "action attempt not found")
	}
	if err != nil {
		return attempt, classifyContext(err)
	}
	return attempt, nil
}
