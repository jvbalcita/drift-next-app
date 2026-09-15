package artifacts_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

type denyAuthz struct{}

func (denyAuthz) Authorize(context.Context, organizations.WorkspaceID, artifacts.ArtifactID, string, string, string) error {
	return platformerrors.New(platformerrors.CodePolicyDenied, "denied")
}

type memoryAudit struct{ events []string }

func (a *memoryAudit) Record(_ context.Context, _ organizations.WorkspaceID, _, _, eventName, _, _ string) error {
	a.events = append(a.events, eventName)
	return nil
}

func artifactFixture(t *testing.T) (*artifacts.Service, *store.DB, *cas.Store, organizations.WorkspaceID) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "artifact-w", Name: "Artifacts", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	casStore, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	fixed := clock.NewFixed(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	service, err := artifacts.NewService(
		store.NewArtifactService(db),
		casStore,
		artifacts.WithClock(fixed),
		artifacts.WithIDs(ids.NewSequence("artifact-1", "ref-1", "artifact-2", "ref-2", "artifact-3", "ref-3", "omit-1")),
		artifacts.WithAuditor(&memoryAudit{}),
		artifacts.WithPolicy(artifacts.PolicyConfig{MaxObjectBytes: 1024, MaxWorkspaceBytes: 4096, AllowDisposableCleanup: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, db, casStore, workspace.ID
}

func TestArtifactServiceStoreReadDeleteLifecycleAndQuota(t *testing.T) {
	service, db, casStore, workspace := artifactFixture(t)
	ctx := context.Background()
	payload := []byte("safe-screenshot-bytes")
	result, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity:    artifacts.SensitivitySafe,
		RetentionClass: artifacts.RetentionDisposable, Payload: payload, ActorType: "operator", ActorID: "op-1",
		OwnerType: "observation", OwnerID: "obs-1",
	})
	if err != nil || result.Omitted || result.Artifact.State != artifacts.Referenced {
		t.Fatalf("store = %#v err=%v", result, err)
	}
	dup, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity:    artifacts.SensitivitySafe,
		RetentionClass: artifacts.RetentionDisposable, Payload: payload, ActorType: "operator", ActorID: "op-1",
	})
	if err != nil || dup.Artifact.ID != result.Artifact.ID {
		t.Fatalf("duplicate store = %#v err=%v", dup, err)
	}
	content, got, err := service.Read(ctx, workspace, result.Artifact.ID, "operator", "op-1")
	if err != nil || string(content) != string(payload) || got.ContentHash == "" {
		t.Fatalf("read = %q/%#v/%v", content, got, err)
	}
	// Protected observation reference blocks delete.
	if _, err := service.Delete(ctx, workspace, result.Artifact.ID, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("protected delete code = %v", platformerrors.CodeOf(err))
	}
	oversized := make([]byte, 2048)
	if _, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity: artifacts.SensitivitySafe,
		Payload:     oversized, ActorType: "operator", ActorID: "op-1",
	}); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("quota code = %v", platformerrors.CodeOf(err))
	}
	denied, err := artifacts.NewService(store.NewArtifactService(db), casStore, artifacts.WithAuthorizer(denyAuthz{}), artifacts.WithIDs(ids.NewSequence("x")), artifacts.WithClock(clock.NewFixed(time.Now().UTC())))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := denied.Read(ctx, workspace, result.Artifact.ID, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unauthorized read code = %v", platformerrors.CodeOf(err))
	}
	_ = casStore
}

func TestStoreIdempotentDoesNotDoubleChargeWorkspaceQuota(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "quota.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "ws-quota", Name: "Quota", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	casStore, err := cas.Open(filepath.Join(t.TempDir(), "cas-quota"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifacts.NewService(
		store.NewArtifactService(db),
		casStore,
		artifacts.WithClock(clock.NewFixed(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))),
		artifacts.WithIDs(ids.NewSequence("artifact-q1", "ref-q1", "artifact-q2", "ref-q2")),
		artifacts.WithAuditor(&memoryAudit{}),
		artifacts.WithPolicy(artifacts.PolicyConfig{MaxObjectBytes: 8192, MaxWorkspaceBytes: 4096, AllowDisposableCleanup: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Fill nearly to the workspace limit with unique bytes.
	filler := make([]byte, 4000)
	for i := range filler {
		filler[i] = byte(i % 251)
	}
	first, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "application/octet-stream", Category: artifacts.CategoryOther,
		Sensitivity: artifacts.SensitivitySafe, RetentionClass: artifacts.RetentionDisposable,
		Payload: filler, ActorType: "operator", ActorID: "op-1",
		OwnerType: "observation", OwnerID: "obs-quota-1",
	})
	if err != nil || first.Omitted {
		t.Fatalf("fill store = %#v err=%v", first, err)
	}
	// A distinct second payload of the same size would exceed quota.
	other := make([]byte, 4000)
	for i := range other {
		other[i] = byte((i + 7) % 251)
	}
	if _, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "application/octet-stream", Category: artifacts.CategoryOther,
		Sensitivity: artifacts.SensitivitySafe, RetentionClass: artifacts.RetentionDisposable,
		Payload: other, ActorType: "operator", ActorID: "op-1",
	}); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("new payload over quota code = %v", platformerrors.CodeOf(err))
	}
	// Re-storing the original bytes (new owner reference) must remain idempotent.
	dup, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace.ID, MediaType: "application/octet-stream", Category: artifacts.CategoryOther,
		Sensitivity: artifacts.SensitivitySafe, RetentionClass: artifacts.RetentionDisposable,
		Payload: filler, ActorType: "operator", ActorID: "op-1",
		OwnerType: "observation", OwnerID: "obs-quota-2",
	})
	if err != nil || dup.Artifact.ID != first.Artifact.ID {
		t.Fatalf("idempotent store under quota pressure = %#v err=%v", dup, err)
	}
}

func TestDeleteBlocksProtectedRetentionWhileStored(t *testing.T) {
	service, _, _, workspace := artifactFixture(t)
	ctx := context.Background()
	result, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity: artifacts.SensitivitySafe, RetentionClass: artifacts.RetentionExecutionEvidence,
		Payload: []byte("evidence-bytes"), ActorType: "operator", ActorID: "op-1",
	})
	if err != nil || result.Omitted {
		t.Fatalf("store = %#v err=%v", result, err)
	}
	if result.Artifact.State != artifacts.Stored && result.Artifact.State != artifacts.Referenced {
		t.Fatalf("state = %s", result.Artifact.State)
	}
	outcome, err := service.Delete(ctx, workspace, result.Artifact.ID, "operator", "op-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeConflict || outcome != artifacts.DeletionOutcomeSkippedProtected {
		t.Fatalf("protected retention delete = %v/%v", outcome, err)
	}
}

func TestArtifactAdmissionRejectsSensitiveAndRecordsOmission(t *testing.T) {
	service, _, _, workspace := artifactFixture(t)
	ctx := context.Background()
	result, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "application/json", Category: artifacts.CategoryUITree,
		Payload: []byte(`{"password":"TEST_ONLY_PASSWORD_SENTINEL"}`), ActorType: "operator", ActorID: "op-1",
	})
	if err != nil || !result.Omitted || result.Artifact.Category != artifacts.CategoryOmission {
		t.Fatalf("omission = %#v err=%v", result, err)
	}
	decision := artifacts.AdmitWithClassification("text/plain", []byte("ok"), artifacts.SensitivityUncertainSensitive)
	if decision.Allowed {
		t.Fatal("uncertain-sensitive must fail closed")
	}
	binary := artifacts.AdmitBytes("image/png", []byte("raw-png-bytes"))
	if binary.Allowed || binary.Sensitivity != artifacts.SensitivityUncertainSensitive {
		t.Fatalf("unsanitized binary must fail closed: %#v", binary)
	}
	safeBinary := artifacts.AdmitWithClassification("image/png", []byte("raw-png-bytes"), artifacts.SensitivitySafe)
	if !safeBinary.Allowed {
		t.Fatalf("explicit SensitivitySafe binary must admit structurally: %#v", safeBinary)
	}
}

func TestArtifactWorkspaceIsolation(t *testing.T) {
	service, db, _, workspace := artifactFixture(t)
	ctx := context.Background()
	other := organizations.Workspace{ID: "other-w", Name: "Other", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(ctx, other, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity: artifacts.SensitivitySafe,
		Payload:     []byte("a"), ActorType: "operator", ActorID: "op-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, other.ID, result.Artifact.ID, "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("cross-workspace get code = %v", platformerrors.CodeOf(err))
	}
}

func TestCleanupFailedVisibilityAndOrphans(t *testing.T) {
	service, db, casStore, workspace := artifactFixture(t)
	ctx := context.Background()
	result, err := service.Store(ctx, artifacts.StoreRequest{
		Workspace: workspace, MediaType: "image/png", Category: artifacts.CategoryScreenshot,
		Sensitivity:    artifacts.SensitivitySafe,
		RetentionClass: artifacts.RetentionDisposable, Payload: []byte("orphan-bytes"),
		ActorType: "operator", ActorID: "op-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := store.NewArtifactService(db)
	if err := casStore.Delete(string(workspace), result.Artifact.ContentHash); err != nil {
		t.Fatal(err)
	}
	from := result.Artifact.State
	if err := meta.UpdateLifecycle(ctx, workspace, result.Artifact.ID, from, artifacts.EligibleForDeletion, "", "", "", time.Now().UTC(), "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := meta.UpdateLifecycle(ctx, workspace, result.Artifact.ID, artifacts.EligibleForDeletion, artifacts.CleanupFailed, artifacts.DeletionOutcomeCleanupFailed, artifacts.FailureCleanup, "", time.Now().UTC(), "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	failed, err := meta.ListCleanupFailed(ctx, workspace)
	if err != nil || len(failed) != 1 {
		t.Fatalf("cleanup_failed = %#v err=%v", failed, err)
	}
	orphans, err := meta.ListOrphanMetadata(ctx, workspace, func(contentHash string) (bool, error) {
		return casStore.Exists(string(workspace), contentHash)
	})
	if err != nil || len(orphans) == 0 {
		t.Fatalf("orphans = %#v err=%v", orphans, err)
	}
	_ = service
}

func TestCapturePersisterIsolation(t *testing.T) {
	service, _, _, workspace := artifactFixture(t)
	persister := artifacts.CapturePersister{Service: service, Workspace: workspace}
	id, err := persister.PersistScreenshot(context.Background(), "caller-should-not-win", "owner-1", "op-1", []byte("png-bytes"), "")
	if err == nil || id == "" || platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unsanitized screenshot must omit with policy denial = %q/%v", id, err)
	}
	omitted, err := service.Get(context.Background(), workspace, artifacts.ArtifactID(id), "operator", "op-1")
	if err != nil || omitted.Category != artifacts.CategoryOmission || omitted.Workspace != workspace {
		t.Fatalf("unsanitized screenshot omission = %#v/%v", omitted, err)
	}
	id, err = persister.PersistSanitizedScreenshot(context.Background(), "caller-should-not-win", "owner-1", "op-1", []byte("png-bytes"), "")
	if err != nil || id == "" {
		t.Fatalf("sanitized screenshot persist = %q/%v", id, err)
	}
	got, err := service.Get(context.Background(), workspace, artifacts.ArtifactID(id), "operator", "op-1")
	if err != nil || got.Workspace != workspace || got.Category != artifacts.CategoryScreenshot {
		t.Fatalf("bound workspace must win over caller placeholder: %#v/%v", got, err)
	}
	treeID, err := persister.PersistUITree(context.Background(), "caller-should-not-win", "owner-1", "op-1", []byte(`{"password":"TEST_ONLY_PASSWORD_SENTINEL"}`))
	if err == nil || treeID == "" || platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("sensitive ui tree must omit with policy denial = %q/%v", treeID, err)
	}
	tree, err := service.Get(context.Background(), workspace, artifacts.ArtifactID(treeID), "operator", "op-1")
	if err != nil || tree.Category != artifacts.CategoryOmission {
		t.Fatalf("sensitive ui tree omission = %#v/%v", tree, err)
	}
}
