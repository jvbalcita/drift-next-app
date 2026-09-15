package product_test

import (
	"path/filepath"
	"strings"
	"testing"

	"drift.local/drift-next/internal/product"
)

func TestDefaultControlPlaneDBPathIsDurableAndLocal(t *testing.T) {
	path := product.DefaultControlPlaneDBPath()
	if path == "" || strings.Contains(path, "secret") {
		t.Fatalf("path = %q", path)
	}
	if filepath.Base(path) != "control-plane.db" {
		t.Fatalf("basename = %q, want control-plane.db", filepath.Base(path))
	}
	if strings.Contains(path, string(filepath.Separator)+"tmp"+string(filepath.Separator)) && !strings.Contains(path, "drift-next") {
		t.Fatalf("unexpected ephemeral path %q", path)
	}
}
