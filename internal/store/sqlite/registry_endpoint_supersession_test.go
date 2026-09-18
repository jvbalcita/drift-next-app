package sqlite_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

// An endpoint change is not an identity change (ARC-162). A device that is
// activated onto a new port keeps ONE identity and ONE current endpoint: the
// endpoint it left becomes superseded, stays readable as history, and says when
// it was superseded. The board, the device list and any count read the current
// endpoint, so the device appears once - at the port it answers on now - while
// the record of the transport it left is still there to be read.
//
// The live shape this pins: the owner activated the S22 onto a port and the
// board still drew its older scanned record beside the new one, because the
// endpoint records were listed rather than the device's current one.

// observedOnUSB is a unit attached by USB: the serial it reports about itself is
// its only name, and a USB transport carries no address.
func observedOnUSB(reported string) discovery.ObservedDevice {
	return discovery.ObservedDevice{
		Serial:         reported,
		HardwareSerial: reported,
		Model:          "SM-S901B",
		DeviceName:     "S22",
		State:          discovery.LinkOnline,
		Evidence:       map[string]string{"connection": "usb"},
	}
}

// observedOnTCP is the SAME unit after it was activated onto a port: adb names it
// by the address it answers on, and the serial it reports about itself is what
// keeps it one device.
func observedOnTCP(reported, host string, port uint16) discovery.ObservedDevice {
	return discovery.ObservedDevice{
		Serial:         host + ":" + strconv.FormatUint(uint64(port), 10),
		Host:           host,
		Port:           port,
		HardwareSerial: reported,
		Model:          "SM-S901B",
		DeviceName:     "S22",
		State:          discovery.LinkOnline,
		Evidence:       map[string]string{"connection": "tcp"},
	}
}

// movedActivatedDevice observes one unit first over USB and then on the port it
// was activated onto, and returns the identity it resolved to.
func movedActivatedDevice(t *testing.T, db *store.DB, workspace organizations.WorkspaceID) devices.DeviceID {
	t.Helper()
	ctx := context.Background()
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	const reported = "R5CT42GS94Z"
	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observedOnUSB(reported)}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals(usb) error = %v", err)
	}
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observedOnTCP(reported, "192.168.1.140", 5555)}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals(tcp) error = %v", err)
	}
	if second[0].DeviceID != first[0].DeviceID {
		t.Fatalf("the move minted a second identity: %q, want %q", second[0].DeviceID, first[0].DeviceID)
	}
	return first[0].DeviceID
}

func TestAMovedDeviceSupersedesTheEndpointItLeftAndDatedIt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-endpoint-supersession")
	newArrivalWorkspace(t, db, workspace)
	deviceID := movedActivatedDevice(t, db, workspace)

	// One device, one current endpoint - and it is the port the unit was
	// activated onto, not the transport it left.
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1: moving a device does not register a second one", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND device_id = ? AND state = 'current'`, workspace, deviceID); got != 1 {
		t.Fatalf("current endpoints = %d, want exactly 1", got)
	}
	current, err := db.ListEndpoints(ctx, workspace, deviceID, true)
	if err != nil {
		t.Fatalf("ListEndpoints(current only) error = %v", err)
	}
	if len(current) != 1 || current[0].Host != "192.168.1.140" || current[0].Port != 5555 {
		t.Fatalf("current endpoint = %#v, want the activated port 192.168.1.140:5555", current)
	}
	if current[0].SupersededAt != nil {
		t.Fatalf("the current endpoint reports superseded_at = %v; the transport the device answers on is not history yet", current[0].SupersededAt)
	}

	// The transport it left is history: still on the record, marked superseded,
	// and dated so a reader can say WHEN the device left it.
	history, err := db.ListEndpoints(ctx, workspace, deviceID, false)
	if err != nil {
		t.Fatalf("ListEndpoints(history) error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("endpoint history = %d rows, want 2: the transport the device left stays on the record", len(history))
	}
	var superseded *endpoints.Endpoint
	for index := range history {
		if history[index].State == endpoints.Superseded {
			superseded = &history[index]
		}
	}
	if superseded == nil {
		t.Fatal("no superseded endpoint in the history: the transport the device left was not superseded")
	}
	if superseded.Transport != endpoints.TransportUSB {
		t.Fatalf("superseded transport = %q, want the USB transport the device was activated away from", superseded.Transport)
	}
	if superseded.SupersededAt == nil {
		t.Fatal("the superseded endpoint does not say when it was superseded")
	}
	var stored string
	if err := store.SQLForTest(db).QueryRow(`SELECT superseded_at FROM device_endpoints WHERE id = ?`, superseded.ID).Scan(&stored); err != nil {
		t.Fatalf("read superseded_at: %v", err)
	}
	if stored == "" {
		t.Fatal("device_endpoints.superseded_at is empty for a superseded row")
	}
	parsed, err := time.Parse(time.RFC3339Nano, stored)
	if err != nil {
		t.Fatalf("stored superseded_at %q is not a timestamp: %v", stored, err)
	}
	if !parsed.Equal(*superseded.SupersededAt) {
		t.Fatalf("superseded_at read back = %v, want the stored value %v", superseded.SupersededAt, parsed)
	}
}

// The projection the console reads is one row per IDENTITY carrying the CURRENT
// endpoint: seeding a device with two endpoint records - one current, one
// superseded - must produce ONE appearance, at the current one.
func TestTheDeviceListAndCountsReadOnlyTheCurrentEndpointOfAMovedDevice(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-endpoint-board")
	newArrivalWorkspace(t, db, workspace)
	movedActivatedDevice(t, db, workspace)

	// What ListDevices does: the identity list, then every device's current
	// endpoint in one read.
	listed, err := store.NewDeviceRepository(db).List(ctx, workspace)
	if err != nil {
		t.Fatalf("DeviceRepository.List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("the device list = %d rows for one device with two endpoint records, want 1 appearance", len(listed))
	}
	current, err := store.NewEndpointRepository(db).ListCurrentByDevice(ctx, workspace)
	if err != nil {
		t.Fatalf("ListCurrentByDevice() error = %v", err)
	}
	projected, observed := current[listed[0].ID]
	if !observed {
		t.Fatal("the listed device is projected with no endpoint; the board must carry the current endpoint")
	}
	if projected.Host != "192.168.1.140" || projected.Port != 5555 {
		t.Fatalf("projected endpoint = %s:%d, want the current one 192.168.1.140:5555", projected.Host, projected.Port)
	}

	// And it is COUNTED once: the read that ignores the state counts the
	// superseded record beside the current one, which is exactly what an
	// endpoint board must not do.
	every, err := db.ListEndpoints(ctx, workspace, devices.DeviceID(""), false)
	if err != nil {
		t.Fatalf("ListEndpoints(all) error = %v", err)
	}
	if len(every) != 2 {
		t.Fatalf("endpoint records = %d, want the 2 the device has (the moved-from record included)", len(every))
	}
	onlyCurrent, err := db.ListEndpoints(ctx, workspace, devices.DeviceID(""), true)
	if err != nil {
		t.Fatalf("ListEndpoints(current only) error = %v", err)
	}
	if len(onlyCurrent) != 1 {
		t.Fatalf("current endpoints counted = %d, want 1: a superseded endpoint is not counted", len(onlyCurrent))
	}
	if onlyCurrent[0].DeviceID != listed[0].ID {
		t.Fatalf("current endpoint belongs to %q, want the listed device %q", onlyCurrent[0].DeviceID, listed[0].ID)
	}
}
