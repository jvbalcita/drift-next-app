package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalCORSAllowsTauriAndViteOrigins(t *testing.T) {
	server := NewHTTPServer("test", "127.0.0.1:0")
	request := httptest.NewRequest(http.MethodOptions, "/readyz", nil)
	request.Header.Set("Origin", "tauri://localhost")
	request.Header.Set("Access-Control-Request-Headers", "X-Drift-Lab-Token")
	record := httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", record.Code)
	}
	if got := record.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("allow origin = %q", got)
	}
}

func TestLocalCORSRejectsUnknownOrigin(t *testing.T) {
	server := NewHTTPServer("test", "127.0.0.1:0")
	request := httptest.NewRequest(http.MethodOptions, "/readyz", nil)
	request.Header.Set("Origin", "https://example.com")
	record := httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", record.Code)
	}
}
