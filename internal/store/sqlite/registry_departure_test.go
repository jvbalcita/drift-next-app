package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A departure is the absence fact the registry never had: the watcher counted
// departures and discarded them, so a device unplugged an hour ago still answered
// as present. These tests pin the fact itself - what a departure writes, what it
// deliberately does not write, and that observing it twice records one fact.

// departedObservation is the observation a departure sink is handed: the
// transport that left, carrying the fact that it is no longer reachable.
func departedObservation(observed discovery.ObservedDevice) discovery.ObservedDevice {
	departure := observed
	departure.State = discovery.LinkOffline
	return departure
}

func recordDeparture(t *testing.T, db *store.DB, workspace organizations.WorkspaceID, observations ...discovery.ObservedDevice) {
	t.Helper()
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	if err := svc.RecordDepartures(context.Background(), workspace, observations, "system", "discovery-watcher"); err != nil {
		t.Fatalf("RecordDepartures() error = %v", err)
	}
}

func TestADepartureSupersedesTheEndpointAndLeavesTheDeviceAlone(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-departure")
	profile := newArrivalWorkspace(t, db, workspace)

	usb := discovery.ObservedDevice{
		Serial:   "SER-DEPARTS",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	deviceID := scanned[0].DeviceID
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'current'`, workspace, deviceID); got != 1 {
		t.Fatalf("current endpoints before the departure = %d, want the one the scan observed", got)
	}
	before, err := store.NewDeviceRepository(db).Get(ctx, workspace, deviceID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if before.LastSeenAt == nil {
		t.Fatal("the observed device has no last_seen_at, so nothing here can tell gone from never seen")
	}

	recordDeparture(t, db, workspace, departedObservation(usb))

	// The transport is no longer current, and the record of the observation it
	// ended is still there: the departure is a fact appended to the device's
	// observation history, not a row deleted from it.
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'current'`, workspace, deviceID); got != 0 {
		t.Fatalf("current endpoints after the departure = %d, want none: the transport that left is not still observed", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'superseded' AND superseded_at IS NOT NULL`, workspace, deviceID); got != 1 {
		t.Fatalf("superseded endpoints after the departure = %d, want the observation the departure ended", got)
	}

	// The device keeps its identity and its last positive observation, so a
	// device that returns resolves to the same device rather than to a new one,
	// and the departure did not refresh the sighting it reports the end of.
	after, err := store.NewDeviceRepository(db).Get(ctx, workspace, deviceID)
	if err != nil {
		t.Fatalf("Get() after the departure error = %v, want the device row to survive", err)
	}
	if after.ID != before.ID {
		t.Fatalf("device identity after the departure = %q, want %q", after.ID, before.ID)
	}
	if after.LastSeenAt == nil || !after.LastSeenAt.Equal(*before.LastSeenAt) {
		t.Fatalf("last_seen_at after the departure = %v, want the sighting it was observed at (%v): a departure records leaving, never a sighting", after.LastSeenAt, before.LastSeenAt)
	}
	if after.State != before.State {
		t.Fatalf("lifecycle column after the departure = %q, want %q: a departure writes no lifecycle state (ARC-116)", after.State, before.State)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM audit_events WHERE workspace_id = ? AND resource_type = 'device' AND resource_id = ? AND event_name = 'device.departed'`, workspace, deviceID); got != 1 {
		t.Fatalf("device.departed audit events = %d, want the departure recorded once", got)
	}
}

// A device that was never observed holds no last positive observation, so it stays
// distinguishable from one that was observed and has since left: that difference
// is what lets a reader answer "never seen" and "gone" differently.
func TestAGoneDeviceStaysDistinguishableFromOneNeverObserved(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-departure-unknown")
	profile := newArrivalWorkspace(t, db, workspace)

	usb := discovery.ObservedDevice{
		Serial:   "SER-WAS-HERE",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	gone := scanned[0].DeviceID

	// A device row that was never observed by anything. It carries an identity
	// and no observation history at all.
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: "device-never-observed", Workspace: workspace, DisplayName: "Never observed", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// And a transport nobody ever observed leaving.
	recordDeparture(t, db, workspace, departedObservation(discovery.ObservedDevice{
		Serial: "SER-NEVER-OBSERVED", State: discovery.LinkOffline, Evidence: map[string]string{"connection": "usb"},
	}))

	goneDevice, err := store.NewDeviceRepository(db).Get(ctx, workspace, gone)
	if err != nil {
		t.Fatalf("Get(gone) error = %v", err)
	}
	neverObserved, err := store.NewDeviceRepository(db).Get(ctx, workspace, "device-never-observed")
	if err != nil {
		t.Fatalf("Get(never observed) error = %v", err)
	}
	if neverObserved.LastSeenAt != nil {
		t.Fatalf("never-observed device last_seen_at = %v, want nil", neverObserved.LastSeenAt)
	}
	if goneDevice.LastSeenAt == nil {
		t.Fatal("the device that departed lost its last positive observation, so gone and never seen became the same fact")
	}

	recordDeparture(t, db, workspace, departedObservation(usb))
	if got := countRows(t, db, `SELECT COUNT(*) FROM audit_events WHERE workspace_id = ? AND event_name = 'device.departed'`, workspace); got != 1 {
		t.Fatalf("device.departed audit events = %d, want one: the transport that left was recorded, and the transport nobody ever observed wrote nothing", got)
	}
}

// Observing the same departure twice records one fact. The watcher cannot see a
// departed transport again, but a retried batch, a restarted sink or a repeated
// poll view must not corrupt or duplicate what the first departure wrote.
func TestADepartureIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-departure-twice")
	profile := newArrivalWorkspace(t, db, workspace)

	usb := discovery.ObservedDevice{
		Serial:   "SER-DEPARTS-TWICE",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	deviceID := scanned[0].DeviceID

	departure := departedObservation(usb)
	recordDeparture(t, db, workspace, departure)
	recordDeparture(t, db, workspace, departure)

	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'superseded'`, workspace, deviceID); got != 1 {
		t.Fatalf("superseded endpoints = %d, want one fact for one departure observed twice", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want the device to survive both departures", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM audit_events WHERE workspace_id = ? AND event_name = 'device.departed'`, workspace); got != 1 {
		t.Fatalf("device.departed audit events = %d, want one: a departure already recorded is not a second fact", got)
	}
}

// A device observed again after leaving is observed at a current endpoint once
// more, which is the whole reason the departure does not delete anything.
func TestADeviceThatReturnsIsObservedAgain(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-departure-return")
	profile := newArrivalWorkspace(t, db, workspace)

	usb := discovery.ObservedDevice{
		Serial:   "SER-ROUND-TRIP",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	deviceID := scanned[0].DeviceID

	recordDeparture(t, db, workspace, departedObservation(usb))
	returned, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{usb}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	if len(returned) != 1 || returned[0].DeviceID != deviceID {
		t.Fatalf("returning transport resolved to %#v, want the identity it had before it left (%q)", returned, deviceID)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want one identity for one transport that left and returned", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'current'`, workspace, deviceID); got != 1 {
		t.Fatalf("current endpoints after the return = %d, want the observation it came back at", got)
	}
}
