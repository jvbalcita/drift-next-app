package media_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestSnapshotPreviewRequiresSelectedDeviceAuthorization(t *testing.T) {
	t.Parallel()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "media-w", Name: "Media", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	casStore, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifacts.NewService(store.NewArtifactService(db), casStore,
		artifacts.WithClock(clock.NewFixed(time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC))),
		artifacts.WithIDs(ids.NewSequence("art-1", "ref-1")),
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.Store(context.Background(), artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity: artifacts.SensitivitySafe,
		Payload:     []byte("preview-png"), ActorType: "operator", ActorID: "op-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := &media.PreviewService{
		Artifacts: service,
		Authz:     media.DenyByDefaultAuthorizer{Allowed: map[string]map[string]bool{"media-w": {"device-1": true}}},
		Limit:     media.DefaultPreviewLimit,
	}
	if _, _, err := preview.SnapshotPreview(context.Background(), workspace.ID, "device-2", stored.Artifact.ID, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("denied device code = %v", platformerrors.CodeOf(err))
	}
	encoded, truncated, err := preview.SnapshotPreview(context.Background(), workspace.ID, "device-1", stored.Artifact.ID, "operator", "op-1")
	if err != nil || truncated || encoded == "" {
		t.Fatalf("preview = %q truncated=%v err=%v", encoded, truncated, err)
	}
}
