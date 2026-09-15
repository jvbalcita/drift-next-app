-- Simplify saved network profiles. Discovery and registration tables remain until later phases.
PRAGMA foreign_keys = OFF;

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

PRAGMA foreign_keys = ON;
