-- Non-secret account references and bounded service/run history.

CREATE TABLE account_sources (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'disabled', 'retired')),
    external_reference TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, provider, display_name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    external_ref TEXT NOT NULL CHECK (length(trim(external_ref)) > 0),
    label TEXT NOT NULL CHECK (length(trim(label)) > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'active', 'inactive', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, source_id, external_ref),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_id) REFERENCES account_sources (workspace_id, id) ON DELETE RESTRICT,
    CHECK (lower(external_ref || label) NOT LIKE '%password%'),
    CHECK (lower(external_ref || label) NOT LIKE '%token%'),
    CHECK (lower(external_ref || label) NOT LIKE '%secret%')
);

CREATE TABLE account_service_states (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    service_name TEXT NOT NULL CHECK (length(trim(service_name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('unknown', 'healthy', 'degraded', 'failed', 'disabled')),
    observed_at TEXT NOT NULL,
    failure_class TEXT,
    details_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, account_id, service_name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%')
);

CREATE TABLE account_runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'running', 'completed', 'failed', 'cancelled')),
    requested_at TEXT NOT NULL,
    finished_at TEXT,
    failure_class TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state IN ('completed', 'failed', 'cancelled') AND finished_at IS NOT NULL) OR state IN ('requested', 'running'))
);

CREATE TABLE account_device_assignments (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    assigned_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);

CREATE UNIQUE INDEX account_device_assignments_one_active_idx
    ON account_device_assignments (workspace_id, account_id, device_id)
    WHERE state = 'active';

CREATE TABLE account_sync_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_source_id TEXT NOT NULL,
    account_id TEXT,
    event_name TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'failed')),
    details_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_source_id) REFERENCES account_sources (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%')
);
