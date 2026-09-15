-- Controlled one-device lab provisioning verification and approval.
-- Canonical devices/endpoints remain in existing tables; this migration only
-- adds durable lab registration staging that does not require a scan candidate.

CREATE TABLE lab_provisioning_checks (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    serial TEXT NOT NULL CHECK (length(trim(serial)) > 0 AND length(serial) <= 128),
    transport_id TEXT NOT NULL CHECK (length(trim(transport_id)) > 0 AND length(transport_id) <= 256),
    connection_type TEXT NOT NULL CHECK (connection_type IN ('usb', 'wireless', 'tcp', 'mock')),
    endpoint_host TEXT NOT NULL DEFAULT '' CHECK (length(endpoint_host) <= 256),
    endpoint_port INTEGER NOT NULL DEFAULT 0 CHECK (endpoint_port >= 0 AND endpoint_port <= 65535),
    notes_json TEXT NOT NULL DEFAULT '[]' CHECK (length(notes_json) <= 8192),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    checked_at TEXT NOT NULL,
    UNIQUE (workspace_id, serial),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE lab_registration_approvals (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    serial TEXT NOT NULL CHECK (length(trim(serial)) > 0 AND length(serial) <= 128),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0 AND length(reason) <= 512),
    decided_at TEXT NOT NULL,
    UNIQUE (workspace_id, serial),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE lab_device_registrations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    serial TEXT NOT NULL CHECK (length(trim(serial)) > 0 AND length(serial) <= 128),
    device_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0 AND length(display_name) <= 128),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    registered_at TEXT NOT NULL,
    UNIQUE (workspace_id, serial),
    -- One-device lab scope for P14: at most one registered lab device per workspace.
    UNIQUE (workspace_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES device_endpoints (workspace_id, id) ON DELETE RESTRICT
);

CREATE INDEX lab_provisioning_checks_workspace_idx ON lab_provisioning_checks (workspace_id, checked_at);
CREATE INDEX lab_registration_approvals_workspace_idx ON lab_registration_approvals (workspace_id, decided_at);
CREATE INDEX lab_device_registrations_device_idx ON lab_device_registrations (workspace_id, device_id);
