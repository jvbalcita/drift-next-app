-- Bounded discovery policy and non-authoritative scan evidence.

CREATE TABLE network_profiles (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    address_policy TEXT NOT NULL CHECK (length(trim(address_policy)) > 0),
    ports_json TEXT NOT NULL CHECK (length(ports_json) <= 8192),
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    state TEXT NOT NULL CHECK (state IN ('draft', 'active', 'disabled', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (state = 'active' OR is_default = 0)
);

CREATE UNIQUE INDEX network_profiles_one_default_idx
    ON network_profiles (workspace_id)
    WHERE is_default = 1 AND state = 'active';

CREATE TABLE scan_runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    network_profile_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('requested', 'running', 'completed', 'failed', 'cancelled')),
    requested_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    failure_class TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, network_profile_id) REFERENCES network_profiles (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state IN ('completed', 'failed', 'cancelled') AND finished_at IS NOT NULL) OR state IN ('requested', 'running'))
);

CREATE TABLE scan_candidates (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    scan_run_id TEXT NOT NULL,
    candidate_key TEXT NOT NULL CHECK (length(trim(candidate_key)) > 0),
    host TEXT,
    port INTEGER CHECK (port BETWEEN 0 AND 65535),
    serial TEXT,
    fingerprint TEXT,
    state TEXT NOT NULL CHECK (state IN ('discovered', 'pending_approval', 'approved', 'rejected', 'expired', 'registered')),
    discovered_at TEXT NOT NULL,
    expires_at TEXT,
    evidence_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, scan_run_id, candidate_key),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, scan_run_id) REFERENCES scan_runs (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(evidence_json) <= 32768)
);

CREATE TABLE approval_decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    candidate_id TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected', 'expired')),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    decided_at TEXT NOT NULL,
    reason TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, candidate_id) REFERENCES scan_candidates (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE registration_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    candidate_id TEXT NOT NULL,
    device_id TEXT,
    endpoint_id TEXT,
    outcome TEXT NOT NULL CHECK (outcome IN ('registered', 'rejected', 'failed')),
    actor_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, candidate_id) REFERENCES scan_candidates (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES device_endpoints (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, endpoint_id, device_id) REFERENCES device_endpoints (workspace_id, id, device_id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768)
);
