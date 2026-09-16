package migrations_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/migrations"
)

// The control plane and drift share one SQLite database and may run at
// different revisions, so a binary must refuse to serve a ledger that a newer
// build already advanced past its own embedded migrations.
func TestCheckLedgerNotAheadRejectsLedgerAdvancedByNewerBinary(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "control-plane.db")
	applied := fstest.MapFS{
		"0002_workspace.sql":       &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
		"0003_device.sql":          &fstest.MapFile{Data: []byte("CREATE TABLE devices (id TEXT PRIMARY KEY);\n")},
		"0004_network_profile.sql": &fstest.MapFile{Data: []byte("CREATE TABLE network_profiles (id TEXT PRIMARY KEY);\n")},
	}
	applyMigrationsForGuardTest(t, dsn, applied)

	staleBinary := fstest.MapFS{
		"0002_workspace.sql": &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
		"0003_device.sql":    &fstest.MapFile{Data: []byte("CREATE TABLE devices (id TEXT PRIMARY KEY);\n")},
	}
	err := migrations.CheckLedgerNotAhead(context.Background(), dsn, staleBinary)
	if err == nil {
		t.Fatal("CheckLedgerNotAhead() error = nil, want a refusal because the ledger is ahead of the binary")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeMigrationChecksumMismatch {
		t.Fatalf("CheckLedgerNotAhead() code = %q, want %q (error %v)", code, platformerrors.CodeMigrationChecksumMismatch, err)
	}
	for _, want := range []string{"0004", "0003", "rebuild", "restart", "control plane"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("CheckLedgerNotAhead() error = %q, want it to name both versions and the remedy (%q missing)", err, want)
		}
	}
}

func TestCheckLedgerNotAheadAcceptsLedgerEqualToTheBinary(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "control-plane.db")
	files := fstest.MapFS{
		"0002_workspace.sql": &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
		"0003_device.sql":    &fstest.MapFile{Data: []byte("CREATE TABLE devices (id TEXT PRIMARY KEY);\n")},
	}
	applyMigrationsForGuardTest(t, dsn, files)

	if err := migrations.CheckLedgerNotAhead(context.Background(), dsn, files); err != nil {
		t.Fatalf("CheckLedgerNotAhead() error = %v, want nil for a ledger equal to the binary", err)
	}
}

func TestCheckLedgerNotAheadAcceptsLedgerBehindAndLeavesPendingMigrationsPending(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "control-plane.db")
	applied := fstest.MapFS{
		"0002_workspace.sql": &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
	}
	applyMigrationsForGuardTest(t, dsn, applied)

	newerBinary := fstest.MapFS{
		"0002_workspace.sql":       &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
		"0003_device.sql":          &fstest.MapFile{Data: []byte("CREATE TABLE devices (id TEXT PRIMARY KEY);\n")},
		"0004_network_profile.sql": &fstest.MapFile{Data: []byte("CREATE TABLE network_profiles (id TEXT PRIMARY KEY);\n")},
	}
	if err := migrations.CheckLedgerNotAhead(context.Background(), dsn, newerBinary); err != nil {
		t.Fatalf("CheckLedgerNotAhead() error = %v, want nil for a ledger behind the binary", err)
	}
	if newest := newestLedgerVersionForGuardTest(t, dsn); newest != 2 {
		t.Fatalf("newest ledger version after check = %d, want 2: the startup check must stay read-only", newest)
	}

	applyMigrationsForGuardTest(t, dsn, newerBinary)
	if newest := newestLedgerVersionForGuardTest(t, dsn); newest != 4 {
		t.Fatalf("newest ledger version after applying pending migrations = %d, want 4", newest)
	}
	for _, table := range []string{"devices", "network_profiles"} {
		if !sharedTableExistsForGuardTest(t, dsn, table) {
			t.Fatalf("pending migration did not create table %q", table)
		}
	}
}

func TestCheckLedgerNotAheadAcceptsFreshDatabaseWithoutCreatingLedgerState(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "control-plane.db")
	files := fstest.MapFS{
		"0002_workspace.sql": &fstest.MapFile{Data: []byte("CREATE TABLE workspaces (id TEXT PRIMARY KEY);\n")},
	}

	if err := migrations.CheckLedgerNotAhead(context.Background(), dsn, files); err != nil {
		t.Fatalf("CheckLedgerNotAhead() error = %v, want nil for a fresh database", err)
	}
	if sharedTableExistsForGuardTest(t, dsn, migrations.LedgerTableName) {
		t.Fatalf("CheckLedgerNotAhead() created %s; the startup check must not migrate", migrations.LedgerTableName)
	}
}

func TestCheckLedgerNotAheadClassifiesContextAndInputFailures(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		source   fstest.MapFS
		dsn      string
		wantCode platformerrors.Code
	}{
		{
			name:     "canceled context",
			ctx:      canceledContextForGuardTest(t),
			source:   fstest.MapFS{"0002_workspace.sql": &fstest.MapFile{Data: []byte("SELECT 1;\n")}},
			dsn:      filepath.Join(t.TempDir(), "control-plane.db"),
			wantCode: platformerrors.CodeCanceled,
		},
		{
			name:     "missing migrations",
			ctx:      context.Background(),
			source:   fstest.MapFS{},
			dsn:      filepath.Join(t.TempDir(), "control-plane.db"),
			wantCode: platformerrors.CodeInvalidInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := migrations.CheckLedgerNotAhead(test.ctx, test.dsn, test.source)
			if err == nil {
				t.Fatal("CheckLedgerNotAhead() error = nil, want a classified failure")
			}
			if code := platformerrors.CodeOf(err); code != test.wantCode {
				t.Fatalf("CheckLedgerNotAhead() code = %q, want %q (error %v)", code, test.wantCode, err)
			}
		})
	}
}

func applyMigrationsForGuardTest(t *testing.T, dsn string, files fstest.MapFS) {
	t.Helper()
	db, err := migrations.Open(context.Background(), dsn, migrations.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	runner, err := migrations.NewRunner(db, files, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
}

func newestLedgerVersionForGuardTest(t *testing.T, dsn string) int64 {
	t.Helper()
	db, err := migrations.Open(context.Background(), dsn, migrations.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var newest int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM ` + migrations.LedgerTableName).Scan(&newest); err != nil {
		t.Fatalf("read newest ledger version: %v", err)
	}
	return newest
}

func sharedTableExistsForGuardTest(t *testing.T, dsn string, table string) bool {
	t.Helper()
	db, err := migrations.Open(context.Background(), dsn, migrations.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("read sqlite_master for %q: %v", table, err)
	}
	return count > 0
}

func canceledContextForGuardTest(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
