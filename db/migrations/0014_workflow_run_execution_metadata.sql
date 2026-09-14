-- Phase 10 workflow/run execution metadata. The original state columns remain
-- compatible with the shipped schema; explicit projections make pause and
-- indeterminate completion durable without rewriting existing tables.

ALTER TABLE observation_snapshots ADD COLUMN app_version TEXT NOT NULL DEFAULT ''
    CHECK (length(app_version) <= 256);

ALTER TABLE runs ADD COLUMN approval_state TEXT NOT NULL DEFAULT 'approved'
    CHECK (approval_state IN ('pending', 'approved', 'rejected'));
ALTER TABLE runs ADD COLUMN paused_at TEXT;
ALTER TABLE runs ADD COLUMN pause_reason TEXT NOT NULL DEFAULT ''
    CHECK (length(pause_reason) <= 1024);
ALTER TABLE runs ADD COLUMN retry_budget INTEGER NOT NULL DEFAULT 0
    CHECK (retry_budget >= 0);
ALTER TABLE runs ADD COLUMN recovery_count INTEGER NOT NULL DEFAULT 0
    CHECK (recovery_count >= 0);

ALTER TABLE action_attempts ADD COLUMN sequence INTEGER NOT NULL DEFAULT 0
    CHECK (sequence >= 0);
ALTER TABLE action_attempts ADD COLUMN invocation_surface TEXT NOT NULL DEFAULT 'replay'
    CHECK (invocation_surface IN ('manual', 'recorder', 'replay', 'mirror', 'ai_suggestion'));
ALTER TABLE action_attempts ADD COLUMN request_hash TEXT NOT NULL DEFAULT ''
    CHECK (length(request_hash) <= 128);
ALTER TABLE action_attempts ADD COLUMN observation_token TEXT
    CHECK (observation_token IS NULL OR length(observation_token) <= 256);
ALTER TABLE action_attempts ADD COLUMN evidence_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(evidence_json) <= 65536)
    CHECK (lower(evidence_json) NOT LIKE '%password%')
    CHECK (lower(evidence_json) NOT LIKE '%token%');
ALTER TABLE action_attempts ADD COLUMN outcome_state TEXT NOT NULL DEFAULT 'pending'
    CHECK (outcome_state IN ('pending', 'verified', 'failed', 'cancelled', 'timed_out', 'indeterminate'));
ALTER TABLE action_attempts ADD COLUMN target_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(target_json) <= 4096)
    CHECK (lower(target_json) NOT LIKE '%password%')
    CHECK (lower(target_json) NOT LIKE '%token%');
ALTER TABLE action_attempts ADD COLUMN timeout_ms INTEGER NOT NULL DEFAULT 1000
    CHECK (timeout_ms > 0 AND timeout_ms <= 300000);
ALTER TABLE action_attempts ADD COLUMN expected_postcondition TEXT NOT NULL DEFAULT ''
    CHECK (length(expected_postcondition) <= 512)
    CHECK (lower(expected_postcondition) NOT LIKE '%password%')
    CHECK (lower(expected_postcondition) NOT LIKE '%token%');

CREATE INDEX runs_workspace_state_created_idx
    ON runs (workspace_id, state, created_at DESC);
CREATE INDEX run_targets_run_state_idx
    ON run_targets (workspace_id, run_id, state, created_at);
CREATE INDEX action_attempts_target_sequence_idx
    ON action_attempts (workspace_id, run_target_id, sequence, created_at);
CREATE INDEX run_events_run_occurred_idx
    ON run_events (workspace_id, run_id, occurred_at, id);

CREATE TABLE run_ai_candidates (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_target_id TEXT,
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    prompt_template_version TEXT NOT NULL CHECK (length(trim(prompt_template_version)) > 0),
    proposal_json TEXT NOT NULL,
    confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    uncertainty TEXT NOT NULL CHECK (length(trim(uncertainty)) > 0),
    evidence_json TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition IN ('unreviewed', 'review_required', 'accepted', 'rejected', 'expired', 'abstained')),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id) REFERENCES run_targets (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(proposal_json) <= 65536),
    CHECK (length(evidence_json) <= 65536),
    CHECK (lower(proposal_json || evidence_json) NOT LIKE '%password%'),
    CHECK (lower(proposal_json || evidence_json) NOT LIKE '%token%')
);

CREATE INDEX run_ai_candidates_target_created_idx
    ON run_ai_candidates (workspace_id, run_target_id, created_at DESC);

CREATE TABLE replay_evidence (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    run_target_id TEXT NOT NULL,
    action_kind TEXT NOT NULL CHECK (action_kind IN ('observe', 'health_check', 'capture', 'tap', 'double_tap', 'long_press', 'text_input', 'text_delete', 'clear', 'swipe', 'scroll', 'drag', 'back', 'home', 'enter', 'key_event', 'ui_change', 'state_change')),
    action_attempt_id TEXT,
    observation_id TEXT,
    package_name TEXT NOT NULL,
    activity_name TEXT NOT NULL,
    app_version TEXT NOT NULL DEFAULT '',
    coordinate_space TEXT NOT NULL,
    target_json TEXT NOT NULL DEFAULT '{}',
    permission_dialog_mode TEXT NOT NULL CHECK (permission_dialog_mode IN ('fail_closed', 'operator_confirmation')),
    irreversible_confirmed INTEGER NOT NULL DEFAULT 0 CHECK (irreversible_confirmed IN (0, 1)),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, run_target_id) REFERENCES run_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, action_attempt_id) REFERENCES action_attempts (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, observation_id) REFERENCES observation_snapshots (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(package_name) <= 256),
    CHECK (length(activity_name) <= 256),
    CHECK (length(app_version) <= 256),
    CHECK (length(coordinate_space) <= 256),
    CHECK (length(target_json) <= 4096),
    CHECK (lower(package_name || activity_name || app_version || coordinate_space || target_json) NOT LIKE '%password%'),
    CHECK (lower(package_name || activity_name || app_version || coordinate_space || target_json) NOT LIKE '%token%')
);

CREATE INDEX replay_evidence_target_created_idx
    ON replay_evidence (workspace_id, run_target_id, created_at DESC);
