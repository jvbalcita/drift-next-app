-- Typed settings/policies, private artifact metadata, recording/skills, audit,
-- packages, and local outbox. Artifact bytes are intentionally not in SQLite.

CREATE TABLE settings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'control_plane', 'edge_host', 'device', 'automation_agent', 'operator_preference')),
    target_id TEXT NOT NULL DEFAULT '',
    setting_key TEXT NOT NULL CHECK (length(trim(setting_key)) > 0),
    value_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('draft', 'active', 'superseded', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(value_json) <= 65536),
    CHECK (lower(value_json) NOT LIKE '%password%'),
    CHECK (lower(value_json) NOT LIKE '%token%'),
    CHECK (lower(value_json) NOT LIKE '%api_key%'),
    CHECK ((scope = 'workspace' AND target_id = '') OR scope <> 'workspace')
);

CREATE UNIQUE INDEX settings_one_active_key_idx
    ON settings (workspace_id, scope, target_id, setting_key)
    WHERE state = 'active';

CREATE TABLE policies (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    version INTEGER NOT NULL CHECK (version > 0),
    rule_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('draft', 'active', 'superseded', 'retired')),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name, version),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(rule_json) <= 131072),
    CHECK (lower(rule_json) NOT LIKE '%password%'),
    CHECK (lower(rule_json) NOT LIKE '%token%')
);

CREATE UNIQUE INDEX policies_one_active_name_idx
    ON policies (workspace_id, name)
    WHERE state = 'active';

CREATE TABLE policy_decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    action TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('allow', 'deny', 'inconclusive')),
    reason_code TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    decided_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, policy_id) REFERENCES policies (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (length(trim(content_hash)) > 0),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    media_type TEXT NOT NULL CHECK (length(trim(media_type)) > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    retention_class TEXT NOT NULL CHECK (retention_class IN ('current', 'operational_history', 'execution_evidence', 'audit_security', 'disposable')),
    state TEXT NOT NULL CHECK (state IN ('pending', 'stored', 'referenced', 'retained', 'eligible_for_deletion', 'deleted', 'cleanup_failed')),
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, content_hash),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE artifact_references (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    owner_type TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
    created_at TEXT NOT NULL,
    ended_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, artifact_id) REFERENCES artifacts (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state = 'active' AND ended_at IS NULL) OR (state = 'ended' AND ended_at IS NOT NULL))
);

CREATE TABLE recording_sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    device_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('requested', 'recording', 'stopping', 'completed', 'discarded', 'failed')),
    source TEXT NOT NULL CHECK (source IN ('fake', 'device', 'mirror', 'unknown')),
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK ((state IN ('recording', 'stopping', 'completed', 'discarded', 'failed') AND started_at IS NOT NULL) OR state = 'requested')
);

CREATE UNIQUE INDEX recording_sessions_one_active_idx
    ON recording_sessions (workspace_id)
    WHERE state IN ('requested', 'recording', 'stopping');

CREATE TABLE recording_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    recording_session_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    logical_action TEXT NOT NULL CHECK (logical_action IN ('tap', 'double_tap', 'long_press', 'text_input', 'text_delete', 'clear', 'swipe', 'scroll', 'drag', 'back', 'home', 'enter', 'key_event', 'ui_change', 'state_change')),
    before_observation_id TEXT,
    after_observation_id TEXT,
    raw_screenshot_hash TEXT,
    annotated_screenshot_hash TEXT,
    ui_tree_hash TEXT,
    redaction_state TEXT NOT NULL CHECK (redaction_state IN ('not_required', 'required', 'redacted', 'rejected')),
    sensitive INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
    duration_ms INTEGER CHECK (duration_ms >= 0),
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, recording_session_id, sequence),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, recording_session_id) REFERENCES recording_sessions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, before_observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, after_observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(details_json) <= 65536),
    CHECK (lower(details_json) NOT LIKE '%password%'),
    CHECK (lower(details_json) NOT LIKE '%token%'),
    CHECK ((sensitive = 0 AND redaction_state <> 'required') OR (sensitive = 1 AND redaction_state IN ('redacted', 'rejected')))
);

CREATE TABLE skills (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE skill_versions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    definition_json TEXT NOT NULL,
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    trust_state TEXT NOT NULL CHECK (trust_state IN ('unreviewed', 'reviewed', 'approved', 'revoked')),
    source_recording_session_id TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, skill_id, version),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, skill_id) REFERENCES skills (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_recording_session_id) REFERENCES recording_sessions (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(definition_json) <= 262144),
    CHECK (length(capabilities_json) <= 65536),
    CHECK (lower(definition_json || capabilities_json) NOT LIKE '%password%'),
    CHECK (lower(definition_json || capabilities_json) NOT LIKE '%token%')
);

CREATE UNIQUE INDEX skill_versions_one_published_idx
    ON skill_versions (workspace_id, skill_id)
    WHERE state = 'published';

CREATE TABLE skill_promotions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    skill_version_id TEXT NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, skill_version_id) REFERENCES skill_versions (workspace_id, id) ON DELETE RESTRICT
);

CREATE TABLE shared_brain_knowledge (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    knowledge_key TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    state TEXT NOT NULL CHECK (state IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    knowledge_json TEXT NOT NULL,
    source_skill_version_id TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, knowledge_key, version),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, source_skill_version_id) REFERENCES skill_versions (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(knowledge_json) <= 131072),
    CHECK (lower(knowledge_json) NOT LIKE '%password%'),
    CHECK (lower(knowledge_json) NOT LIKE '%token%')
);

CREATE TABLE package_manifests (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    package_name TEXT NOT NULL CHECK (length(trim(package_name)) > 0),
    package_version TEXT NOT NULL CHECK (length(trim(package_version)) > 0),
    trust_state TEXT NOT NULL CHECK (trust_state IN ('unreviewed', 'reviewed', 'approved', 'revoked')),
    manifest_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, package_name, package_version),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(manifest_json) <= 131072),
    CHECK (lower(manifest_json) NOT LIKE '%password%'),
    CHECK (lower(manifest_json) NOT LIKE '%token%')
);

CREATE TABLE operational_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    event_name TEXT NOT NULL CHECK (length(trim(event_name)) > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    idempotency_key TEXT,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 131072),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    event_name TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 131072),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);

CREATE TABLE retention_decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    retention_class TEXT NOT NULL CHECK (retention_class IN ('current', 'operational_history', 'execution_evidence', 'audit_security', 'disposable')),
    decision TEXT NOT NULL CHECK (decision IN ('retain', 'eligible_for_deletion', 'deleted', 'blocked')),
    reason TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE idempotency_keys (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    key TEXT NOT NULL CHECK (length(trim(key)) > 0),
    operation_type TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('reserved', 'succeeded', 'failed', 'indeterminate')),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, key),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);

CREATE TABLE outbox_messages (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    event_name TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    payload_json TEXT NOT NULL DEFAULT '{}',
    state TEXT NOT NULL CHECK (state IN ('recorded', 'delivered', 'retryable', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_class TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 131072),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);
