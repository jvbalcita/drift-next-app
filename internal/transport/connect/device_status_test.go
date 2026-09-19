package transportconnect_test

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/endpoints"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The wire device status is what every consumer reads, and the console turns it
// into an offer of control. These tests pin it to observation facts: a device is
// only reported as reachable while something has observed it, a device nobody has
// observed is not reported as reachable at all, and a device that was observed and
// has since left is reported differently from one that was never seen.
//
// Two of the cases below are fault-injection anchors, and they fail in opposite
// directions if the derivation is re-pointed at devices.State:
//   - a device whose row is `active` and that nobody has observed would be reported
//     ONLINE (the defect this card is about), and
//   - a device whose row says `retired` while it is currently observed would be
//     reported OFFLINE (a device the operator can reach, hidden).

const statusWorkspace = organizations.WorkspaceID("workspace-a")

// observe records a transport the way the post-launch watcher does, and returns
// the identity the serial-keyed upsert resolved it to.
func observe(t *testing.T, db *store.DB, serial string) devices.DeviceID {
	t.Helper()
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(context.Background(), statusWorkspace, []discovery.ObservedDevice{{
		Serial: serial, State: discovery.LinkOnline, Evidence: map[string]string{"connection": "usb"},
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals(%s) error = %v", serial, err)
	}
	if len(observed) != 1 || observed[0].DeviceID == "" {
		t.Fatalf("RecordArrivals(%s) = %#v, want one device carrying an identity", serial, observed)
	}
	return observed[0].DeviceID
}

func depart(t *testing.T, db *store.DB, serial string) {
	t.Helper()
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	if err := svc.RecordDepartures(context.Background(), statusWorkspace, []discovery.ObservedDevice{{
		Serial: serial, State: discovery.LinkOffline, Evidence: map[string]string{"connection": "usb"},
	}}, "system", "discovery-watcher"); err != nil {
		t.Fatalf("RecordDepartures(%s) error = %v", serial, err)
	}
}

func statusOf(t *testing.T, db *store.DB, id devices.DeviceID) *driftv1.Device {
	t.Helper()
	response, err := transportconnect.NewDeviceHandler(db).GetDevice(context.Background(), connectrpc.NewRequest(&driftv1.GetDeviceRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(statusWorkspace)},
		DeviceId:  string(id),
	}))
	if err != nil {
		t.Fatalf("GetDevice(%s) error = %v", id, err)
	}
	return response.Msg.GetDevice()
}

func listDevicesForTest(t *testing.T, db *store.DB) []*driftv1.Device {
	t.Helper()
	response, err := transportconnect.NewDeviceHandler(db).ListDevices(context.Background(), connectrpc.NewRequest(&driftv1.ListDevicesRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(statusWorkspace)},
	}))
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	return response.Msg.GetDevices()
}

func TestTheDeviceStatusIsDerivedFromObservationFactsNotTheLifecycleColumn(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()

	// Observed, and still attached: the endpoint observation is current, so the
	// device reads ONLINE.
	observed := observe(t, db, "SER-OBSERVED")

	// A device row that exists and says `active`, which nothing has ever observed.
	// The lifecycle reading alone would call this reachable; fail closed instead.
	neverObserved := devices.DeviceID("device-never-observed")
	if err := store.NewDeviceService(db).Create(ctx, devices.Device{
		ID: neverObserved, Workspace: statusWorkspace, DisplayName: "Never observed", State: devices.Active,
	}, "operator", "op-1"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Observed, then observed leaving: a last positive observation and no current
	// endpoint.
	departed := observe(t, db, "SER-DEPARTED")
	depart(t, db, "SER-DEPARTED")

	// The ruled-out machine set to one of its ruled-out readings while the device
	// is currently observed. The status must not follow it. (This is the parked
	// transition seam's remaining use: it is how a test reaches a retired row.)
	if err := store.NewDeviceService(db).Transition(ctx, statusWorkspace, observed, devices.Retired, 1, "operator", "op-1"); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	observedDevice := statusOf(t, db, observed)
	if got := observedDevice.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("currently observed device status = %v, want ONLINE: it is observed at a current endpoint", got)
	}
	if observedDevice.GetLastSeenAt() == "" {
		t.Fatal("the observed device carries no last_seen_at, so the fact the status is derived from does not reach the wire")
	}

	neverObservedDevice := statusOf(t, db, neverObserved)
	// Criterion 2 of ARC-146: "never observed" is its own reading, and pinning it
	// to UNSPECIFIED is what keeps it distinguishable from the device that was
	// observed and is not observed now (ARC-130's distinction).
	if got := neverObservedDevice.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED {
		t.Fatalf("never-observed device status = %v, want UNSPECIFIED: a device nobody has seen is not a device anyone can reach, and it is not the same reading as one that left", got)
	}
	if neverObservedDevice.GetLastSeenAt() != "" {
		t.Fatalf("never-observed device last_seen_at = %q, want empty", neverObservedDevice.GetLastSeenAt())
	}

	departedDevice := statusOf(t, db, departed)
	if got := departedDevice.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_OFFLINE {
		t.Fatalf("departed device status = %v, want OFFLINE: it was observed and is not observed now", got)
	}
	if departedDevice.GetLastSeenAt() == "" {
		t.Fatal("the departed device lost its last positive observation, so gone and never seen became the same fact")
	}
	if departedDevice.GetStatus() == neverObservedDevice.GetStatus() {
		t.Fatalf("gone and never observed both report %v, want them distinguishable", departedDevice.GetStatus())
	}
	if departedDevice.GetEndpointId() != "" {
		t.Fatalf("departed device endpoint = %q, want none", departedDevice.GetEndpointId())
	}
}

// The console reads the device list, and the list path is not the single-row path.
// A status derived from an observation the list does not carry would be derived
// from nothing, so the list is asserted separately.
func TestTheDeviceListReportsAStatusDerivedFromTheObservationsItCarries(t *testing.T) {
	db := openProductDB(t)
	observed := observe(t, db, "SER-LISTED")

	listed := listDevicesForTest(t, db)
	if len(listed) != 1 {
		t.Fatalf("ListDevices() = %d device(s), want one", len(listed))
	}
	if listed[0].GetId() != string(observed) {
		t.Fatalf("listed device = %q, want %q", listed[0].GetId(), observed)
	}
	if got := listed[0].GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("listed device status = %v, want ONLINE", got)
	}
	if listed[0].GetLastSeenAt() == "" {
		t.Fatal("the listed device carries no last_seen_at: the list read the observation and dropped it")
	}

	// And the device stops being reported as reachable once nothing is observing
	// it, rather than staying healthy because its row is still `active`.
	depart(t, db, "SER-LISTED")
	listed = listDevicesForTest(t, db)
	if len(listed) != 1 {
		t.Fatalf("ListDevices() after the departure = %d device(s), want the device to keep its identity", len(listed))
	}
	if got := listed[0].GetStatus(); got == driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("departed device status = %v, want anything but ONLINE: nothing is observing it", got)
	}
	if listed[0].GetLastSeenAt() == "" {
		t.Fatal("the departed device lost its last positive observation")
	}
}

// An unauthorized device is ATTACHED, and an operator has to be able to see it,
// tell it apart from a device that is gone, and read WHY it cannot be used. The
// transport the adapter read is reported, the authorization state is reported,
// and the status is not ONLINE: the plane has no usable transport for it.
func TestAnUnauthorizedObservationIsListedWithItsTransportAndState(t *testing.T) {
	db := openProductDB(t)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(context.Background(), statusWorkspace, []discovery.ObservedDevice{{
		Serial: "SER-UNAUTHORIZED-LIST",
		State:  discovery.LinkUnauthorized,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	projected := statusOf(t, db, observed[0].DeviceID)
	if got := projected.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_UNAUTHORIZED {
		t.Fatalf("unauthorized device status = %v, want UNAUTHORIZED: the device is attached and this host is not authorized by it", got)
	}
	if got := projected.GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("unauthorized device transport = %v, want USB: the adapter read the transport, so the projection must not report none", got)
	}
	if projected.GetEndpointId() == "" {
		t.Fatal("unauthorized device carries no endpoint id, so nothing downstream can place the unit the operator has to authorize")
	}
	if projected.GetLastSeenAt() == "" {
		t.Fatal("unauthorized observation lost last_seen_at, so the console cannot distinguish it from never observed")
	}

	// The list path is the path the console's device board reads, and the USB
	// view it draws is filtered on exactly this transport.
	listed := listDevicesForTest(t, db)
	if len(listed) != 1 || listed[0].GetId() != string(observed[0].DeviceID) {
		t.Fatalf("ListDevices() = %#v, want the unauthorized unit listed", listed)
	}
	if got := listed[0].GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("listed unauthorized device transport = %v, want USB", got)
	}
	if got := listed[0].GetStatus(); got == driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("listed unauthorized device status = %v, want anything but ONLINE: no action can be dispatched over it", got)
	}
}

// A transport this host may not open is a DIFFERENT reading from a device that
// refuses this host: one is fixed at the machine, the other on the device. Both
// are attached, and neither may read as ONLINE.
func TestANoPermissionsObservationIsListedAsItsOwnState(t *testing.T) {
	db := openProductDB(t)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(context.Background(), statusWorkspace, []discovery.ObservedDevice{{
		Serial: "SER-NO-PERMS-LIST",
		State:  discovery.LinkNoPermissions,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	projected := statusOf(t, db, observed[0].DeviceID)
	if got := projected.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_NO_PERMISSIONS {
		t.Fatalf("no-permissions device status = %v, want NO_PERMISSIONS", got)
	}
	if got := projected.GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("no-permissions device transport = %v, want USB", got)
	}
}

// A device that is attached but not answering is attached: it is neither "gone"
// nor "never seen", and the transport it is at is the fact that says so. Only
// the reading changes, from ONLINE to OFFLINE.
func TestAnOfflineObservationStillCarriesItsTransport(t *testing.T) {
	db := openProductDB(t)
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(context.Background(), statusWorkspace, []discovery.ObservedDevice{{
		Serial: "SER-OFFLINE-LIST",
		State:  discovery.LinkOffline,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	projected := statusOf(t, db, observed[0].DeviceID)
	if got := projected.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_OFFLINE {
		t.Fatalf("offline device status = %v, want OFFLINE", got)
	}
	if got := projected.GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("offline device transport = %v, want USB: the transport is listed, and it is where the device is", got)
	}
}

// History and the service-bound path write a current endpoint with no link state
// recorded, and it still reads the way it did: the rule that produced those rows
// only ever made one current when the adapter could use the device, so an
// unrecorded link state is usable rather than a fourth, unusable state.
func TestACurrentEndpointWithoutARecordedLinkStateReadsAsUsable(t *testing.T) {
	db := openProductDB(t)
	id := observe(t, db, "SER-HISTORY")
	if err := store.NewEndpointService(db).BindCurrent(context.Background(), endpoints.Endpoint{
		ID:         "endpoint-bound",
		Workspace:  statusWorkspace,
		DeviceID:   id,
		Transport:  endpoints.TransportUSB,
		Serial:     "SER-HISTORY",
		State:      endpoints.Current,
		ObservedAt: time.Now().UTC(),
	}, "operator", "op-1"); err != nil {
		t.Fatalf("BindCurrent() error = %v", err)
	}
	current, err := store.NewEndpointRepository(db).ListCurrent(context.Background(), statusWorkspace, id)
	if err != nil {
		t.Fatalf("ListCurrent() error = %v", err)
	}
	if len(current) != 1 || current[0].LinkState != endpoints.LinkStateUnrecorded {
		t.Fatalf("current endpoint = %#v, want one with no recorded link state", current)
	}
	projected := statusOf(t, db, id)
	if got := projected.GetStatus(); got != driftv1.DeviceStatus_DEVICE_STATUS_ONLINE {
		t.Fatalf("device with unrecorded link state = %v, want ONLINE: the rule that wrote it current only did so for a usable device", got)
	}
	if got := projected.GetTransport(); got != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("device with unrecorded link state transport = %v, want USB", got)
	}
}
