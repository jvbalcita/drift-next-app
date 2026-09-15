package transportconnect_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestArtifactHandlerListGetRead(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "connect-w", Name: "Connect", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	casStore, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifacts.NewService(store.NewArtifactService(db), casStore,
		artifacts.WithClock(clock.NewFixed(time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC))),
		artifacts.WithIDs(ids.NewSequence("c1")),
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.Store(context.Background(), artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Payload: []byte("connect-png"), ActorType: "operator", ActorID: "op-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := transportconnect.NewArtifactHandler(service)
	listed, err := handler.ListArtifacts(context.Background(), connectrpc.NewRequest(&driftv1.ListArtifactsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)},
		ActorId:   "op-1",
	}))
	if err != nil || len(listed.Msg.Artifacts) != 1 {
		t.Fatalf("list = %#v err=%v", listed, err)
	}
	got, err := handler.GetArtifact(context.Background(), connectrpc.NewRequest(&driftv1.GetArtifactRequest{
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)},
		ArtifactId: string(stored.Artifact.ID),
		ActorId:    "op-1",
	}))
	if err != nil || got.Msg.Artifact.GetContentHash() == "" {
		t.Fatalf("get = %#v err=%v", got, err)
	}
	read, err := handler.ReadArtifact(context.Background(), connectrpc.NewRequest(&driftv1.ReadArtifactRequest{
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)},
		ArtifactId: string(stored.Artifact.ID),
		ActorId:    "op-1",
	}))
	if err != nil || string(read.Msg.Content) != "connect-png" {
		t.Fatalf("read = %#v err=%v", read, err)
	}
}
