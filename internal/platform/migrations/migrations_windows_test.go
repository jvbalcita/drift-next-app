//go:build windows

package migrations_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	"drift.local/drift-next/internal/platform/migrations"
)

func TestOpenAndApplyWindowsBarePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := migrations.Open(context.Background(), path, migrations.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	runner, err := migrations.NewRunner(db, fstest.MapFS{
		"0002_windows.sql": &fstest.MapFile{Data: []byte("CREATE TABLE windows_path_probe (id INTEGER PRIMARY KEY);\n")},
	}, migrations.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	var tableName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'windows_path_probe'`).Scan(&tableName); err != nil {
		t.Fatalf("windows bare-path migration table was not created: %v", err)
	}
}
