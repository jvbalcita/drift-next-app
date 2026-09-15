package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/accounts"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// AccountRepository reads non-secret account projections and their append-only
// state, run, assignment, and connector-event history.
type AccountRepository struct{ store *DB }

func NewAccountRepository(store *DB) *AccountRepository { return &AccountRepository{store: store} }

func (r *AccountRepository) validate(ctx context.Context, workspace organizations.WorkspaceID) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	return validateWorkspace(string(workspace))
}

func (r *AccountRepository) ListSources(ctx context.Context, workspace organizations.WorkspaceID) ([]accounts.AccountSource, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, provider, display_name, state, external_reference, metadata_json, created_at, updated_at, row_version FROM account_sources WHERE workspace_id=? ORDER BY display_name, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.AccountSource, 0)
	for rows.Next() {
		var source accounts.AccountSource
		if err := scanAccountSource(rows, &source); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) GetSource(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountSourceID) (accounts.AccountSource, error) {
	var source accounts.AccountSource
	if err := r.validate(ctx, workspace); err != nil {
		return source, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return source, platformerrors.New(platformerrors.CodeInvalidInput, "account source ID is required")
	}
	err := scanAccountSource(r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, provider, display_name, state, external_reference, metadata_json, created_at, updated_at, row_version FROM account_sources WHERE workspace_id=? AND id=?`, workspace, id), &source)
	if err == sql.ErrNoRows {
		return source, platformerrors.New(platformerrors.CodeNotFound, "account source not found")
	}
	return source, classifyContext(err)
}

func (r *AccountRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]accounts.Account, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, source_id, external_ref, label, metadata_json, state, created_at, updated_at, row_version FROM accounts WHERE workspace_id=? ORDER BY label, id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.Account, 0)
	for rows.Next() {
		var account accounts.Account
		if err := scanAccount(rows, &account); err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountID) (accounts.Account, error) {
	var account accounts.Account
	if err := r.validate(ctx, workspace); err != nil {
		return account, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return account, platformerrors.New(platformerrors.CodeInvalidInput, "account ID is required")
	}
	err := scanAccount(r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, source_id, external_ref, label, metadata_json, state, created_at, updated_at, row_version FROM accounts WHERE workspace_id=? AND id=?`, workspace, id), &account)
	if err == sql.ErrNoRows {
		return account, platformerrors.New(platformerrors.CodeNotFound, "account not found")
	}
	return account, classifyContext(err)
}

func (r *AccountRepository) ListServiceStates(ctx context.Context, workspace organizations.WorkspaceID, accountID accounts.AccountID) ([]accounts.ServiceStateProjection, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_id, service_name, stage, state, observed_at, failure_class, details_json, row_version FROM account_service_states WHERE workspace_id=? AND (?='' OR account_id=?) ORDER BY account_id, service_name, id`, workspace, accountID, accountID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.ServiceStateProjection, 0)
	for rows.Next() {
		var state accounts.ServiceStateProjection
		if err := scanServiceState(rows, &state); err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) ListServiceStateHistory(ctx context.Context, workspace organizations.WorkspaceID, accountID accounts.AccountID) ([]accounts.ServiceStateHistory, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_id, service_name, stage, state, observed_at, failure_class, details_json, row_version, recorded_at FROM account_service_state_history WHERE workspace_id=? AND (?='' OR account_id=?) ORDER BY observed_at, id`, workspace, accountID, accountID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.ServiceStateHistory, 0)
	for rows.Next() {
		var history accounts.ServiceStateHistory
		if err := scanServiceStateHistory(rows, &history); err != nil {
			return nil, err
		}
		result = append(result, history)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) ListRuns(ctx context.Context, workspace organizations.WorkspaceID, accountID accounts.AccountID) ([]accounts.AccountRun, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_id, state, requested_at, started_at, finished_at, failure_class, correlation_id, row_version FROM account_runs WHERE workspace_id=? AND (?='' OR account_id=?) ORDER BY requested_at, id`, workspace, accountID, accountID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.AccountRun, 0)
	for rows.Next() {
		var run accounts.AccountRun
		if err := scanAccountRun(rows, &run); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) ListRunEvents(ctx context.Context, workspace organizations.WorkspaceID, runID accounts.AccountRunID) ([]accounts.AccountRunEvent, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_run_id, state, failure_class, correlation_id, actor_type, actor_id, occurred_at FROM account_run_events WHERE workspace_id=? AND account_run_id=? ORDER BY occurred_at, id`, workspace, runID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.AccountRunEvent, 0)
	for rows.Next() {
		var event accounts.AccountRunEvent
		if err := scanAccountRunEvent(rows, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) ListAssignments(ctx context.Context, workspace organizations.WorkspaceID, accountID accounts.AccountID) ([]accounts.AccountDeviceAssignment, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_id, device_id, state, assigned_at, ended_at, row_version FROM account_device_assignments WHERE workspace_id=? AND (?='' OR account_id=?) ORDER BY assigned_at, id`, workspace, accountID, accountID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.AccountDeviceAssignment, 0)
	for rows.Next() {
		var assignment accounts.AccountDeviceAssignment
		if err := scanAccountAssignment(rows, &assignment); err != nil {
			return nil, err
		}
		result = append(result, assignment)
	}
	return result, classifyContext(rows.Err())
}

func (r *AccountRepository) GetAssignment(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountDeviceAssignmentID) (accounts.AccountDeviceAssignment, error) {
	var assignment accounts.AccountDeviceAssignment
	if err := r.validate(ctx, workspace); err != nil {
		return assignment, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return assignment, platformerrors.New(platformerrors.CodeInvalidInput, "assignment ID is required")
	}
	err := scanAccountAssignment(r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, account_id, device_id, state, assigned_at, ended_at, row_version FROM account_device_assignments WHERE workspace_id=? AND id=?`, workspace, id), &assignment)
	if err == sql.ErrNoRows {
		return assignment, platformerrors.New(platformerrors.CodeNotFound, "account-device assignment not found")
	}
	if err != nil {
		return assignment, classifyContext(err)
	}
	return assignment, nil
}

func (r *AccountRepository) ListSyncEvents(ctx context.Context, workspace organizations.WorkspaceID, sourceID accounts.AccountSourceID) ([]accounts.SyncEvent, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, account_source_id, account_id, event_name, occurred_at, outcome, details_json, idempotency_key, correlation_id FROM account_sync_events WHERE workspace_id=? AND (?='' OR account_source_id=?) ORDER BY occurred_at, id`, workspace, sourceID, sourceID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]accounts.SyncEvent, 0)
	for rows.Next() {
		var event accounts.SyncEvent
		if err := scanSyncEvent(rows, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, classifyContext(rows.Err())
}

func scanAccountSource(row interface{ Scan(...any) error }, source *accounts.AccountSource) error {
	var external, created, updated sql.NullString
	var rowVersion int64
	if err := row.Scan(&source.ID, &source.Workspace, &source.Provider, &source.DisplayName, &source.State, &external, &source.MetadataJSON, &created, &updated, &rowVersion); err != nil {
		return err
	}
	if external.Valid {
		source.ExternalReference = external.String
	}
	source.CreatedAt = parseTimeOrZero(created.String)
	source.UpdatedAt = parseTimeOrZero(updated.String)
	source.RowVersion = uint64(rowVersion)
	return nil
}

func scanAccount(row interface{ Scan(...any) error }, account *accounts.Account) error {
	var created, updated string
	var rowVersion int64
	if err := row.Scan(&account.ID, &account.Workspace, &account.SourceID, &account.ExternalRef, &account.Label, &account.MetadataJSON, &account.State, &created, &updated, &rowVersion); err != nil {
		return err
	}
	account.CreatedAt = parseTimeOrZero(created)
	account.UpdatedAt = parseTimeOrZero(updated)
	account.RowVersion = uint64(rowVersion)
	return nil
}

func scanServiceState(row interface{ Scan(...any) error }, state *accounts.ServiceStateProjection) error {
	var failure, observed sql.NullString
	var rowVersion int64
	if err := row.Scan(&state.ID, &state.Workspace, &state.AccountID, &state.ServiceName, &state.Stage, &state.State, &observed, &failure, &state.DetailsJSON, &rowVersion); err != nil {
		return err
	}
	state.ObservedAt = parseTimeOrZero(observed.String)
	if failure.Valid {
		state.FailureClass = domain.FailureClass(failure.String)
	}
	state.RowVersion = uint64(rowVersion)
	return nil
}

func scanServiceStateHistory(row interface{ Scan(...any) error }, history *accounts.ServiceStateHistory) error {
	var failure, observed, recorded sql.NullString
	var rowVersion int64
	if err := row.Scan(&history.ID, &history.Workspace, &history.AccountID, &history.ServiceName, &history.Stage, &history.State, &observed, &failure, &history.DetailsJSON, &rowVersion, &recorded); err != nil {
		return err
	}
	history.ObservedAt = parseTimeOrZero(observed.String)
	history.RowVersion = uint64(rowVersion)
	history.RecordedAt = parseTimeOrZero(recorded.String)
	if failure.Valid {
		history.FailureClass = domain.FailureClass(failure.String)
	}
	return nil
}

func scanAccountRun(row interface{ Scan(...any) error }, run *accounts.AccountRun) error {
	var finished, failure, requested, started, correlation sql.NullString
	var rowVersion int64
	if err := row.Scan(&run.ID, &run.Workspace, &run.AccountID, &run.State, &requested, &started, &finished, &failure, &correlation, &rowVersion); err != nil {
		return err
	}
	run.RequestedAt = parseTimeOrZero(requested.String)
	if started.Valid {
		run.StartedAt = parseOptionalTime(started.String)
	}
	if finished.Valid {
		run.FinishedAt = parseOptionalTime(finished.String)
	}
	if failure.Valid {
		run.FailureClass = domain.FailureClass(failure.String)
	}
	if correlation.Valid {
		run.CorrelationID = correlation.String
	}
	run.RowVersion = uint64(rowVersion)
	return nil
}

func scanAccountRunEvent(row interface{ Scan(...any) error }, event *accounts.AccountRunEvent) error {
	var failure, occurred sql.NullString
	if err := row.Scan(&event.ID, &event.Workspace, &event.RunID, &event.State, &failure, &event.CorrelationID, &event.ActorType, &event.ActorID, &occurred); err != nil {
		return err
	}
	if failure.Valid {
		event.FailureClass = domain.FailureClass(failure.String)
	}
	event.OccurredAt = parseTimeOrZero(occurred.String)
	return nil
}

func scanAccountAssignment(row interface{ Scan(...any) error }, assignment *accounts.AccountDeviceAssignment) error {
	var assigned, ended sql.NullString
	var rowVersion int64
	if err := row.Scan(&assignment.ID, &assignment.Workspace, &assignment.AccountID, &assignment.DeviceID, &assignment.State, &assigned, &ended, &rowVersion); err != nil {
		return err
	}
	assignment.AssignedAt = parseTimeOrZero(assigned.String)
	assignment.EndedAt = parseNullableTime(ended)
	assignment.RowVersion = uint64(rowVersion)
	return nil
}

func scanSyncEvent(row interface{ Scan(...any) error }, event *accounts.SyncEvent) error {
	var accountID, occurred string
	if err := row.Scan(&event.ID, &event.Workspace, &event.SourceID, &accountID, &event.EventName, &occurred, &event.Outcome, &event.DetailsJSON, &event.IdempotencyKey, &event.CorrelationID); err != nil {
		return err
	}
	if accountID != "" {
		id := accounts.AccountID(accountID)
		event.AccountID = &id
	}
	event.OccurredAt = parseTimeOrZero(occurred)
	return nil
}

func parseOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func validateAccountMutation(ctx context.Context, store *DB, workspace organizations.WorkspaceID, actorType, actorID string) error {
	if ctx == nil || store == nil || store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	return nil
}

type AccountSourceService struct{ store *DB }

func NewAccountSourceService(store *DB) *AccountSourceService {
	return &AccountSourceService{store: store}
}

func (s *AccountSourceService) Create(ctx context.Context, source accounts.AccountSource, actorType, actorID string) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, source.Workspace, actorType, actorID); err != nil {
		return err
	}
	if source.MetadataJSON == "" {
		source.MetadataJSON = `{}`
	}
	if source.CreatedAt.IsZero() {
		source.CreatedAt = s.store.clock.Now().UTC()
	}
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = source.CreatedAt
	}
	if source.RowVersion == 0 {
		source.RowVersion = 1
	}
	if err := source.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "account source is invalid", err)
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO account_sources (id, workspace_id, provider, display_name, state, external_reference, metadata_json, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, source.ID, source.Workspace, source.Provider, source.DisplayName, source.State, nullableString(source.ExternalReference), source.MetadataJSON, source.CreatedAt.UTC().Format(time.RFC3339Nano), source.UpdatedAt.UTC().Format(time.RFC3339Nano), source.RowVersion)
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(source.Workspace), "account_source", string(source.ID), "account_source.created", actorType, actorID)
	})
}

func (s *AccountSourceService) Update(ctx context.Context, source accounts.AccountSource, expected uint64, actorType, actorID string) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, source.Workspace, actorType, actorID); err != nil {
		return err
	}
	if expected == 0 {
		return platformerrors.New(platformerrors.CodeInvalidInput, "account source row version is required")
	}
	if source.MetadataJSON == "" {
		source.MetadataJSON = `{}`
	}
	if err := source.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "account source is invalid", err)
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current accounts.AccountSource
		if err := scanAccountSource(tx.QueryRowContext(ctx, `SELECT id, workspace_id, provider, display_name, state, external_reference, metadata_json, created_at, updated_at, row_version FROM account_sources WHERE workspace_id=? AND id=?`, source.Workspace, source.ID), &current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account source not found")
		} else if err != nil {
			return err
		}
		if source.State != current.State {
			if err := accounts.TransitionSource(current.State, source.State); err != nil {
				return platformerrors.Wrap(platformerrors.CodeConflict, "account source cannot transition", err)
			}
		}
		if source.Provider != current.Provider {
			return platformerrors.New(platformerrors.CodeConflict, "account source provider is immutable")
		}
		result, err := tx.ExecContext(ctx, `UPDATE account_sources SET provider=?, display_name=?, state=?, external_reference=?, metadata_json=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, source.Provider, source.DisplayName, source.State, nullableString(source.ExternalReference), source.MetadataJSON, now, source.Workspace, source.ID, expected)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "account source"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(source.Workspace), "account_source", string(source.ID), "account_source.updated", actorType, actorID)
	})
}

func (s *AccountSourceService) Transition(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountSourceID, next accounts.SourceState, expected uint64, actorType, actorID string) error {
	if s == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, workspace, actorType, actorID); err != nil {
		return err
	}
	if expected == 0 || !next.Valid() {
		return platformerrors.New(platformerrors.CodeInvalidInput, "account source transition fields are invalid")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current accounts.SourceState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM account_sources WHERE workspace_id=? AND id=?`, workspace, id).Scan(&current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account source not found")
		} else if err != nil {
			return err
		}
		if err := accounts.TransitionSource(current, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "account source cannot transition", err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE account_sources SET state=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, next, now, workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "account source"); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "account_source", string(id), "account_source.transitioned", actorType, actorID)
	})
}

type AccountService struct{ store *DB }

func NewAccountService(store *DB) *AccountService { return &AccountService{store: store} }

func (s *AccountService) Create(ctx context.Context, account accounts.Account, actorType, actorID string) (accounts.Account, error) {
	if s == nil {
		return account, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, account.Workspace, actorType, actorID); err != nil {
		return account, err
	}
	if account.MetadataJSON == "" {
		account.MetadataJSON = `{}`
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = s.store.clock.Now().UTC()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
	if account.RowVersion == 0 {
		account.RowVersion = 1
	}
	if err := account.Validate(); err != nil {
		return account, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var sourceState accounts.SourceState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM account_sources WHERE workspace_id=? AND id=?`, account.Workspace, account.SourceID).Scan(&sourceState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account source not found")
		} else if err != nil {
			return err
		}
		if sourceState != accounts.SourceActive {
			return platformerrors.New(platformerrors.CodePolicyDenied, "account source is not active")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, workspace_id, source_id, external_ref, label, metadata_json, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, account.ID, account.Workspace, account.SourceID, account.ExternalRef, account.Label, account.MetadataJSON, account.State, account.CreatedAt.UTC().Format(time.RFC3339Nano), account.UpdatedAt.UTC().Format(time.RFC3339Nano), account.RowVersion)
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(account.Workspace), "account", string(account.ID), "account.created", actorType, actorID)
	})
	return account, err
}

func (s *AccountService) Update(ctx context.Context, account accounts.Account, expected uint64, actorType, actorID string) (accounts.Account, error) {
	if s == nil {
		return account, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, account.Workspace, actorType, actorID); err != nil {
		return account, err
	}
	if expected == 0 {
		return account, platformerrors.New(platformerrors.CodeInvalidInput, "account row version is required")
	}
	if account.MetadataJSON == "" {
		account.MetadataJSON = `{}`
	}
	if err := account.Validate(); err != nil {
		return account, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account is invalid", err)
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current accounts.Account
		if err := scanAccount(tx.QueryRowContext(ctx, `SELECT id, workspace_id, source_id, external_ref, label, metadata_json, state, created_at, updated_at, row_version FROM accounts WHERE workspace_id=? AND id=?`, account.Workspace, account.ID), &current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account not found")
		} else if err != nil {
			return err
		}
		if account.State != current.State {
			if err := accounts.Transition(current.State, account.State); err != nil {
				return platformerrors.Wrap(platformerrors.CodeConflict, "account cannot transition", err)
			}
		}
		if account.SourceID != current.SourceID {
			return platformerrors.New(platformerrors.CodeConflict, "account source reference is immutable")
		}
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET source_id=?, external_ref=?, label=?, metadata_json=?, state=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, account.SourceID, account.ExternalRef, account.Label, account.MetadataJSON, account.State, now.Format(time.RFC3339Nano), account.Workspace, account.ID, expected)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(result, "account"); err != nil {
			return err
		}
		account.CreatedAt = current.CreatedAt
		account.UpdatedAt = now
		account.RowVersion = expected + 1
		return s.store.recordMutation(ctx, tx, string(account.Workspace), "account", string(account.ID), "account.updated", actorType, actorID)
	})
	return account, err
}

func (s *AccountService) Transition(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountID, next accounts.State, expected uint64, actorType, actorID string) (accounts.Account, error) {
	if s == nil {
		return accounts.Account{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, workspace, actorType, actorID); err != nil {
		return accounts.Account{}, err
	}
	if expected == 0 || !next.Valid() {
		return accounts.Account{}, platformerrors.New(platformerrors.CodeInvalidInput, "account transition fields are invalid")
	}
	now := s.store.clock.Now().UTC()
	var resultAccount accounts.Account
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := scanAccount(tx.QueryRowContext(ctx, `SELECT id, workspace_id, source_id, external_ref, label, metadata_json, state, created_at, updated_at, row_version FROM accounts WHERE workspace_id=? AND id=?`, workspace, id), &resultAccount); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account not found")
		} else if err != nil {
			return err
		}
		if err := accounts.Transition(resultAccount.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "account cannot transition", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE accounts SET state=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, next, now.Format(time.RFC3339Nano), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "account"); err != nil {
			return err
		}
		resultAccount.State, resultAccount.UpdatedAt, resultAccount.RowVersion = next, now, expected+1
		return s.store.recordMutation(ctx, tx, string(workspace), "account", string(id), "account.transitioned", actorType, actorID)
	})
	return resultAccount, err
}

func (s *AccountService) RecordServiceState(ctx context.Context, state accounts.ServiceStateProjection, actorType, actorID string) (accounts.ServiceStateProjection, error) {
	if s == nil {
		return state, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, state.Workspace, actorType, actorID); err != nil {
		return state, err
	}
	if state.DetailsJSON == "" {
		state.DetailsJSON = `{}`
	}
	if state.ObservedAt.IsZero() {
		state.ObservedAt = s.store.clock.Now().UTC()
	}
	if err := state.Validate(); err != nil {
		return state, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account service state is invalid", err)
	}
	recordedAt := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current accounts.ServiceStateProjection
		err := scanServiceState(tx.QueryRowContext(ctx, `SELECT id, workspace_id, account_id, service_name, stage, state, observed_at, failure_class, details_json, row_version FROM account_service_states WHERE workspace_id=? AND account_id=? AND service_name=?`, state.Workspace, state.AccountID, state.ServiceName), &current)
		if err == sql.ErrNoRows {
			id, idErr := s.store.ids.NewID()
			if idErr != nil {
				return platformerrors.Wrap(platformerrors.CodeInternal, "generate account service state ID", idErr)
			}
			state.ID = accounts.AccountServiceStateID(id)
			state.RowVersion = 1
			_, err = tx.ExecContext(ctx, `INSERT INTO account_service_states (id, workspace_id, account_id, service_name, stage, state, observed_at, failure_class, details_json, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, state.ID, state.Workspace, state.AccountID, state.ServiceName, state.Stage, state.State, state.ObservedAt.UTC().Format(time.RFC3339Nano), nullableString(string(state.FailureClass)), state.DetailsJSON, state.RowVersion)
			if err != nil {
				return mapConstraint(err)
			}
		} else if err != nil {
			return err
		} else {
			state.ID = current.ID
			state.RowVersion = current.RowVersion + 1
			updated, updateErr := tx.ExecContext(ctx, `UPDATE account_service_states SET stage=?, state=?, observed_at=?, failure_class=?, details_json=?, row_version=row_version+1 WHERE workspace_id=? AND account_id=? AND service_name=? AND row_version=?`, state.Stage, state.State, state.ObservedAt.UTC().Format(time.RFC3339Nano), nullableString(string(state.FailureClass)), state.DetailsJSON, state.Workspace, state.AccountID, state.ServiceName, current.RowVersion)
			if updateErr != nil {
				return mapConstraint(updateErr)
			}
			if err := RequireAffected(updated, "account service state"); err != nil {
				return err
			}
		}
		historyID, err := s.store.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate account service state history ID", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_service_state_history (id, workspace_id, account_id, service_name, stage, state, observed_at, failure_class, details_json, row_version, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, historyID, state.Workspace, state.AccountID, state.ServiceName, state.Stage, state.State, state.ObservedAt.UTC().Format(time.RFC3339Nano), nullableString(string(state.FailureClass)), state.DetailsJSON, state.RowVersion, recordedAt.Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(state.Workspace), "account_service_state", string(state.ID), "account.service_state.recorded", actorType, actorID)
	})
	return state, err
}

func (s *AccountService) CreateRun(ctx context.Context, run accounts.AccountRun, actorType, actorID string) (accounts.AccountRun, error) {
	if s == nil {
		return run, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, run.Workspace, actorType, actorID); err != nil {
		return run, err
	}
	if run.State != "" && run.State != accounts.RunRequested {
		return run, platformerrors.New(platformerrors.CodeInvalidInput, "new account runs must start requested")
	}
	run.State = accounts.RunRequested
	if run.RequestedAt.IsZero() {
		run.RequestedAt = s.store.clock.Now().UTC()
	}
	if strings.TrimSpace(run.CorrelationID) == "" {
		run.CorrelationID = "corr-account-run-" + string(run.ID)
	}
	run.FinishedAt = nil
	run.FailureClass = ""
	run.RowVersion = 1
	if err := run.Validate(); err != nil {
		return run, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account run is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_runs (id, workspace_id, account_id, state, requested_at, started_at, finished_at, failure_class, correlation_id, row_version) VALUES (?, ?, ?, ?, ?, NULL, NULL, NULL, ?, ?)`, run.ID, run.Workspace, run.AccountID, run.State, run.RequestedAt.UTC().Format(time.RFC3339Nano), run.CorrelationID, run.RowVersion); err != nil {
			return mapConstraint(err)
		}
		if err := insertAccountRunEvent(ctx, tx, s.store, run, actorType, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(run.Workspace), "account_run", string(run.ID), "account.run.requested", actorType, actorID)
	})
	return run, err
}

func (s *AccountService) TransitionRun(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountRunID, next accounts.AccountRunState, failure domain.FailureClass, expected uint64, actorType, actorID string) (accounts.AccountRun, error) {
	if s == nil {
		return accounts.AccountRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, workspace, actorType, actorID); err != nil {
		return accounts.AccountRun{}, err
	}
	if expected == 0 || !next.Valid() || (failure != "" && !failure.Valid()) {
		return accounts.AccountRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "account run transition fields are invalid")
	}
	if next == accounts.RunFailed && failure == "" {
		return accounts.AccountRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "failed account run requires a failure class")
	}
	if next != accounts.RunFailed && failure != "" {
		return accounts.AccountRun{}, platformerrors.New(platformerrors.CodeInvalidInput, "only failed account runs may carry a failure class")
	}
	now := s.store.clock.Now().UTC()
	var resultRun accounts.AccountRun
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := scanAccountRun(tx.QueryRowContext(ctx, `SELECT id, workspace_id, account_id, state, requested_at, started_at, finished_at, failure_class, correlation_id, row_version FROM account_runs WHERE workspace_id=? AND id=?`, workspace, id), &resultRun); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account run not found")
		} else if err != nil {
			return err
		}
		if err := accounts.TransitionRun(resultRun.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "account run cannot transition", err)
		}
		var started, finished any
		startedAt := resultRun.StartedAt
		if next == accounts.RunRunning {
			started = now.Format(time.RFC3339Nano)
			startedAt = &now
		} else if resultRun.StartedAt != nil {
			started = resultRun.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if next == accounts.RunCompleted || next == accounts.RunFailed || next == accounts.RunCancelled {
			finished = now.Format(time.RFC3339Nano)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE account_runs SET state=?, started_at=?, finished_at=?, failure_class=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, next, started, finished, nullableString(string(failure)), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "account run"); err != nil {
			return err
		}
		resultRun.State, resultRun.FailureClass, resultRun.RowVersion = next, failure, expected+1
		resultRun.StartedAt = startedAt
		resultRun.FinishedAt = nil
		if finished != nil {
			resultRun.FinishedAt = &now
		}
		if err := insertAccountRunEvent(ctx, tx, s.store, resultRun, actorType, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "account_run", string(id), "account.run.transitioned", actorType, actorID)
	})
	return resultRun, err
}

func insertAccountRunEvent(ctx context.Context, tx *sql.Tx, store *DB, run accounts.AccountRun, actorType, actorID string) error {
	id, err := store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate account run event ID", err)
	}
	var failure any
	if run.FailureClass != "" {
		failure = string(run.FailureClass)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_run_events (id, workspace_id, account_run_id, state, failure_class, correlation_id, actor_type, actor_id, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, run.Workspace, run.ID, run.State, failure, run.CorrelationID, actorType, actorID, store.clock.Now().UTC().Format(time.RFC3339Nano))
	return mapConstraint(err)
}

func (s *AccountService) RecordSyncEvent(ctx context.Context, event accounts.SyncEvent, actorType, actorID string) (accounts.SyncEvent, error) {
	if s == nil {
		return event, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, event.Workspace, actorType, actorID); err != nil {
		return event, err
	}
	if event.ID == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return event, platformerrors.Wrap(platformerrors.CodeInternal, "generate account sync event ID", err)
		}
		event.ID = accounts.AccountSyncEventID(id)
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.store.clock.Now().UTC()
	}
	if strings.TrimSpace(event.IdempotencyKey) == "" {
		event.IdempotencyKey = string(event.ID)
	}
	if strings.TrimSpace(event.CorrelationID) == "" {
		event.CorrelationID = "corr-" + string(event.ID)
	}
	if event.DetailsJSON == "" {
		event.DetailsJSON = `{}`
	}
	if err := event.Validate(); err != nil {
		return event, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account sync event is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var accountID any
		if event.AccountID != nil {
			accountID = *event.AccountID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_sync_events (id, workspace_id, account_source_id, account_id, event_name, occurred_at, outcome, details_json, idempotency_key, correlation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Workspace, event.SourceID, accountID, event.EventName, event.OccurredAt.UTC().Format(time.RFC3339Nano), event.Outcome, event.DetailsJSON, event.IdempotencyKey, event.CorrelationID); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(event.Workspace), "account_sync_event", string(event.ID), "account.sync_event.recorded", actorType, actorID)
	})
	return event, err
}

type AccountAssignmentService struct{ store *DB }

func NewAccountAssignmentService(store *DB) *AccountAssignmentService {
	return &AccountAssignmentService{store: store}
}

func (s *AccountAssignmentService) Assign(ctx context.Context, assignment accounts.AccountDeviceAssignment, actorType, actorID string) (accounts.AccountDeviceAssignment, error) {
	if s == nil {
		return assignment, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, assignment.Workspace, actorType, actorID); err != nil {
		return assignment, err
	}
	if assignment.ID == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return assignment, platformerrors.Wrap(platformerrors.CodeInternal, "generate account assignment ID", err)
		}
		assignment.ID = accounts.AccountDeviceAssignmentID(id)
	}
	assignment.State = accounts.AssignmentActive
	assignment.EndedAt = nil
	assignment.RowVersion = 1
	if assignment.AssignedAt.IsZero() {
		assignment.AssignedAt = s.store.clock.Now().UTC()
	}
	if err := assignment.Validate(); err != nil {
		return assignment, platformerrors.Wrap(platformerrors.CodeInvalidInput, "account-device assignment is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var accountState accounts.State
		if err := tx.QueryRowContext(ctx, `SELECT state FROM accounts WHERE workspace_id=? AND id=?`, assignment.Workspace, assignment.AccountID).Scan(&accountState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account not found")
		} else if err != nil {
			return err
		}
		if accountState == accounts.Retired {
			return platformerrors.New(platformerrors.CodePolicyDenied, "retired account cannot be assigned to a device")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_device_assignments (id, workspace_id, account_id, device_id, state, assigned_at, ended_at, row_version) VALUES (?, ?, ?, ?, 'active', ?, NULL, 1)`, assignment.ID, assignment.Workspace, assignment.AccountID, assignment.DeviceID, assignment.AssignedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(assignment.Workspace), "account_device_assignment", string(assignment.ID), "account.device_assignment.created", actorType, actorID)
	})
	return assignment, err
}

func (s *AccountAssignmentService) End(ctx context.Context, workspace organizations.WorkspaceID, id accounts.AccountDeviceAssignmentID, expected uint64, actorType, actorID string) (accounts.AccountDeviceAssignment, error) {
	if s == nil {
		return accounts.AccountDeviceAssignment{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateAccountMutation(ctx, s.store, workspace, actorType, actorID); err != nil {
		return accounts.AccountDeviceAssignment{}, err
	}
	if expected == 0 {
		return accounts.AccountDeviceAssignment{}, platformerrors.New(platformerrors.CodeInvalidInput, "account-device assignment row version is required")
	}
	now := s.store.clock.Now().UTC()
	var assignment accounts.AccountDeviceAssignment
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var assigned, ended sql.NullString
		var rowVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT id, workspace_id, account_id, device_id, state, assigned_at, ended_at, row_version FROM account_device_assignments WHERE workspace_id=? AND id=?`, workspace, id).Scan(&assignment.ID, &assignment.Workspace, &assignment.AccountID, &assignment.DeviceID, &assignment.State, &assigned, &ended, &rowVersion); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "account-device assignment not found")
		} else if err != nil {
			return err
		}
		assignment.AssignedAt = parseTimeOrZero(assigned.String)
		assignment.RowVersion = uint64(rowVersion)
		if assignment.State != accounts.AssignmentActive {
			return platformerrors.New(platformerrors.CodeConflict, "account-device assignment is already ended")
		}
		updated, err := tx.ExecContext(ctx, `UPDATE account_device_assignments SET state='ended', ended_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND state='active' AND row_version=?`, now.Format(time.RFC3339Nano), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "account-device assignment"); err != nil {
			return err
		}
		assignment.State, assignment.EndedAt, assignment.RowVersion = accounts.AssignmentEnded, &now, expected+1
		return s.store.recordMutation(ctx, tx, string(workspace), "account_device_assignment", string(id), "account.device_assignment.ended", actorType, actorID)
	})
	return assignment, err
}
