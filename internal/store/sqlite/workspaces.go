package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// WorkspaceRepository exposes read-only workspace projections and CAS updates.
type WorkspaceRepository struct{ store *DB }

func NewWorkspaceRepository(store *DB) *WorkspaceRepository {
	return &WorkspaceRepository{store: store}
}

func (r *WorkspaceRepository) Get(ctx context.Context, id organizations.WorkspaceID) (organizations.Workspace, error) {
	var w organizations.Workspace
	if ctx == nil {
		return w, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return w, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if strings.TrimSpace(string(id)) == "" {
		return w, platformerrors.New(platformerrors.CodeInvalidInput, "workspace ID is required")
	}
	var state string
	var version int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id, name, state, row_version FROM workspaces WHERE id = ?`, string(id)).Scan(&w.ID, &w.Name, &state, &version)
	if err == sql.ErrNoRows {
		return w, platformerrors.New(platformerrors.CodeNotFound, "workspace not found")
	}
	if err != nil {
		return w, classifyContext(err)
	}
	w.State, w.RowVersion = organizations.WorkspaceState(state), uint64(version)
	return w, nil
}

func (r *WorkspaceRepository) List(ctx context.Context) ([]organizations.Workspace, error) {
	if ctx == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if r == nil || r.store == nil || r.store.db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, name, state, row_version FROM workspaces ORDER BY id`)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]organizations.Workspace, 0)
	for rows.Next() {
		var w organizations.Workspace
		var state string
		var version int64
		if err := rows.Scan(&w.ID, &w.Name, &state, &version); err != nil {
			return nil, err
		}
		w.State, w.RowVersion = organizations.WorkspaceState(state), uint64(version)
		result = append(result, w)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext(err)
	}
	return result, nil
}

// WorkspaceService owns state-changing workspace commands. Each command has
// one transaction and appends exactly one audit decision and outbox intent.
type WorkspaceService struct{ store *DB }

func NewWorkspaceService(store *DB) *WorkspaceService { return &WorkspaceService{store: store} }

func (s *WorkspaceService) Create(ctx context.Context, w organizations.Workspace, actorType, actorID string) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if strings.TrimSpace(string(w.ID)) == "" || strings.TrimSpace(w.Name) == "" || !w.State.Valid() || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "workspace and actor fields are required")
	}
	now := s.store.clock.Now().UTC().Format(time.RFC3339Nano)
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, state, created_at, updated_at, row_version) VALUES (?, ?, ?, ?, ?, 1)`, string(w.ID), w.Name, string(w.State), now, now)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "constraint") {
				return platformerrors.Wrap(platformerrors.CodeConflict, "workspace already exists", err)
			}
			return err
		}
		return s.record(ctx, tx, string(w.ID), "workspace.created", actorType, actorID)
	})
}

func (s *WorkspaceService) Transition(ctx context.Context, id organizations.WorkspaceID, next organizations.WorkspaceState, expected uint64, actorType, actorID string) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite store is required")
	}
	if strings.TrimSpace(string(id)) == "" || !next.Valid() || expected == 0 || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "transition fields are required")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM workspaces WHERE id = ?`, string(id)).Scan(&current); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "workspace not found")
		} else if err != nil {
			return err
		}
		if err := organizations.Transition(organizations.WorkspaceState(current), next); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE workspaces SET state = ?, updated_at = ?, row_version = row_version + 1 WHERE id = ? AND row_version = ?`, string(next), s.store.clock.Now().UTC().Format(time.RFC3339Nano), string(id), expected)
		if err != nil {
			return err
		}
		if err := RequireAffected(result, "workspace"); err != nil {
			return err
		}
		return s.record(ctx, tx, string(id), "workspace.transitioned", actorType, actorID)
	})
}

func (s *WorkspaceService) record(ctx context.Context, tx *sql.Tx, id, event, actorType, actorID string) error {
	auditID, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate audit ID", err)
	}
	outboxID, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate outbox ID", err)
	}
	entry := AuditEntry{ID: auditID, WorkspaceID: id, ActorType: actorType, ActorID: actorID, EventName: event, SchemaVersion: 1, ResourceType: "workspace", ResourceID: id, CorrelationID: fmt.Sprintf("workspace:%s:%s", id, event), PayloadJSON: `{}`}
	if err := s.store.audit.AppendTx(ctx, tx, entry); err != nil {
		return err
	}
	return s.store.outbox.AppendTx(ctx, tx, OutboxEntry{ID: outboxID, WorkspaceID: id, EventName: event, SchemaVersion: 1, CorrelationID: entry.CorrelationID, PayloadJSON: `{}`})
}
