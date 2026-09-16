package execution_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
)

// fakeStatusReader stands in for the lab boundary's status read. It reports the
// attached set, so the observer can be asserted to read device state from
// enumeration rather than from anything the caller asserts about the device.
type fakeStatusReader struct {
	discovered []adb.DiscoveredDevice
	calls      int
}

func (f *fakeStatusReader) Status(context.Context) lab.Status {
	f.calls++
	return lab.Status{Discovered: f.discovered}
}

// TestTheProbeObserverReadsDeviceStateFromTheAttachedSet: the readiness probe can
// judge a device without the composition root holding the concrete adapter, which
// is the whole reason this seam exists.
func TestTheProbeObserverReadsDeviceStateFromTheAttachedSet(t *testing.T) {
	reader := &fakeStatusReader{discovered: []adb.DiscoveredDevice{
		{Serial: "serial-alpha", State: adb.StateDevice},
		{Serial: "serial-beta", State: adb.StateUnauthorized},
	}}
	observer, err := execution.NewTransportObserverFromAttached(reader)
	if err != nil {
		t.Fatalf("building the transport observer failed: %v", err)
	}

	state, err := observer.TransportState(context.Background(), "serial-alpha")
	if err != nil {
		t.Fatalf("reading device state failed: %v", err)
	}
	if state != adb.StateDevice {
		t.Fatalf("state = %q, want %q", state, adb.StateDevice)
	}

	// A device the host lists as unauthorized is reported unauthorized, never
	// rounded to a usable state.
	state, err = observer.TransportState(context.Background(), "serial-beta")
	if err != nil {
		t.Fatalf("reading device state failed: %v", err)
	}
	if state != adb.StateUnauthorized {
		t.Fatalf("state = %q, want %q", state, adb.StateUnauthorized)
	}
}

// TestAnUnlistedSerialIsOfflineNotAssumedReachable: enumeration is not selection.
// A serial nobody can see must not be reported as a device the boundary may
// dispatch to, matching what the ADB-backed observer does with an unlisted serial.
func TestAnUnlistedSerialIsOfflineNotAssumedReachable(t *testing.T) {
	observer, err := execution.NewTransportObserverFromAttached(&fakeStatusReader{
		discovered: []adb.DiscoveredDevice{{Serial: "serial-alpha", State: adb.StateDevice}},
	})
	if err != nil {
		t.Fatalf("building the transport observer failed: %v", err)
	}

	state, err := observer.TransportState(context.Background(), "serial-not-attached")
	if err != nil {
		t.Fatalf("reading device state failed: %v", err)
	}
	if state != adb.StateOffline {
		t.Fatalf("state = %q, want %q: an unlisted serial must not be assumed reachable", state, adb.StateOffline)
	}
}

// TestTheProbeObserverRefusesNoSource: an observer with nothing to read would
// report every device offline, so it refuses to be built instead and the
// composition root keeps the probe's own fail-closed default.
func TestTheProbeObserverRefusesNoSource(t *testing.T) {
	if _, err := execution.NewTransportObserverFromAttached(nil); err == nil {
		t.Fatal("a transport observer over no status source was built; it must fail closed at construction")
	}
}
