-- Operator expectation and deletion records are separate from device lifecycle.
-- `devices` remains the durable identity/evidence anchor. Membership in
-- device_registry_entries is the live registry projection and may be removed.
CREATE TABLE device_registry_entries (
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, device_id),
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT
);

INSERT INTO device_registry_entries (workspace_id, device_id, created_at)
SELECT workspace_id, id, created_at FROM devices;

CREATE TABLE device_retirement_decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('retired', 'restored')),
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) > 0),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    decided_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT
);

CREATE INDEX device_retirement_decisions_device_idx
    ON device_retirement_decisions (workspace_id, device_id, decided_at DESC);

CREATE TABLE device_deletions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    hardware_serial TEXT,
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) > 0),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    confirmation_device_id TEXT NOT NULL,
    deleted_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (confirmation_device_id = device_id)
);

CREATE INDEX device_deletions_device_idx
    ON device_deletions (workspace_id, device_id, deleted_at DESC);

CREATE TABLE device_deletion_returns (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    deletion_id TEXT NOT NULL,
    deleted_device_id TEXT NOT NULL,
    returned_device_id TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    UNIQUE (workspace_id, returned_device_id),
    FOREIGN KEY (workspace_id, deletion_id) REFERENCES device_deletions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, deleted_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, returned_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT
);
