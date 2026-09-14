-- Phase 9 timeline indexes and operational-event actor provenance.

ALTER TABLE device_events
    ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'service'
    CHECK (actor_type IN ('operator', 'service', 'edge_agent', 'system'));

CREATE INDEX inventory_snapshots_device_observed_idx
    ON inventory_snapshots (workspace_id, device_id, observed_at DESC);

CREATE INDEX health_samples_device_sampled_idx
    ON health_samples (workspace_id, device_id, sampled_at DESC);

CREATE INDEX device_events_device_occurred_idx
    ON device_events (workspace_id, device_id, occurred_at DESC);
