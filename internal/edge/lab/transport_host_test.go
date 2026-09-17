package lab_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
)

// fakeHostTransport stands in for the host-level allow-listed runner. Like
// fakeTransport it is a pointer type with nothing wrapped around it, so an
// accessor that re-wraps what it was given fails the identity assertion below
// instead of passing it.
type fakeHostTransport struct {
	runs int
}

func (f *fakeHostTransport) RunHostAllowlisted(_ context.Context, _ []string) (adb.Result, error) {
	f.runs++
	return adb.Result{}, nil
}

// TestLabModeExposesTheHostTransportItWasGiven: the composition root builds the
// transport surface over the host runner, and what it gets back must be the value
// this service was built with — never a wrapper, because the transport surface
// must reach the adb server through the same object the observation and input
// paths already use.
func TestLabModeExposesTheHostTransportItWasGiven(t *testing.T) {
	fake := newFakeAdapter()
	host := &fakeHostTransport{}

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabHostTransport(host))
	if err != nil {
		t.Fatalf("building a lab service with a host transport failed: %v", err)
	}

	got := service.HostTransport()
	if got == nil {
		t.Fatal("a service built with a host transport exposes none")
	}
	exposed, ok := got.(*fakeHostTransport)
	if !ok {
		t.Fatalf("the exposed host transport is %T, not the transport this service was built with", got)
	}
	if exposed != host {
		t.Fatal("the exposed host transport is a different value from the one this service was built with")
	}
}

// TestEachTransportOptInIsItsOwnDecision: binding the device runner must not
// expose a host runner, and binding the host runner must not expose a device
// runner. They are different powers, so a service asked for one and not the other
// must expose exactly one — otherwise a route could be mounted over a boundary
// nobody opted into.
func TestEachTransportOptInIsItsOwnDecision(t *testing.T) {
	fake := newFakeAdapter()

	onlyDevice, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabTransport(&fakeTransport{}))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}
	if got := onlyDevice.HostTransport(); got != nil {
		t.Fatalf("binding the device transport exposed a host transport (%T); the two opt-ins are separate decisions", got)
	}

	onlyHost, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabHostTransport(&fakeHostTransport{}))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}
	if got := onlyHost.DeviceTransport(); got != nil {
		t.Fatalf("binding the host transport exposed a device transport (%T); the two opt-ins are separate decisions", got)
	}
}

// TestNoHostTransportWithoutTheExplicitOptIn: no host runner, no transport
// surface, no route — the composition root builds none from a service that
// exposes none.
func TestNoHostTransportWithoutTheExplicitOptIn(t *testing.T) {
	fake := newFakeAdapter()

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}
	if got := service.HostTransport(); got != nil {
		t.Fatalf("a service built without the host-transport opt-in exposed %T; no host runner, no transport surface", got)
	}
}

// TestTheHostTransportOptInRefusesNothing: an opt-in that binds nothing is a bug
// at the composition root, so it fails closed when the service is built rather
// than leaving a service that looks ready to open transports and is not.
func TestTheHostTransportOptInRefusesNothing(t *testing.T) {
	fake := newFakeAdapter()

	_, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabHostTransport(nil))
	if !errors.Is(err, lab.ErrHostTransportRequired) {
		t.Fatalf("a host-transport opt-in binding nothing returned %v, want %v", err, lab.ErrHostTransportRequired)
	}
}
