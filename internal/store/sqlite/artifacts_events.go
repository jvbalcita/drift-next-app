package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type ArtifactRepository struct{ store *DB }

func NewArtifactRepository(store *DB) *ArtifactRepository { return &ArtifactRepository{store: store} }

func (r *ArtifactRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID) (artifacts.Artifact, error) {
	var artifact artifacts.Artifact
	var created, deleted sql.NullString
	if err := validateWorkspace(string(workspace)); err != nil {
		return artifact, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return artifact, platformerrors.New(platformerrors.CodeInvalidInput, "artifact ID is required")
	}
	var size int64
	err := r.store.db.QueryRowContext(ctx, `SELECT id, workspace_id, content_hash, size_bytes, media_type, schema_version, retention_class, state, created_at, deleted_at FROM artifacts WHERE workspace_id=? AND id=?`, workspace, id).Scan(&artifact.ID, &artifact.Workspace, &artifact.ContentHash, &size, &artifact.MediaType, &artifact.SchemaVersion, &artifact.RetentionClass, &artifact.State, &created, &deleted)
	if err == sql.ErrNoRows {
		return artifact, platformerrors.New(platformerrors.CodeNotFound, "artifact metadata not found")
	}
	if err != nil {
		return artifact, classifyContext(err)
	}
	artifact.SizeBytes = size
	artifact.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	if deleted.Valid {
		t, _ := time.Parse(time.RFC3339Nano, deleted.String)
		artifact.DeletedAt = &t
	}
	return artifact, nil
}

func (r *ArtifactRepository) ListReferences(ctx context.Context, workspace organizations.WorkspaceID, ownerType, ownerID string) ([]artifacts.Reference, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ownerType) == "" || strings.TrimSpace(ownerID) == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "artifact reference owner is required")
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, workspace_id, artifact_id, owner_type, owner_id, state, created_at, ended_at FROM artifact_references WHERE workspace_id=? AND owner_type=? AND owner_id=? ORDER BY created_at, id`, workspace, ownerType, ownerID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]artifacts.Reference, 0)
	for rows.Next() {
		var reference artifacts.Reference
		var created string
		var ended sql.NullString
		if err := rows.Scan(&reference.ID, &reference.Workspace, &reference.ArtifactID, &reference.OwnerType, &reference.OwnerID, &reference.State, &created, &ended); err != nil {
			return nil, err
		}
		reference.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if ended.Valid {
			value, _ := time.Parse(time.RFC3339Nano, ended.String)
			reference.EndedAt = &value
		}
		result = append(result, reference)
	}
	return result, rows.Err()
}

type ArtifactService struct{ store *DB }

func NewArtifactService(store *DB) *ArtifactService { return &ArtifactService{store: store} }

// Record persists metadata only. Artifact bytes must be written by a private
// content-addressed store before this record is promoted to Stored.
func (s *ArtifactService) Record(ctx context.Context, artifact artifacts.Artifact, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := artifact.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "artifact metadata is invalid", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	var deleted any
	if artifact.DeletedAt != nil {
		deleted = artifact.DeletedAt.UTC().Format(time.RFC3339Nano)
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts (id, workspace_id, content_hash, size_bytes, media_type, schema_version, retention_class, state, created_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, artifact.ID, artifact.Workspace, artifact.ContentHash, artifact.SizeBytes, artifact.MediaType, artifact.SchemaVersion, artifact.RetentionClass, artifact.State, artifact.CreatedAt.UTC().Format(time.RFC3339Nano), deleted); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(artifact.Workspace), "artifact", string(artifact.ID), "artifact.metadata.recorded", actorType, actorID)
	})
}

func (s *ArtifactService) Reference(ctx context.Context, reference artifacts.Reference, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := reference.Validate(); err != nil || strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "artifact reference is invalid")
	}
	var ended any
	if reference.EndedAt != nil {
		ended = reference.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	eventName := "artifact.referenced"
	if reference.State == artifacts.ReferenceEnded {
		eventName = "artifact.reference.ended"
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var state artifacts.State
		if err := tx.QueryRowContext(ctx, `SELECT state FROM artifacts WHERE workspace_id=? AND id=?`, reference.Workspace, reference.ArtifactID).Scan(&state); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "artifact metadata not found")
		} else if err != nil {
			return err
		}
		if reference.State == artifacts.ReferenceActive && state != artifacts.Stored && state != artifacts.Referenced {
			return platformerrors.New(platformerrors.CodeConflict, "artifact is not stored and cannot be referenced")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_references (id, workspace_id, artifact_id, owner_type, owner_id, state, created_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, reference.ID, reference.Workspace, reference.ArtifactID, reference.OwnerType, reference.OwnerID, reference.State, reference.CreatedAt.UTC().Format(time.RFC3339Nano), ended); err != nil {
			return mapConstraint(err)
		}
		if reference.State == artifacts.ReferenceActive && state == artifacts.Stored {
			if _, err := tx.ExecContext(ctx, `UPDATE artifacts SET state='referenced' WHERE workspace_id=? AND id=? AND state='stored'`, reference.Workspace, reference.ArtifactID); err != nil {
				return err
			}
		}
		return s.store.recordMutation(ctx, tx, string(reference.Workspace), "artifact_reference", reference.ID, eventName, actorType, actorID)
	})
}

type EventService struct{ store *DB }

func NewEventService(store *DB) *EventService { return &EventService{store: store} }

func (s *EventService) AppendDevice(ctx context.Context, event events.Event) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := event.Validate(); err != nil || event.ResourceType != "device" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "device event is invalid or contains sensitive material")
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_events (id, workspace_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_type, actor_id, source, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Workspace, event.ResourceID, event.Name, event.SchemaVersion, event.CorrelationID, nullableString(event.CausationID), event.ActorType, event.ActorID, event.Source, event.PayloadJSON, event.OccurredAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return mapConstraint(err)
		}
		return nil
	})
}
