package sqlite_test

import (
	"context"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
	"testing"
)

func TestOutboxAcknowledgementIsWorkspaceScoped(t *testing.T) {
	db := openTestDB(t)
	svc := store.NewWorkspaceService(db)
	for _, w := range []organizations.Workspace{{ID: "outbox-a", Name: "A", State: organizations.WorkspaceActive}, {ID: "outbox-b", Name: "B", State: organizations.WorkspaceActive}} {
		if err := svc.Create(context.Background(), w, "operator", "op"); err != nil {
			t.Fatal(err)
		}
	}
	repo := store.NewOutboxRepository(db)
	pending, err := repo.ListPending(context.Background(), "outbox-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) == 0 {
		t.Fatal("expected pending outbox message")
	}
	msg := pending[0]
	if err := repo.MarkDelivered(context.Background(), "outbox-b", msg.ID, msg.Attempts); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("wrong-workspace acknowledgement code=%v, want not_found", platformerrors.CodeOf(err))
	}
	unchanged, err := repo.ListPending(context.Background(), "outbox-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged) == 0 || unchanged[0].ID != msg.ID || unchanged[0].Attempts != msg.Attempts {
		t.Fatalf("wrong-workspace acknowledgement changed owner message: %#v", unchanged)
	}
	if err := repo.MarkDelivered(context.Background(), "outbox-a", msg.ID, msg.Attempts); err != nil {
		t.Fatalf("owner acknowledgement error=%v", err)
	}
}
