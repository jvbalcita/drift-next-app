package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const artifactSelectColumns = `id, workspace_id, content_hash, size_bytes, media_type, schema_version, retention_class, state, created_at, deleted_at, category, updated_at, deletion_outcome, failure_classification, omission_reason`

func scanArtifact(scanner interface {
	Scan(dest ...any) error
}) (artifacts.Artifact, error) {
	var artifact artifacts.Artifact
	var size int64
	var created string
	var deleted, updated sql.NullString
	var category, deletionOutcome, failureClass, omission sql.NullString
	if err := scanner.Scan(
		&artifact.ID,
		&artifact.Workspace,
		&artifact.ContentHash,
		&size,
		&artifact.MediaType,
		&artifact.SchemaVersion,
		&artifact.RetentionClass,
		&artifact.State,
		&created,
		&deleted,
		&category,
		&updated,
		&deletionOutcome,
		&failureClass,
		&omission,
	); err != nil {
		return artifact, err
	}
	artifact.SizeBytes = size
	artifact.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if deleted.Valid {
		t, _ := time.Parse(time.RFC3339Nano, deleted.String)
		artifact.DeletedAt = &t
	}
	if updated.Valid && updated.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, updated.String)
		artifact.UpdatedAt = &t
	}
	if category.Valid && category.String != "" {
		artifact.Category = artifacts.Category(category.String)
	} else {
		artifact.Category = artifacts.CategoryUnspecified
	}
	if deletionOutcome.Valid {
		artifact.DeletionOutcome = artifacts.DeletionOutcome(deletionOutcome.String)
	}
	if failureClass.Valid {
		artifact.FailureClassification = artifacts.FailureClassification(failureClass.String)
	}
	if omission.Valid {
		artifact.OmissionReason = omission.String
	}
	return artifact, nil
}

func (r *ArtifactRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID) (artifacts.Artifact, error) {
	var artifact artifacts.Artifact
	if err := validateWorkspace(string(workspace)); err != nil {
		return artifact, err
	}
	if strings.TrimSpace(string(id)) == "" {
		return artifact, platformerrors.New(platformerrors.CodeInvalidInput, "artifact ID is required")
	}
	row := r.store.db.QueryRowContext(ctx, `SELECT `+artifactSelectColumns+` FROM artifacts WHERE workspace_id=? AND id=?`, workspace, id)
	artifact, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return artifacts.Artifact{}, platformerrors.New(platformerrors.CodeNotFound, "artifact metadata not found")
	}
	if err != nil {
		return artifacts.Artifact{}, classifyContext(err)
	}
	return artifact, nil
}

func (r *ArtifactRepository) GetByHash(ctx context.Context, workspace organizations.WorkspaceID, contentHash string) (artifacts.Artifact, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return artifacts.Artifact{}, err
	}
	if strings.TrimSpace(contentHash) == "" {
		return artifacts.Artifact{}, platformerrors.New(platformerrors.CodeInvalidInput, "content hash is required")
	}
	row := r.store.db.QueryRowContext(ctx, `SELECT `+artifactSelectColumns+` FROM artifacts WHERE workspace_id=? AND content_hash=?`, workspace, contentHash)
	artifact, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return artifacts.Artifact{}, platformerrors.New(platformerrors.CodeNotFound, "artifact metadata not found")
	}
	if err != nil {
		return artifacts.Artifact{}, classifyContext(err)
	}
	return artifact, nil
}

func (r *ArtifactRepository) List(ctx context.Context, workspace organizations.WorkspaceID, filter artifacts.ListFilter) ([]artifacts.Artifact, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return nil, err
	}
	query := `SELECT ` + artifactSelectColumns + ` FROM artifacts WHERE workspace_id=?`
	args := []any{workspace}
	if len(filter.States) > 0 {
		placeholders := make([]string, 0, len(filter.States))
		for _, state := range filter.States {
			placeholders = append(placeholders, "?")
			args = append(args, state)
		}
		query += ` AND state IN (` + strings.Join(placeholders, ",") + `)`
	}
	if len(filter.Categories) > 0 {
		placeholders := make([]string, 0, len(filter.Categories))
		for _, category := range filter.Categories {
			placeholders = append(placeholders, "?")
			args = append(args, category)
		}
		query += ` AND category IN (` + strings.Join(placeholders, ",") + `)`
	}
	if len(filter.Retention) > 0 {
		placeholders := make([]string, 0, len(filter.Retention))
		for _, retention := range filter.Retention {
			placeholders = append(placeholders, "?")
			args = append(args, retention)
		}
		query += ` AND retention_class IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY created_at DESC, id DESC`
	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
	}
	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]artifacts.Artifact, 0)
	for rows.Next() {
		artifact, scanErr := scanArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
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

func (r *ArtifactRepository) HasActiveProtectedReference(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID) (bool, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return false, err
	}
	var count int
	err := r.store.db.QueryRowContext(ctx, `
SELECT COUNT(1)
FROM artifact_references
WHERE workspace_id=? AND artifact_id=? AND state='active'
  AND owner_type IN ('audit', 'run', 'lease', 'policy', 'recording', 'observation', 'execution_evidence')
`, workspace, id).Scan(&count)
	if err != nil {
		return false, classifyContext(err)
	}
	return count > 0, nil
}

func (r *ArtifactRepository) Usage(ctx context.Context, workspace organizations.WorkspaceID) (artifacts.Usage, error) {
	if err := validateWorkspace(string(workspace)); err != nil {
		return artifacts.Usage{}, err
	}
	var usage artifacts.Usage
	usage.Workspace = string(workspace)
	err := r.store.db.QueryRowContext(ctx, `
SELECT
  COUNT(1),
  COALESCE(SUM(CASE WHEN state != 'deleted' THEN size_bytes ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN state = 'cleanup_failed' THEN 1 ELSE 0 END), 0)
FROM artifacts
WHERE workspace_id=?
`, workspace).Scan(&usage.ObjectCount, &usage.TotalBytes, &usage.CleanupFailed)
	if err != nil {
		return artifacts.Usage{}, classifyContext(err)
	}
	return usage, nil
}

func (r *ArtifactRepository) ListCleanupFailed(ctx context.Context, workspace organizations.WorkspaceID) ([]artifacts.Artifact, error) {
	return r.List(ctx, workspace, artifacts.ListFilter{States: []artifacts.State{artifacts.CleanupFailed}})
}

func (r *ArtifactRepository) ListOrphanMetadata(ctx context.Context, workspace organizations.WorkspaceID, exists func(contentHash string) (bool, error)) ([]artifacts.Artifact, error) {
	if exists == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "CAS existence probe is required")
	}
	listed, err := r.List(ctx, workspace, artifacts.ListFilter{
		States: []artifacts.State{artifacts.Stored, artifacts.Referenced, artifacts.Retained, artifacts.EligibleForDeletion, artifacts.CleanupFailed},
	})
	if err != nil {
		return nil, err
	}
	orphans := make([]artifacts.Artifact, 0)
	for _, artifact := range listed {
		if artifact.Category == artifacts.CategoryOmission || artifact.SizeBytes == 0 {
			continue
		}
		ok, existsErr := exists(artifact.ContentHash)
		if existsErr != nil {
			return nil, existsErr
		}
		if !ok {
			orphans = append(orphans, artifact)
		}
	}
	return orphans, nil
}

// Record persists metadata only. Artifact bytes must be written by a private
// content-addressed store before this record is promoted to Stored.
func (s *ArtifactService) Record(ctx context.Context, artifact artifacts.Artifact, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if artifact.Category == "" {
		artifact.Category = artifacts.CategoryUnspecified
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
	var updated any
	if artifact.UpdatedAt != nil {
		updated = artifact.UpdatedAt.UTC().Format(time.RFC3339Nano)
	} else {
		updated = artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts (id, workspace_id, content_hash, size_bytes, media_type, schema_version, retention_class, state, created_at, deleted_at, category, updated_at, deletion_outcome, failure_classification, omission_reason) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			artifact.ID, artifact.Workspace, artifact.ContentHash, artifact.SizeBytes, artifact.MediaType, artifact.SchemaVersion, artifact.RetentionClass, artifact.State, artifact.CreatedAt.UTC().Format(time.RFC3339Nano), deleted, artifact.Category, updated, artifact.DeletionOutcome, artifact.FailureClassification, artifact.OmissionReason); err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(artifact.Workspace), "artifact", string(artifact.ID), "artifact.metadata.recorded", actorType, actorID)
	})
}

func (s *ArtifactService) Get(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID) (artifacts.Artifact, error) {
	return NewArtifactRepository(s.store).Get(ctx, workspace, id)
}

func (s *ArtifactService) GetByHash(ctx context.Context, workspace organizations.WorkspaceID, contentHash string) (artifacts.Artifact, error) {
	return NewArtifactRepository(s.store).GetByHash(ctx, workspace, contentHash)
}

func (s *ArtifactService) List(ctx context.Context, workspace organizations.WorkspaceID, filter artifacts.ListFilter) ([]artifacts.Artifact, error) {
	return NewArtifactRepository(s.store).List(ctx, workspace, filter)
}

func (s *ArtifactService) ListReferences(ctx context.Context, workspace organizations.WorkspaceID, ownerType, ownerID string) ([]artifacts.Reference, error) {
	return NewArtifactRepository(s.store).ListReferences(ctx, workspace, ownerType, ownerID)
}

func (s *ArtifactService) HasActiveProtectedReference(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID) (bool, error) {
	return NewArtifactRepository(s.store).HasActiveProtectedReference(ctx, workspace, id)
}

func (s *ArtifactService) Usage(ctx context.Context, workspace organizations.WorkspaceID) (artifacts.Usage, error) {
	return NewArtifactRepository(s.store).Usage(ctx, workspace)
}

func (s *ArtifactService) ListCleanupFailed(ctx context.Context, workspace organizations.WorkspaceID) ([]artifacts.Artifact, error) {
	return NewArtifactRepository(s.store).ListCleanupFailed(ctx, workspace)
}

func (s *ArtifactService) ListOrphanMetadata(ctx context.Context, workspace organizations.WorkspaceID, exists func(contentHash string) (bool, error)) ([]artifacts.Artifact, error) {
	return NewArtifactRepository(s.store).ListOrphanMetadata(ctx, workspace, exists)
}

func (s *ArtifactService) UpdateLifecycle(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID, from, to artifacts.State, outcome artifacts.DeletionOutcome, failure artifacts.FailureClassification, omitted string, at time.Time, actorType, actorID string) error {
	if ctx == nil || s == nil || s.store == nil || s.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if err := artifacts.Transition(from, to); err != nil && from != to {
		return platformerrors.Wrap(platformerrors.CodeConflict, "artifact transition rejected", err)
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "actor fields are required")
	}
	at = at.UTC()
	var deleted any
	if to == artifacts.Deleted {
		deleted = at.Format(time.RFC3339Nano)
	}
	return WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE artifacts
SET state=?, updated_at=?, deleted_at=COALESCE(?, deleted_at), deletion_outcome=?, failure_classification=?, omission_reason=CASE WHEN ? <> '' THEN ? ELSE omission_reason END
WHERE workspace_id=? AND id=? AND state=?
`, to, at.Format(time.RFC3339Nano), deleted, outcome, failure, omitted, omitted, workspace, id, from)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return platformerrors.New(platformerrors.CodeConflict, "artifact lifecycle update did not apply")
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "artifact", string(id), "artifact.lifecycle."+string(to), actorType, actorID)
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
		if reference.State == artifacts.ReferenceActive && state != artifacts.Stored && state != artifacts.Referenced && state != artifacts.Retained {
			return platformerrors.New(platformerrors.CodeConflict, "artifact is not stored and cannot be referenced")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_references (id, workspace_id, artifact_id, owner_type, owner_id, state, created_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, reference.ID, reference.Workspace, reference.ArtifactID, reference.OwnerType, reference.OwnerID, reference.State, reference.CreatedAt.UTC().Format(time.RFC3339Nano), ended); err != nil {
			return mapConstraint(err)
		}
		if reference.State == artifacts.ReferenceActive && state == artifacts.Stored {
			if _, err := tx.ExecContext(ctx, `UPDATE artifacts SET state='referenced', updated_at=? WHERE workspace_id=? AND id=? AND state='stored'`, reference.CreatedAt.UTC().Format(time.RFC3339Nano), reference.Workspace, reference.ArtifactID); err != nil {
				return err
			}
		}
		return s.store.recordMutation(ctx, tx, string(reference.Workspace), "artifact_reference", reference.ID, eventName, actorType, actorID)
	})
}

// Ensure ArtifactService implements artifacts.MetadataStore.
var _ artifacts.MetadataStore = (*ArtifactService)(nil)
