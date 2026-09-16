package execution_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/execution"
)

type fakeSerials struct {
	serial string
	err    error
	calls  int
}

func (f *fakeSerials) CurrentSerial(context.Context, string, string) (string, error) {
	f.calls++
	return f.serial, f.err
}

type fakeObservationSource struct {
	observation adapter.Observation
	err         error
	calls       int
}

func (f *fakeObservationSource) Observe(context.Context) (adapter.Observation, error) {
	f.calls++
	return f.observation, f.err
}

type fakeSourceFactory struct {
	source *fakeObservationSource
	err    error
	serial string
	calls  int
}

func (f *fakeSourceFactory) build(serial string) (execution.ObservationSource, error) {
	f.calls++
	f.serial = serial
	if f.err != nil {
		return nil, f.err
	}
	return f.source, nil
}

func observerFor(t *testing.T, serials execution.EndpointSerialResolver, factory execution.ObservationSourceFactory) *execution.ObservationPostconditionObserver {
	t.Helper()
	observer, err := execution.NewObservationPostconditionObserver(serials, factory)
	if err != nil {
		t.Fatalf("building the postcondition observer failed: %v", err)
	}
	return observer
}

// TestTheObserverCarriesTheObservationItRead: the facts an observation does
// supply are carried faithfully, and the freshness token with them, because a
// postcondition is evaluated against them.
func TestTheObserverCarriesTheObservationItRead(t *testing.T) {
	serials := &fakeSerials{serial: "serial-alpha"}
	source := &fakeObservationSource{observation: adapter.Observation{Token: "obs-after", PackageName: "com.example.app"}}
	factory := &fakeSourceFactory{source: source}
	observer := observerFor(t, serials, factory.build)

	got, err := observer.ObservePostcondition(context.Background(),
		action.Intent{Workspace: "workspace-a", DeviceID: "device-1", Kind: action.LaunchApp, ObservationToken: "obs-before"},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.app"}})
	if err != nil {
		t.Fatalf("observing a launch postcondition failed: %v", err)
	}
	if got.Token != "obs-after" {
		t.Fatalf("observation token = %q, want %q", got.Token, "obs-after")
	}
	if got.ForegroundPackage != "com.example.app" {
		t.Fatalf("foreground package = %q, want the observed package", got.ForegroundPackage)
	}
	if got.Partial {
		t.Fatal("an observation that supplied every fact this postcondition needs was reported as partial")
	}
	if source.calls != 1 || serials.calls != 1 || factory.calls != 1 {
		t.Fatalf("read the device %d times through %d resolutions and %d source builds", source.calls, serials.calls, factory.calls)
	}
	if factory.serial != "serial-alpha" {
		t.Fatalf("the observation source was built for serial %q, want the resolved serial", factory.serial)
	}
}

// TestAnUnobservableForegroundPackageIsPartialNotAMismatch: an empty observed
// package means "this observation cannot say which package is in the foreground",
// not "the device is showing the wrong app". The boundary must record the attempt
// indeterminate rather than failing an action that may well have succeeded - the
// evaluator checks Partial before it compares the package, so a partial
// observation never reaches the comparison.
func TestAnUnobservableForegroundPackageIsPartialNotAMismatch(t *testing.T) {
	serials := &fakeSerials{serial: "serial-alpha"}
	source := &fakeObservationSource{observation: adapter.Observation{Token: "obs-after"}}
	observer := observerFor(t, serials, (&fakeSourceFactory{source: source}).build)

	got, err := observer.ObservePostcondition(context.Background(),
		action.Intent{Workspace: "workspace-a", DeviceID: "device-1", Kind: action.LaunchApp, ObservationToken: "obs-before"},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.app"}})
	if err != nil {
		t.Fatalf("observing a launch postcondition failed: %v", err)
	}
	if !got.Partial {
		t.Fatal("an observation that cannot see the foreground package was reported as complete; the postcondition would then be failed for a mismatch nobody observed")
	}
	if got.ForegroundPackage != "" {
		t.Fatalf("foreground package = %q, want it left unobserved rather than invented", got.ForegroundPackage)
	}
}

// TestTheAddressedFieldLengthIsNeverInvented: a typed-text postcondition names the
// addressed field's length, and the field is identified by an opaque handle. The
// resolver that turns a handle into a field is ARC-73's open work, so the length
// cannot be observed today. The observation is partial - which makes the attempt
// indeterminate - and no count is presented as if it had been read.
func TestTheAddressedFieldLengthIsNeverInvented(t *testing.T) {
	serials := &fakeSerials{serial: "serial-alpha"}
	source := &fakeObservationSource{observation: adapter.Observation{Token: "obs-after", PackageName: "com.example.app"}}
	observer := observerFor(t, serials, (&fakeSourceFactory{source: source}).build)

	got, err := observer.ObservePostcondition(context.Background(),
		action.Intent{Workspace: "workspace-a", DeviceID: "device-1", Kind: action.TextInput, ObservationToken: "obs-before"},
		execution.InputPayload{Text: &execution.TextReference{Handle: "text-handle-1", Length: 5}})
	if err != nil {
		t.Fatalf("observing a typed-text postcondition failed: %v", err)
	}
	if !got.Partial {
		t.Fatal("an observation that cannot read the addressed field was reported as complete")
	}
	if got.FieldLength != 0 {
		t.Fatalf("addressed field length = %d; an unreadable field must not be reported with a count", got.FieldLength)
	}
}

// TestAFailedResolutionReadsNoDevice: no serial means no device was named, so the
// observation source is never built and never read. Resolving first is what keeps
// an unnameable device from being observed "somehow".
func TestAFailedResolutionReadsNoDevice(t *testing.T) {
	serials := &fakeSerials{err: errors.New("device has no current transport endpoint")}
	source := &fakeObservationSource{}
	factory := &fakeSourceFactory{source: source}
	observer := observerFor(t, serials, factory.build)

	if _, err := observer.ObservePostcondition(context.Background(),
		action.Intent{Workspace: "workspace-a", DeviceID: "device-1", Kind: action.LaunchApp},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.app"}}); err == nil {
		t.Fatal("a device with no resolvable serial produced an observation")
	}
	if factory.calls != 0 || source.calls != 0 {
		t.Fatalf("a refused resolution still reached the device: %d source builds, %d reads", factory.calls, source.calls)
	}
}

// TestAReadFailureIsNeverAFabricatedObservation: an observation that could not be
// taken is an error, not an empty observation that some postcondition might pass.
func TestAReadFailureIsNeverAFabricatedObservation(t *testing.T) {
	serials := &fakeSerials{serial: "serial-alpha"}
	source := &fakeObservationSource{err: errors.New("transport lost")}
	observer := observerFor(t, serials, (&fakeSourceFactory{source: source}).build)

	got, err := observer.ObservePostcondition(context.Background(),
		action.Intent{Workspace: "workspace-a", DeviceID: "device-1", Kind: action.LaunchApp},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.app"}})
	if err == nil {
		t.Fatalf("a failed observation returned %+v instead of an error", got)
	}
}

// TestTheObserverRefusesIncompleteConstruction: an observer without a resolver or
// a source cannot observe anything, so it refuses to be built rather than failing
// on every dispatch.
func TestTheObserverRefusesIncompleteConstruction(t *testing.T) {
	if _, err := execution.NewObservationPostconditionObserver(nil, nil); err == nil {
		t.Fatal("an observer with no resolver and no source was built")
	}
	if _, err := execution.NewObservationPostconditionObserver(&fakeSerials{}, nil); err == nil {
		t.Fatal("an observer with no observation source was built")
	}
}
