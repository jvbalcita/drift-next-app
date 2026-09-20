-- Record a device the plane refused to carry, on the event that names it.
--
-- mirror_events held ZERO rows on the owner's host while sixteen mirror_sessions
-- rows sat unfinished (finishing them is ARC-161), so a frame that showed nothing
-- could not be explained from the plane's own record at all: the reason lived only
-- in the console's own memory of the request that failed, and a later reader of
-- the database saw sessions that never ended and nothing that said why. The
-- refusal this plane gives - "media: N devices are already being mirrored, which
-- is the configured device session capacity" - is a fact about a DEVICE, and the
-- table had no column for one.
--
-- Two columns are added, and each exists for a case the refusals actually have:
--
--   device_id          the device the event is about. A live mirror is opened for
--                      one device, and the refusal names it; without this column
--                      the only way to say which device was refused would be a
--                      payload a reader has to parse.
--   mirror_session_id  becomes NULLABLE. A live stream is NOT a supervised
--                      source/follower session and must not be tied to one: a
--                      viewer confers no authority to act on a device (AGENTS.md
--                      section 2), so a stream that had to reference a session -
--                      and through it a control session - would carry authority
--                      it was never granted. A refusal is therefore recorded
--                      against the device's own mirror session WHERE ONE EXISTS
--                      (the session an operator's frame-opening already created)
--                      and against no session where none does, which is exactly
--                      the fact an operator needs: this device was refused, and
--                      here is the session it was refused for, if there was one.
--
-- sqlite has no ALTER for a column's nullability, so the table is rebuilt. It is a
-- leaf - nothing references it - and the rebuild copies every existing row
-- byte-for-byte with device_id NULL, which reads as "recorded before this column
-- existed" rather than as any device.

CREATE TABLE mirror_events_next (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    mirror_session_id TEXT,
    mirror_target_id TEXT,
    device_id TEXT,
    event_name TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    actor_id TEXT NOT NULL,
    source TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_session_id) REFERENCES mirror_sessions (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_target_id) REFERENCES mirror_targets (workspace_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, mirror_target_id, mirror_session_id) REFERENCES mirror_targets (workspace_id, id, mirror_session_id) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id, device_id) REFERENCES devices (workspace_id, id) ON DELETE RESTRICT,
    CHECK (length(payload_json) <= 65536),
    CHECK (lower(payload_json) NOT LIKE '%password%'),
    CHECK (lower(payload_json) NOT LIKE '%token%')
);

INSERT INTO mirror_events_next (id, workspace_id, mirror_session_id, mirror_target_id, device_id, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at)
SELECT id, workspace_id, mirror_session_id, mirror_target_id, NULL, event_name, schema_version, correlation_id, causation_id, actor_id, source, payload_json, occurred_at
FROM mirror_events;

DROP TABLE mirror_events;

ALTER TABLE mirror_events_next RENAME TO mirror_events;

-- The read this record exists for: what was refused for this device, in order.
CREATE INDEX mirror_events_device_idx ON mirror_events (workspace_id, device_id, occurred_at);
