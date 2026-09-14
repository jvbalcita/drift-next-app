-- Durable emergency-stop gate and supervised manual action attempts.

CREATE TABLE control_halts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('clear', 'emergency_stop')),
    reason TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(reason) <= 1024)
);

CREATE TABLE control_action_attempts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    lease_id TEXT NOT NULL,
    action_kind TEXT NOT NULL,
    invocation_surface TEXT NOT NULL CHECK (invocation_surface IN ('manual', 'recorder', 'replay', 'mirror', 'ai_suggestion')),
    state TEXT NOT NULL CHECK (state IN ('authorized', 'dispatched', 'acknowledged', 'verified', 'failed', 'timed_out', 'cancelled', 'indeterminate')),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    observation_token TEXT,
    postcondition_state TEXT NOT NULL CHECK (postcondition_state IN ('pending', 'passed', 'failed', 'unknown')),
    cleanup_state TEXT NOT NULL DEFAULT 'pending' CHECK (cleanup_state IN ('pending', 'succeeded', 'failed')),
    failure_class TEXT,
    created_at TEXT NOT NULL,
    dispatched_at TEXT,
    finished_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id) REFERENCES device_leases (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, lease_id, device_id) REFERENCES device_leases (workspace_id, id, device_id) ON DELETE RESTRICT,
    CHECK (length(request_hash) <= 128),
    CHECK (observation_token IS NULL OR length(observation_token) <= 256)
);

CREATE INDEX control_action_attempts_device_created_idx
    ON control_action_attempts (workspace_id, device_id, created_at DESC);
