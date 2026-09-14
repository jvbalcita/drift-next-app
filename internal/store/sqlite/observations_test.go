package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/health"
	"drift.local/drift-next/internal/inventory"
	"drift.local/drift-next/internal/observations"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

func observationFixture(workspace organizations.WorkspaceID, id, token string, captured time.Time) observations.ObservationSnapshot {
	return observations.ObservationSnapshot{ID: observations.ObservationID(id), Workspace: workspace, DeviceID: "device-1", CapturedAt: captured, CaptureCorrelationID: "capture-" + id, CoordinateSpace: "screen_px", PackageName: "com.example.fake", ActivityName: ".MainActivity", Orientation: "portrait", DisplayWidth: 1080, DisplayHeight: 1920, UITreeHash: "sha256:tree-" + id, ScreenshotHash: "sha256:image-" + id, Source: observations.SourceFake, ProtocolVersion: "fake-1", ModelVersion: "fixture-1", FreshnessToken: token, CaptureStatus: observations.CaptureComplete, State: observations.Recorded}
}

func observationStoreFixture(t *testing.T) (*store.DB, organizations.WorkspaceID) {
	t.Helper()
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "observations-w", Name: "Observations", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	return db, workspace.ID
}

func TestObservationServiceStoresImmutableHistoryAndCurrentFreshness(t *testing.T) {
	db, workspace := observationStoreFixture(t)
	ctx := context.Background()
	service := store.NewObservationService(db)
	first := observationFixture(workspace, "observation-1", "fresh-1", time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC))
	if err := service.Record(ctx, first, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	second := observationFixture(workspace, "observation-2", "fresh-2", first.CapturedAt.Add(time.Minute))
	second.Truncated = true
	second.CaptureSkewMillis = 125
	if err := service.Record(ctx, second, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	current, err := store.NewObservationRepository(db).Current(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != second.ID || current.FreshnessToken != "fresh-2" || !current.Truncated || current.CaptureSkewMillis != 125 {
		t.Fatalf("current observation = %#v, want second fresh observation", current)
	}
	history, err := store.NewObservationRepository(db).List(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].State != observations.Recorded || history[1].State != observations.Superseded {
		t.Fatalf("observation history = %#v, want recorded then superseded", history)
	}
	duplicate := observationFixture(workspace, "observation-3", "fresh-2", second.CapturedAt.Add(time.Minute))
	if err := service.Record(ctx, duplicate, "edge_agent", "agent-1"); platformerrors.CodeOf(err) != platformerrors.CodeConflict {
		t.Fatalf("duplicate freshness code = %v, want conflict", platformerrors.CodeOf(err))
	}
	partial := observationFixture(workspace, "observation-partial", "fresh-partial", second.CapturedAt.Add(2*time.Minute))
	partial.CaptureStatus = observations.CapturePartial
	partial.ErrorClass = domain.FailureObservation
	if err := service.Record(ctx, partial, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryHealthAndEventServicesKeepCurrentProjectionsAndRejectSensitivePayloads(t *testing.T) {
	db, workspace := observationStoreFixture(t)
	ctx := context.Background()
	observation := observationFixture(workspace, "observation-1", "fresh-1", time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC))
	if err := store.NewObservationService(db).Record(ctx, observation, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	inventoryService := store.NewInventoryService(db)
	first := inventory.Record{ID: "inventory-current", Workspace: workspace, DeviceID: "device-1", SourceObservationID: string(observation.ID), InventoryJSON: `{"apps":["com.example.fake"],"version":"1"}`, ObservedAt: observation.CapturedAt}
	current, err := inventoryService.Record(ctx, first, "edge_agent", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.RowVersion != 1 {
		t.Fatalf("initial inventory row version = %d, want 1", current.RowVersion)
	}
	second := first
	second.InventoryJSON = `{"apps":["com.example.fake","com.example.other"],"version":"2"}`
	second.ObservedAt = first.ObservedAt.Add(time.Minute)
	current, err = inventoryService.Record(ctx, second, "edge_agent", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.RowVersion != 2 || len(current.InventoryJSON) == 0 {
		t.Fatalf("updated inventory = %#v, want row version 2", current)
	}
	snapshots, err := store.NewInventoryRepository(db).ListSnapshots(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("inventory snapshots = %d, want 2", len(snapshots))
	}
	unsafe := first
	unsafe.ID = "inventory-unsafe"
	unsafe.InventoryJSON = `{"access_token":"not-stored"}`
	if _, err := inventoryService.Record(ctx, unsafe, "edge_agent", "agent-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive inventory code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	battery := 87
	if err := store.NewHealthService(db).Record(ctx, health.Sample{ID: "health-1", Workspace: workspace, DeviceID: "device-1", ObservationID: string(observation.ID), Status: health.Healthy, Battery: &battery, SampledAt: observation.CapturedAt, DetailsJSON: `{"agent":"fake","latency_ms":12}`}, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	healthCurrent, err := store.NewHealthRepository(db).Current(ctx, workspace, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if healthCurrent.Status != health.Healthy || healthCurrent.SourceSample != "health-1" {
		t.Fatalf("health current = %#v, want healthy sample", healthCurrent)
	}
	if err := store.NewHealthService(db).Record(ctx, health.Sample{ID: "health-unsafe", Workspace: workspace, DeviceID: "device-1", Status: health.Healthy, SampledAt: observation.CapturedAt, DetailsJSON: `{"cookie":"not-stored"}`}, "edge_agent", "agent-1"); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive health code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	event := events.Event{ID: "event-1", Workspace: workspace, Name: "device.fake.updated", SchemaVersion: 1, CorrelationID: "corr-1", ActorType: events.ActorEdge, ActorID: "agent-1", Source: events.SourceFake, ResourceType: "device", ResourceID: "device-1", PayloadJSON: `{"state":"ready"}`, OccurredAt: observation.CapturedAt}
	if err := store.NewEventService(db).AppendDevice(ctx, event); err != nil {
		t.Fatal(err)
	}
	eventHistory, err := store.NewEventRepository(db).ListDevice(ctx, workspace, "device-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(eventHistory) != 1 || eventHistory[0].ActorType != events.ActorEdge || eventHistory[0].Source != events.SourceFake || eventHistory[0].ResourceID != "device-1" {
		t.Fatalf("device event history = %#v, want typed device event", eventHistory)
	}
	unsafeEvent := event
	unsafeEvent.ID = "event-unsafe"
	unsafeEvent.PayloadJSON = `{"` + "author" + "ization" + `":"Bearer not-stored"}`
	if err := store.NewEventService(db).AppendDevice(ctx, unsafeEvent); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("sensitive event code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
}

func TestArtifactServiceStoresMetadataAndReferencesWithoutBytes(t *testing.T) {
	db, workspace := observationStoreFixture(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	artifact := artifacts.Artifact{ID: "artifact-1", Workspace: workspace, ContentHash: "sha256:fixture", SizeBytes: 128, MediaType: "application/json", SchemaVersion: 1, RetentionClass: artifacts.RetentionOperationalHistory, State: artifacts.Stored, CreatedAt: created}
	if err := store.NewArtifactService(db).Record(ctx, artifact, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewArtifactService(db).Reference(ctx, artifacts.Reference{ID: "reference-1", Workspace: workspace, ArtifactID: artifact.ID, OwnerType: "observation", OwnerID: "observation-1", State: "active", CreatedAt: created}, "edge_agent", "agent-1"); err != nil {
		t.Fatal(err)
	}
	references, err := store.NewArtifactRepository(db).ListReferences(ctx, workspace, "observation", "observation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0].ArtifactID != artifact.ID || references[0].State != artifacts.ReferenceActive {
		t.Fatalf("artifact references = %#v, want one active reference", references)
	}
	got, err := store.NewArtifactRepository(db).Get(ctx, workspace, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentHash != artifact.ContentHash || got.SizeBytes != artifact.SizeBytes || got.State != artifacts.Referenced {
		t.Fatalf("artifact metadata = %#v, want referenced metadata", got)
	}
}
