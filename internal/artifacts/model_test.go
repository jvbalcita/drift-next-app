package artifacts_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/artifacts"
)

func TestArtifactMetadataAndReferenceLifecycleValidation(t *testing.T) {
	created := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	artifact := artifacts.Artifact{ID: "artifact-1", Workspace: "workspace-1", ContentHash: "sha256:fixture", SizeBytes: 10, MediaType: "application/json", SchemaVersion: 1, RetentionClass: artifacts.RetentionOperationalHistory, State: artifacts.Stored, CreatedAt: created}
	if err := artifact.Validate(); err != nil {
		t.Fatal(err)
	}
	deleted := artifact
	deleted.State = artifacts.Deleted
	if err := deleted.Validate(); err == nil {
		t.Fatal("deleted artifact without deletion time accepted")
	}
	reference := artifacts.Reference{ID: "reference-1", Workspace: "workspace-1", ArtifactID: artifact.ID, OwnerType: "observation", OwnerID: "observation-1", State: artifacts.ReferenceActive, CreatedAt: created}
	if err := reference.Validate(); err != nil {
		t.Fatal(err)
	}
	reference.EndedAt = &created
	if err := reference.Validate(); err == nil {
		t.Fatal("active reference with end time accepted")
	}
	reference.State = artifacts.ReferenceEnded
	if err := reference.Validate(); err != nil {
		t.Fatal(err)
	}
}
