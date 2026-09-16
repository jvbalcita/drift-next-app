package lab_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
)

// fakeTransport stands in for the device's own allow-listed ADB runner. It is a
// pointer type with nothing wrapped around it, so an accessor that re-wraps what
// it was given fails the identity assertion below instead of passing it.
type fakeTransport struct {
	runs int
}

func (f *fakeTransport) RunAllowlisted(_ context.Context, _ string, _ []string) (adb.Result, error) {
	f.runs++
	return adb.Result{}, nil
}

// TestLabModeExposesTheTransportItWasGiven is the ARC-76 assertion made
// executable. The composition root must be able to hand the device input path
// the device's own transport, and what it gets back must be that transport -
// never a counting, logging or otherwise wrapping one, because a render-size
// read through a counting wrapper looks like an attempted input and the device
// actor turns the attempt indeterminate.
func TestLabModeExposesTheTransportItWasGiven(t *testing.T) {
	fake := newFakeAdapter()
	transport := &fakeTransport{}

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabTransport(transport))
	if err != nil {
		t.Fatalf("building a lab service with a transport failed: %v", err)
	}

	got := service.DeviceTransport()
	if got == nil {
		t.Fatal("a service built with a transport exposes none")
	}
	exposed, ok := got.(*fakeTransport)
	if !ok {
		t.Fatalf("the exposed transport is %T, not the transport this service was built with: a wrapper here counts a read-only precondition read as an attempted input", got)
	}
	if exposed != transport {
		t.Fatal("the exposed transport is a different value from the one this service was built with")
	}
}

// TestNoTransportWithoutTheExplicitOptIn: opting into real-device mode is not the
// same decision as exposing the device's transport. A service built without the
// transport option exposes none, and the composition root then builds no input
// path from it - no transport, no route.
func TestNoTransportWithoutTheExplicitOptIn(t *testing.T) {
	fake := newFakeAdapter()

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}

	if got := service.DeviceTransport(); got != nil {
		t.Fatalf("a service built without the transport opt-in exposed %T; no transport, no input path", got)
	}
}

// TestTheTransportOptInRefusesNothing: an opt-in that binds nothing is a bug at
// the composition root, so it fails closed when the service is built rather than
// leaving a service that looks ready to dispatch input and is not.
func TestTheTransportOptInRefusesNothing(t *testing.T) {
	if _, err := lab.NewService(lab.WithLabTransport(nil)); err == nil {
		t.Fatal("binding a nil transport was accepted; it must fail closed")
	}
}
