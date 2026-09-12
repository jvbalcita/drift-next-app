package health

import (
	"net/http/httptest"
	"testing"
)

func TestHandlerReportsHealthyAndReady(t *testing.T) {
	handler := Handler("control-plane", func() bool { return true })

	for _, path := range []string{"/healthz", "/readyz"} {
		record := httptest.NewRecorder()
		handler.ServeHTTP(record, httptest.NewRequest("GET", path, nil))

		if record.Code != 200 {
			t.Fatalf("%s status = %d, want 200", path, record.Code)
		}
		if got := record.Header().Get("content-type"); got != "application/json" {
			t.Fatalf("%s content-type = %q, want application/json", path, got)
		}
	}
}

func TestHandlerReportsNotReady(t *testing.T) {
	handler := Handler("edge-agent", func() bool { return false })
	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest("GET", "/readyz", nil))

	if record.Code != 503 {
		t.Fatalf("ready status = %d, want 503", record.Code)
	}
}
