-- Allow multiple registered lab devices per workspace. Capacity is owned by
-- registration.Service MaxRegisteredDevices (0 = unlimited). Unique serial
-- identity remains enforced.

CREATE TABLE lab_device_registrations_next (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    serial TEXT NOT NULL CHECK (length(trim(serial)) > 0 AND length(serial) <= 128),
    device_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0 AND length(display_name) <= 128),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) > 0),
    registered_at TEXT NOT NULL,
    UNIQUE (workspace_id, serial),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES device_endpoints (workspace_id, id) ON DELETE RESTRICT
);

INSERT INTO lab_device_registrations_next (
    id, workspace_id, serial, device_id, endpoint_id, display_name, actor_id, registered_at
)
SELECT id, workspace_id, serial, device_id, endpoint_id, display_name, actor_id, registered_at
FROM lab_device_registrations;

DROP TABLE lab_device_registrations;
ALTER TABLE lab_device_registrations_next RENAME TO lab_device_registrations;

CREATE INDEX lab_device_registrations_device_idx ON lab_device_registrations (workspace_id, device_id);
