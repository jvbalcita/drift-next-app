-- Artifact category, lifecycle audit columns, and omission metadata.
-- Bytes remain outside SQLite in the private CAS. Do not edit 0009.

ALTER TABLE artifacts ADD COLUMN category TEXT NOT NULL DEFAULT 'unspecified';
ALTER TABLE artifacts ADD COLUMN updated_at TEXT;
ALTER TABLE artifacts ADD COLUMN deletion_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN failure_classification TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN omission_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS artifacts_workspace_state_idx
    ON artifacts (workspace_id, state);

CREATE INDEX IF NOT EXISTS artifacts_workspace_category_idx
    ON artifacts (workspace_id, category);

CREATE INDEX IF NOT EXISTS artifacts_workspace_cleanup_failed_idx
    ON artifacts (workspace_id, state)
    WHERE state = 'cleanup_failed';
