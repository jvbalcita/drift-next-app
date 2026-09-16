package service_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The application boundary added in slice 4c is what satisfies the port this route
// serves, and the compiler is what proved it was missing: the dispatcher's own port
// takes execution.InputRequest while the route's port takes DeviceInputTarget, so a
// dispatcher on its own does not mount this route. These assertions close that loop
// from the other side of the gate the nil case already covers - absent without a
// dispatcher, present with one.

// The boundary is the thing that satisfies the port. If this stops compiling, the
// route has no production mount again, which is the failure the compiler caught.
var _ transportconnect.DeviceInputs = (*transportconnect.DeviceInputBoundary)(nil)

type mountDispatch struct{}

func (mountDispatch) Run(context.Context, execution.InputRequest, string, string) (action.Result, error) {
	return action.Result{}, nil
}

type mountSerials struct{}

func (mountSerials) CurrentSerial(context.Context, string, string) (string, error) {
	return "serial-alpha", nil
}

type mountAttemptIDs struct{}

func (mountAttemptIDs) NewID() (string, error) { return "attempt-1", nil }

// TestAConstructedApplicationBoundaryMountsTheRoute is the positive half of the
// gate. With the whole path constructed the route is present, so the surface an
// operator reaches is the one that was actually built rather than a refusal
// masquerading as a surface.
func TestAConstructedApplicationBoundaryMountsTheRoute(t *testing.T) {
	boundary, err := transportconnect.NewDeviceInputBoundary(mountDispatch{}, mountSerials{}, mountAttemptIDs{})
	if err != nil {
		t.Fatalf("building the application boundary failed: %v", err)
	}

	route := service.DeviceInputRoute(boundary, "lab-token-value")
	if route.Path == "" {
		t.Fatal("a constructed application boundary mounted no route: the input surface is unreachable even though the whole path exists")
	}
	if route.Handler == nil {
		t.Fatalf("route %q carries no handler", route.Path)
	}
}
