-- Phase 10.5 recorder and skill metadata. Evidence bytes remain outside
-- SQLite in the private content-addressed artifact store.

ALTER TABLE recording_sessions ADD COLUMN session_number INTEGER NOT NULL DEFAULT 0
    CHECK (session_number >= 0);
ALTER TABLE recording_sessions ADD COLUMN automation_id TEXT NOT NULL DEFAULT ''
    CHECK (length(automation_id) <= 256);
ALTER TABLE recording_sessions ADD COLUMN deleted_at TEXT;
ALTER TABLE recording_sessions ADD COLUMN cleanup_state TEXT NOT NULL DEFAULT 'none'
    CHECK (cleanup_state IN ('none', 'pending', 'done', 'failed'));
ALTER TABLE recording_sessions ADD COLUMN review_state TEXT NOT NULL DEFAULT 'unreviewed'
    CHECK (review_state IN ('unreviewed', 'approved', 'rejected'));

CREATE UNIQUE INDEX recording_sessions_number_idx
    ON recording_sessions (workspace_id, session_number)
    WHERE session_number > 0;
CREATE INDEX recording_sessions_workspace_created_idx
    ON recording_sessions (workspace_id, created_at DESC, id);

ALTER TABLE recording_events ADD COLUMN correlation_id TEXT NOT NULL DEFAULT ''
    CHECK (length(correlation_id) <= 256);
ALTER TABLE recording_events ADD COLUMN started_at TEXT;
ALTER TABLE recording_events ADD COLUMN finished_at TEXT;
ALTER TABLE recording_events ADD COLUMN before_state_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(before_state_json) <= 131072)
    CHECK (lower(before_state_json) NOT LIKE '%password%')
    CHECK (lower(before_state_json) NOT LIKE '%secret%')
    CHECK (lower(before_state_json) NOT LIKE '%token%');
ALTER TABLE recording_events ADD COLUMN action_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(action_json) <= 65536)
    CHECK (lower(action_json) NOT LIKE '%password%')
    CHECK (lower(action_json) NOT LIKE '%secret%')
    CHECK (lower(action_json) NOT LIKE '%token%');
ALTER TABLE recording_events ADD COLUMN after_state_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(after_state_json) <= 131072)
    CHECK (lower(after_state_json) NOT LIKE '%password%')
    CHECK (lower(after_state_json) NOT LIKE '%secret%')
    CHECK (lower(after_state_json) NOT LIKE '%token%');
ALTER TABLE recording_events ADD COLUMN before_freshness TEXT
    CHECK (before_freshness IS NULL OR length(before_freshness) <= 256);
ALTER TABLE recording_events ADD COLUMN after_freshness TEXT
    CHECK (after_freshness IS NULL OR length(after_freshness) <= 256);
ALTER TABLE recording_events ADD COLUMN capture_errors_json TEXT NOT NULL DEFAULT '[]'
    CHECK (length(capture_errors_json) <= 32768)
    CHECK (lower(capture_errors_json) NOT LIKE '%password%')
    CHECK (lower(capture_errors_json) NOT LIKE '%secret%')
    CHECK (lower(capture_errors_json) NOT LIKE '%token%');
ALTER TABLE recording_events ADD COLUMN review_state TEXT NOT NULL DEFAULT 'unreviewed'
    CHECK (review_state IN ('unreviewed', 'approved', 'rejected'));
ALTER TABLE recording_events ADD COLUMN reviewer_id TEXT NOT NULL DEFAULT ''
    CHECK (length(reviewer_id) <= 256);
ALTER TABLE recording_events ADD COLUMN reviewed_at TEXT;

CREATE INDEX recording_events_session_sequence_idx
    ON recording_events (workspace_id, recording_session_id, sequence);

CREATE TABLE recording_event_evidence (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    recording_event_id TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('before', 'action', 'after', 'review')),
    evidence_kind TEXT NOT NULL CHECK (evidence_kind IN ('raw_screenshot', 'annotated_screenshot', 'ui_tree', 'ocr', 'trace', 'log')),
    artifact_id TEXT,
    content_hash TEXT,
    media_type TEXT,
    schema_version INTEGER NOT NULL DEFAULT 0 CHECK (schema_version >= 0),
    authoritative INTEGER NOT NULL DEFAULT 0 CHECK (authoritative IN (0, 1)),
    omitted INTEGER NOT NULL DEFAULT 0 CHECK (omitted IN (0, 1)),
    omission_reason TEXT NOT NULL DEFAULT '' CHECK (length(omission_reason) <= 512),
    created_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, recording_event_id) REFERENCES recording_events (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(artifact_id) <= 256),
    CHECK (length(content_hash) <= 256),
    CHECK (length(media_type) <= 256),
    CHECK (lower(artifact_id || content_hash || media_type || omission_reason) NOT LIKE '%password%'),
    CHECK (lower(artifact_id || content_hash || media_type || omission_reason) NOT LIKE '%secret%'),
    CHECK (lower(artifact_id || content_hash || media_type || omission_reason) NOT LIKE '%token%'),
    CHECK ((omitted = 1 AND artifact_id IS NULL AND content_hash IS NULL AND authoritative = 0 AND length(trim(omission_reason)) > 0) OR (omitted = 0 AND (artifact_id IS NOT NULL OR content_hash IS NOT NULL) AND length(trim(media_type)) > 0 AND schema_version > 0))
);

CREATE INDEX recording_event_evidence_event_phase_idx
    ON recording_event_evidence (workspace_id, recording_event_id, phase, id);

ALTER TABLE skill_versions ADD COLUMN manifest_json TEXT NOT NULL DEFAULT '{}'
    CHECK (length(manifest_json) <= 262144)
    CHECK (lower(manifest_json) NOT LIKE '%password%')
    CHECK (lower(manifest_json) NOT LIKE '%secret%')
    CHECK (lower(manifest_json) NOT LIKE '%token%');
ALTER TABLE skill_versions ADD COLUMN fixtures_json TEXT NOT NULL DEFAULT '[]'
    CHECK (length(fixtures_json) <= 65536)
    CHECK (lower(fixtures_json) NOT LIKE '%password%')
    CHECK (lower(fixtures_json) NOT LIKE '%secret%')
    CHECK (lower(fixtures_json) NOT LIKE '%token%');
ALTER TABLE skill_versions ADD COLUMN risk_class TEXT NOT NULL DEFAULT 'low'
    CHECK (risk_class IN ('low', 'medium', 'high', 'irreversible'));
ALTER TABLE skill_versions ADD COLUMN retry_class TEXT NOT NULL DEFAULT 'safe'
    CHECK (retry_class IN ('safe', 'after_observation', 'never_blind'));
ALTER TABLE skill_versions ADD COLUMN reviewer_id TEXT NOT NULL DEFAULT ''
    CHECK (length(reviewer_id) <= 256);
ALTER TABLE skill_versions ADD COLUMN reviewed_at TEXT;
ALTER TABLE skill_versions ADD COLUMN rollback_of TEXT
    CHECK (length(rollback_of) <= 256);

CREATE INDEX skill_versions_workspace_state_idx
    ON skill_versions (workspace_id, skill_id, state, version DESC);
