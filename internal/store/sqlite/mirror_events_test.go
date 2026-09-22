package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/mirrors"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestFollowerOutcomeRecordsBoundedLatencyDimensions(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	events := store.NewMirrorEventService(db)
	err := events.RecordFollowerInputOutcome(context.Background(), mirrors.FollowerInputOutcomeRecord{
		WorkspaceID: mirrorEventWorkspace, SourceDeviceID: mirrorEventSource, DeviceID: mirrorEventFollower,
		RunID: "fanout-1", Disposition: "accepted", Reason: "delivered", Detail: "The follower received the gesture.",
		AcceptanceLatencyMillis: (3 * time.Millisecond).Milliseconds(), QueueWaitMillis: (7 * time.Millisecond).Milliseconds(),
		CompletionLatencyMillis: (41 * time.Millisecond).Milliseconds(), ActorID: "op-1",
	})
	if err != nil {
		t.Fatalf("RecordFollowerInputOutcome: %v", err)
	}
	recorded := readMirrorEvents(t, db, mirrorEventFollower)
	if len(recorded) != 1 {
		t.Fatalf("recorded %d events, want one", len(recorded))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(recorded[0].Payload), &payload); err != nil {
		t.Fatalf("decode follower outcome payload: %v", err)
	}
	for field, want := range map[string]float64{"acceptance_latency_ms": 3, "queue_wait_ms": 7, "completion_latency_ms": 41} {
		if got, ok := payload[field].(float64); !ok || got != want {
			t.Fatalf("%s = %v, want %.0f", field, payload[field], want)
		}
	}
}

// A refused live stream has to be readable from the PLANE's own record, because
// the console that asked keeps the sentence only while it is showing the frame.
// The owner's host is what makes the case: sixteen mirror_sessions rows sat
// unfinished with ZERO mirror_events rows beside them, so a frame that had carried
// nothing could not be explained by anything in the database at all.

const (
	mirrorEventWorkspace = "workspace-mirror-events"
	mirrorEventSource    = "device-event-source"
	mirrorEventFollower  = "device-event-follower"
)

// seedMirrorWorkspace creates the workspace and the two devices the refusal cases
// need, the way the product creates them.
func seedMirrorWorkspace(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: mirrorEventWorkspace, Name: "Mirror events", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []devices.DeviceID{mirrorEventSource, mirrorEventFollower} {
		device := devices.Device{ID: id, Workspace: mirrorEventWorkspace, DisplayName: string(id), State: devices.Active}
		if err := store.NewDeviceService(db).Create(ctx, device, "operator", "op-1"); err != nil {
			t.Fatalf("create device %s: %v", id, err)
		}
	}
}

// startMirrorSession records a mirror session for the source device, which is what
// opening a device's frame does. The refusal cases below are about what happens
// AFTER that session exists and the plane refuses a stream for it.
func startMirrorSession(t *testing.T, db *store.DB) string {
	t.Helper()
	session, err := store.NewMirrorService(db).StartPreview(context.Background(), mirrorEventWorkspace,
		mirrorEventSource, []devices.DeviceID{mirrorEventFollower}, "operator", "op-1")
	if err != nil {
		t.Fatalf("start mirror preview: %v", err)
	}
	return string(session.ID)
}

type mirrorEventRow struct {
	SessionID sql.NullString
	TargetID  sql.NullString
	DeviceID  sql.NullString
	EventName string
	Source    string
	ActorID   string
	Payload   string
}

func readMirrorEvents(t *testing.T, db *store.DB, deviceID string) []mirrorEventRow {
	t.Helper()
	rows, err := store.SQLForTest(db).QueryContext(context.Background(),
		`SELECT mirror_session_id, mirror_target_id, device_id, event_name, source, actor_id, payload_json FROM mirror_events WHERE workspace_id=? AND device_id=? ORDER BY occurred_at, id`,
		mirrorEventWorkspace, deviceID)
	if err != nil {
		t.Fatalf("read mirror events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []mirrorEventRow
	for rows.Next() {
		var row mirrorEventRow
		if err := rows.Scan(&row.SessionID, &row.TargetID, &row.DeviceID, &row.EventName, &row.Source, &row.ActorID, &row.Payload); err != nil {
			t.Fatalf("scan mirror event: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read mirror events: %v", err)
	}
	return out
}

// TestARefusedStreamIsRecordedAgainstTheSessionsOwnDevice: the refusal names the
// device AND the session that device already had, so the row a reader finds beside
// the unfinished session is the reason it is unfinished.
func TestARefusedStreamIsRecordedAgainstTheSessionsOwnDevice(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	sessionID := startMirrorSession(t, db)

	events := store.NewMirrorEventService(db)
	if err := events.RecordStreamRefusal(context.Background(), mirrors.StreamRefusal{
		WorkspaceID:     mirrorEventWorkspace,
		DeviceID:        mirrorEventSource,
		ActorType:       "operator",
		ActorID:         "op-1",
		ViewerPurpose:   "operator",
		Reason:          "media: 4 devices are already being mirrored, which is the configured device session capacity",
		Capacity:        4,
		OperatorReserve: 1,
	}); err != nil {
		t.Fatalf("RecordStreamRefusal: %v", err)
	}

	recorded := readMirrorEvents(t, db, mirrorEventSource)
	if len(recorded) != 1 {
		t.Fatalf("the refusal left %d event(s), want one", len(recorded))
	}
	row := recorded[0]
	if row.SessionID.String != sessionID {
		t.Fatalf("the event names session %q, want the device's own %q", row.SessionID.String, sessionID)
	}
	if row.DeviceID.String != mirrorEventSource {
		t.Fatalf("the event names device %q, want the device it refused", row.DeviceID.String)
	}
	if row.EventName != "mirror.stream_refused" {
		t.Fatalf("the event is named %q, want the mirror's own refusal", row.EventName)
	}
	if row.Source != "control-plane" {
		t.Fatalf("the event's source is %q, want the plane that recorded it", row.Source)
	}
	if row.ActorID != "op-1" {
		t.Fatalf("the event's actor is %q, want the caller who asked for the stream", row.ActorID)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		t.Fatalf("the event's payload is not readable: %v", err)
	}
	if reason, _ := payload["reason"].(string); !strings.Contains(reason, "4 devices are already being mirrored") {
		t.Fatalf("the event's payload carries %q, which is not the plane's own sentence", reason)
	}
	if capacity, ok := payload["capacity"].(float64); !ok || capacity != 4 {
		t.Fatalf("the event's payload names capacity %v, want the bound that was spent", payload["capacity"])
	}
	if purpose, _ := payload["viewer_purpose"].(string); purpose != "operator" {
		t.Fatalf("the event's payload names purpose %q, want the operator's own frame", purpose)
	}
}

// TestARefusedStreamForADeviceWithNoSessionStillNamesTheDevice: a live mirror is a
// viewer and carries no authority to act on a device, so it is never tied to a
// session of its own. A refusal for a device the plane has no session for is still
// recorded, and it names the device rather than inventing a session to hang the row
// on.
func TestARefusedStreamForADeviceWithNoSessionStillNamesTheDevice(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)

	events := store.NewMirrorEventService(db)
	if err := events.RecordStreamRefusal(context.Background(), mirrors.StreamRefusal{
		WorkspaceID:   mirrorEventWorkspace,
		DeviceID:      mirrorEventFollower,
		ActorType:     "operator",
		ActorID:       "op-1",
		ViewerPurpose: "ambient",
		Reason:        "media: the console's grid is already carrying its 3 device session(s) of the plane's 4, and the rest is kept for the operator's own frame",
		Capacity:      4,
	}); err != nil {
		t.Fatalf("RecordStreamRefusal: %v", err)
	}

	recorded := readMirrorEvents(t, db, mirrorEventFollower)
	if len(recorded) != 1 {
		t.Fatalf("the refusal left %d event(s), want one", len(recorded))
	}
	if recorded[0].SessionID.Valid {
		t.Fatalf("an event for a device with no session names session %q", recorded[0].SessionID.String)
	}
	if recorded[0].DeviceID.String != mirrorEventFollower {
		t.Fatalf("the event names device %q, want the device it refused", recorded[0].DeviceID.String)
	}
	// No session was invented to hold the row: the sessions table is untouched.
	var sessions int
	if err := store.SQLForTest(db).QueryRow(`SELECT count(*) FROM mirror_sessions WHERE workspace_id=?`, mirrorEventWorkspace).Scan(&sessions); err != nil {
		t.Fatalf("count mirror sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("recording a refusal created %d mirror session(s): a viewer is not a supervised session", sessions)
	}
}

// TestARefusalIncompleteIsRefused: what makes the record diagnosable is that it
// carries the device, the actor and the sentence. A row missing any of them is a
// row a reader cannot act on, so it is refused rather than written.
func TestARefusalIncompleteIsRefused(t *testing.T) {
	db := openTestDB(t)
	seedMirrorWorkspace(t, db)
	events := store.NewMirrorEventService(db)
	complete := mirrors.StreamRefusal{
		WorkspaceID:   mirrorEventWorkspace,
		DeviceID:      mirrorEventSource,
		ActorType:     "operator",
		ActorID:       "op-1",
		ViewerPurpose: "operator",
		Reason:        "media: 4 devices are already being mirrored, which is the configured device session capacity",
		Capacity:      4,
	}
	cases := []struct {
		name   string
		change func(refusal mirrors.StreamRefusal) mirrors.StreamRefusal
	}{
		{name: "no device", change: func(r mirrors.StreamRefusal) mirrors.StreamRefusal { r.DeviceID = ""; return r }},
		{name: "no reason", change: func(r mirrors.StreamRefusal) mirrors.StreamRefusal { r.Reason = "  "; return r }},
		{name: "no actor", change: func(r mirrors.StreamRefusal) mirrors.StreamRefusal { r.ActorID = ""; return r }},
		{name: "no workspace", change: func(r mirrors.StreamRefusal) mirrors.StreamRefusal { r.WorkspaceID = ""; return r }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := events.RecordStreamRefusal(context.Background(), test.change(complete))
			if code := platformerrors.CodeOf(err); code != platformerrors.CodeInvalidInput {
				t.Fatalf("an incomplete refusal was recorded (%v), want invalid_input", err)
			}
		})
	}
	if recorded := readMirrorEvents(t, db, mirrorEventSource); len(recorded) != 0 {
		t.Fatalf("an incomplete refusal left %d event(s)", len(recorded))
	}
}
