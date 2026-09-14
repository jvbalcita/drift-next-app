-- Phase 6 registry retry identity and one registration outcome per candidate.

ALTER TABLE scan_runs ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX scan_runs_one_key_idx
    ON scan_runs (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX registration_events_one_success_idx
    ON registration_events (workspace_id, candidate_id)
    WHERE outcome = 'registered';
