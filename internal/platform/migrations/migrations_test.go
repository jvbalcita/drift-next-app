package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/migrations"
	_ "modernc.org/sqlite"
)

func TestApplyFreshDatabase(t *testing.T) {
	db := openSQLite(t)

	files := fstest.MapFS{
		"0002_workspace.sql": &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
	}
	runner, err := migrations.NewRunner(db, files, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	var tableName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'workspaces'`).Scan(&tableName); err != nil {
		t.Fatalf("workspaces table was not created: %v", err)
	}
	if tableName != "workspaces" {
		t.Fatalf("table name = %q, want workspaces", tableName)
	}

	var version int
	var name, checksum, appliedAt string
	var dirty bool
	if err := db.QueryRow(`SELECT version, name, checksum, applied_at, dirty FROM drift_schema_migrations`).Scan(&version, &name, &checksum, &appliedAt, &dirty); err != nil {
		t.Fatalf("migration ledger row was not created: %v", err)
	}
	if version != 2 || name != "workspace" || checksum == "" || appliedAt == "" || dirty {
		t.Fatalf("ledger row = (%d, %q, %q, %q, %t), want applied version 2", version, name, checksum, appliedAt, dirty)
	}
}

func TestApplyIncrementalUpgradeIsOrderedAndIdempotent(t *testing.T) {
	db := openSQLite(t)

	first, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_first.sql": &fstest.MapFile{Data: []byte("CREATE TABLE first_step (id INTEGER PRIMARY KEY);\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	if err := first.Apply(context.Background()); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}

	incremental, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE first_step (id INTEGER PRIMARY KEY);\n")},
		"0003_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE second_step (id INTEGER PRIMARY KEY REFERENCES first_step(id));\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner(incremental) error = %v", err)
	}
	if err := incremental.Apply(context.Background()); err != nil {
		t.Fatalf("incremental Apply() error = %v", err)
	}
	if err := incremental.Apply(context.Background()); err != nil {
		t.Fatalf("idempotent Apply() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM drift_schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migration ledger rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("migration ledger row count = %d, want 2", count)
	}
	for _, table := range []string{"first_step", "second_step"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %q was not created: %v", table, err)
		}
	}
}

func TestApplyRejectsMalformedDuplicateAndHistoricalMigrationFiles(t *testing.T) {
	tests := []struct {
		name  string
		files fstest.MapFS
	}{
		{
			name: "malformed filename",
			files: fstest.MapFS{
				"migration.sql": &fstest.MapFile{Data: []byte("CREATE TABLE ignored (id INTEGER);\n")},
			},
		},
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"0002_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE first (id INTEGER);\n")},
				"0002_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE second (id INTEGER);\n")},
			},
		},
		{
			name: "historical PostgreSQL bootstrap",
			files: fstest.MapFS{
				"0001_initial.sql": &fstest.MapFile{Data: []byte("CREATE EXTENSION IF NOT EXISTS pgcrypto;\n")},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLite(t)
			runner, err := migrations.NewRunner(db, test.files, migrations.Options{})
			if err != nil {
				t.Fatalf("NewRunner() error = %v", err)
			}
			err = runner.Apply(context.Background())
			if err == nil {
				t.Fatal("Apply() error = nil, want invalid migration input")
			}
			if got := platformerrors.CodeOf(err); got != platformerrors.CodeInvalidInput {
				t.Fatalf("CodeOf(Apply()) = %q, want %q (%v)", got, platformerrors.CodeInvalidInput, err)
			}
			if errors.Is(err, context.Canceled) {
				t.Fatalf("Apply() unexpectedly canceled: %v", err)
			}
		})
	}
}

func TestApplyRejectsAppliedChecksumMutation(t *testing.T) {
	db := openSQLite(t)
	originalSQL := []byte("CREATE TABLE immutable_step (id INTEGER PRIMARY KEY);\n")
	original := fstest.MapFS{
		"0002_immutable.sql": &fstest.MapFile{Data: originalSQL},
	}
	fixed := clock.NewFixed(time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC))
	first, err := migrations.NewRunner(db, original, migrations.Options{Clock: fixed})
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	if err := first.Apply(context.Background()); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}

	mutated := fstest.MapFS{
		"0002_immutable.sql": &fstest.MapFile{Data: []byte("CREATE TABLE changed_step (id INTEGER PRIMARY KEY);\n")},
	}
	second, err := migrations.NewRunner(db, mutated, migrations.Options{Clock: fixed})
	if err != nil {
		t.Fatalf("NewRunner(second) error = %v", err)
	}
	err = second.Apply(context.Background())
	if err == nil {
		t.Fatal("Apply() error = nil, want checksum mismatch")
	}
	if got := platformerrors.CodeOf(err); got != platformerrors.CodeMigrationChecksumMismatch {
		t.Fatalf("CodeOf(Apply()) = %q, want %q (%v)", got, platformerrors.CodeMigrationChecksumMismatch, err)
	}

	var checksum, appliedAt string
	if err := db.QueryRow(`SELECT checksum, applied_at FROM drift_schema_migrations WHERE version = 2`).Scan(&checksum, &appliedAt); err != nil {
		t.Fatalf("read immutable ledger row: %v", err)
	}
	expectedHash := sha256.Sum256(originalSQL)
	if checksum != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("ledger checksum = %q, want exact SHA-256 %q", checksum, hex.EncodeToString(expectedHash[:]))
	}
	if appliedAt != "2026-09-13T12:00:00Z" {
		t.Fatalf("applied_at = %q, want UTC fixed clock value", appliedAt)
	}
}

func TestFailedMigrationLeavesDirtyWithoutPartialSQLAndRequiresRepair(t *testing.T) {
	db := openSQLite(t)
	files := fstest.MapFS{
		"0002_recoverable.sql": &fstest.MapFile{Data: []byte("CREATE TABLE partial_step (id INTEGER PRIMARY KEY);\nINSERT INTO repair_prerequisite (id) VALUES (1);\n")},
	}
	runner, err := migrations.NewRunner(db, files, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err == nil {
		t.Fatal("first Apply() error = nil, want migration SQL failure")
	}

	var partialCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'partial_step'`).Scan(&partialCount); err != nil {
		t.Fatalf("check rolled-back table: %v", err)
	}
	if partialCount != 0 {
		t.Fatalf("partial_step table count = %d, want 0 after failed transaction", partialCount)
	}
	var dirty bool
	if err := db.QueryRow(`SELECT dirty FROM drift_schema_migrations WHERE version = 2`).Scan(&dirty); err != nil {
		t.Fatalf("read dirty migration row: %v", err)
	}
	if !dirty {
		t.Fatal("dirty = false, want failed migration to remain dirty")
	}
	if err := runner.Apply(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeMigrationDirty {
		t.Fatalf("second Apply() error = %v, want migration_dirty", err)
	}

	if _, err := db.Exec(`CREATE TABLE repair_prerequisite (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("prepare explicit repair condition: %v", err)
	}
	if err := runner.Repair(context.Background(), 2); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() after Repair() error = %v", err)
	}
	var repairedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM repair_prerequisite`).Scan(&repairedCount); err != nil {
		t.Fatalf("read repaired prerequisite: %v", err)
	}
	if repairedCount != 1 {
		t.Fatalf("repair prerequisite row count = %d, want 1", repairedCount)
	}
}

func TestApplyClassifiesLockContentionWithinBusyTimeout(t *testing.T) {
	db := openSQLite(t)
	lockConnection, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn(lock) error = %v", err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold SQLite writer reservation: %v", err)
	}
	defer func() { _, _ = lockConnection.ExecContext(context.Background(), "ROLLBACK") }()

	runner, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_locked.sql": &fstest.MapFile{Data: []byte("CREATE TABLE never_reached (id INTEGER);\n")},
	}, migrations.Options{BusyTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	started := time.Now()
	err = runner.Apply(context.Background())
	elapsed := time.Since(started)
	if got := platformerrors.CodeOf(err); got != platformerrors.CodeMigrationLocked {
		t.Fatalf("CodeOf(Apply()) = %q, want %q (%v)", got, platformerrors.CodeMigrationLocked, err)
	}
	if elapsed >= time.Second {
		t.Fatalf("lock contention took %s, want bounded failure under one second", elapsed)
	}
}

func TestApplyHonorsCanceledContextBeforeDatabaseWork(t *testing.T) {
	db := openSQLite(t)
	runner, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_canceled.sql": &fstest.MapFile{Data: []byte("CREATE TABLE should_not_run (id INTEGER);\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Apply(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
}

func TestApplyRejectsAppliedNameMutation(t *testing.T) {
	db := openSQLite(t)
	original, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_original.sql": &fstest.MapFile{Data: []byte("CREATE TABLE named_step (id INTEGER PRIMARY KEY);\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner(original) error = %v", err)
	}
	if err := original.Apply(context.Background()); err != nil {
		t.Fatalf("original Apply() error = %v", err)
	}

	renamed, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_renamed.sql": &fstest.MapFile{Data: []byte("CREATE TABLE named_step (id INTEGER PRIMARY KEY);\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner(renamed) error = %v", err)
	}
	err = renamed.Apply(context.Background())
	if got := platformerrors.CodeOf(err); got != platformerrors.CodeMigrationChecksumMismatch {
		t.Fatalf("CodeOf(Apply()) = %q, want %q (%v)", got, platformerrors.CodeMigrationChecksumMismatch, err)
	}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenInitializesSQLitePragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := migrations.Open(context.Background(), path, migrations.OpenOptions{BusyTimeout: 1234 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 1234 {
		t.Fatalf("busy_timeout = %d, want 1234", busyTimeout)
	}
}
