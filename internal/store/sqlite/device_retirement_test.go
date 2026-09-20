package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestRetirementIsAttributedHiddenRestorableAndReappearanceIsSurfaced(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("retire-w")
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: workspace, Name: "Retire", State: organizations.WorkspaceActive}, "operator", "owner"); err != nil {
		t.Fatal(err)
	}
	deviceID := devices.DeviceID("device-retire")
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: deviceID, Workspace: workspace, DisplayName: "Bench 1", State: devices.Active}, "operator", "creator"); err != nil {
		t.Fatal(err)
	}
	decision, err := store.NewDeviceService(db).Retire(ctx, workspace, deviceID, "left the lab", "operator", "op-7")
	if err != nil {
		t.Fatal(err)
	}
	if decision.ActorID != "op-7" || !decision.Retired || decision.DecidedAt.IsZero() {
		t.Fatalf("decision = %#v", decision)
	}
	listed, err := store.NewDeviceRepository(db).List(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("default list contains retired device: %#v", listed)
	}
	listed, err = store.NewDeviceRepository(db).ListIncludingRetired(ctx, workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !listed[0].Retired {
		t.Fatalf("retired list = %#v", listed)
	}
	if _, err := store.SQLForTest(db).ExecContext(ctx, `INSERT INTO device_endpoints (id,workspace_id,device_id,endpoint_type,serial,state,link_state,observed_at) VALUES ('returned','retire-w','device-retire','adb_usb','SER','current','online','2999-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	listed, err = store.NewDeviceRepository(db).List(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !listed[0].Retired || !listed[0].ObservedAgain {
		t.Fatalf("reappeared retired device = %#v", listed)
	}
	if _, err := store.NewDeviceService(db).Restore(ctx, workspace, deviceID, "expected again", "operator", "op-8"); err != nil {
		t.Fatal(err)
	}
	restored, err := store.NewDeviceRepository(db).Get(ctx, workspace, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Retired {
		t.Fatalf("restored device remains retired: %#v", restored)
	}
}

func TestPermanentDeletionRetainsEvidenceAndReturnGetsNewIdentity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("delete-w")
	profile := newArrivalWorkspace(t, db, workspace)
	observation := discovery.ObservedDevice{Serial: "transport-1", HardwareSerial: "HW-DELETE", Model: "Pixel", State: discovery.LinkOnline, Evidence: map[string]string{"source": "fake"}}
	scanner := discovery.NewFakeScanner([]discovery.ObservedDevice{observation})
	svc := discovery.NewService(db, scanner)
	_, first, err := svc.StartScan(ctx, workspace, profile.ID, "scan-before-delete", "system", "watcher")
	if err != nil {
		t.Fatal(err)
	}
	oldID := first[0].DeviceID
	if _, refusal, err := store.NewDeviceService(db).Delete(ctx, workspace, oldID, string(oldID), "gone", "operator", "op-1"); err != nil || refusal == nil || refusal.Reason != devices.DeleteRefusalDeviceAttached {
		t.Fatalf("attached deletion: refusal=%#v err=%v", refusal, err)
	}
	if _, err := store.SQLForTest(db).ExecContext(ctx, `UPDATE device_endpoints SET state='superseded',superseded_at='2026-01-01T00:00:00Z' WHERE workspace_id=? AND device_id=? AND state='current'`, workspace, oldID); err != nil {
		t.Fatal(err)
	}
	deleted, refusal, err := store.NewDeviceService(db).Delete(ctx, workspace, oldID, string(oldID), "gone for good", "operator", "op-1")
	if err != nil || refusal != nil {
		t.Fatalf("Delete() refusal=%#v err=%v", refusal, err)
	}
	if deleted.ActorID != "op-1" || deleted.ID == "" {
		t.Fatalf("deletion = %#v", deleted)
	}
	var endpoints, deletions int
	if err := store.SQLForTest(db).QueryRowContext(ctx, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id=? AND device_id=?`, workspace, oldID).Scan(&endpoints); err != nil {
		t.Fatal(err)
	}
	if err := store.SQLForTest(db).QueryRowContext(ctx, `SELECT COUNT(*) FROM device_deletions WHERE workspace_id=? AND device_id=? AND actor_id='op-1'`, workspace, oldID).Scan(&deletions); err != nil {
		t.Fatal(err)
	}
	if endpoints == 0 || deletions != 1 {
		t.Fatalf("retained endpoints=%d deletion records=%d", endpoints, deletions)
	}
	_, again, err := svc.StartScan(ctx, workspace, profile.ID, "scan-after-delete", "system", "watcher")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].DeviceID == oldID {
		t.Fatalf("returned device reused deleted identity %q", oldID)
	}
	var linkedOld string
	if err := store.SQLForTest(db).QueryRowContext(ctx, `SELECT deleted_device_id FROM device_deletion_returns WHERE workspace_id=? AND returned_device_id=?`, workspace, again[0].DeviceID).Scan(&linkedOld); err != nil {
		t.Fatal(err)
	}
	if linkedOld != string(oldID) {
		t.Fatalf("return links deleted identity %q, want %q", linkedOld, oldID)
	}
}

func TestPermanentDeletionRequiresExactDeviceID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("confirm-w")
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: workspace, Name: "Confirm", State: organizations.WorkspaceActive}, "operator", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{ID: "device-exact", Workspace: workspace, DisplayName: "Same name", State: devices.Active}, "operator", "owner"); err != nil {
		t.Fatal(err)
	}
	_, refusal, err := store.NewDeviceService(db).Delete(ctx, workspace, "device-exact", "Same name", "gone", "operator", "op-1")
	if err != nil || refusal == nil || refusal.Reason != devices.DeleteRefusalConfirmationMismatch {
		t.Fatalf("refusal=%#v err=%v", refusal, err)
	}
}
