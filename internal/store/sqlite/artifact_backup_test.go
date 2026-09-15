package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/backup"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestArtifactBackupRestoreVerification(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "backup-w", Name: "Backup", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	casStore, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifacts.NewService(store.NewArtifactService(db), casStore,
		artifacts.WithClock(clock.NewFixed(time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC))),
		artifacts.WithIDs(ids.NewSequence("a1")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store(context.Background(), artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Payload: []byte("unit"), ActorType: "operator", ActorID: "op-1",
	}); err != nil {
		t.Fatal(err)
	}
	verifier := &backup.Verifier{
		DB:     store.SQLForTest(db),
		Meta:   store.NewArtifactService(db),
		Bytes:  casStore,
		Leases: staleLeaseCounter{},
	}
	report, err := verifier.VerifyExportRestore(context.Background(), workspace.ID)
	if err == nil {
		t.Fatal("expected restore verification to refuse stale leases")
	}
	if !report.ForeignKeysOK || report.VerifiedBytes != 1 || report.StaleLeasesRefused != 2 {
		t.Fatalf("report=%#v", report)
	}
}

type staleLeaseCounter struct{}

func (staleLeaseCounter) CountResumableStale(context.Context, organizations.WorkspaceID, time.Time) (int, error) {
	return 2, nil
}
