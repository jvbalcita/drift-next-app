-- Give the registry the identity a device reports about itself.
--
-- A TCP adb transport's serial IS the address the device answers on (see
-- runtimeDevice in internal/product/enumerator.go), and the registry keys a
-- device on its serial. So a device whose address changed - a new DHCP lease, a
-- reconnect on another interface - arrived with a serial no endpoint row held,
-- did not collide with the identity registered at the old address, and was
-- upserted as a SECOND device. That is how every unit in the lab ended up with
-- two rows (45 device rows for a fleet of about 22) and why the console can
-- offer a row whose transport was last observed somewhere the unit no longer
-- answers.
--
-- hardware_serial holds the serial the DEVICE reports for itself (Android
-- ro.serialno), which does not change when its transport does, so the registry
-- can resolve a device that moved to the identity it already has. It is a match
-- key - an alias - and never the primary identity: devices.id stays the opaque
-- identifier, and the column is nullable because a device that reports no
-- usable serial (a fake device, a transport adb lists as offline) keeps
-- resolving by its transport exactly as it does today.
--
-- The partial unique index makes "one device, one identity" a durable
-- invariant rather than a calling convention: two device rows in one workspace
-- cannot claim the same reported serial, so a duplicate claim fails loudly
-- instead of silently splitting one device across two rows.
--
-- Rows registered before this migration keep hardware_serial NULL. Deciding
-- what happens to them is a data decision for the owner; this migration
-- deliberately backfills nothing and merges nothing.
ALTER TABLE devices ADD COLUMN hardware_serial TEXT
    CHECK (hardware_serial IS NULL OR (length(hardware_serial) BETWEEN 1 AND 64));

CREATE UNIQUE INDEX devices_one_identity_per_hardware_serial_idx
    ON devices (workspace_id, hardware_serial)
    WHERE hardware_serial IS NOT NULL;
