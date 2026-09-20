package transportconnect

import (
	"context"
	"strings"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The two artifact ports a catalogued device FILE operation needs, adapted from
// the workspace's own artifact service.
//
// They are two ports rather than one because the two directions are not the same
// authority: a reader cannot write, so an import cannot be turned into an
// export, and a writer cannot read, so an export cannot be turned into an
// import. Neither takes a path: the reader is addressed by an artifact identity
// this workspace holds, and the writer takes bytes this boundary read off a
// device plus a bounded file NAME, which it uses as an owner label rather than
// as a path.
//
// This is the composition root's adapter rather than a member of either package:
// the edge boundary states the port and the artifact service owns the storage,
// and neither should have to know the other's types.

// DeviceArtifactSource binds the artifact service to the device operations'
// artifact ports. Both ports are served by the one service.
type DeviceArtifactSource struct {
	service *artifacts.Service
}

// NewDeviceArtifactSource binds the ports to a constructed artifact service. A
// nil service yields nil, so a composition root that has no artifact store binds
// nothing and the operations that need one refuse rather than acting without it.
func NewDeviceArtifactSource(service *artifacts.Service) *DeviceArtifactSource {
	if service == nil {
		return nil
	}
	return &DeviceArtifactSource{service: service}
}

// ReadArtifact resolves the bytes of one artifact this workspace holds, for the
// operations whose source is stored content rather than a caller's buffer.
//
// It refuses an artifact whose bytes were omitted: an omission is metadata about
// content this product declined to keep, and handing its (absent) bytes to a
// device as if they were the file the operator chose would send a zero-length
// package. The refusal is the artifact boundary's own NOT_FOUND, which the
// operation boundary reports as its own `artifact_unavailable`.
func (s *DeviceArtifactSource) ReadArtifact(ctx context.Context, workspace, artifactID, actorType, actorID string) ([]byte, string, error) {
	if s == nil || s.service == nil {
		return nil, "", platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no artifact store, so a device file operation cannot resolve its source")
	}
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(artifactID) == "" {
		return nil, "", platformerrors.New(platformerrors.CodeInvalidInput, "an artifact read requires a workspace and an artifact")
	}
	payload, artifact, err := s.service.Read(ctx, organizations.WorkspaceID(workspace), artifacts.ArtifactID(artifactID), actorType, actorID)
	if err != nil {
		return nil, "", err
	}
	return payload, artifact.MediaType, nil
}

// StorePulledFile keeps the bytes one export read off a device as an artifact of
// this workspace, and answers its identity.
//
// The bytes go through the artifact service's own admission, unmodified: content
// this product cannot establish as safe is NOT stored, an omission record is
// kept instead, and this answers the content refusal the operation boundary
// reports as its own reason. Storing such bytes anyway would be this adapter
// overruling the admission rule with a caller's convenience, which is exactly
// what the rule exists to prevent. `fileName` is an owner LABEL on the record,
// never a path: the service composes its own storage location.
func (s *DeviceArtifactSource) StorePulledFile(ctx context.Context, workspace, mediaType, fileName string, payload []byte, actorType, actorID string) (string, error) {
	if s == nil || s.service == nil {
		return "", platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no artifact store, so a pulled file cannot be kept")
	}
	if strings.TrimSpace(workspace) == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "a file export requires a workspace")
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "a file export requires the actor it runs on behalf of")
	}
	result, err := s.service.Store(ctx, artifacts.StoreRequest{
		Workspace:      organizations.WorkspaceID(workspace),
		MediaType:      strings.TrimSpace(mediaType),
		Category:       artifacts.CategoryOther,
		RetentionClass: artifacts.RetentionExecutionEvidence,
		SchemaVersion:  1,
		Payload:        payload,
		ActorType:      actorType,
		ActorID:        actorID,
		OwnerType:      "device_file_export",
		OwnerID:        fileName,
	})
	if err != nil {
		return "", err
	}
	if result.Omitted {
		// The service kept omission metadata and no bytes. Reporting the
		// artifact as this export's outcome would name a record whose bytes do
		// not exist, so the refusal is stated as the refusal it is.
		return "", platformerrors.Wrap(platformerrors.CodePolicyDenied, "the artifact store did not admit the bytes the device answered with", execution.ErrContentRefused)
	}
	return string(result.Artifact.ID), nil
}
