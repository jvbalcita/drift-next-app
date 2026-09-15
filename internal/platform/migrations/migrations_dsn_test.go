package migrations

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSQLiteDSNWithPragmasPreservesWindowsBarePath(t *testing.T) {
	const dsn = `C:\Users\operator\drift.db`
	got, err := sqliteDSNWithPragmas(dsn, 2*time.Second)
	if err != nil {
		t.Fatalf("sqliteDSNWithPragmas() error = %v", err)
	}
	if !strings.HasPrefix(got, dsn) {
		t.Fatalf("sqliteDSNWithPragmas() = %q, want bare path prefix %q", got, dsn)
	}
}

func TestSQLiteDSNWithPragmasAddsToExplicitFileURI(t *testing.T) {
	const dsn = `file:///Users/operator/drift.db?cache=shared`
	got, err := sqliteDSNWithPragmas(dsn, 2*time.Second)
	if err != nil {
		t.Fatalf("sqliteDSNWithPragmas() error = %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(result) error = %v", err)
	}
	if parsed.Scheme != "file" || parsed.Path != "/Users/operator/drift.db" {
		t.Fatalf("sqliteDSNWithPragmas() = %q, want file URI path preserved", got)
	}
	if parsed.Query().Get("cache") != "shared" {
		t.Fatalf("sqliteDSNWithPragmas() = %q, want cache=shared preserved", got)
	}
	if parsed.Query().Get("_txlock") != "immediate" {
		t.Fatalf("sqliteDSNWithPragmas() = %q, want _txlock=immediate", got)
	}
}

func TestSQLiteDSNWithPragmasOpensBareMemoryDSN(t *testing.T) {
	dsn, err := sqliteDSNWithPragmas(":memory:", time.Second)
	if err != nil {
		t.Fatalf("sqliteDSNWithPragmas() error = %v", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE dsn_probe (id INTEGER)"); err != nil {
		t.Fatalf("bare memory DSN did not open: %v", err)
	}
}
