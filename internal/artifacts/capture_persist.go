package artifacts

import (
	"context"
	"strings"

	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// CapturePersister adapts Service for observation/recording capture paths.
type CapturePersister struct {
	Service   *Service
	Workspace organizations.WorkspaceID
}

// PersistScreenshot stores PNG bytes through the artifact service only.
// Raw device bytes are admitted only when Sensitivity is left unset: binary
// media fails closed as uncertain-sensitive and omission metadata is retained.
// Use PersistSanitizedScreenshot after a trusted sanitization boundary.
func (p CapturePersister) PersistScreenshot(ctx context.Context, workspace, ownerID, actorID string, png []byte, contentHash string) (string, error) {
	return p.persistScreenshot(ctx, workspace, ownerID, actorID, png, contentHash, "")
}

// PersistSanitizedScreenshot admits PNG bytes only after an explicit safe
// classification from a trusted sanitization boundary.
func (p CapturePersister) PersistSanitizedScreenshot(ctx context.Context, workspace, ownerID, actorID string, png []byte, contentHash string) (string, error) {
	return p.persistScreenshot(ctx, workspace, ownerID, actorID, png, contentHash, SensitivitySafe)
}

func (p CapturePersister) persistScreenshot(ctx context.Context, workspace, ownerID, actorID string, png []byte, contentHash string, sensitivity SensitivityClass) (string, error) {
	if p.Service == nil {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "artifact service is required")
	}
	ws := p.Workspace
	if strings.TrimSpace(workspace) != "" {
		ws = organizations.WorkspaceID(workspace)
	}
	if strings.TrimSpace(actorID) == "" {
		actorID = "capture"
	}
	result, err := p.Service.Store(ctx, StoreRequest{
		Workspace:      ws,
		MediaType:      "image/png",
		Category:       CategoryScreenshot,
		RetentionClass: RetentionExecutionEvidence,
		SchemaVersion:  1,
		Payload:        png,
		DeclaredHash:   contentHash,
		Sensitivity:    sensitivity,
		ActorType:      "edge_agent",
		ActorID:        actorID,
		OwnerType:      "observation",
		OwnerID:        ownerID,
	})
	if err != nil {
		return "", err
	}
	if result.Omitted {
		return string(result.Artifact.ID), platformerrors.New(platformerrors.CodePolicyDenied, "screenshot admission omitted bytes")
	}
	return string(result.Artifact.ID), nil
}

// PersistUITree stores a sanitized UI-tree document through the artifact service.
func (p CapturePersister) PersistUITree(ctx context.Context, workspace, ownerID, actorID string, sanitizedJSON []byte) (string, error) {
	if p.Service == nil {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "artifact service is required")
	}
	ws := p.Workspace
	if strings.TrimSpace(workspace) != "" {
		ws = organizations.WorkspaceID(workspace)
	}
	if strings.TrimSpace(actorID) == "" {
		actorID = "capture"
	}
	result, err := p.Service.Store(ctx, StoreRequest{
		Workspace:      ws,
		MediaType:      "application/json",
		Category:       CategoryUITree,
		RetentionClass: RetentionExecutionEvidence,
		SchemaVersion:  1,
		Payload:        sanitizedJSON,
		ActorType:      "edge_agent",
		ActorID:        actorID,
		OwnerType:      "observation",
		OwnerID:        ownerID,
	})
	if err != nil {
		return "", err
	}
	if result.Omitted {
		return string(result.Artifact.ID), platformerrors.New(platformerrors.CodePolicyDenied, "ui-tree admission omitted bytes")
	}
	return string(result.Artifact.ID), nil
}
