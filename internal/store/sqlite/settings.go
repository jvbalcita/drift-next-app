package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/settings"
)

type SettingRepository struct{ store *DB }

func NewSettingRepository(store *DB) *SettingRepository { return &SettingRepository{store: store} }

func (r *SettingRepository) validate(ctx context.Context, workspace organizations.WorkspaceID) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	return validateWorkspace(string(workspace))
}

func (r *SettingRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id settings.SettingID) (settings.Setting, error) {
	var setting settings.Setting
	if err := r.validate(ctx, workspace); err != nil {
		return setting, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return setting, platformerrors.New(platformerrors.CodeInvalidInput, "setting ID is required")
	}
	err := scanSetting(r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, scope, target_id, setting_key, value_json, state, created_at, updated_at, row_version FROM settings WHERE workspace_id=? AND id=?`, workspace, id), &setting)
	if err == sql.ErrNoRows {
		return setting, platformerrors.New(platformerrors.CodeNotFound, "setting not found")
	}
	return setting, classifyContext(err)
}

func (r *SettingRepository) List(ctx context.Context, workspace organizations.WorkspaceID, scope settings.Scope) ([]settings.Setting, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, scope, target_id, setting_key, value_json, state, created_at, updated_at, row_version FROM settings WHERE workspace_id=? AND (?='' OR scope=?) ORDER BY scope, setting_key, id`, workspace, scope, scope)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]settings.Setting, 0)
	for rows.Next() {
		var setting settings.Setting
		if err := scanSetting(rows, &setting); err != nil {
			return nil, err
		}
		result = append(result, setting)
	}
	return result, classifyContext(rows.Err())
}

func (r *SettingRepository) ListHistory(ctx context.Context, workspace organizations.WorkspaceID, id settings.SettingID) ([]settings.Change, error) {
	if err := r.validate(ctx, workspace); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, setting_id, scope, target_id, setting_key, value_json, state, row_version, actor_type, actor_id, changed_at FROM setting_history WHERE workspace_id=? AND setting_id=? ORDER BY row_version, id`, workspace, id)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]settings.Change, 0)
	for rows.Next() {
		var change settings.Change
		if err := scanSettingChange(rows, &change); err != nil {
			return nil, err
		}
		result = append(result, change)
	}
	return result, classifyContext(rows.Err())
}

func scanSetting(row interface{ Scan(...any) error }, setting *settings.Setting) error {
	var created, updated string
	var rowVersion int64
	if err := row.Scan(&setting.ID, &setting.Workspace, &setting.Scope, &setting.TargetID, &setting.Key, &setting.ValueJSON, &setting.State, &created, &updated, &rowVersion); err != nil {
		return err
	}
	setting.CreatedAt = parseTimeOrZero(created)
	setting.UpdatedAt = parseTimeOrZero(updated)
	setting.RowVersion = uint64(rowVersion)
	return nil
}

func scanSettingChange(row interface{ Scan(...any) error }, change *settings.Change) error {
	var changed string
	var rowVersion int64
	if err := row.Scan(&change.ID, &change.Workspace, &change.SettingID, &change.Scope, &change.TargetID, &change.Key, &change.ValueJSON, &change.State, &rowVersion, &change.ActorType, &change.ActorID, &changed); err != nil {
		return err
	}
	change.RowVersion = uint64(rowVersion)
	change.ChangedAt = parseTimeOrZero(changed)
	return nil
}

type SettingService struct{ store *DB }

func NewSettingService(store *DB) *SettingService { return &SettingService{store: store} }

func (s *SettingService) Create(ctx context.Context, setting settings.Setting, actorType, actorID string) (settings.Setting, error) {
	if s == nil {
		return setting, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateSettingMutation(ctx, s.store, setting.Workspace, actorType, actorID); err != nil {
		return setting, err
	}
	if setting.State == "" {
		setting.State = settings.Active
	}
	if setting.CreatedAt.IsZero() {
		setting.CreatedAt = s.store.clock.Now().UTC()
	}
	if setting.UpdatedAt.IsZero() {
		setting.UpdatedAt = setting.CreatedAt
	}
	setting.RowVersion = 1
	if err := setting.Validate(); err != nil {
		return setting, platformerrors.Wrap(platformerrors.CodeInvalidInput, "setting is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (id, workspace_id, scope, target_id, setting_key, value_json, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, setting.ID, setting.Workspace, setting.Scope, setting.TargetID, setting.Key, setting.ValueJSON, setting.State, setting.CreatedAt.UTC().Format(time.RFC3339Nano), setting.UpdatedAt.UTC().Format(time.RFC3339Nano), setting.RowVersion); err != nil {
			return mapConstraint(err)
		}
		if err := insertSettingChange(ctx, tx, s.store, setting, actorType, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(setting.Workspace), "setting", string(setting.ID), "setting.created", actorType, actorID)
	})
	return setting, err
}

func (s *SettingService) Update(ctx context.Context, setting settings.Setting, expected uint64, actorType, actorID string) (settings.Setting, error) {
	if s == nil {
		return setting, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateSettingMutation(ctx, s.store, setting.Workspace, actorType, actorID); err != nil {
		return setting, err
	}
	if expected == 0 {
		return setting, platformerrors.New(platformerrors.CodeInvalidInput, "setting row version is required")
	}
	if err := setting.Validate(); err != nil {
		return setting, platformerrors.Wrap(platformerrors.CodeInvalidInput, "setting is invalid", err)
	}
	now := s.store.clock.Now().UTC()
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current settings.Setting
		if err := scanSetting(tx.QueryRowContext(ctx, `SELECT id, workspace_id, scope, target_id, setting_key, value_json, state, created_at, updated_at, row_version FROM settings WHERE workspace_id=? AND id=?`, setting.Workspace, setting.ID), &current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "setting not found")
		} else if err != nil {
			return err
		}
		if current.Scope != setting.Scope || current.TargetID != setting.TargetID || current.Key != setting.Key {
			return platformerrors.New(platformerrors.CodeConflict, "setting identity is immutable")
		}
		if setting.State != current.State {
			if err := settings.Transition(current.State, setting.State); err != nil {
				return platformerrors.Wrap(platformerrors.CodeConflict, "setting cannot transition", err)
			}
		}
		updated, err := tx.ExecContext(ctx, `UPDATE settings SET value_json=?, state=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, setting.ValueJSON, setting.State, now.Format(time.RFC3339Nano), setting.Workspace, setting.ID, expected)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "setting"); err != nil {
			return err
		}
		setting.CreatedAt, setting.UpdatedAt, setting.RowVersion = current.CreatedAt, now, expected+1
		if err := insertSettingChange(ctx, tx, s.store, setting, actorType, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(setting.Workspace), "setting", string(setting.ID), "setting.updated", actorType, actorID)
	})
	return setting, err
}

func (s *SettingService) Transition(ctx context.Context, workspace organizations.WorkspaceID, id settings.SettingID, next settings.State, expected uint64, actorType, actorID string) (settings.Setting, error) {
	if s == nil {
		return settings.Setting{}, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if err := validateSettingMutation(ctx, s.store, workspace, actorType, actorID); err != nil {
		return settings.Setting{}, err
	}
	if expected == 0 || !next.Valid() {
		return settings.Setting{}, platformerrors.New(platformerrors.CodeInvalidInput, "setting transition fields are invalid")
	}
	now := s.store.clock.Now().UTC()
	var result settings.Setting
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if err := scanSetting(tx.QueryRowContext(ctx, `SELECT id, workspace_id, scope, target_id, setting_key, value_json, state, created_at, updated_at, row_version FROM settings WHERE workspace_id=? AND id=?`, workspace, id), &result); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "setting not found")
		} else if err != nil {
			return err
		}
		if err := settings.Transition(result.State, next); err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "setting cannot transition", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE settings SET state=?, updated_at=?, row_version=row_version+1 WHERE workspace_id=? AND id=? AND row_version=?`, next, now.Format(time.RFC3339Nano), workspace, id, expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(updated, "setting"); err != nil {
			return err
		}
		result.State, result.UpdatedAt, result.RowVersion = next, now, expected+1
		if err := insertSettingChange(ctx, tx, s.store, result, actorType, actorID); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "setting", string(id), "setting.transitioned", actorType, actorID)
	})
	return result, err
}

func validateSettingMutation(ctx context.Context, store *DB, workspace organizations.WorkspaceID, actorType, actorID string) error {
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

func insertSettingChange(ctx context.Context, tx *sql.Tx, store *DB, setting settings.Setting, actorType, actorID string) error {
	id, err := store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate setting history ID", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO setting_history (id, workspace_id, setting_id, scope, target_id, setting_key, value_json, state, row_version, actor_type, actor_id, changed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, setting.Workspace, setting.ID, setting.Scope, setting.TargetID, setting.Key, setting.ValueJSON, setting.State, setting.RowVersion, actorType, actorID, store.clock.Now().UTC().Format(time.RFC3339Nano))
	return mapConstraint(err)
}
