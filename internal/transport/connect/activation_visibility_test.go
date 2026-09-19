package transportconnect_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/connection"
)

// The refusal itself is correct and stays: restarting adbd on a device that has
// not authorized this host can only make it worse, and nothing here adds a way to
// force it. What must not happen is the device disappearing because of it.
//
// This test runs the two real seams together - the registry the watcher and every
// scan write through, and the real activator over a real enumeration - because the
// defect it guards is a SEAM defect: an attached-but-unauthorized unit was written
// as an endpoint no reader would call current, so it had no transport to report,
// and the operator saw one device out of twenty while nineteen were plugged in
// (ARC-196).
func TestAnActivationRefusalLeavesTheDeviceListedWithItsTransportAndState(t *testing.T) {
	db := openProductDB(t)
	ctx := context.Background()
	const serial = "SER-BENCH-UNAUTHORIZED"

	// The observation the adapter actually makes of an attached unit whose
	// debugging prompt has not been accepted.
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	observed, err := svc.RecordArrivals(ctx, statusWorkspace, []discovery.ObservedDevice{{
		Serial: serial, State: discovery.LinkUnauthorized,
	}}, "system", "discovery-watcher")
	if err != nil {
		t.Fatalf("RecordArrivals() error = %v", err)
	}
	before := statusOf(t, db, observed[0].DeviceID)
	if before.GetStatus() != driftv1.DeviceStatus_DEVICE_STATUS_UNAUTHORIZED {
		t.Fatalf("before activation: status = %v, want UNAUTHORIZED", before.GetStatus())
	}

	runner := &forbiddenRunner{t: t}
	activator, err := connection.NewActivator(connection.ActivatorConfig{
		Runner: runner,
		Enumerator: fixedFleet{devices: []adb.DiscoveredDevice{{
			Serial: serial, State: adb.StateUnauthorized, ConnectionType: adb.ConnectionUSB,
		}}},
		Wait: func(context.Context, time.Duration) error {
			t.Fatal("a refused activation must not enter the settle window")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewActivator() error = %v", err)
	}

	_, err = activator.Activate(ctx, serial, 5555)
	var refusal *connection.ActivationRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate() = %v, want an *ActivationRefusalError", err)
	}
	if refusal.Reason != connection.ActivationRefusalNotAuthorized {
		t.Fatalf("refusal reason = %q, want %q", refusal.Reason, connection.ActivationRefusalNotAuthorized)
	}
	if runner.called {
		t.Fatal("a refused activation reached the device")
	}

	// The device is STILL there, with the transport and the state it had: a
	// refusal is not a disconnection, and a device missing from the board after
	// an operator's own action is the outcome this card is about.
	after := statusOf(t, db, observed[0].DeviceID)
	if after.GetId() != before.GetId() {
		t.Fatalf("after the refusal the device identity changed: %q → %q", before.GetId(), after.GetId())
	}
	if after.GetStatus() != driftv1.DeviceStatus_DEVICE_STATUS_UNAUTHORIZED {
		t.Fatalf("after the refusal status = %v, want UNAUTHORIZED (still attached, still unauthorized)", after.GetStatus())
	}
	if after.GetTransport() != driftv1.DeviceTransport_DEVICE_TRANSPORT_USB {
		t.Fatalf("after the refusal transport = %v, want USB", after.GetTransport())
	}
	if after.GetEndpointId() != before.GetEndpointId() || after.GetEndpointId() == "" {
		t.Fatalf("after the refusal endpoint = %q, want the same current endpoint %q", after.GetEndpointId(), before.GetEndpointId())
	}
	listed := listDevicesForTest(t, db)
	if len(listed) != 1 || listed[0].GetId() != before.GetId() {
		t.Fatalf("ListDevices() after the refusal = %#v, want the refused device still listed", listed)
	}

	// A fleet activation names the refused device in its own report, with the
	// reason and a sentence that says what is actually true for a unit with no
	// display: no host can accept that prompt for it.
	report, err := activator.ActivateFleet(ctx, 5555)
	if err != nil {
		t.Fatalf("ActivateFleet() error = %v", err)
	}
	if report.Refused() != 1 || len(report.Devices) != 1 {
		t.Fatalf("report = %#v, want one refused device carrying its own row", report)
	}
	entry := report.Devices[0]
	if entry.Serial != serial || entry.Refusal != connection.ActivationRefusalNotAuthorized {
		t.Fatalf("report entry = %#v, want the refused serial with its own reason", entry)
	}
	message := entry.Message()
	if !strings.Contains(message, serial) || !strings.Contains(message, "no host can accept that prompt for it") {
		t.Fatalf("report sentence = %q, want it to name the device and the boundary", message)
	}
	if strings.Contains(message, "device's screen") {
		t.Fatalf("report sentence = %q tells the operator to use a screen the device has not got", message)
	}

	// And the refusal did not cost the device its place in the registry either.
	if len(listDevicesForTest(t, db)) != 1 {
		t.Fatal("the fleet activation dropped the refused device from the registry")
	}
}

// fixedFleet answers every enumeration with the same transports, which is what a
// bench that has not changed answers with.
type fixedFleet struct{ devices []adb.DiscoveredDevice }

func (f fixedFleet) Enumerate(context.Context) ([]adb.DiscoveredDevice, error) {
	return append([]adb.DiscoveredDevice(nil), f.devices...), nil
}

// forbiddenRunner fails the test if a device command is ever run. An activation
// that refuses must refuse BEFORE anything reaches the device.
type forbiddenRunner struct {
	t      *testing.T
	called bool
}

func (r *forbiddenRunner) RunAllowlisted(_ context.Context, _ string, args []string) (adb.Result, error) {
	r.called = true
	r.t.Fatalf("a device command was run for an unauthorized device: %v", args)
	return adb.Result{}, errors.New("no device command may be run for a device that has not authorized this host")
}
