package main

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The composition root is the only place three individually-working
// collaborators can still fail to compose, and the only place that can build
// the boundary over a different runner than the one the service was given.
//
// In production the lab service hands back ONE adapter that satisfies both
// narrow interfaces, so "which object" is not the question - "which method each
// collaborator calls" is. Connector and Restarter are host-scoped and must call
// RunHostAllowlisted; Activator is device-scoped and must call RunAllowlisted.
// The recording adapter below counts the two methods separately, so a boundary
// built over a second, silent adapter leaves the counts at zero and fails.

// recordingAdapter satisfies both narrow runners, as the real adapter does, and
// records which method was invoked.
type recordingAdapter struct {
	hostCalls   int
	deviceCalls int
}

func (a *recordingAdapter) RunHostAllowlisted(context.Context, []string) (adb.Result, error) {
	a.hostCalls++
	return adb.Result{}, nil
}

func (a *recordingAdapter) RunAllowlisted(context.Context, string, []string) (adb.Result, error) {
	a.deviceCalls++
	return adb.Result{}, nil
}

// recordingEnumerator answers with one authorized USB transport, which is what
// both the restart and the activation precondition read before they act.
type recordingEnumerator struct {
	calls int
}

func (e *recordingEnumerator) Enumerate(context.Context) ([]adb.DiscoveredDevice, error) {
	e.calls++
	return []adb.DiscoveredDevice{{
		Serial:         "SERIAL1",
		State:          adb.StateDevice,
		ConnectionType: "usb",
	}}, nil
}

// currentEndpoint returns the address the plane currently observes, which the
// connector and restarter both require before they will dial anything.
func currentEndpoint(context.Context) ([]string, error) {
	return []string{"10.0.0.5:5555"}, nil
}

// TestConnectionOperationsRefusesAServiceWithNoHostRunner is the mount's reason
// for existing: the device runner and the enumerator are both bound and both
// correct, and the transport surface still must not be built, because nothing
// here can reach the adb server itself. A boundary composed in that state would
// mount routes whose every host operation fails at the first call.
func TestConnectionOperationsRefusesAServiceWithNoHostRunner(t *testing.T) {
	adapter := &recordingAdapter{}
	enumerator := &recordingEnumerator{}
	service, err := lab.NewService(
		lab.WithLabTransport(adapter),
		lab.WithLabEnumerator(enumerator),
	)
	if err != nil {
		t.Fatalf("construct lab service: %v", err)
	}
	operations, err := connectionOperations(service, []uint16{5555}, connection.CurrentEndpointSourceFunc(currentEndpoint))
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("composing a boundary with no host runner returned %v, want %v", err, platformerrors.CodeUnavailable)
	}
	if operations.Connector != nil || operations.Restarter != nil || operations.Activator != nil {
		t.Fatal("a refused composition returned a partially wired boundary")
	}
	if adapter.hostCalls != 0 || adapter.deviceCalls != 0 || enumerator.calls != 0 {
		t.Fatalf("a refused composition reached a runner: host=%d device=%d enumerator=%d",
			adapter.hostCalls, adapter.deviceCalls, enumerator.calls)
	}
}

// TestConnectionOperationsGivesEachCollaboratorItsOwnRunner drives one
// operation through each collaborator and asserts which method answered:
// Connector and Restarter must reach the host runner, Activator must reach the
// device runner. A boundary that constructed fine but reached nothing - or
// reached the wrong method - fails here rather than at the first operator call.
func TestConnectionOperationsGivesEachCollaboratorItsOwnRunner(t *testing.T) {
	ctx := context.Background()
	adapter := &recordingAdapter{}
	enumerator := &recordingEnumerator{}
	service, err := lab.NewService(
		lab.WithLabTransport(adapter),
		lab.WithLabEnumerator(enumerator),
		lab.WithLabHostTransport(adapter),
	)
	if err != nil {
		t.Fatalf("construct lab service: %v", err)
	}
	operations, err := connectionOperations(service, []uint16{5555}, connection.CurrentEndpointSourceFunc(currentEndpoint))
	if err != nil {
		t.Fatalf("compose connection operations: %v", err)
	}
	if operations.Connector == nil || operations.Restarter == nil || operations.Activator == nil {
		t.Fatalf("composed boundary = %+v, want all three operations wired", operations)
	}

	// Connector: a host operation (adb connect), never a device input.
	if _, err := operations.Connect(ctx, "10.0.0.5:5555"); err != nil {
		t.Fatalf("connect through the composed boundary: %v", err)
	}
	if adapter.hostCalls != 1 || adapter.deviceCalls != 0 {
		t.Fatalf("connect reached host=%d device=%d, want host=1 device=0", adapter.hostCalls, adapter.deviceCalls)
	}

	// Restarter: host operations only (kill-server, start-server, then a
	// re-dial of the endpoint it already observed).
	hostBefore := adapter.hostCalls
	if _, err := operations.Restart(ctx, []string{"10.0.0.5:5555"}); err != nil {
		t.Fatalf("restart through the composed boundary: %v", err)
	}
	if adapter.hostCalls <= hostBefore {
		t.Fatalf("restart reached host=%d, want more than the %d calls before it", adapter.hostCalls, hostBefore)
	}
	if adapter.deviceCalls != 0 {
		t.Fatalf("restart reached the device runner %d times, want 0", adapter.deviceCalls)
	}

	// Activator: a device-scoped operation (adb tcpip on one serial), never a
	// host operation. The activator settles for its production window and then
	// re-reads the fleet, so this call is deliberately the slow one.
	hostBefore = adapter.hostCalls
	if _, err := operations.ChangeTransportMode(ctx, "SERIAL1", 5555); err != nil {
		t.Fatalf("change transport mode through the composed boundary: %v", err)
	}
	if adapter.deviceCalls != 1 {
		t.Fatalf("activation reached the device runner %d times, want 1", adapter.deviceCalls)
	}
	if adapter.hostCalls != hostBefore {
		t.Fatalf("activation reached the host runner %d times, want none", adapter.hostCalls-hostBefore)
	}
}
