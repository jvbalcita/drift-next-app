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
