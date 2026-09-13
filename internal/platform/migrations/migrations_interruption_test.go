package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	platformerrors "drift.local/drift-next/internal/platform/errors"
	_ "modernc.org/sqlite"
)

func TestCommittedDirtyMarkerSurvivesRunnerInterruptionAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.db")
	files := fstest.MapFS{
		"0002_interrupted.sql": &fstest.MapFile{Data: []byte("CREATE TABLE interrupted_step (id INTEGER PRIMARY KEY);\n")},
	}
	firstDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(first) error = %v", err)
	}
	firstRunner, err := NewRunner(firstDB, files, Options{})
	if err != nil {
		_ = firstDB.Close()
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	ctx := context.Background()
	conn, err := firstDB.Conn(ctx)
	if err != nil {
		_ = firstDB.Close()
		t.Fatalf("db.Conn() error = %v", err)
	}
	if err := firstRunner.configureConnection(ctx, conn); err != nil {
		_ = conn.Close()
		_ = firstDB.Close()
		t.Fatalf("configureConnection() error = %v", err)
	}
	if err := firstRunner.bootstrapLedger(ctx, conn); err != nil {
		_ = conn.Close()
		_ = firstDB.Close()
		t.Fatalf("bootstrapLedger() error = %v", err)
	}
	items, err := loadMigrations(ctx, files)
	if err != nil {
		_ = conn.Close()
		_ = firstDB.Close()
		t.Fatalf("loadMigrations() error = %v", err)
	}
	markedDirty, err := firstRunner.markDirty(ctx, conn, items[0])
	if err != nil {
		_ = conn.Close()
		_ = firstDB.Close()
		t.Fatalf("markDirty() error = %v", err)
	}
	if !markedDirty {
		_ = conn.Close()
		_ = firstDB.Close()
		t.Fatal("markDirty() = false, want a newly committed dirty marker")
	}
	// Closing the connection and database immediately after the committed
	// marker models a runner being interrupted before it can execute SQL. A
	// true os.Exit child would terminate the test binary before cleanup and add
	// no coverage beyond this durable boundary.
	if err := conn.Close(); err != nil {
		_ = firstDB.Close()
		t.Fatalf("close interrupted connection: %v", err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatalf("close interrupted database: %v", err)
	}

	resumedDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(resumed) error = %v", err)
	}
	t.Cleanup(func() { _ = resumedDB.Close() })
	resumedRunner, err := NewRunner(resumedDB, files, Options{})
	if err != nil {
		t.Fatalf("NewRunner(resumed) error = %v", err)
	}
	if err := resumedRunner.Apply(ctx); platformerrors.CodeOf(err) != platformerrors.CodeMigrationDirty {
		t.Fatalf("resumed Apply() error = %v, want migration_dirty", err)
	}
	if err := resumedRunner.Repair(ctx, 2); err != nil {
		t.Fatalf("Repair() after interrupted marker error = %v", err)
	}
	if err := resumedRunner.Apply(ctx); err != nil {
		t.Fatalf("Apply() after Repair() error = %v", err)
	}
}
