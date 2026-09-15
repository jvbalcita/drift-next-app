package service_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/service"
)

func TestValidateLoopbackAddressAcceptsOnlyLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "127.0.0.2:8080", "[::1]:8080", "localhost:8080", "LOCALHOST:0"} {
		if err := service.ValidateLoopbackAddress(address); err != nil {
			t.Fatalf("ValidateLoopbackAddress(%q) = %v, want nil", address, err)
		}
	}

	for _, address := range []string{"", ":8080", "0.0.0.0:8080", "[::]:8080", "192.168.1.10:8080", "drift.example:8080", "127.0.0.1", "127.0.0.1:"} {
		err := service.ValidateLoopbackAddress(address)
		if !errors.Is(err, service.ErrAddressNotLoopback) {
			t.Fatalf("ValidateLoopbackAddress(%q) = %v, want ErrAddressNotLoopback", address, err)
		}
	}
}

func TestRequireLabTokenRejectsAMissingOrWrongLocalToken(t *testing.T) {
	labService, err := lab.NewService()
	if err != nil {
		t.Fatalf("lab.NewService() error = %v", err)
	}
	route := service.LabAdapterRoute(labService, "lab-token-value")
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)

	call := func(token string) int {
		request := httptest.NewRequest(http.MethodPost, route.Path+"GetLabStatus", strings.NewReader(`{"workspace":{"workspaceId":"workspace-1"}}`))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set(service.LabTokenHeader, token)
		}
		record := httptest.NewRecorder()
		server.Handler.ServeHTTP(record, request)
		return record.Code
	}

	if status := call(""); status != http.StatusUnauthorized {
		t.Fatalf("GetLabStatus without a token = %d, want %d", status, http.StatusUnauthorized)
	}
	if status := call("lab-token-valu"); status != http.StatusUnauthorized {
		t.Fatalf("GetLabStatus with a wrong token = %d, want %d", status, http.StatusUnauthorized)
	}
	if status := call("lab-token-value"); status != http.StatusOK {
		t.Fatalf("GetLabStatus with the configured token = %d, want %d", status, http.StatusOK)
	}
}
