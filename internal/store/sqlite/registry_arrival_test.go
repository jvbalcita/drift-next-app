package sqlite_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// The arrival path a post-launch watcher feeds: the watcher enumerated the
// transports itself, so it records observations without opening a scan run. It
// must still land on the serial-keyed upsert the startup scan uses, because the
// device it just saw on a second transport already has an identity, and minting
// a second one is the 22-cards-for-21-devices symptom.

func newArrivalWorkspace(t *testing.T, db *store.DB, workspace organizations.WorkspaceID) networkprofiles.NetworkProfile {
	t.Helper()
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: workspace, Name: string(workspace), State: organizations.WorkspaceActive, RowVersion: 1}, "operator", "op"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	profile := networkprofiles.NetworkProfile{
		ID:            "profile-arrivals",
		Workspace:     workspace,
		Name:          "Lab range",
		AddressPolicy: "192.0.2.0/28",
		Ports:         []uint16{5555},
		IsDefault:     true,
	}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op"); err != nil {
		t.Fatalf("create network profile: %v", err)
	}
	return profile
}

func countRows(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := store.SQLForTest(db).QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count(%s): %v", query, err)
	}
	return count
}

// A device observed by the startup scan on USB and later by the watcher on a
// second transport must keep one identity: the arrival upserts the serial the
// scan already registered instead of creating a second device.
func TestWatcherArrivalOnASecondTransportKeepsTheStartupDeviceIdentity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-two-transports")
	profile := newArrivalWorkspace(t, db, workspace)

	// The startup scan, through the same machinery main.go starts at launch.
	usb := discovery.ObservedDevice{
		Serial:   "SER-USB-ARRIVAL",
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedDevice{usb}))
	_, scanned, err := svc.StartScan(ctx, workspace, profile.ID, "startup-scan-key", "system", "control-plane")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	if len(scanned) != 1 || scanned[0].DeviceID == "" {
		t.Fatalf("StartScan() observations = %#v, want one observed device carrying an identity", scanned)
	}

	// The arrival: the same device, now answering over TCP on a port the profile
	// does not accept. Same serial, second transport.
	tcp := discovery.ObservedDevice{
		Serial:   "SER-USB-ARRIVAL",
		Host:     "192.168.1.106",
		Port:     5556,
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "tcp"},
	}
	arrived, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{tcp}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	if len(arrived) != 1 {
		t.Fatalf("RecordArrivals() returned %d observations, want 1", len(arrived))
	}
	if arrived[0].DeviceID != scanned[0].DeviceID {
		t.Fatalf("arrival minted a second identity: scan=%q arrival=%q; one serial keeps one device_id",
			scanned[0].DeviceID, arrived[0].DeviceID)
	}
	if !arrived[0].Known {
		t.Fatal("arrival reported Known = false, want true: the serial was already registered by the startup scan")
	}
	if arrived[0].EndpointID == "" || arrived[0].EndpointID == scanned[0].EndpointID {
		t.Fatalf("arrival endpoint = %q, want a new endpoint beside the scanned one %q", arrived[0].EndpointID, scanned[0].EndpointID)
	}

	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1: a second transport is not a second device", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ?`, workspace); got != 2 {
		t.Fatalf("endpoints = %d, want 2: both transports stay on the record", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'current'`, workspace); got != 1 {
		t.Fatalf("current endpoints = %d, want 1: the newest transport is current, the other is superseded", got)
	}
}

// A transport nobody has seen before is registered by its arrival, exactly as a
// scan would register it, and the arrival opens no scan run.
func TestWatcherArrivalRegistersAFirstSightTransportWithoutAScanRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-first-sight")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	arrived, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:   "SER-FIRST-SIGHT",
		Model:    "SM-G9750",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "usb"},
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	if len(arrived) != 1 || arrived[0].DeviceID == "" {
		t.Fatalf("RecordArrivals() observations = %#v, want one device with a fresh identity", arrived)
	}
	if arrived[0].Known {
		t.Fatal("arrival reported Known = true for a transport no scan had observed")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM scan_runs WHERE workspace_id = ?`, workspace); got != 0 {
		t.Fatalf("scan_runs = %d, want 0: an arrival is an observation, not a scan", got)
	}
	// The observation still lands on the canonical evidence trail, under the
	// same event a scan records.
	if got := countRows(t, db, `SELECT COUNT(*) FROM audit_events WHERE workspace_id = ? AND resource_type = 'device' AND event_name = 'device.observed'`, workspace); got != 1 {
		t.Fatalf("device.observed audit rows = %d, want 1", got)
	}
}

// The watcher polls, so it re-reports a transport that has not changed. A
// repeated poll must not mint a second device, a second endpoint, or a new
// current endpoint: only an observation is refreshed.
func TestRepeatedWatcherPollsOnAnUnchangedTransportMintNothing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-repeat")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observation := discovery.ObservedDevice{
		Serial:   "SER-STEADY",
		Host:     "192.0.2.9",
		Port:     5555,
		Model:    "SM-S908E",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"connection": "tcp"},
	}
	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observation}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{observation}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if first[0].DeviceID != second[0].DeviceID || first[0].EndpointID != second[0].EndpointID {
		t.Fatalf("an unchanged transport moved identity: device %q→%q endpoint %q→%q",
			first[0].DeviceID, second[0].DeviceID, first[0].EndpointID, second[0].EndpointID)
	}
	if !second[0].Known {
		t.Fatal("second poll reported Known = false, want true: the transport was already registered")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("devices = %d, want 1 after two polls", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ?`, workspace); got != 1 {
		t.Fatalf("endpoints = %d, want 1 after two polls of the same transport", got)
	}
}

// A poll that found nothing new is not a failure.
func TestWatcherArrivalOfNothingIsANoOp(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-empty")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	arrived, err := svc.RecordArrivals(ctx, workspace, nil, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals(nil) error = %v, want no error", err)
	}
	if len(arrived) != 0 {
		t.Fatalf("RecordArrivals(nil) = %#v, want nothing observed", arrived)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 0 {
		t.Fatalf("devices = %d, want 0", got)
	}
}

// A batch carrying one unidentifiable transport is refused whole: no arrival is
// half-applied, and the valid observation beside it is not persisted either.
func TestWatcherArrivalRefusesAnUnidentifiableObservationWhole(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-batch")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	_, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{
		{Serial: "SER-VALID", State: discovery.LinkOnline},
		{Port: 5556, State: discovery.LinkOnline}, // neither a serial nor a host: nothing to identify
	}, "system", "discovery-watcher")
	if err == nil {
		t.Fatal("RecordArrivals() error = nil, want the batch refused")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 0 {
		t.Fatalf("devices = %d, want 0: a refused batch persists nothing, including its valid member", got)
	}
}

// The arrival path keeps the evidence gate the scan path applies: evidence
// carrying credential material is refused and reaches no row.
func TestWatcherArrivalRefusesEvidenceCarryingCredentialMaterial(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-evidence")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	_, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:   "SER-EVIDENCE",
		State:    discovery.LinkOnline,
		Evidence: map[string]string{"token": "abc123"},
	}}, "system", "discovery-watcher")
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("RecordArrivals() code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 0 {
		t.Fatalf("devices = %d, want 0: refused evidence reaches no row", got)
	}
}

// An arrival is attributed: the audit trail names the actor that recorded it.
func TestWatcherArrivalRequiresAnActor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-actor")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	_, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-ACTOR",
		State:  discovery.LinkOnline,
	}}, "", "")
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("RecordArrivals() code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM devices WHERE workspace_id = ?`, workspace); got != 0 {
		t.Fatalf("devices = %d, want 0: an unattributed arrival is not persisted", got)
	}
}

// An unauthorized transport IS where the device is. The registry records it as
// the device's current endpoint, carrying the link state the adapter observed, so
// a surface can show the very unit an operator has to authorize. What the row
// does NOT do is make the transport usable: the action path reads the link state
// and refuses over it, naming what the transport reported (ARC-196).
func TestUnauthorizedArrivalIsTheCurrentTransportAndNotUsable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-unauthorized")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-UNAUTHORIZED",
		Host:   "192.0.2.9",
		Port:   5555,
		Model:  "SM-G9750",
		State:  discovery.LinkUnauthorized,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	if len(observed) != 1 {
		t.Fatalf("RecordArrivals() returned %d observations, want one", len(observed))
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'current'`, workspace); got != 1 {
		t.Fatalf("current endpoints = %d, want the unauthorized transport to be the device's current one: the unit is attached, and a registry that says it is nowhere is why it was invisible", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'observed'`, workspace); got != 0 {
		t.Fatalf("observed-only endpoints = %d, want none: an attached transport is the current one", got)
	}
	current, err := store.NewEndpointRepository(db).ListCurrent(ctx, workspace, observed[0].DeviceID)
	if err != nil {
		t.Fatalf("ListCurrent() error = %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("ListCurrent() = %d endpoint(s), want one", len(current))
	}
	if current[0].LinkState != endpoints.LinkStateUnauthorized {
		t.Fatalf("current endpoint link state = %q, want unauthorized: the adapter's own reading is what the record carries", current[0].LinkState)
	}
	if current[0].LinkState.Usable() {
		t.Fatal("an unauthorized transport reads as usable, so an action could be dispatched over a device that never authorized this host")
	}
	if current[0].Serial != "SER-UNAUTHORIZED" || current[0].Host != "192.0.2.9" || current[0].Port != 5555 {
		t.Fatalf("current endpoint = %#v, want the transport the observation named", current[0])
	}

	// One current endpoint per device survives a second observation of the same
	// transport: the row is refreshed, not duplicated.
	if _, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-UNAUTHORIZED", Host: "192.0.2.9", Port: 5555, State: discovery.LinkUnauthorized,
	}}, "system", "discovery-watcher"); err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE workspace_id = ? AND state = 'current'`, workspace); got != 1 {
		t.Fatalf("current endpoints after a repeat observation = %d, want still one", got)
	}

	device, err := store.NewDeviceRepository(db).Get(ctx, workspace, observed[0].DeviceID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if device.DisplayName != "SM-G9750" || device.PlatformVersion != "SM-G9750" {
		t.Fatalf("device projection = %#v, want the adapter model refreshed into name and phone-model source", device)
	}
}

// A host that may not open the transport at all is its own reading, not the
// device refusing this host: the operator fixes that one at the machine, and a
// registry that spells both `unauthorized` sends them to the wrong place.
func TestNoPermissionsArrivalIsRecordedAsItsOwnLinkState(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-no-permissions")
	newArrivalWorkspace(t, db, workspace)

	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-NO-PERMISSIONS",
		State:  discovery.LinkNoPermissions,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	current, err := store.NewEndpointRepository(db).ListCurrent(ctx, workspace, observed[0].DeviceID)
	if err != nil {
		t.Fatalf("ListCurrent() error = %v", err)
	}
	if len(current) != 1 || current[0].LinkState != endpoints.LinkStateNoPermissions {
		t.Fatalf("current endpoint = %#v, want one carrying the no-permissions link state", current)
	}
	if current[0].LinkState.Usable() {
		t.Fatal("a transport this host may not open reads as usable")
	}
}

// A later adapter observation can provide the model after the first transport
// sighting only supplied a serial/address. The transport-shaped placeholder is
// replaceable; an operator-owned name is not.
func TestObservationRefreshesGeneratedDeviceNameAndPhoneModel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-model-refresh")
	newArrivalWorkspace(t, db, workspace)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))

	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-MODEL-REFRESH",
		Host:   "192.0.2.10",
		Port:   5555,
		State:  discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial: "SER-MODEL-REFRESH",
		Host:   "192.0.2.10",
		Port:   5555,
		Model:  "SM-G9750",
		State:  discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if first[0].DeviceID != second[0].DeviceID {
		t.Fatalf("model refresh minted a new device identity: first=%q second=%q", first[0].DeviceID, second[0].DeviceID)
	}
	device, err := store.NewDeviceRepository(db).Get(ctx, workspace, first[0].DeviceID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if device.DisplayName != "SM-G9750" || device.PlatformVersion != "SM-G9750" {
		t.Fatalf("refreshed device = %#v, want model-backed display name and phone-model source", device)
	}
}

func TestObservationCapturesAndRefreshesAndroidDeviceName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("w-arrival-device-name")
	newArrivalWorkspace(t, db, workspace)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))

	first, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:     "SER-DEVICE-NAME",
		Model:      "SM-G9750",
		DeviceName: "ALTA 1",
		State:      discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("first RecordArrivals() error = %v", err)
	}
	second, err := svc.RecordArrivals(ctx, workspace, []discovery.ObservedDevice{{
		Serial:     "SER-DEVICE-NAME",
		Model:      "SM-G9750",
		DeviceName: "MEKENI 21",
		State:      discovery.LinkOnline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("second RecordArrivals() error = %v", err)
	}
	if first[0].DeviceID != second[0].DeviceID {
		t.Fatalf("device-name refresh minted a new device identity: first=%q second=%q", first[0].DeviceID, second[0].DeviceID)
	}
	device, err := store.NewDeviceRepository(db).Get(ctx, workspace, first[0].DeviceID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if device.DisplayName != "MEKENI 21" || device.PlatformVersion != "SM-G9750" {
		t.Fatalf("device projection = %#v, want the captured name and phone model", device)
	}
}
