-- Persisted operator order for the device groups themselves. The legacy
-- registry API reordered groups through POST /groups/reorder; this column is
-- where that order lives now. Nothing derives group order from insertion time
-- or from the id, so a reorder survives every list.
--
-- Forward-only and additive: existing rows default to 0 and are ordered by name
-- until an operator reorders them.

ALTER TABLE device_groups ADD COLUMN position INTEGER NOT NULL DEFAULT 0;

CREATE INDEX device_groups_order_idx ON device_groups (workspace_id, position);
