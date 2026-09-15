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
func (p CapturePersister) PersistScreenshot(ctx context.Context, workspace, ownerID, actorID string, png []byte, contentHash string) (string, error) {
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
