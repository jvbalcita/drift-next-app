package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type failingAuditWriter struct{}

func (failingAuditWriter) AppendTx(context.Context, *sql.Tx, store.AuditEntry) error {
	return errors.New("audit writer failure")
}

type failingOutboxWriter struct{}

func (failingOutboxWriter) AppendTx(context.Context, *sql.Tx, store.OutboxEntry) error {
	return errors.New("outbox writer failure")
}

func TestIdempotencySameHashIsDuplicateAndDifferentHashConflicts(t *testing.T) {
	db := openTestDB(t)
	if err := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{ID: "idem-w", Name: "Idem", State: organizations.WorkspaceActive}, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	first, err := db.ReserveIdempotency(context.Background(), "idem-w", "command-1", "workspace.create", "hash-a")
	if err != nil || !first {
		t.Fatalf("first reservation = %v, %v", first, err)
	}
	second, err := db.ReserveIdempotency(context.Background(), "idem-w", "command-1", "workspace.create", "hash-a")
	if err != nil || second {
		t.Fatalf("same-hash retry = %v, %v; want duplicate", second, err)
	}
	if _, err := db.ReserveIdempotency(context.Background(), "idem-w", "command-1", "workspace.create", "hash-b"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("mismatched hash code = %v, want conflict", platformerrors.CodeOf(err))
	}
}

func TestAuditFailureRollsBackWorkspaceMutation(t *testing.T) {
	assertWorkspaceCreateRollsBack(t, failingAuditWriter{})
}

func TestOutboxFailureRollsBackWorkspaceMutation(t *testing.T) {
	assertWorkspaceCreateRollsBack(t, failingOutboxWriter{})
}

func assertWorkspaceCreateRollsBack(t *testing.T, auditOrOutbox any) {
	t.Helper()
	good := openTestDB(t)
	options := store.Options{}
	if writer, ok := auditOrOutbox.(failingAuditWriter); ok {
		options.Audit = writer
	}
	if writer, ok := auditOrOutbox.(failingOutboxWriter); ok {
		options.Outbox = writer
	}
	injected, err := store.FromDB(store.SQLForTest(good), options)
	if err != nil {
		t.Fatal(err)
	}
	err = store.NewWorkspaceService(injected).Create(context.Background(), organizations.Workspace{ID: "rollback-w", Name: "Rollback", State: organizations.WorkspaceActive}, "operator", "op")
	if err == nil {
		t.Fatal("Create() unexpectedly succeeded")
	}
	if _, err := store.NewWorkspaceRepository(injected).Get(context.Background(), "rollback-w"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("workspace after writer failure code = %v, want not_found", platformerrors.CodeOf(err))
	}
}
