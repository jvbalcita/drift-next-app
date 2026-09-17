package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A device and an edge agent each carry the time they were last observed. Both
// list reads selected that column and threw the value away, one function after the
// other, so every consumer of the list - including the console - was told a device
// had never been seen. The single-row reads were correct, which is what made it
// look like a projection problem rather than a dropped column: the fact was read,
// and then discarded in the loop that builds the list.

func TestTheDeviceListCarriesTheLastObservationEachRowHolds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-list-last-seen")
	profile := newArrivalWorkspace(t, db, workspace)

	usb := discovery.ObservedDevice{
		Serial:   "SER-LIST-SEEN",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	observedID := scanned[0].DeviceID

	// A device row that was never observed by anything: it must keep reading as
	// never seen, so the fix cannot be "stamp every row with the same time".
	neverObservedID := devices.DeviceID("device-never-observed")
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: neverObservedID, Workspace: workspace, DisplayName: "Never observed", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	listed, err := store.NewDeviceRepository(db).List(ctx, workspace)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() = %d device(s), want both", len(listed))
	}
	single, err := store.NewDeviceRepository(db).Get(ctx, workspace, observedID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, device := range listed {
		switch device.ID {
		case observedID:
			if device.LastSeenAt == nil {
				t.Fatal("the listed device has no last_seen_at, so the list read the column and dropped it")
			}
			if single.LastSeenAt == nil || !single.LastSeenAt.Equal(*device.LastSeenAt) {
				t.Fatalf("list last_seen_at = %v, Get last_seen_at = %v: the two reads must report the same fact", device.LastSeenAt, single.LastSeenAt)
			}
		case neverObservedID:
			if device.LastSeenAt != nil {
				t.Fatalf("never-observed device last_seen_at = %v, want nil", device.LastSeenAt)
			}
		default:
			t.Fatalf("unexpected device in list: %q", device.ID)
		}
	}
}

func TestTheEdgeAgentListCarriesTheLastObservationEachRowHolds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-agent-list-last-seen")
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: workspace, Name: "Agents", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	at := "2026-09-17T00:00:00Z"
	if _, err := store.SQLForTest(db).ExecContext(ctx, `INSERT INTO edge_agents (id, workspace_id, display_name, version, state, last_seen_at, created_at, updated_at, row_version) VALUES ('agent-1', ?, 'Agent One', '1.0.0', 'active', ?, ?, ?, 1)`, workspace, at, at, at); err != nil {
		t.Fatalf("insert edge agent: %v", err)
	}

	listed, err := store.NewEdgeAgentRepository(db).List(ctx, workspace)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() = %d edge agent(s), want one", len(listed))
	}
	if listed[0].LastSeenAt == nil {
		t.Fatal("the listed edge agent has no last_seen_at, so the list read the column and dropped it")
	}
	if got := listed[0].LastSeenAt.UTC().Format("2006-01-02T15:04:05Z"); got != at {
		t.Fatalf("listed last_seen_at = %s, want the stored %s", got, at)
	}
	single, err := store.NewEdgeAgentRepository(db).Get(ctx, workspace, "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if single.LastSeenAt == nil || !single.LastSeenAt.Equal(*listed[0].LastSeenAt) {
		t.Fatalf("list last_seen_at = %v, Get last_seen_at = %v: the two reads must report the same fact", listed[0].LastSeenAt, single.LastSeenAt)
	}
}
