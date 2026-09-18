package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

// A device can be reachable on more than one transport at the same time. The
// real case observed on the owner's console: one SM-S908E answering on its USB
// serial AND on 192.168.1.106:5556 over TCP, both live, both drawn.
//
// Done bar 3: "A dropped endpoint reconnects without minting a second identity
// — assert one device_id, not two." The registry keys a device on
// (workspace_id, serial) and keeps the transport as the mutable fact, so the
// same serial observed over a second transport must resolve to the SAME device
// and add an endpoint, never create a second device.
func TestOneSerialOnTwoTransportsKeepsOneDeviceIdentity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.Workspace{ID: "w-identity", Name: "Identity", State: organizations.WorkspaceActive, RowVersion: 1}
	if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op"); err != nil {
		t.Fatal(err)
	}

	// FinishScan refuses a run that is not in state 'running' (and refuses an
	// unknown run outright), so each observation gets its own running run.
	startScan := func(id string) {
		t.Helper()
		if _, err := store.SQLForTest(db).Exec(
			`INSERT INTO scan_runs (id, workspace_id, network_profile_id, state, requested_at, idempotency_key) VALUES (?, ?, ?, 'running', ?, ?)`,
			id, workspace.ID, "profile-1", "2026-09-17T00:00:00Z", id,
		); err != nil {
			t.Fatalf("insert scan run %s: %v", id, err)
		}
	}

	// First observation: the device attached by USB. The serial is the identity.
	usb := discovery.ObservedDevice{
		Serial:   "SER-USB-1",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	startScan("scan-usb")
	_, first, err := db.FinishScan(ctx, workspace.ID, "scan-usb", []discovery.ObservedDevice{usb}, "operator", "op")
	if err != nil {
		t.Fatalf("FinishScan(usb): %v", err)
	}

	// Second observation: the SAME device, now reachable over TCP on a port the
	// profile does not accept. Same serial, different transport.
	tcp := discovery.ObservedDevice{
		Serial:   "SER-USB-1",
		Host:     "192.168.1.106",
		Port:     5556,
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "tcp"},
	}
	startScan("scan-tcp")
	_, second, err := db.FinishScan(ctx, workspace.ID, "scan-tcp", []discovery.ObservedDevice{tcp}, "operator", "op")
	if err != nil {
		t.Fatalf("FinishScan(tcp): %v", err)
	}

	if first[0].DeviceID != second[0].DeviceID {
		t.Fatalf("the same serial on two transports minted two identities: first=%q second=%q; one device must keep one device_id",
			first[0].DeviceID, second[0].DeviceID)
	}

	var devices int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace.ID).Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if devices != 1 {
		t.Fatalf("devices = %d, want 1: a second transport is not a second device", devices)
	}

	// Both transports stay on the record: the off-port one is precisely what
	// port activation exists for, so losing it here would make Activate
	// unreachable.
	var endpointRows int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ?`, workspace.ID).Scan(&endpointRows); err != nil {
		t.Fatal(err)
	}
	if endpointRows != 2 {
		t.Fatalf("endpoints = %d, want 2: both transports must be recorded against the one device", endpointRows)
	}

	// And the transport is the mutable half: the newest observation is current.
	var current int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'current'`, workspace.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatalf("current endpoints = %d, want 1: superseding history must not leave two current transports", current)
	}

	// The same device_id must be what the client is given back, not a fresh one.
	if first[0].DeviceID != second[0].DeviceID || string(second[0].DeviceID) == "" {
		t.Fatalf("second observation returned device_id %q; want the stable identity from the first", second[0].DeviceID)
	}

	// Both transports carry their own transport on the one identity: the serial
	// observed over USB and then over TCP records USB and TCP — not two copies of
	// one answer, and not a transport invented from the address of the other.
	recorded, err := db.ListEndpoints(ctx, workspace.ID, first[0].DeviceID, false)
	if err != nil {
		t.Fatalf("ListEndpoints() = %v", err)
	}
	seen := map[endpoints.Transport]int{}
	for _, endpoint := range recorded {
		seen[endpoint.Transport]++
	}
	if seen[endpoints.TransportUSB] != 1 || seen[endpoints.TransportTCP] != 1 {
		t.Fatalf("transports recorded against device %q = %v, want one USB and one TCP transport", first[0].DeviceID, seen)
	}

	// The transport is the mutable half and the current one is the newest
	// observation, so the device the console reads is reachable over TCP — while
	// the USB transport it was observed over stays on the record instead of
	// being lost.
	currentOnly, err := db.ListEndpoints(ctx, workspace.ID, first[0].DeviceID, true)
	if err != nil {
		t.Fatalf("ListEndpoints(current only) = %v", err)
	}
	if len(currentOnly) != 1 || currentOnly[0].Transport != endpoints.TransportTCP {
		t.Fatalf("current transport = %#v, want exactly the TCP transport the newest observation recorded", currentOnly)
	}
}

// The identity of a TCP device cannot be its transport: adb names a TCP device
// by the address it answers on, so the serial the registry keys on changes the
// moment the device's address does. Observed live: every unit in the lab ended
// up registered twice (45 device rows for a fleet of about 22) after the fleet
// took new DHCP leases, because each new address arrived as a serial no device
// row held. The serial the DEVICE reports for itself is what must keep the
// identity, so one unit answers to one device_id wherever it is observed.
func TestAReportedSerialKeepsOneIdentityAcrossATransportMove(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-identity-move")
	newArrivalWorkspace(t, db, workspace)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))

	// The unit answers on 192.168.1.123 and reports the serial it carries.
	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:         "192.168.1.123:5555",
		Host:           "192.168.1.123",
		Port:           5555,
		HardwareSerial: "R58M43QGSQX",
		Model:          "SM-G9750",
		DeviceName:     "ALTA 13",
		State:          discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}

	// The same unit, now answering on a new lease, reporting the same serial.
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:         "192.168.1.134:5555",
		Host:           "192.168.1.134",
		Port:           5555,
		HardwareSerial: "R58M43QGSQX",
		Model:          "SM-G9750",
		DeviceName:     "ALTA 13",
		State:          discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if second[0].DeviceID != first[0].DeviceID {
		t.Fatalf("a new address minted a second identity: first=%q second=%q; one device must keep one device_id",
			first[0].DeviceID, second[0].DeviceID)
	}
	if !second[0].Known {
		t.Fatal("the moved device reported Known = false, want true: its reported serial was already registered")
	}

	// And a third move, to a lease nobody has seen at all.
	third, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:         "192.168.1.150:5555",
		Host:           "192.168.1.150",
		Port:           5555,
		HardwareSerial: "R58M43QGSQX",
		Model:          "SM-G9750",
		DeviceName:     "ALTA 13",
		State:          discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("third RecordArrivals() error = %v", err)
	}
	if third[0].DeviceID != first[0].DeviceID {
		t.Fatalf("a second move minted a third identity: %q, want %q", third[0].DeviceID, first[0].DeviceID)
	}

	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1: three addresses of one device are not three devices", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ?`, workspace); got != 3 {
		t.Fatalf("endpoints = %d, want 3: every address the unit was observed at stays on the record", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'current'`, workspace); got != 1 {
		t.Fatalf("current endpoints = %d, want 1: only the newest transport is current", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ? AND hardware_serial = 'R58M43QGSQX'`, workspace); got != 1 {
		t.Fatalf("rows carrying the reported serial = %d, want 1", got)
	}
}

// A device registered before the registry could match on a reported serial — and
// every row the live plane already holds — carries none, so the first
// observation that reports one must be matched by the transport it arrives on
// and learn the serial then. Without that the fix would only ever help rows that
// already had one, and the fleet would keep splitting.
func TestAnObservationLearnsTheReportedSerialItResolvesTo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-identity-learn")
	newArrivalWorkspace(t, db, workspace)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))

	// The row as it exists today: registered by address, no reported serial.
	registered, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:     "192.168.1.123:5555",
		Host:       "192.168.1.123",
		Port:       5555,
		Model:      "SM-G9750",
		DeviceName: "ALTA 13",
		State:      discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ? AND hardware_serial IS NULL`, workspace); got != 1 {
		t.Fatalf("rows without a reported serial = %d, want 1: an observation that reported none must not invent one", got)
	}

	// The same address, now reporting the serial it carries: the identity is the
	// one already registered there, and it learns the serial.
	learned, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:         "192.168.1.123:5555",
		Host:           "192.168.1.123",
		Port:           5555,
		HardwareSerial: "R58M43QGSQX",
		Model:          "SM-G9750",
		DeviceName:     "ALTA 13",
		State:          discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if learned[0].DeviceID != registered[0].DeviceID {
		t.Fatalf("learning a reported serial minted a new identity: %q, want %q", learned[0].DeviceID, registered[0].DeviceID)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ? AND hardware_serial = 'R58M43QGSQX'`, workspace); got != 1 {
		t.Fatalf("rows carrying the learned serial = %d, want 1", got)
	}

	// The move that used to split the fleet now resolves through what was learned.
	moved, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:         "192.168.1.134:5555",
		Host:           "192.168.1.134",
		Port:           5555,
		HardwareSerial: "R58M43QGSQX",
		Model:          "SM-G9750",
		DeviceName:     "ALTA 13",
		State:          discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("third RecordArrivals() error = %v", err)
	}
	if moved[0].DeviceID != registered[0].DeviceID {
		t.Fatalf("the move after learning minted a second identity: %q, want %q", moved[0].DeviceID, registered[0].DeviceID)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1", got)
	}
}

// A device that reports no serial is matched exactly as it was before the
// registry could ask for one: by its transport. A fake device, and a transport
// adb lists without a device behind it, must keep working that way rather than
// failing to register at all.
func TestAnObservationWithoutAReportedSerialStillMatchesByItsTransport(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-identity-transport")
	newArrivalWorkspace(t, db, workspace)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))

	observation := discovery.ObservedDevice{
		Serial:     "192.0.2.10:5555",
		Host:       "192.0.2.10",
		Port:       5555,
		Model:      "SM-G9750",
		DeviceName: "ALTA-FAKE",
		State:      discovery.LinkOnline,
	}
	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observation}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observation}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if first[0].DeviceID != second[0].DeviceID {
		t.Fatalf("an unchanged transport minted a second identity: %q then %q", first[0].DeviceID, second[0].DeviceID)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1 after two observations of one transport", got)
	}
}
