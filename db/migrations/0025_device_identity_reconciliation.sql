-- Reconcile the duplicate device rows the pre-ARC-160 discovery path minted:
-- fold each duplicate's endpoint history onto the unit's canonical row, then
-- retire the duplicate row in place, with the reason recorded. Nothing is
-- deleted, and no row is merged that the database's own evidence cannot name.
--
-- WHAT WENT WRONG (ARC-160). The registry keys a device on its serial, and for
-- a TCP transport the serial adb reports IS the address the device answers on.
-- A unit whose lease changed therefore arrived with a serial no endpoint row
-- held, did not collide with the identity registered at the old address, and
-- was upserted as a SECOND device row. That left this plane with 45 device rows
-- for a fleet of 22 units, two rows per name, and a console that could draw --
-- or pick -- a row whose transport was an address the unit no longer answers
-- on. ARC-160 stopped new duplicates (devices.hardware_serial, migration 0024)
-- and deliberately left the existing rows alone: reconciling them is the
-- owner's data decision, recorded on ARC-163 on 2026-09-18.
--
-- THE OWNER'S DECISION. Merge each duplicate's endpoint history onto the
-- canonical device, THEN retire the duplicate device row. Never delete. The
-- duplicate rows are history, not garbage: each one is referenced by the audit
-- record that proves how it was created (system/discovery-watcher at
-- 2026-09-17T14:43:18/23 and 2026-09-18T06:53:28), and AGENTS.md section 2
-- retires a row that append-only evidence references in place rather than
-- deleting it. Re-pointing the endpoint rows onto the canonical device keeps
-- the observation history at the right identity; retiring the duplicate row
-- takes it out of what an operator sees without erasing how it got there.
--
-- HOW A DUPLICATE IS IDENTIFIED, AND WHAT IS NOT GUESSED. 0024 backfills
-- nothing, so on a plane that has not yet been scanned by the fixed binary
-- every devices.hardware_serial is still NULL and that column alone cannot tell
-- a duplicate from a moved device. The identity evidence that does exist is the
-- row's own observation record, and this migration matches on exactly that:
--
--   (1) A row the registry named after the device itself -- its display_name is
--       not one of its own endpoint serials -- is grouped with the other rows
--       carrying the same display_name in the same workspace. A display name is
--       not an identity (AGENTS.md section 2), so the name only forms the
--       CANDIDATE group. The canonical row inside the group is, in order: the
--       row that already holds devices.hardware_serial, the serial the device
--       reports for itself -- which is the identity ARC-160 made authoritative
--       and is what decides once the fixed binary has observed the unit -- and
--       failing that the row whose endpoint history is the most recent. The
--       remaining rows of the group are its duplicates: they are the rows the
--       unit stopped answering at.
--
--   (2) A row the registry named after the transport it was reached at -- its
--       display_name is one of its own endpoint serials -- carries no name to
--       group on, so it is reconciled only where the fleet's own record names
--       the unit it belongs to. Those pairings are the explicit list at the
--       bottom of this comment block, and each one is a transport pair of a
--       single unit, not a guess at an address:
--
--         * R5CT42GS94Z  <->  192.168.1.106:5556
--           One unit observed on two transports at once -- the USB serial it
--           reports for itself and the TCP address it answers on. That pair is
--           the live case ARC-160 was written for and is asserted in
--           internal/store/sqlite/registry_identity_test.go.
--
--         * 192.168.1.148:5555 -> ALTA 8   (canonical transport
--                                              192.168.1.101:5555)
--           192.168.1.149:5555 -> ALTA 18  (canonical transport
--                                              192.168.1.128:5555)
--           The watcher sweep at 2026-09-17T14:43:23 minted twenty rows for the
--           twenty units of the fleet, in ascending address order, .130 through
--           .149. Eighteen of them carry the ALTA name the unit reports;
--           ALTA 8 and ALTA 18 are the only two names absent from that sweep,
--           and these two rows are the only two it minted without a name. The
--           SET is therefore settled by the sweep's own accounting, with the
--           20 ALTA names proven to be 20 distinct units by ARC-160's
--           `adb devices -l` reading. Which of the two rows belongs to ALTA 8
--           and which to ALTA 18 is not recorded anywhere in this database, and
--           is assigned here by ascending address. Both rows carry the same
--           observation history shape (fourteen TCP observations each, at the
--           same instants), so a swap would move the same evidence and change
--           no count.
--
-- Every unit that this migration cannot name is left exactly as it is: a
-- transport-named row with no pairing above is a unit of its own, not a
-- duplicate. That is deliberate -- over-merging would move one unit's history
-- onto another unit's identity, which is the defect this repairs.
--
-- STATE AFTER APPLY, AND WHAT IS NOT CREATED. A duplicate's endpoint rows are
-- re-pointed onto the canonical device and their own observation times,
-- transports and serials are untouched: every endpoint's history is preserved,
-- exactly as evidence. Where the canonical device and its duplicate both held a
-- current endpoint, the most recently observed one stays current and the others
-- become superseded at that instant, so a device never ends up with two current
-- transports. Where a device has no current endpoint, the migration creates
-- none: a transport last observed offline is not a reachable transport
-- (AGENTS.md section 2), and a repair has no observation to invent. The
-- duplicate device row is retired -- state 'retired', row_version advanced --
-- and the reason, the canonical device and the number of endpoint rows folded
-- are written to device_identity_reconciliations, which is append-only and
-- keyed to both device rows.
--
-- The duplicate's OTHER references -- its leases, group placements, mirror
-- targets, health and account assignments -- are left where they are, pointing
-- at the retired row. They are the record of what was done with that identity
-- while it was current, not observation history, and re-pointing them is a
-- separate decision with its own uniqueness questions (a lease has one
-- (device, fencing token) row and a placement one membership per device).
--
-- This migration is idempotent: it records a pairing only for a duplicate that
-- is not already recorded, re-points only endpoints that still sit on a
-- duplicate, retires only a row that is not already retired, and collapses a
-- device's current endpoints only when more than one exists. A second apply
-- changes nothing.

CREATE TABLE IF NOT EXISTS device_identity_reconciliations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    duplicate_device_id TEXT NOT NULL,
    canonical_device_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0 AND length(reason) <= 1024),
    endpoint_rows_folded INTEGER NOT NULL DEFAULT 0 CHECK (endpoint_rows_folded >= 0),
    reconciled_at TEXT NOT NULL,
    UNIQUE (workspace_id, duplicate_device_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, duplicate_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, canonical_device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (duplicate_device_id <> canonical_device_id)
);

CREATE INDEX IF NOT EXISTS device_identity_reconciliations_canonical_idx
    ON device_identity_reconciliations (workspace_id, canonical_device_id);

-- (1) Rows registered twice under one display name. The canonical row is the
-- one holding the serial the device reports for itself when any row in the
-- group holds one, and otherwise the row whose endpoint history is the most
-- recent; ties break on the newest row, then the id, so the choice is a
-- function of the data alone.
WITH observed AS (
    SELECT e.device_id,
           e.observed_at,
           COALESCE(e.serial, '') AS serial,
           ROW_NUMBER() OVER (
               PARTITION BY e.device_id
               ORDER BY e.observed_at DESC, e.id DESC
           ) AS recency_rank
    FROM device_endpoints AS e
),
candidate AS (
    SELECT d.workspace_id,
           d.id AS device_id,
           d.display_name,
           d.created_at,
           COALESCE(o.observed_at, '') AS last_observed_at,
           COALESCE(o.serial, '') AS last_transport,
           d.hardware_serial,
           ROW_NUMBER() OVER (
               PARTITION BY d.workspace_id, d.display_name
               ORDER BY (d.hardware_serial IS NULL),
                        COALESCE(o.observed_at, '') DESC,
                        d.created_at DESC,
                        d.id DESC
           ) AS identity_rank
    FROM devices AS d
    LEFT JOIN observed AS o
           ON o.device_id = d.id AND o.recency_rank = 1
    WHERE d.state <> 'retired'
      -- Named after the device, not after the transport it was reached at.
      AND NOT EXISTS (
              SELECT 1 FROM device_endpoints AS own
              WHERE own.device_id = d.id AND own.serial = d.display_name
          )
)
INSERT INTO device_identity_reconciliations (
    id, workspace_id, duplicate_device_id, canonical_device_id, reason,
    endpoint_rows_folded, reconciled_at
)
SELECT 'reconciliation-' || duplicate.device_id,
       duplicate.workspace_id,
       duplicate.device_id,
       canonical.device_id,
       'duplicate identity: ' || duplicate.display_name ||
           ' was registered twice when its transport address changed; this row was last observed at ' ||
           CASE WHEN duplicate.last_transport = '' THEN 'no transport' ELSE duplicate.last_transport END ||
           ', the unit''s canonical identity was last observed at ' ||
           CASE WHEN canonical.last_transport = '' THEN 'no transport' ELSE canonical.last_transport END,
       (SELECT COUNT(*) FROM device_endpoints AS folded
        WHERE folded.workspace_id = duplicate.workspace_id
          AND folded.device_id = duplicate.device_id),
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM candidate AS duplicate
JOIN candidate AS canonical
  ON canonical.workspace_id = duplicate.workspace_id
 AND canonical.display_name = duplicate.display_name
 AND canonical.identity_rank = 1
WHERE duplicate.identity_rank > 1
  AND NOT EXISTS (
          SELECT 1 FROM device_identity_reconciliations AS recorded
          WHERE recorded.workspace_id = duplicate.workspace_id
            AND recorded.duplicate_device_id = duplicate.device_id
      );

-- (2) The transport-named rows whose unit the fleet's own record names. Each
-- pair is (the duplicate's transport, the canonical's transport, the evidence
-- that they are one unit). Rows are resolved through the endpoint serial the
-- registry recorded, so the pairing survives a differing display name on either
-- side. A transport is recorded once per observation, so the resolution is
-- deduplicated to the device each serial belongs to.
WITH pairing (duplicate_transport, canonical_transport, evidence) AS (
    VALUES
        ('192.168.1.106:5556', 'R5CT42GS94Z',
         'one unit observed on two transports at once: the USB serial it reports for itself and the TCP address it answers on (ARC-160, registry_identity_test.go)'),
        ('192.168.1.148:5555', '192.168.1.101:5555',
         'the 2026-09-17T14:43:23 watcher sweep minted twenty rows for the twenty units of the fleet and named eighteen of them; this row is one of the two it minted unnamed, and ALTA 8 is one of the two names that sweep does not carry'),
        ('192.168.1.149:5555', '192.168.1.128:5555',
         'the 2026-09-17T14:43:23 watcher sweep minted twenty rows for the twenty units of the fleet and named eighteen of them; this row is the other it minted unnamed, and ALTA 18 is the other name that sweep does not carry')
),
listener AS (
    SELECT DISTINCT workspace_id, serial, device_id FROM device_endpoints
),
resolved AS (
    SELECT duplicate.workspace_id       AS workspace_id,
           duplicate.id                 AS duplicate_device_id,
           canonical.id                 AS canonical_device_id,
           MIN(pairing.evidence)        AS evidence,
           MIN(pairing.duplicate_transport) AS duplicate_transport,
           MIN(pairing.canonical_transport) AS canonical_transport
    FROM pairing
    JOIN listener AS duplicate_listener
      ON duplicate_listener.serial = pairing.duplicate_transport
    JOIN devices AS duplicate
      ON duplicate.id = duplicate_listener.device_id
     AND duplicate.workspace_id = duplicate_listener.workspace_id
     AND duplicate.state <> 'retired'
    JOIN listener AS canonical_listener
      ON canonical_listener.serial = pairing.canonical_transport
     AND canonical_listener.workspace_id = duplicate_listener.workspace_id
    JOIN devices AS canonical
      ON canonical.id = canonical_listener.device_id
     AND canonical.workspace_id = canonical_listener.workspace_id
     AND canonical.state <> 'retired'
    WHERE duplicate.id <> canonical.id
      AND NOT EXISTS (
              SELECT 1 FROM device_identity_reconciliations AS recorded
              WHERE recorded.workspace_id = duplicate.workspace_id
                AND recorded.duplicate_device_id = duplicate.id
          )
    GROUP BY duplicate.workspace_id, duplicate.id, canonical.id
)
INSERT INTO device_identity_reconciliations (
    id, workspace_id, duplicate_device_id, canonical_device_id, reason,
    endpoint_rows_folded, reconciled_at
)
SELECT 'reconciliation-' || resolved.duplicate_device_id,
       resolved.workspace_id,
       resolved.duplicate_device_id,
       resolved.canonical_device_id,
       'duplicate identity: ' || resolved.evidence ||
           '; this row was last observed at ' || resolved.duplicate_transport ||
           ', the unit''s canonical identity was last observed at ' || resolved.canonical_transport,
       (SELECT COUNT(*) FROM device_endpoints AS folded
        WHERE folded.workspace_id = resolved.workspace_id
          AND folded.device_id = resolved.duplicate_device_id),
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM resolved;

-- The two rules above are decided independently of each other - the transport
-- pairing from the transports a unit answered on, the name grouping from the
-- name it reported - so a pairing can name a canonical row that the other rule
-- treats as a duplicate. Follow each chain to the row that actually survives, so
-- the recorded reason names the identity the history lands on and no pairing
-- points at a row that is itself folded somewhere else. The walk is bounded: a
-- chain longer than a handful of hops is a data shape this repair does not claim
-- to fix, and a bounded walk refuses rather than looping.
WITH RECURSIVE chain (workspace_id, duplicate_device_id, canonical_device_id, hops) AS (
    SELECT workspace_id, duplicate_device_id, canonical_device_id, 1
    FROM device_identity_reconciliations
    UNION ALL
    SELECT chain.workspace_id, chain.duplicate_device_id, next.canonical_device_id, chain.hops + 1
    FROM chain
    JOIN device_identity_reconciliations AS next
      ON next.workspace_id = chain.workspace_id
     AND next.duplicate_device_id = chain.canonical_device_id
    WHERE chain.hops < 8
),
root AS (
    SELECT workspace_id,
           duplicate_device_id,
           canonical_device_id,
           ROW_NUMBER() OVER (
               PARTITION BY workspace_id, duplicate_device_id
               ORDER BY hops DESC
           ) AS depth_rank
    FROM chain
)
UPDATE device_identity_reconciliations
SET canonical_device_id = (
        SELECT root.canonical_device_id
        FROM root
        WHERE root.workspace_id = device_identity_reconciliations.workspace_id
          AND root.duplicate_device_id = device_identity_reconciliations.duplicate_device_id
          AND root.depth_rank = 1
    )
WHERE EXISTS (
    SELECT 1 FROM device_identity_reconciliations AS chained
    WHERE chained.workspace_id = device_identity_reconciliations.workspace_id
      AND chained.duplicate_device_id = device_identity_reconciliations.canonical_device_id
);

-- One current endpoint per unit, before any endpoint moves. The unique index
-- device_endpoints_one_current_idx allows a device only one current endpoint, so
-- a unit whose duplicate and canonical rows BOTH hold one must be settled first:
-- the most recently observed current endpoint of the unit is the transport the
-- unit was last reachable at, and it is the one that stays current; the others
-- are superseded at that instant, which is what a departure records (AGENTS.md
-- section 2). No current endpoint is created for a unit that has none: a
-- transport last observed offline is not a reachable transport, and a repair has
-- no observation to invent.
WITH unit_current AS (
    SELECT e.id                                   AS endpoint_id,
           e.workspace_id,
           COALESCE(recorded.canonical_device_id, e.device_id) AS unit_device_id,
           MAX(e.observed_at) OVER (
               PARTITION BY e.workspace_id, COALESCE(recorded.canonical_device_id, e.device_id)
           )                                      AS unit_newest_at,
           ROW_NUMBER() OVER (
               PARTITION BY e.workspace_id, COALESCE(recorded.canonical_device_id, e.device_id)
               ORDER BY e.observed_at DESC, e.id DESC
           )                                      AS recency_rank
    FROM device_endpoints AS e
    LEFT JOIN device_identity_reconciliations AS recorded
           ON recorded.workspace_id = e.workspace_id
          AND recorded.duplicate_device_id = e.device_id
    WHERE e.state = 'current'
)
UPDATE device_endpoints
SET state = 'superseded',
    superseded_at = (
        SELECT unit_current.unit_newest_at
        FROM unit_current
        WHERE unit_current.endpoint_id = device_endpoints.id
    )
WHERE id IN (
    SELECT endpoint_id FROM unit_current WHERE recency_rank > 1
);

-- Re-point the duplicate's endpoint history onto the canonical device. The
-- endpoint rows themselves are untouched: same ids, same transports, same
-- serials, same observation times, so the history that proves where the unit
-- has been survives at the identity it belongs to.
UPDATE device_endpoints
SET device_id = (
        SELECT recorded.canonical_device_id
        FROM device_identity_reconciliations AS recorded
        WHERE recorded.workspace_id = device_endpoints.workspace_id
          AND recorded.duplicate_device_id = device_endpoints.device_id
    )
WHERE device_id IN (
    SELECT duplicate_device_id FROM device_identity_reconciliations
);

-- Retire the duplicate in place. The row is kept, with its id, its name, its
-- creation time and every reference append-only evidence holds; it simply stops
-- being a device the fleet answers at, and stop being counted as one.
UPDATE devices
SET state = 'retired',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    row_version = row_version + 1
WHERE state <> 'retired'
  AND id IN (
      SELECT duplicate_device_id FROM device_identity_reconciliations
  );
