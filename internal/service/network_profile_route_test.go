package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// TestNetworkProfileDeleteIsMountedOnTheProductRoute exercises the delete
// through the mounted Connect route instead of calling the handler directly. A
// handler that compiles and passes a unit test proves nothing about whether an
// operator can actually reach the RPC, so this asserts the wiring: the route is
// mounted, the procedure resolves, and the second delete answers with a real
// not_found instead of a success.
func TestNetworkProfileDeleteIsMountedOnTheProductRoute(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	profile := networkprofiles.NetworkProfile{
		ID: "profile-1", Workspace: "workspace-a", Name: "Lab",
		AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555},
	}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	const token = "lab-token-value"
	routes := service.ProductRoutes(transportconnect.NewProductHandlers(db, discovery.NewFakeScanner(nil)), token)
	path := ""
	for _, route := range routes {
		if route.Path == "/"+driftv1connect.NetworkProfileServiceName+"/" {
			path = route.Path
		}
	}
	if path == "" {
		t.Fatalf("ProductRoutes() did not mount %s", driftv1connect.NetworkProfileServiceName)
	}
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", routes...)

	call := func(requestID string) (int, string) {
		body := `{"context":{"requestId":"` + requestID + `","idempotencyKey":"` + requestID + `","actorId":"op-1"},` +
			`"workspace":{"workspaceId":"workspace-a"},"networkProfileId":"profile-1"}`
		request := httptest.NewRequest(http.MethodPost, path+"DeleteNetworkProfile", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(service.LabTokenHeader, token)
		record := httptest.NewRecorder()
		server.Handler.ServeHTTP(record, request)
		return record.Code, record.Body.String()
	}

	if status, body := call("delete-1"); status != http.StatusOK {
		t.Fatalf("mounted DeleteNetworkProfile = %d body=%s, want 200", status, body)
	}
	remaining, err := store.NewNetworkProfileRepository(db).List(ctx, "workspace-a")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("profiles after the mounted delete = %#v err=%v, want none", remaining, err)
	}
	status, body := call("delete-2")
	if status != http.StatusNotFound {
		t.Fatalf("second DeleteNetworkProfile = %d body=%s, want 404 not_found", status, body)
	}
	if !strings.Contains(body, "not_found") {
		t.Fatalf("second DeleteNetworkProfile body = %s, want a not_found code", body)
	}
}
