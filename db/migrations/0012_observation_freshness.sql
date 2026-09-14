-- Observation freshness and capture provenance metadata.

ALTER TABLE observation_snapshots ADD COLUMN model_version TEXT NOT NULL DEFAULT '';
ALTER TABLE observation_snapshots ADD COLUMN truncated INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1));
ALTER TABLE observation_snapshots ADD COLUMN freshness_token TEXT NOT NULL DEFAULT '';
ALTER TABLE observation_snapshots ADD COLUMN capture_skew_ms INTEGER NOT NULL DEFAULT 0 CHECK (capture_skew_ms BETWEEN -86400000 AND 86400000);

UPDATE observation_snapshots
SET model_version = CASE
        WHEN length(trim(protocol_version)) > 0 THEN protocol_version
        ELSE 'legacy'
    END,
    freshness_token = 'legacy:' || id
WHERE model_version = '' OR freshness_token = '';

CREATE UNIQUE INDEX observation_snapshots_device_freshness_idx
    ON observation_snapshots (workspace_id, device_id, freshness_token)
    WHERE freshness_token <> '';
