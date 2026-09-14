-- Phase 12 account projections, scoped-setting versions, and policy UX history.
-- Account and connector data remains metadata-only; no credential column is
-- introduced by this migration.

-- Rebuild the original sync-event table so a disabled connector attempt can
-- be recorded as an auditable no-op without changing the shipped migration.
ALTER TABLE account_sync_events RENAME TO account_sync_events_v15;
CREATE TABLE account_sync_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_source_id TEXT NOT NULL,
    account_id TEXT,
    event_name TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'failed', 'disabled')),
    details_json TEXT NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_source_id) REFERENCES account_sources (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%'),
    CHECK (lower(details_json) NOT LIKE '%secret%')
);
INSERT INTO account_sync_events (id, workspace_id, account_source_id, account_id, event_name, occurred_at, outcome, details_json)
SELECT id, workspace_id, account_source_id, account_id, event_name, occurred_at, outcome, details_json
FROM account_sync_events_v15;
DROP TABLE account_sync_events_v15;

CREATE UNIQUE INDEX account_sync_events_idempotency_idx
    ON account_sync_events (workspace_id, idempotency_key)
    WHERE idempotency_key <> '';

ALTER TABLE account_sources ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(metadata_json) <= 32768)
    CHECK (lower(metadata_json) NOT LIKE '%password%')
    CHECK (lower(metadata_json) NOT LIKE '%token%')
    CHECK (lower(metadata_json) NOT LIKE '%secret%');
ALTER TABLE account_sources ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);

ALTER TABLE accounts ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(metadata_json) <= 32768)
    CHECK (lower(metadata_json) NOT LIKE '%password%')
    CHECK (lower(metadata_json) NOT LIKE '%token%')
    CHECK (lower(metadata_json) NOT LIKE '%secret%');

ALTER TABLE account_service_states ADD COLUMN stage TEXT NOT NULL DEFAULT 'unknown'
    CHECK (stage IN ('unknown', 'queued', 'ready', 'running', 'blocked', 'completed'));
ALTER TABLE account_service_states ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);

ALTER TABLE account_runs ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);
ALTER TABLE account_runs ADD COLUMN started_at TEXT;
ALTER TABLE account_runs ADD COLUMN correlation_id TEXT NOT NULL DEFAULT ''
    CHECK (length(correlation_id) <= 256);

ALTER TABLE account_device_assignments ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);

ALTER TABLE settings ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);

ALTER TABLE policies ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE policies ADD COLUMN row_version INTEGER NOT NULL DEFAULT 1
    CHECK (row_version > 0);

UPDATE policies SET updated_at = created_at WHERE updated_at = '';

CREATE TABLE account_service_state_history (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    service_name TEXT NOT NULL CHECK (length(trim(service_name)) > 0),
    stage TEXT NOT NULL CHECK (stage IN ('unknown', 'queued', 'ready', 'running', 'blocked', 'completed')),
    state TEXT NOT NULL CHECK (state IN ('unknown', 'healthy', 'degraded', 'failed', 'disabled')),
    observed_at TEXT NOT NULL,
    failure_class TEXT,
    details_json TEXT NOT NULL DEFAULT '{}',
    row_version INTEGER NOT NULL CHECK (row_version > 0),
    recorded_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%'),
    CHECK (lower(details_json) NOT LIKE '%secret%')
);

CREATE INDEX account_service_state_history_lookup_idx
    ON account_service_state_history (workspace_id, account_id, service_name, observed_at DESC, id);

CREATE TABLE account_run_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    account_run_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'running', 'completed', 'failed', 'cancelled')),
    failure_class TEXT,
    correlation_id TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) > 0),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, account_run_id) REFERENCES account_runs (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(correlation_id) <= 256)
);

CREATE INDEX account_run_events_lookup_idx
    ON account_run_events (workspace_id, account_run_id, occurred_at, id);

CREATE TABLE setting_history (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    setting_id TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'control_plane', 'edge_host', 'device', 'automation_agent', 'operator_preference')),
    target_id TEXT NOT NULL DEFAULT '',
    setting_key TEXT NOT NULL CHECK (length(trim(setting_key)) > 0),
    value_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('draft', 'active', 'superseded', 'retired')),
    row_version INTEGER NOT NULL CHECK (row_version > 0),
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) > 0),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    changed_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, setting_id) REFERENCES settings (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(value_json) <= 65536),
    CHECK (lower(value_json) NOT LIKE '%password%'),
    CHECK (lower(value_json) NOT LIKE '%token%'),
    CHECK (lower(value_json) NOT LIKE '%api_key%')
);

CREATE INDEX setting_history_lookup_idx
    ON setting_history (workspace_id, setting_id, changed_at DESC, id);
