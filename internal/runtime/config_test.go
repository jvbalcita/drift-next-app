package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"drift.local/drift-next/internal/runtime"
)

func TestLoadOrCreateRuntimeConfigurationIsRestartSafe(t *testing.T) {
	dir := t.TempDir()
	first, err := runtime.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("configuration changed across reload: first=%+v second=%+v", first, second)
	}
	info, err := os.Stat(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime config mode = %o, want 600", info.Mode().Perm())
	}
}
