-- Simplify discovery: a saved network profile is scan policy, a scan run is
-- history, and the pending-candidate / registration / provisioning lifecycle is
-- removed outright.
--
-- SQLite evaluates an ON DELETE RESTRICT constraint immediately and cannot defer
-- it, and `PRAGMA foreign_keys` is a no-op inside the transaction the migration
-- runner already holds, so foreign keys are enforced while this file runs.
--
-- The removed lifecycle tables are dropped first, children before parents:
-- approval_decisions and registration_events both reference scan_candidates,
-- and scan_candidates references scan_runs. Dropping scan_candidates while those
-- rows exist performs an implicit DELETE against scan_runs and fails on its
-- RESTRICT key, which is exactly what blocked the scan_runs rebuild below for
-- any database that had ever produced a candidate. Dropping the doomed tables
-- removes the blocker.
--
-- scan_runs is then rebuilt without its composite network_profiles reference,
-- keeping network_profile_id as a nullable historical column: scan history is
-- immutable evidence that must neither block profile deletion nor disappear
-- with the profile it names. Its idempotency_key column and partial unique
-- index are preserved. network_profiles is rebuilt to the saved-policy shape
-- (delete-only: no state, no row_version, one default per workspace).

DROP TABLE IF EXISTS registration_events;
DROP TABLE IF EXISTS approval_decisions;
DROP TABLE IF EXISTS scan_candidates;
DROP TABLE IF EXISTS lab_device_registrations;
DROP TABLE IF EXISTS lab_provisioning_checks;
DROP TABLE IF EXISTS lab_registration_approvals;

CREATE TABLE scan_runs_next (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    network_profile_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('requested', 'running', 'completed', 'failed', 'cancelled')),
    requested_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    failure_class TEXT,
    idempotency_key TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK ((state IN ('completed', 'failed', 'cancelled') AND finished_at IS NOT NULL) OR state IN ('requested', 'running'))
);

INSERT INTO scan_runs_next (id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, failure_class, idempotency_key)
SELECT id, workspace_id, network_profile_id, state, requested_at, started_at, finished_at, failure_class, idempotency_key
FROM scan_runs;

DROP TABLE scan_runs;
ALTER TABLE scan_runs_next RENAME TO scan_runs;

CREATE UNIQUE INDEX scan_runs_one_key_idx
    ON scan_runs (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE network_profiles_next (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT NOT NULL,
    name           TEXT NOT NULL CHECK (length(trim(name)) > 0 AND length(name) <= 128),
    address_policy TEXT NOT NULL CHECK (length(trim(address_policy)) > 0),
    ports_json     TEXT NOT NULL CHECK (length(ports_json) <= 8192),
    is_default     INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0,1)),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

INSERT INTO network_profiles_next (id, workspace_id, name, address_policy, ports_json, is_default, created_at, updated_at)
SELECT id, workspace_id, name, address_policy, ports_json, is_default, created_at, updated_at
FROM network_profiles;

DROP TABLE network_profiles;
ALTER TABLE network_profiles_next RENAME TO network_profiles;
CREATE UNIQUE INDEX network_profiles_one_default_idx
    ON network_profiles (workspace_id) WHERE is_default = 1;
