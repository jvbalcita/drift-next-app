package service_test

import (
	"context"
	"path/filepath"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The fleet device-settings surface is mounted only when the whole path exists.
// These assertions close the gate from both sides: absent without an applier, and
// present with one. A route whose applier cannot apply is worse than no route at
// all, because it advertises a surface an operator surface will render and then
// find dead.

// The production applier is the thing that satisfies the port. If this stops
// compiling, the route has no production mount, which is the failure this pair of
// assertions exists to catch.
var _ transportconnect.DeviceSettings = (*execution.DeviceSettingsApplier)(nil)

// TestDeviceSettingsRouteIsNotMountedWithoutAnApplier is the negative half.
func TestDeviceSettingsRouteIsNotMountedWithoutAnApplier(t *testing.T) {
	if route := service.DeviceSettingsRoute(nil, "lab-token-value"); route.Path != "" || route.Handler != nil {
		t.Fatalf("no applier mounted a route: %#v", route)
	}
	// An interface holding a typed nil is the shape a caller creates by passing
	// an uninitialised applier, and it must not slip past the gate either.
	var typedNil *execution.DeviceSettingsApplier
	if route := service.DeviceSettingsRoute(typedNil, "lab-token-value"); route.Path != "" || route.Handler != nil {
		t.Fatalf("a typed-nil applier mounted a route: %#v", route)
	}
}

// TestDeviceSettingsRouteIsMountedWithAConstructedApplier is the positive half.
// The applier is built over real dependencies that touch no device: a fleet reader
// over an open store, a dispatcher over the same store, and an identity source.
func TestDeviceSettingsRouteIsMountedWithAConstructedApplier(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dispatcher, err := execution.NewInputDispatcher(settingsMountControl{}, settingsMountProbe{}, settingsMountObserver{}, settingsMountTransport{}, nil)
	if err != nil {
		t.Fatalf("build a dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })

	applier, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(db), db, dispatcher, settingsMountAttemptIDs{})
	if err != nil {
		t.Fatalf("build the settings applier: %v", err)
	}
	route := service.DeviceSettingsRoute(applier, "lab-token-value")
	if route.Path == "" {
		t.Fatal("a constructed applier mounted no route: the settings surface is unreachable even though the whole path exists")
	}
	if route.Handler == nil {
		t.Fatalf("route %q carries no handler", route.Path)
	}
}

type settingsMountControl struct{}

func (settingsMountControl) Authorize(context.Context, action.Intent, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) Complete(context.Context, action.Completion, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) MarkIndeterminate(context.Context, action.Completion, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) Timeout(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) Cancel(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	return action.Result{}, nil
}
func (settingsMountControl) Cleanup(context.Context, string, string, string, string, bool) (action.Result, error) {
	return action.Result{}, nil
}

type settingsMountProbe struct{}

func (settingsMountProbe) ProbeControl(context.Context, execution.InputRequest) (execution.RefusalReason, error) {
	return "", nil
}

type settingsMountObserver struct{}

func (settingsMountObserver) ObservePostcondition(context.Context, action.Intent, execution.InputPayload) (execution.PostconditionObservation, error) {
	return execution.PostconditionObservation{}, nil
}

type settingsMountTransport struct{}

func (settingsMountTransport) RunDeviceCommand(context.Context, string, []string) (adb.Result, error) {
	return adb.Result{}, nil
}

type settingsMountAttemptIDs struct{}

func (settingsMountAttemptIDs) NewID() (string, error) { return "attempt-1", nil }
