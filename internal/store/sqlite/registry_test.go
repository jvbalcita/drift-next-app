package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestNetworkProfileUpdateRejectsNilContextWithoutRowVersionGuidance(t *testing.T) {
	db := openTestDB(t)
	profile := networkprofiles.NetworkProfile{ID: "profile-update", Workspace: "update-w", Name: "Lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}}
	err := store.NewNetworkProfileService(db).Update(nil, profile, "operator", "op-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("Update(nil context) code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	if strings.Contains(err.Error(), "row version") {
		t.Fatalf("Update(nil context) error = %q, but network profiles no longer have a row version", err)
	}
}

func TestNetworkProfileDeleteSucceedsWithScanHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "delete-w", Name: "Delete", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	profile := networkprofiles.NetworkProfile{ID: "profile-delete", Workspace: "delete-w", Name: "Lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, IsDefault: true}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	if _, _, err := svc.StartScan(ctx, "delete-w", profile.ID, "scan-key-delete", "operator", "op-1"); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	history, err := db.ListScanRuns(ctx, "delete-w")
	if err != nil || len(history) != 1 {
		t.Fatalf("scan history before delete = %#v, error = %v", history, err)
	}

	if err := store.NewNetworkProfileService(db).Delete(ctx, "delete-w", profile.ID, "operator", "op-1"); err != nil {
		t.Fatalf("Delete() error = %v, want deletion to succeed with scan history present", err)
	}

	remaining, err := db.ListScanRuns(ctx, "delete-w")
	if err != nil {
		t.Fatalf("ListScanRuns() after delete error = %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("scan history after profile deletion = %d, want 1 (history is immutable evidence)", len(remaining))
	}
	if remaining[0].NetworkProfileID != "" {
		t.Fatalf("scan history profile reference = %q, want a null reference once the profile is gone", remaining[0].NetworkProfileID)
	}
}

func TestNetworkProfileDeleteReportsNotFoundWithoutCrossWorkspaceDamage(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, workspace := range []organizations.Workspace{
		{ID: "scope-a", Name: "A", State: organizations.WorkspaceActive},
		{ID: "scope-b", Name: "B", State: organizations.WorkspaceActive},
	} {
		if err := store.NewWorkspaceService(db).Create(ctx, workspace, "operator", "op-1"); err != nil {
			t.Fatal(err)
		}
	}
	profiles := store.NewNetworkProfileService(db)
	profileA := networkprofiles.NetworkProfile{ID: "profile-a", Workspace: "scope-a", Name: "A", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}}
	if err := profiles.Create(ctx, profileA, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	// A profile that does not exist, and a profile that exists in another
	// workspace, are both plain not_found here: the delete is workspace-scoped,
	// so it must not report on, or remove, another workspace's row.
	if err := profiles.Delete(ctx, "scope-a", "profile-missing", "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("Delete(unknown profile) code = %v, want not_found; err=%v", platformerrors.CodeOf(err), err)
	}
	if err := profiles.Delete(ctx, "scope-b", profileA.ID, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("Delete(other workspace) code = %v, want not_found; err=%v", platformerrors.CodeOf(err), err)
	}
	listed, err := store.NewNetworkProfileRepository(db).List(ctx, "scope-a")
	if err != nil || len(listed) != 1 || listed[0].ID != profileA.ID {
		t.Fatalf("profiles after refused deletes = %#v err=%v, want the original profile intact", listed, err)
	}
}
