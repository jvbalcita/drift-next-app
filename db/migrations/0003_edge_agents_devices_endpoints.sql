-- Edge/runtime, stable device identity, endpoint history, and bindings.

CREATE TABLE edge_agents (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'unhealthy', 'offline', 'retired')),
    last_seen_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE edge_agent_capabilities (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    edge_agent_id TEXT NOT NULL,
    capability TEXT NOT NULL CHECK (length(trim(capability)) > 0),
    version TEXT NOT NULL CHECK (length(trim(version)) > 0),
    observed_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, edge_agent_id, capability),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, edge_agent_id) REFERENCES edge_agents (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE edge_agent_heartbeats (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    edge_agent_id TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'unhealthy', 'offline')),
    version TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, edge_agent_id) REFERENCES edge_agents (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 32768)
);

CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    platform_version TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('registered', 'active', 'unavailable', 'retired')),
    last_seen_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE INDEX devices_workspace_state_idx ON devices (workspace_id, state);

CREATE TABLE device_endpoints (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    endpoint_type TEXT NOT NULL CHECK (endpoint_type IN ('mock', 'adb_usb', 'adb_tcp')),
    serial TEXT,
    host TEXT,
    port INTEGER CHECK (port BETWEEN 0 AND 65535),
    state TEXT NOT NULL CHECK (state IN ('observed', 'current', 'superseded', 'retired')),
    observed_at TEXT NOT NULL,
    superseded_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, id, device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (serial IS NOT NULL OR host IS NOT NULL OR endpoint_type = 'mock'),
    CHECK ((state IN ('superseded', 'retired') AND superseded_at IS NOT NULL) OR (state IN ('observed', 'current') AND superseded_at IS NULL))
);

CREATE UNIQUE INDEX device_endpoints_one_current_idx
    ON device_endpoints (workspace_id, device_id)
    WHERE state = 'current';

CREATE TABLE device_bindings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    edge_agent_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    bound_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, edge_agent_id) REFERENCES edge_agents (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);

CREATE UNIQUE INDEX device_bindings_one_active_idx
    ON device_bindings (workspace_id, device_id)
    WHERE state = 'active';
