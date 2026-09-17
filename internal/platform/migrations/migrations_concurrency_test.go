package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestApplyConcurrentRunnersDoNotReportFalseDirty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	firstDB := openConcurrentTestSQLite(t, path)
	secondDB := openConcurrentTestSQLite(t, path)
	files := fstest.MapFS{
		"0002_concurrent.sql": &fstest.MapFile{Data: []byte("CREATE TABLE concurrent_step (id INTEGER PRIMARY KEY);\n")},
	}
	first, err := NewRunner(firstDB, files, Options{BusyTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	second, err := NewRunner(secondDB, files, Options{BusyTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewRunner(second) error = %v", err)
	}

	firstRead := make(chan struct{})
	secondRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	first.afterLedgerRead = func() {
		close(firstRead)
		<-releaseFirst
	}
	second.afterLedgerRead = func() {
		close(secondRead)
		<-releaseSecond
	}

	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { firstResult <- first.Apply(context.Background()) }()
	go func() { secondResult <- second.Apply(context.Background()) }()

	waitForConcurrentRead(t, firstRead)
	waitForConcurrentRead(t, secondRead)
	close(releaseFirst)
	close(releaseSecond)
	firstErr := <-firstResult
	secondErr := <-secondResult
	for name, err := range map[string]error{"first": firstErr, "second": secondErr} {
		if err != nil && platformerrors.CodeOf(err) != platformerrors.CodeMigrationDirty {
			t.Fatalf("%s Apply() error = %v, want nil or migration_dirty", name, err)
		}
	}
	if firstErr != nil && secondErr != nil {
		t.Fatalf("both concurrent Apply() calls failed: first = %v, second = %v", firstErr, secondErr)
	}
	t.Logf("concurrent Apply outcomes: first=%s second=%s", platformerrors.CodeOf(firstErr), platformerrors.CodeOf(secondErr))

	var version int
	var name, checksum, appliedAt string
	var dirty bool
	if err := firstDB.QueryRow(`SELECT version, name, checksum, applied_at, dirty FROM drift_schema_migrations WHERE version = 2`).Scan(&version, &name, &checksum, &appliedAt, &dirty); err != nil {
		t.Fatalf("read converged migration ledger: %v", err)
	}
	if version != 2 || name != "concurrent" || checksum == "" || appliedAt == "" || dirty {
		t.Fatalf("converged migration ledger = version %d, name %q, checksum %q, applied_at %q, dirty %t; want a clean version 2 row", version, name, checksum, appliedAt, dirty)
	}
	var tableName string
	if err := firstDB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'concurrent_step'`).Scan(&tableName); err != nil {
		t.Fatalf("concurrent migration table was not created: %v", err)
	}
}

func openConcurrentTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func waitForConcurrentRead(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent runner did not reach the post-ledger-read barrier")
	}
}
