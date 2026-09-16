package execution

import (
	"context"
	"testing"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// A reference belongs to the workspace whose operator supplied the value. The
// handle is only unique inside that scope, so a workspace must not be able to
// reach another workspace's value even by naming the handle.
func TestAReferenceIsNotVisibleOutsideItsWorkspace(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(referenceWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := registry.Resolve(context.Background(), "another-workspace", referenceTo(registryHandle, registryValue))
	if err == nil {
		t.Fatal("another workspace released a reference it never registered")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeNotFound {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeNotFound)
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1: a refused lookup in another workspace must not consume the value", held)
	}

	value, err := registry.Resolve(context.Background(), referenceWorkspace, referenceTo(registryHandle, registryValue))
	if err != nil {
		t.Fatalf("the owning workspace could not release its own reference: %v", err)
	}
	if value != registryValue {
		t.Fatal("the released value is not the value that was registered")
	}
}

// Every operation names the scope it acts in. A missing scope is refused rather
// than treated as a wildcard, because a wildcard is what would let one workspace
// reach another's value.
func TestAnOperationWithoutAWorkspaceIsRefused(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register("", registryHandle, registryValue); err == nil {
		t.Fatal("a registration with no workspace was accepted")
	}
	if err := registry.Register("   ", registryHandle, registryValue); err == nil {
		t.Fatal("a registration with a blank workspace was accepted")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: a refused registration holds nothing", held)
	}

	if err := registry.Register(referenceWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := registry.Resolve(context.Background(), "", referenceTo(registryHandle, registryValue)); err == nil {
		t.Fatal("a resolve with no workspace was served")
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1: a resolve with no scope must not consume the value", held)
	}
}

// Capacity is counted per workspace, so one workspace cannot spend another's
// budget and a full workspace does not stop a different one from working.
func TestOneWorkspaceCannotSpendAnothersCapacity(t *testing.T) {
	now := &movingClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)}
	registry, err := NewTextReferenceRegistry(now, DefaultTextReferenceTTL, 1)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register("workspace-a", registryHandle, registryValue); err != nil {
		t.Fatalf("register in the first workspace: %v", err)
	}
	if err := registry.Register("workspace-a", "reference-2", registryValue); err == nil {
		t.Fatal("the first workspace grew past its own capacity")
	}
	if err := registry.Register("workspace-b", registryHandle, registryValue); err != nil {
		t.Fatalf("the second workspace was refused because the first was full: %v", err)
	}
	if held := registry.Held(); held != 2 {
		t.Fatalf("references held = %d, want 2", held)
	}
}
