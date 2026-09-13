-- Historical device grouping and logical automation-agent assignments.

CREATE TABLE device_groups (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE device_group_memberships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, group_id) REFERENCES device_groups (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);

CREATE UNIQUE INDEX device_group_memberships_one_active_group_idx
    ON device_group_memberships (workspace_id, device_id)
    WHERE state = 'active';

CREATE UNIQUE INDEX device_group_memberships_active_order_idx
    ON device_group_memberships (workspace_id, group_id, position)
    WHERE state = 'active';

CREATE TABLE automation_agents (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'suspended', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE automation_agent_profiles (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    automation_agent_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    personality_json TEXT NOT NULL DEFAULT '{}',
    goals_json TEXT NOT NULL DEFAULT '[]',
    rules_json TEXT NOT NULL DEFAULT '[]',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    memory_scope TEXT NOT NULL CHECK (memory_scope IN ('none', 'workspace', 'agent')),
    memory_retention_class TEXT NOT NULL CHECK (length(trim(memory_retention_class)) > 0),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, automation_agent_id, version),
    UNIQUE (workspace_id, id, automation_agent_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, automation_agent_id) REFERENCES automation_agents (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(personality_json) <= 65536),
    CHECK (length(goals_json) <= 65536),
    CHECK (length(rules_json) <= 65536),
    CHECK (length(capabilities_json) <= 65536),
    CHECK (lower(personality_json || goals_json || rules_json || capabilities_json) NOT LIKE '%password%'),
    CHECK (lower(personality_json || goals_json || rules_json || capabilities_json) NOT LIKE '%token%'),
    CHECK (lower(personality_json || goals_json || rules_json || capabilities_json) NOT LIKE '%api_key%')
);

CREATE UNIQUE INDEX automation_agent_profiles_one_published_idx
    ON automation_agent_profiles (workspace_id, automation_agent_id)
    WHERE state = 'published';

CREATE TABLE automation_agent_device_assignments (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    automation_agent_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    precedence INTEGER NOT NULL DEFAULT 0,
    assigned_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, automation_agent_id) REFERENCES automation_agents (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, profile_id) REFERENCES automation_agent_profiles (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, profile_id, automation_agent_id) REFERENCES automation_agent_profiles (workspace_id, id, automation_agent_id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);

CREATE UNIQUE INDEX automation_agent_assignments_one_active_device_idx
    ON automation_agent_device_assignments (workspace_id, device_id)
    WHERE state = 'active';
