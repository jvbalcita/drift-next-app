package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
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
	if firstErr != nil {
		t.Fatalf("first Apply() error = %v", firstErr)
	}
	if secondErr != nil {
		t.Fatalf("second Apply() reported a false concurrent failure: %v", secondErr)
	}

	var dirty bool
	if err := firstDB.QueryRow(`SELECT dirty FROM drift_schema_migrations WHERE version = 2`).Scan(&dirty); err != nil {
		t.Fatalf("read converged migration ledger: %v", err)
	}
	if dirty {
		t.Fatal("converged migration ledger remains dirty")
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
