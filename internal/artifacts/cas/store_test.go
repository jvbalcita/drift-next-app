package cas_test

import (
	"os"
	"path/filepath"
	"testing"

	"drift.local/drift-next/internal/artifacts/cas"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestCASPutGetDeleteIdempotentAndRejectsTraversal(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "cas")
	store, err := cas.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("artifact-bytes")
	hash, err := store.Put("workspace-1", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Put("workspace-1", hash, payload)
	if err != nil || again != hash {
		t.Fatalf("idempotent put = %q/%v, want %q", again, err, hash)
	}
	got, err := store.Get("workspace-1", hash)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("get = %q/%v", got, err)
	}
	if err := store.Verify("workspace-1", hash, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put("workspace-1", "sha256:deadbeef", payload); platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("hash mismatch code = %v", platformerrors.CodeOf(err))
	}
	if _, err := store.Put("../escape", "", payload); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("traversal workspace code = %v", platformerrors.CodeOf(err))
	}
	if _, err := store.Get("workspace-1", "sha256:../../etc/passwd"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("traversal hash code = %v", platformerrors.CodeOf(err))
	}
	if err := store.Delete("workspace-1", hash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("workspace-1", hash); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("deleted get code = %v", platformerrors.CodeOf(err))
	}
}

func TestCASRejectsSymlinkObject(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "cas")
	store, err := cas.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("linked")
	hash, err := store.Put("workspace-1", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := store.ObjectRelPath("workspace-1", hash)
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(root, rel)
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, object); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("workspace-1", hash); platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("symlink get code = %v", platformerrors.CodeOf(err))
	}
}

func TestCASOpenRequiresAbsoluteRoot(t *testing.T) {
	t.Parallel()
	if _, err := cas.Open("relative"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("relative root code = %v", platformerrors.CodeOf(err))
	}
}
