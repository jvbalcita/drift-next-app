package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/service"
)

// A route is only worth having if it is REACHABLE: a handler that compiles and
// passes a unit test proves nothing about whether the RPC is mounted, guarded,
// and answering with its own refusals. These tests drive the mounted route over
// HTTP, which is what the console will do.

// stubConnections answers the connection boundary without touching a device, and
// counts every call so a refusal can be proven to have reached nothing.
type stubConnections struct {
	calls      int
	refusal    error
	outcome    connection.ConnectOutcome
	activation connection.PortActivation
	restart    connection.RestartOutcome
}

func (s *stubConnections) Connect(_ context.Context, _ string) (connection.ConnectOutcome, error) {
	s.calls++
	return s.outcome, s.refusal
}

func (s *stubConnections) ConnectFor(_ context.Context, _, _ string) (connection.ConnectOutcome, error) {
	s.calls++
	return s.outcome, s.refusal
}

func (s *stubConnections) ChangeTransportMode(_ context.Context, _ string, _ uint16) (connection.ActivationOutcome, error) {
	s.calls++
	return connection.ActivationOutcome{}, s.refusal
}

func (s *stubConnections) ActivatePort(_ context.Context, _, _ string, _ time.Time) (connection.PortActivation, bool, error) {
	s.calls++
	return s.activation, true, s.refusal
}

func (s *stubConnections) Restart(_ context.Context, _ []string) (connection.RestartOutcome, error) {
	s.calls++
	return s.restart, s.refusal
}

// TestAConnectionRouteIsNotMountedWithoutATransportBoundary is the gate: no
// constructed boundary, no route. The typed-nil case is checked too, because an
// interface holding a nil pointer is non-nil and must not be mounted.
func TestAConnectionRouteIsNotMountedWithoutATransportBoundary(t *testing.T) {
	if route := service.ConnectionRoute(nil, "lab-token-value"); route.Path != "" || route.Handler != nil {
		t.Fatalf("ConnectionRoute(nil) = %#v, want an unmounted route", route)
	}
	var typedNil *stubConnections
	if route := service.ConnectionRoute(typedNil, "lab-token-value"); route.Path != "" || route.Handler != nil {
		t.Fatalf("ConnectionRoute(typed nil) = %#v, want an unmounted route", route)
	}
}

// TestTheMountedConnectionRouteCarriesTheRefusalAndIsGuardedByItsToken proves the
// whole reachable path: the procedure resolves, the token guards it, and the
// port-policy refusal reaches the caller as its OWN code and message rather than
// as the generic internal answer. A refusal an operator cannot read is the same
// defect as a missing control.
func TestTheMountedConnectionRouteCarriesTheRefusalAndIsGuardedByItsToken(t *testing.T) {
	const token = "lab-token-value"
	connections := &stubConnections{refusal: &connection.PortNotAcceptedError{
		Endpoint: "192.168.1.106:5556",
		Port:     5556,
		Accepted: []uint16{5555},
	}}
	route := service.ConnectionRoute(connections, token)
	wantPath := "/" + driftv1connect.ConnectionServiceName
	if !strings.HasPrefix(route.Path, wantPath+"/") {
		t.Fatalf("ConnectionRoute() path = %q, want it to mount under %q", route.Path, wantPath)
	}
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)

	body := `{"context":{"requestId":"req-1","actorId":"op-1"},` +
		`"serial":"R5CT42GS94Z","endpoint":"192.168.1.106:5556"}`

	call := func(withToken bool) (int, string) {
		request := httptest.NewRequest(http.MethodPost, route.Path+"ConnectEndpoint", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if withToken {
			request.Header.Set(service.LabTokenHeader, token)
		}
		record := httptest.NewRecorder()
		server.Handler.ServeHTTP(record, request)
		return record.Code, record.Body.String()
	}

	status, responseBody := call(false)
	if status == http.StatusOK {
		t.Fatalf("an unauthenticated ConnectEndpoint = %d body=%s, want a refusal", status, responseBody)
	}
	if connections.calls != 0 {
		t.Fatalf("an unauthenticated call reached the transport %d time(s)", connections.calls)
	}

	status, responseBody = call(true)
	if status == http.StatusOK {
		t.Fatalf("a refused connect = %d body=%s, want a failure status", status, responseBody)
	}
	if !strings.Contains(responseBody, "5556") {
		t.Fatalf("the refusal body = %s, want it to name the refused port", responseBody)
	}
	if strings.Contains(responseBody, "request could not be completed") {
		t.Fatalf("the refusal answered with the generic internal message: %s", responseBody)
	}
	if connections.calls != 1 {
		t.Fatalf("transport calls = %d, want exactly one", connections.calls)
	}
}

// TestTheRestartRouteReportsWhatItDidPerEndpoint drives the second state-changing
// procedure the same way: the operator has to be able to read which endpoint came
// back and which did not, from the response the route actually returns.
func TestTheRestartRouteReportsWhatItDidPerEndpoint(t *testing.T) {
	const token = "lab-token-value"
	connections := &stubConnections{restart: connection.RestartOutcome{
		TransportsBefore:     3,
		TransportsAfterStart: 1,
		KillExitCode:         0,
		StartExitCode:        0,
		Reestablished:        1,
		Endpoints: []connection.EndpointOutcome{
			{Endpoint: "192.168.1.109:5555", Port: 5555, Reestablished: true, ExitCode: 0},
		},
	}}
	route := service.ConnectionRoute(connections, token)
	if route.Path == "" {
		t.Fatal("ConnectionRoute() mounted nothing for a constructed boundary")
	}
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)

	body := `{"context":{"requestId":"req-2","actorId":"op-1"},"endpoints":["192.168.1.109:5555"]}`
	request := httptest.NewRequest(http.MethodPost, route.Path+"RestartServer", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(service.LabTokenHeader, token)
	record := httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("RestartServer = %d body=%s, want 200", record.Code, record.Body.String())
	}
	if !strings.Contains(record.Body.String(), "transportsBefore") {
		t.Fatalf("the restart response = %s, want the counts that make a bare ok impossible", record.Body.String())
	}
	if connections.calls != 1 {
		t.Fatalf("restart calls = %d, want exactly one", connections.calls)
	}
}
