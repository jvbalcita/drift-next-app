package service_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/service"
)

func TestHTTPServerKeepsHealthEndpointsWhenNoRouteIsMounted(t *testing.T) {
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0")

	for _, path := range []string{"/healthz", "/readyz"} {
		record := httptest.NewRecorder()
		server.Handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, path, nil))
		if record.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, record.Code, http.StatusOK)
		}
	}
}

func TestHTTPServerMountsTheLabAdapterRouteOnlyWhenConstructed(t *testing.T) {
	labService, err := lab.NewService()
	if err != nil {
		t.Fatalf("lab.NewService() error = %v", err)
	}
	route := service.LabAdapterRoute(labService, "")
	if !strings.Contains(route.Path, "drift.v1.LabAdapterService") {
		t.Fatalf("LabAdapterRoute() path = %q, want the generated Connect prefix", route.Path)
	}

	mounted := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)
	request := httptest.NewRequest(http.MethodPost, route.Path+"GetLabStatus", strings.NewReader(`{"workspace":{"workspaceId":"workspace-1"}}`))
	request.Header.Set("Content-Type", "application/json")
	record := httptest.NewRecorder()
	mounted.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusOK {
		t.Fatalf("GetLabStatus status = %d, want %d (body %q)", record.Code, http.StatusOK, record.Body.String())
	}
	if !strings.Contains(record.Body.String(), "LAB_MODE_MOCK") {
		t.Fatalf("GetLabStatus body = %q, want the mock mode projection", record.Body.String())
	}

	bare := service.NewHTTPServer("control-plane", "127.0.0.1:0")
	bareRecord := httptest.NewRecorder()
	bare.Handler.ServeHTTP(bareRecord, httptest.NewRequest(http.MethodPost, route.Path+"GetLabStatus", strings.NewReader("{}")))
	if bareRecord.Code == http.StatusOK {
		t.Fatalf("unmounted lab route status = %d, want a non-success status", bareRecord.Code)
	}
}

func TestHTTPServerMountsLabRegistrationRouteWhenConstructed(t *testing.T) {
	labService, err := lab.NewService()
	if err != nil {
		t.Fatal(err)
	}
	registrationService := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.LabStatusProbe{
			Source:       labService,
			AllowedPorts: []uint16{5555},
		},
	})
	route := service.LabRegistrationRoute(registrationService, "", []uint16{5555})
	if !strings.Contains(route.Path, "drift.v1.LabRegistrationService") {
		t.Fatalf("LabRegistrationRoute() path = %q", route.Path)
	}
	mounted := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)
	request := httptest.NewRequest(http.MethodPost, route.Path+"GetLabRegistrationStatus", strings.NewReader(`{"workspace":{"workspaceId":"workspace-1"},"context":{"requestId":"req-1"}}`))
	request.Header.Set("Content-Type", "application/json")
	record := httptest.NewRecorder()
	mounted.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusOK {
		t.Fatalf("GetLabRegistrationStatus status = %d body=%q", record.Code, record.Body.String())
	}
}
