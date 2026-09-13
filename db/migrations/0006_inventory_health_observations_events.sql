-- Current projections and append-only device observation/history records.

CREATE TABLE observation_snapshots (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    capture_correlation_id TEXT NOT NULL,
    coordinate_space TEXT NOT NULL,
    package_name TEXT,
    activity_name TEXT,
    orientation TEXT,
    display_width INTEGER CHECK (display_width >= 0),
    display_height INTEGER CHECK (display_height >= 0),
    ui_tree_hash TEXT,
    screenshot_hash TEXT,
    source TEXT NOT NULL CHECK (source IN ('fake', 'device', 'mirror', 'unknown')),
    protocol_version TEXT NOT NULL,
    capture_status TEXT NOT NULL CHECK (capture_status IN ('complete', 'partial', 'failed')),
    error_class TEXT,
    state TEXT NOT NULL CHECK (state IN ('recorded', 'superseded', 'retained')),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(coordinate_space) <= 128),
    CHECK ((capture_status = 'complete' AND error_class IS NULL) OR capture_status <> 'complete')
);

CREATE INDEX observation_snapshots_device_captured_idx
    ON observation_snapshots (workspace_id, device_id, captured_at DESC);

CREATE TABLE device_inventory (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    source_observation_id TEXT,
    inventory_json TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_observation_id, device_id) REFERENCES observation_snapshots (workspace_id, id, device_id) ON DELETE RESTRICT,
    CHECK (length(inventory_json) <= 65536),
    CHECK (lower(inventory_json) NOT LIKE '%password%'),
    CHECK (lower(inventory_json) NOT LIKE '%token%')
);

CREATE TABLE inventory_snapshots (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    source_observation_id TEXT,
    inventory_json TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_observation_id, device_id) REFERENCES observation_snapshots (workspace_id, id, device_id) ON DELETE RESTRICT,
    CHECK (length(inventory_json) <= 65536),
    CHECK (lower(inventory_json) NOT LIKE '%password%'),
    CHECK (lower(inventory_json) NOT LIKE '%token%')
);

CREATE TABLE device_capabilities (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    capability TEXT NOT NULL CHECK (length(trim(capability)) > 0),
    version TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('available', 'unavailable')),
    observed_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, device_id, capability),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE health_samples (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    observation_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('healthy', 'degraded', 'unhealthy', 'unknown')),
    battery_percent INTEGER CHECK (battery_percent BETWEEN 0 AND 100),
    sampled_at TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, observation_id, device_id) REFERENCES observation_snapshots (workspace_id, id, device_id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%')
);

CREATE TABLE device_health_current (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    source_sample_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('healthy', 'degraded', 'unhealthy', 'unknown')),
    sampled_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_sample_id) REFERENCES health_samples (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_sample_id, device_id) REFERENCES health_samples (workspace_id, id, device_id) ON DELETE RESTRICT
);

CREATE TABLE device_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    event_name TEXT NOT NULL CHECK (length(trim(event_name)) > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 65536),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);
