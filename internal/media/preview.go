// Package media provides local/snapshot preview and selected-device media
// authorization seams. It does not implement WebRTC, SFU, or TURN.
package media

import (
	"context"
	"encoding/base64"
	"strings"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// DefaultPreviewLimit is the hard maximum for base64 preview payloads (32 KiB PNG).
const DefaultPreviewLimit = 32 << 10

// Authorizer decides whether an actor may view media for a selected device.
type Authorizer interface {
	AuthorizeSelectedDevice(ctx context.Context, workspace organizations.WorkspaceID, deviceID, actorType, actorID string) error
}

// DenyByDefaultAuthorizer requires explicit allowlisting of selected devices.
type DenyByDefaultAuthorizer struct {
	Allowed map[string]map[string]bool // workspace -> deviceID
}

// AuthorizeSelectedDevice fails closed unless the device is allowlisted.
func (a DenyByDefaultAuthorizer) AuthorizeSelectedDevice(_ context.Context, workspace organizations.WorkspaceID, deviceID, actorType, actorID string) error {
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodePolicyDenied, "media actor is required")
	}
	if strings.TrimSpace(deviceID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "selected device is required")
	}
	if a.Allowed == nil || !a.Allowed[string(workspace)][deviceID] {
		return platformerrors.New(platformerrors.CodePolicyDenied, "selected-device media access denied")
	}
	return nil
}

// PreviewService serves bounded local/snapshot previews through the artifact service.
type PreviewService struct {
	Artifacts *artifacts.Service
	Authz     Authorizer
	Limit     int
}

// SnapshotPreview returns a bounded base64 preview for an authorized artifact.
func (s *PreviewService) SnapshotPreview(ctx context.Context, workspace organizations.WorkspaceID, deviceID string, artifactID artifacts.ArtifactID, actorType, actorID string) (string, bool, error) {
	if s == nil || s.Artifacts == nil {
		return "", false, platformerrors.New(platformerrors.CodeInvalidInput, "media preview service is required")
	}
	if s.Authz == nil {
		return "", false, platformerrors.New(platformerrors.CodePolicyDenied, "media authorizer is required")
	}
	if err := s.Authz.AuthorizeSelectedDevice(ctx, workspace, deviceID, actorType, actorID); err != nil {
		return "", false, err
	}
	payload, artifact, err := s.Artifacts.Read(ctx, workspace, artifactID, actorType, actorID)
	if err != nil {
		return "", false, err
	}
	if artifact.Category != artifacts.CategoryScreenshot && artifact.Category != artifacts.CategoryAnnotatedScreenshot {
		return "", false, platformerrors.New(platformerrors.CodeInvalidInput, "preview requires a screenshot artifact")
	}
	limit := s.Limit
	if limit <= 0 {
		limit = DefaultPreviewLimit
	}
	if len(payload) > limit {
		return "", true, nil
	}
	return base64.StdEncoding.EncodeToString(payload), false, nil
}
