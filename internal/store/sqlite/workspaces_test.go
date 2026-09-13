package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type fixedIDs struct{ next int }

func (g *fixedIDs) NewID() (string, error) { g.next++; return "id-" + string(rune('0'+g.next)), nil }

func TestWorkspaceServiceCreatesAndReadsInWorkspace(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewWorkspaceRepository(db)
	svc := store.NewWorkspaceService(db)
	want := organizations.Workspace{ID: "workspace-a", Name: "A", State: organizations.WorkspaceActive, RowVersion: 1}
	if err := svc.Create(context.Background(), want, "operator", "op-1"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != want {
		t.Fatalf("Get() = %#v, want %#v", got, want)
	}
	if _, err := store.NewWorkspaceRepository(db).Get(context.Background(), "workspace-b"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("missing Get() code = %v, want not_found", platformerrors.CodeOf(err))
	}
}

func TestWorkspaceServiceUsesOptimisticConcurrencyAndRollsBackAuditFailure(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewWorkspaceRepository(db)
	svc := store.NewWorkspaceService(db)
	if err := svc.Create(context.Background(), organizations.Workspace{ID: "w", Name: "One", State: organizations.WorkspaceActive}, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Transition(context.Background(), "w", organizations.WorkspaceSuspended, 99, "operator", "op"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("stale transition code = %v, want conflict", platformerrors.CodeOf(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	err := svc.Transition(ctx, "w", organizations.WorkspaceSuspended, 1, "operator", "op")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled transition error = %v, want deadline", err)
	}
	got, err := repo.Get(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != organizations.WorkspaceActive || got.RowVersion != 1 {
		t.Fatalf("rollback changed workspace: %#v", got)
	}
}

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var _ = time.Time{}
