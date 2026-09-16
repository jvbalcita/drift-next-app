package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// A device input route is only worth having if an operator can reach it and if
// the refusal that comes back is the input's own refusal. These tests exercise
// the mounted route over HTTP rather than calling the handler directly, because
// a handler that compiles and passes a unit test proves nothing about whether
// the RPC is reachable.

// refusingDeviceInputs is a dispatcher that records what it was asked and
// answers with a real refusal shape: the same reason, code, failure class and
// stable message the dispatch boundary's own table binds for an expired lease.
type refusingDeviceInputs struct {
	calls     int
	last      transportconnect.DeviceInputTarget
	actorType string
	actorID   string
}

func (f *refusingDeviceInputs) Run(_ context.Context, input transportconnect.DeviceInputTarget, actorType, actorID string) (action.Result, error) {
	f.calls++
	f.last = input
	f.actorType = actorType
	f.actorID = actorID
	return action.Result{}, &execution.Refusal{
		Reason:       execution.RefusalLeaseExpired,
		Code:         platformerrors.CodeLeaseConflict,
		FailureClass: domain.FailureLeaseConflict,
		Message:      "the device lease has expired",
	}
}

func tapBody() string {
	return `{"context":{"requestId":"req-1","idempotencyKey":"idem-1","actorId":"op-1"},` +
		`"workspace":{"workspaceId":"workspace-a"},` +
		`"deviceId":"device-a","leaseId":"lease-1","fencingToken":"7","observationToken":"obs-1",` +
		`"tap":{"point":{"x":540,"y":960},"renderSpace":{"renderWidth":1080,"renderHeight":2280,"observationToken":"obs-1"}}}`
}

// TestADeviceInputRouteIsNotMountedWithoutADispatcher is the gate: no
// constructed dispatcher, no route. A mounted surface that could only answer
// with a refusal would be a control an operator surface renders and finds dead.
func TestADeviceInputRouteIsNotMountedWithoutADispatcher(t *testing.T) {
	route := service.DeviceInputRoute(nil, "lab-token-value")
	if route.Path != "" || route.Handler != nil {
		t.Fatalf("DeviceInputRoute(nil) = %#v, want an unmounted route", route)
	}
	var typedNil *refusingDeviceInputs
	if route := service.DeviceInputRoute(typedNil, "lab-token-value"); route.Path != "" || route.Handler != nil {
		t.Fatalf("DeviceInputRoute(typed nil) = %#v, want an unmounted route", route)
	}
}

// TestTheMountedDeviceInputRouteCarriesTheInputAndReturnsItsOwnRefusal proves
// the whole reachable path in one case: the route is mounted, the procedure
// resolves, the token guards it, the caller's input reaches the dispatcher
// intact, and the refusal comes back as its own code and message rather than as
// the generic internal answer.
func TestTheMountedDeviceInputRouteCarriesTheInputAndReturnsItsOwnRefusal(t *testing.T) {
	const token = "lab-token-value"
	inputs := &refusingDeviceInputs{}
	route := service.DeviceInputRoute(inputs, token)
	wantPath := "/" + driftv1connect.DeviceInputServiceName + "/"
	if route.Path != wantPath {
		t.Fatalf("DeviceInputRoute() path = %q, want %q", route.Path, wantPath)
	}
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)

	call := func(withToken bool) (int, string) {
		request := httptest.NewRequest(http.MethodPost, route.Path+"Tap", strings.NewReader(tapBody()))
		request.Header.Set("Content-Type", "application/json")
		if withToken {
			request.Header.Set(service.LabTokenHeader, token)
		}
		record := httptest.NewRecorder()
		server.Handler.ServeHTTP(record, request)
		return record.Code, record.Body.String()
	}

	// Without the token the route is not reachable, and nothing was dispatched.
	status, body := call(false)
	if status == http.StatusOK {
		t.Fatalf("an unauthenticated Tap = %d body=%s, want a refusal", status, body)
	}
	if inputs.calls != 0 {
		t.Fatalf("an unauthenticated Tap reached the dispatcher %d times, want 0", inputs.calls)
	}

	status, body = call(true)
	if status == http.StatusOK {
		t.Fatalf("a refused Tap = %d body=%s, want a failure status", status, body)
	}
	if strings.Contains(body, "request could not be completed") {
		t.Fatalf("the refusal answered with the generic internal message: %s", body)
	}
	if !strings.Contains(body, "the device lease has expired") {
		t.Fatalf("the refusal body = %s, want the boundary's own stable message", body)
	}
	if status != http.StatusConflict {
		t.Fatalf("a lease conflict answered %d, want %d", status, http.StatusConflict)
	}
	if inputs.calls != 1 {
		t.Fatalf("dispatcher calls = %d, want exactly 1", inputs.calls)
	}

	// The input reached the dispatcher intact, including the frame that travels
	// with the coordinate and the attempt identity it did not have to invent.
	if inputs.last.Workspace != "workspace-a" || inputs.last.DeviceID != "device-a" {
		t.Fatalf("dispatched target = %#v, want workspace-a/device-a", inputs.last)
	}
	if inputs.last.LeaseID != "lease-1" || inputs.last.FencingToken != 7 || inputs.last.ObservationToken != "obs-1" {
		t.Fatalf("dispatched control tuple = %#v, want the caller's lease tuple", inputs.last)
	}
	if inputs.last.IdempotencyKey != "idem-1" {
		t.Fatalf("dispatched idempotency key = %q, want the caller's key", inputs.last.IdempotencyKey)
	}
	tap := inputs.last.Payload.Tap
	if tap == nil || tap.Point.X != 540 || tap.Point.Y != 960 {
		t.Fatalf("dispatched tap payload = %#v, want the caller's point", tap)
	}
	if tap.Space.Width != 1080 || tap.Space.Height != 2280 || tap.Space.ObservationToken != "obs-1" {
		t.Fatalf("dispatched render space = %#v, want the frame that travelled with the coordinate", tap.Space)
	}
	if inputs.actorID != "op-1" || inputs.actorType == "" {
		t.Fatalf("dispatched actor = %q/%q, want the calling operator", inputs.actorType, inputs.actorID)
	}
}
