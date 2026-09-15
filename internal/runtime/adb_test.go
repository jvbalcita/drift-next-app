package runtime_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"drift.local/drift-next/internal/runtime"
)

func TestDiscoverADBFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adb")
	if err := os.WriteFile(path, []byte("adb"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.DiscoverADB("", func(string) (string, error) { return path, nil }, os.Stat, "darwin", t.TempDir())
	if err != nil || got != path {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestDiscoverADBFromStandardLocation(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "Library", "Android", "sdk", "platform-tools", "adb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("adb"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.DiscoverADB("", func(string) (string, error) { return "", errors.New("not on path") }, os.Stat, "darwin", home)
	if err != nil || got != path {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestDiscoverADBRejectsMissingExecutable(t *testing.T) {
	if _, err := runtime.DiscoverADB("/missing/adb", func(string) (string, error) { return "", errors.New("missing") }, os.Stat, "darwin", t.TempDir()); err == nil {
		t.Fatal("expected missing adb error")
	}
}
