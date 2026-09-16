-- Append-only observation/evidence per dispatched device action (ARC-64).
--
-- This table is history, not a projection. `control_action_attempts` remains the
-- current projection of an attempt's state machine; a row here records what
-- happened for one device action, so a later recording can reference the outcome
-- and the resulting observation instead of the intent it was built from. The two
-- are kept separate on purpose (AGENTS.md section 2): nothing in this table is
-- updated, and nothing in the projection is derived from it.
--
-- No row carries typed content or a credential. There is no column for the value
-- behind a typed-text reference and none for its handle: a text action records
-- the addressed field's length, which is a count, and the freshness token and
-- foreground package are opaque, pattern-checked handles. The repository and the
-- dispatch boundary each refuse a value that is not one.
--
-- Append-only is enforced here rather than by convention. The triggers below
-- refuse every UPDATE and DELETE, so a caller that reaches the table without
-- going through the repository cannot rewrite or erase evidence.

CREATE TABLE action_evidence (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    serial TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    action_kind TEXT NOT NULL,
    invocation_surface TEXT NOT NULL CHECK (invocation_surface IN ('manual', 'recorder', 'replay', 'mirror', 'ai_suggestion')),
    disposition TEXT NOT NULL CHECK (disposition IN ('dispatched', 'refused', 'replayed')),
    refusal_reason TEXT NOT NULL DEFAULT '',
    failure_class TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'pending', 'verified', 'failed', 'cancelled', 'timed_out', 'indeterminate')),
    postcondition_state TEXT NOT NULL DEFAULT '' CHECK (postcondition_state IN ('', 'pending', 'passed', 'failed', 'unknown')),
    observation_token TEXT NOT NULL DEFAULT '',
    observation_foreground_package TEXT NOT NULL DEFAULT '',
    observation_field_length INTEGER NOT NULL DEFAULT 0 CHECK (observation_field_length >= 0),
    observation_partial INTEGER NOT NULL DEFAULT 0 CHECK (observation_partial IN (0, 1)),
    observation_failure_class TEXT NOT NULL DEFAULT '',
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    recorded_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(id) <= 128),
    CHECK (length(serial) <= 256),
    CHECK (length(attempt_id) <= 128),
    CHECK (length(action_kind) <= 64),
    CHECK (length(refusal_reason) <= 64),
    CHECK (length(failure_class) <= 64),
    CHECK (length(observation_token) <= 256),
    CHECK (length(observation_foreground_package) <= 255),
    CHECK (length(observation_failure_class) <= 64)
);

-- A recording resolves what happened for one attempt, and separately for one
-- target device. Append order is the insertion order - the repository reads the
-- history by the table's own insertion order and not by `recorded_at`, because
-- two appends can carry the same timestamp and a history whose order ties is a
-- history that is not readable.
CREATE INDEX action_evidence_attempt_idx
    ON action_evidence (workspace_id, attempt_id);

CREATE INDEX action_evidence_device_idx
    ON action_evidence (workspace_id, device_id);

CREATE TRIGGER action_evidence_is_append_only_update
BEFORE UPDATE ON action_evidence
BEGIN
    SELECT RAISE(ABORT, 'action evidence is append-only');
END;

CREATE TRIGGER action_evidence_is_append_only_delete
BEFORE DELETE ON action_evidence
BEGIN
    SELECT RAISE(ABORT, 'action evidence is append-only');
END;
