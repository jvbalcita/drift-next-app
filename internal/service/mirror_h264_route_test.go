package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestMirrorH264TicketRouteRequiresLabTokenAndLocalConsoleOrigin(t *testing.T) {
	routes := service.MirrorH264Routes(emptyMirrorRoutes{}, "local-secret")
	if len(routes) != 2 {
		t.Fatalf("raw H.264 route count = %d, want ticket and WebSocket routes", len(routes))
	}
	server := service.NewHTTPServer("test", "127.0.0.1:0", routes...)

	request := httptest.NewRequest(http.MethodPost, transportconnect.MirrorH264TicketPath, nil)
	request.Header.Set("Origin", "http://localhost:5173")
	record := httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusUnauthorized {
		t.Fatalf("ticket without lab token status = %d, want 401", record.Code)
	}

	request = httptest.NewRequest(http.MethodPost, transportconnect.MirrorH264TicketPath, nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set(service.LabTokenHeader, "local-secret")
	record = httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusForbidden {
		t.Fatalf("ticket from an unconfigured browser origin status = %d, want 403", record.Code)
	}

	request = httptest.NewRequest(http.MethodPost, transportconnect.MirrorH264TicketPath, nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set(service.LabTokenHeader, "local-secret")
	record = httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	if record.Code != http.StatusBadRequest {
		t.Fatalf("authenticated ticket request without stream identity status = %d, want 400", record.Code)
	}
}

type emptyMirrorRoutes struct{}

func (emptyMirrorRoutes) Open(context.Context, string, string, media.MirrorTransportKind, media.MirrorViewerPurpose, media.MirrorPreview) (transportconnect.DeviceMirrorStream, error) {
	return nil, nil
}

func (emptyMirrorRoutes) Stream(string) (transportconnect.DeviceMirrorStream, bool) {
	return nil, false
}
func (emptyMirrorRoutes) Carrying() []string { return nil }
