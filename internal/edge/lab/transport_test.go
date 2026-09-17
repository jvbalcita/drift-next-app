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

// TestLabModeExposesTheCapturePathItWasGiven: the composition root must be able
// to drive a frame engine over the device's own adapter, and what it gets back
// must be that adapter - a second one built alongside it would capture through a
// different path than the one the rest of the product observes through.
func TestLabModeExposesTheCapturePathItWasGiven(t *testing.T) {
	fake := newFakeAdapter()

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabFrameTransport(fake))
	if err != nil {
		t.Fatalf("building a lab service with a capture path failed: %v", err)
	}

	got := service.FrameTransport()
	if got == nil {
		t.Fatal("a service built with a capture path exposes none")
	}
	exposed, ok := got.(*fakeAdapter)
	if !ok {
		t.Fatalf("the exposed capture path is %T, not the adapter this service was built with", got)
	}
	if exposed != fake {
		t.Fatal("the exposed capture path is a different value from the one this service was built with")
	}
}

// TestNoCapturePathWithoutTheExplicitOptIn: the fixture adapter that backs mock
// mode must not survive into lab mode. A fixture frame reported as a device's
// screen is exactly the claim this boundary exists to prevent, so a lab-mode
// service built without the capture opt-in exposes no capture path at all and the
// composition root starts no frame engine from it.
func TestNoCapturePathWithoutTheExplicitOptIn(t *testing.T) {
	fake := newFakeAdapter()

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}

	if got := service.FrameTransport(); got != nil {
		t.Fatalf("a lab service built without the capture opt-in exposed %T; no capture path, no frame engine", got)
	}
}

// TestTheCaptureOptInRefusesNothing: like the other transport opt-ins, binding
// nothing fails closed at construction.
func TestTheCaptureOptInRefusesNothing(t *testing.T) {
	if _, err := lab.NewService(lab.WithLabFrameTransport(nil)); err == nil {
		t.Fatal("binding a nil capture path was accepted; it must fail closed")
	}
}

// TestMockModeExposesTheDeterministicCapturePath: mock mode is how every other
// layer is exercised without a device, so the capture path a frame engine is
// built over must exist there too - and it must be a fixture that reaches no
// device.
func TestMockModeExposesTheDeterministicCapturePath(t *testing.T) {
	service, err := lab.NewService()
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	capturer := service.FrameTransport()
	if capturer == nil {
		t.Fatal("mock mode exposes no capture path, so no frame engine can be built without a device")
	}
	shot, err := capturer.Screenshot(context.Background(), "mock-device-alpha")
	if err != nil {
		t.Fatalf("the mock capture path failed for a fixture serial: %v", err)
	}
	if len(shot.PNG) == 0 || shot.Hash == "" {
		t.Fatalf("the mock capture path returned no bounded, hashed payload: %d byte(s), hash %q", len(shot.PNG), shot.Hash)
	}
}

// TestLabModeFromTheEnvironmentBindsTheCapturePath: the operator's opt-in is what
// binds the capture path in real-device mode, so the composition root's frame
// engine captures through the same adapter the service observes through.
func TestLabModeFromTheEnvironmentBindsTheCapturePath(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case lab.EnvLabMode:
			return "1", true
		case lab.EnvADBPath:
			return "/usr/local/bin/adb", true
		default:
			return "", false
		}
	}
	service, err := lab.NewServiceFromEnv(lookup)
	if err != nil {
		t.Fatalf("NewServiceFromEnv() error = %v", err)
	}
	if status := service.Status(context.Background()); status.Mode != lab.ModeLab {
		t.Fatalf("mode = %q, want %q", status.Mode, lab.ModeLab)
	}
	if service.FrameTransport() == nil {
		t.Fatal("lab mode bound no capture path, so the composition root would start no frame engine")
	}
	if service.DeviceTransport() == nil {
		t.Fatal("lab mode bound no device transport")
	}
}
