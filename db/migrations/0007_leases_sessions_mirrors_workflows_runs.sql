-- Supervised control, per-device fencing, mirror targets, workflows, and runs.

CREATE TABLE control_sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    holder_id TEXT NOT NULL CHECK (length(trim(holder_id)) > 0),
    state TEXT NOT NULL CHECK (state IN ('requested', 'active', 'closing', 'closed', 'expired', 'revoked')),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    closed_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE device_leases (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    holder_id TEXT NOT NULL CHECK (length(trim(holder_id)) > 0),
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    state TEXT NOT NULL CHECK (state IN ('requested', 'active', 'released', 'expired', 'revoked')),
    acquired_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    released_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, device_id, fencing_token),
    UNIQUE (workspace_id, id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, session_id) REFERENCES control_sessions (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND released_at IS NULL) OR (state IN ('requested', 'released', 'expired', 'revoked')))
);

CREATE UNIQUE INDEX device_leases_one_active_idx
    ON device_leases (workspace_id, device_id)
    WHERE state = 'active';

CREATE TABLE lease_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    lease_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'active', 'released', 'expired', 'revoked')),
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    actor_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id) REFERENCES device_leases (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE mirror_sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    control_session_id TEXT NOT NULL,
    source_device_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'active', 'paused', 'stopping', 'completed', 'failed', 'cancelled')),
    failure_policy TEXT NOT NULL CHECK (failure_policy IN ('continue', 'pause', 'stop_all')),
    created_at TEXT NOT NULL,
    finished_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, control_session_id) REFERENCES control_sessions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE mirror_targets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    mirror_session_id TEXT NOT NULL,
    follower_device_id TEXT NOT NULL,
    lease_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('pending', 'leased', 'queued', 'running', 'succeeded', 'failed', 'cancelled', 'cleanup_failed')),
    failure_class TEXT,
    created_at TEXT NOT NULL,
    finished_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, mirror_session_id, follower_device_id),
    UNIQUE (workspace_id, id, mirror_session_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_session_id) REFERENCES mirror_sessions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, follower_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id) REFERENCES device_leases (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id, follower_device_id) REFERENCES device_leases (workspace_id, id, device_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX mirror_targets_one_target_per_lease_idx
    ON mirror_targets (workspace_id, lease_id)
    WHERE lease_id IS NOT NULL;

CREATE TABLE mirror_action_batches (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    mirror_session_id TEXT NOT NULL,
    source_action_id TEXT,
    action_kind TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_session_id) REFERENCES mirror_sessions (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE mirror_action_attempts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    mirror_target_id TEXT NOT NULL,
    action_kind TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('authorized', 'dispatched', 'acknowledged', 'verified', 'failed', 'timed_out', 'cancelled')),
    idempotency_key TEXT NOT NULL,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    failure_class TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, batch_id) REFERENCES mirror_action_batches (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_target_id) REFERENCES mirror_targets (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE workflows (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE workflow_versions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    definition_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, workflow_id, version),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflows (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(definition_json) <= 262144),
    CHECK (lower(definition_json) NOT LIKE '%password%'),
    CHECK (lower(definition_json) NOT LIKE '%token%')
);

CREATE UNIQUE INDEX workflow_versions_one_published_idx
    ON workflow_versions (workspace_id, workflow_id)
    WHERE state = 'published';

CREATE TABLE workflow_steps (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    workflow_version_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    action_kind TEXT NOT NULL CHECK (action_kind IN ('observe', 'health_check', 'capture', 'tap', 'double_tap', 'long_press', 'text_input', 'text_delete', 'clear', 'swipe', 'scroll', 'drag', 'back', 'home', 'enter', 'key_event', 'ui_change', 'state_change')),
    risk_class TEXT NOT NULL CHECK (risk_class IN ('low', 'medium', 'high', 'irreversible')),
    retry_class TEXT NOT NULL CHECK (retry_class IN ('safe', 'after_observation', 'never_blind')),
    definition_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, workflow_version_id, sequence),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, workflow_version_id) REFERENCES workflow_versions (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(definition_json) <= 65536),
    CHECK (lower(definition_json) NOT LIKE '%password%'),
    CHECK (lower(definition_json) NOT LIKE '%token%')
);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    workflow_version_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'validating', 'queued', 'running', 'completing', 'completed', 'failed', 'cancelled')),
    concurrency_limit INTEGER NOT NULL CHECK (concurrency_limit > 0),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    failure_class TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, workflow_version_id) REFERENCES workflow_versions (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE target_set_snapshots (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    selector_type TEXT NOT NULL CHECK (selector_type IN ('explicit_devices', 'group', 'automation_agent', 'capability')),
    selector_json TEXT NOT NULL,
    resolved_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, run_id),
    UNIQUE (workspace_id, id, run_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_id) REFERENCES runs (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(selector_json) <= 65536),
    CHECK (lower(selector_json) NOT LIKE '%password%'),
    CHECK (lower(selector_json) NOT LIKE '%token%')
);

CREATE TABLE run_targets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    target_snapshot_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'leased', 'queued', 'running', 'verifying', 'succeeded', 'failed', 'cancelled', 'cleanup_failed')),
    failure_class TEXT,
    lease_id TEXT,
    current_observation_id TEXT,
    created_at TEXT NOT NULL,
    finished_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, run_id, device_id),
    UNIQUE (workspace_id, id, run_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_id) REFERENCES runs (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, target_snapshot_id) REFERENCES target_set_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, target_snapshot_id, run_id) REFERENCES target_set_snapshots (workspace_id, id, run_id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id) REFERENCES device_leases (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id, device_id) REFERENCES device_leases (workspace_id, id, device_id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, current_observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, current_observation_id, device_id) REFERENCES observation_snapshots (workspace_id, id, device_id) ON DELETE RESTRICT
);

CREATE TABLE target_run_steps (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_target_id TEXT NOT NULL,
    workflow_step_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'verifying', 'succeeded', 'failed', 'cancelled', 'cleanup_failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, run_target_id, workflow_step_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id) REFERENCES run_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, workflow_step_id) REFERENCES workflow_steps (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE action_attempts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_target_id TEXT NOT NULL,
    target_run_step_id TEXT,
    action_kind TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('authorized', 'dispatched', 'acknowledged', 'verified', 'failed', 'timed_out', 'cancelled')),
    idempotency_key TEXT NOT NULL,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    postcondition_state TEXT NOT NULL CHECK (postcondition_state IN ('pending', 'passed', 'failed', 'unknown')),
    failure_class TEXT,
    created_at TEXT NOT NULL,
    finished_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id) REFERENCES run_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, target_run_step_id) REFERENCES target_run_steps (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE run_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    run_target_id TEXT,
    event_name TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_id) REFERENCES runs (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id) REFERENCES run_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id, run_id) REFERENCES run_targets (workspace_id, id, run_id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 65536),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);

CREATE TABLE mirror_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    mirror_session_id TEXT NOT NULL,
    mirror_target_id TEXT,
    event_name TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_session_id) REFERENCES mirror_sessions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_target_id) REFERENCES mirror_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_target_id, mirror_session_id) REFERENCES mirror_targets (workspace_id, id, mirror_session_id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 65536),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);
