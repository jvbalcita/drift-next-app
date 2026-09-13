package sqlite_test

import (
	"context"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
	"testing"
)

func TestDeviceAndEdgeAgentReadsAreWorkspaceScoped(t *testing.T) {
	db := openTestDB(t)
	ws := store.NewWorkspaceService(db)
	for _, w := range []organizations.Workspace{{ID: "scope-a", Name: "A", State: organizations.WorkspaceActive}, {ID: "scope-b", Name: "B", State: organizations.WorkspaceActive}} {
		if err := ws.Create(context.Background(), w, "operator", "op"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "scoped-device", Workspace: "scope-a", DisplayName: "Device", PlatformVersion: "fake", State: devices.Registered}, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewDeviceRepository(db).Get(context.Background(), "scope-b", "scoped-device"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("cross-workspace device read code=%v", platformerrors.CodeOf(err))
	}
	if err := store.NewDeviceService(db).Transition(context.Background(), "scope-b", "scoped-device", devices.Active, 1, "operator", "op"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("cross-workspace transition code=%v", platformerrors.CodeOf(err))
	}
	got, err := store.NewDeviceRepository(db).Get(context.Background(), "scope-a", "scoped-device")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != devices.Registered || got.RowVersion != 1 {
		t.Fatalf("cross-workspace transition mutated device: %#v", got)
	}
	if _, err := store.SQLForTest(db).Exec(`INSERT INTO edge_agents (id,workspace_id,display_name,version,state,created_at,updated_at,row_version) VALUES ('scoped-edge','scope-a','Edge','test','pending','2026-09-14T00:00:00Z','2026-09-14T00:00:00Z',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewEdgeAgentRepository(db).Get(context.Background(), "scope-b", "scoped-edge"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("cross-workspace edge read code=%v", platformerrors.CodeOf(err))
	}
}

func TestOpenForwardsBusyTimeoutOption(t *testing.T) {
	db, err := store.Open(context.Background(), "file:busy-timeout-test?mode=memory&cache=shared", store.Options{BusyTimeoutSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var timeout int
	if err := store.SQLForTest(db).QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 2000 {
		t.Fatalf("PRAGMA busy_timeout = %d, want 2000", timeout)
	}
}
