-- Drift Next SQLite schema: workspace and principal foundations.
-- This file is immutable once released. It intentionally does not alter the
-- historical PostgreSQL 0001 bootstrap.

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'suspended', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (id)
);

CREATE TABLE principals (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('operator', 'service', 'edge_agent', 'system')),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'suspended', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE operators (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    principal_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    row_version INTEGER NOT NULL DEFAULT 1 CHECK (row_version > 0),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, principal_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, principal_id) REFERENCES principals (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE operator_memberships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    operator_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (length(trim(role)) > 0),
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, operator_id) REFERENCES operators (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);
