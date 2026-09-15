package transportconnect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/organizations"
)

const (
	maxArtifactActorBytes = 128
	maxArtifactIDBytes    = 128
	maxArtifactListLimit  = 200
)

// ArtifactAPI is the application boundary adapted by ArtifactHandler.
type ArtifactAPI interface {
	List(ctx context.Context, workspace organizations.WorkspaceID, filter artifacts.ListFilter, actorType, actorID string) ([]artifacts.Artifact, error)
	Get(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID, actorType, actorID string) (artifacts.Artifact, error)
	Read(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID, actorType, actorID string) ([]byte, artifacts.Artifact, error)
	Delete(ctx context.Context, workspace organizations.WorkspaceID, id artifacts.ArtifactID, actorType, actorID string) (artifacts.DeletionOutcome, error)
	Health(ctx context.Context, workspace organizations.WorkspaceID, actorType, actorID string) (artifacts.StorageHealth, error)
}

// ArtifactHandler adapts the artifact service to Connect RPCs.
type ArtifactHandler struct {
	service ArtifactAPI
}

// NewArtifactHandler constructs a thin Connect adapter.
func NewArtifactHandler(service ArtifactAPI) *ArtifactHandler {
	return &ArtifactHandler{service: service}
}

func (h *ArtifactHandler) ListArtifacts(ctx context.Context, request *connectrpc.Request[driftv1.ListArtifactsRequest]) (*connectrpc.Response[driftv1.ListArtifactsResponse], error) {
	if request == nil {
		return nil, invalidArgument("artifact list request is required")
	}
	msg := request.Msg
	if err := validateWorkspace(msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateArtifactActor(msg.GetActorId()); err != nil {
		return nil, err
	}
	limit := int(msg.GetLimit())
	if limit < 0 || limit > maxArtifactListLimit {
		return nil, invalidArgument("artifact list limit is out of range")
	}
	filter := artifacts.ListFilter{Limit: limit}
	for _, state := range msg.GetStates() {
		filter.States = append(filter.States, artifacts.State(state))
	}
	for _, category := range msg.GetCategories() {
		filter.Categories = append(filter.Categories, artifacts.Category(category))
	}
	service, err := h.api()
	if err != nil {
		return nil, err
	}
	listed, listErr := service.List(ctx, organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()), filter, "operator", msg.GetActorId())
	if listErr != nil {
		return nil, MapError(listErr)
	}
	records := make([]*driftv1.ArtifactRecord, 0, len(listed))
	for _, artifact := range listed {
		records = append(records, artifactRecordProto(artifact))
	}
	return connectrpc.NewResponse(&driftv1.ListArtifactsResponse{Artifacts: records}), nil
}

func (h *ArtifactHandler) GetArtifact(ctx context.Context, request *connectrpc.Request[driftv1.GetArtifactRequest]) (*connectrpc.Response[driftv1.GetArtifactResponse], error) {
	if request == nil {
		return nil, invalidArgument("artifact get request is required")
	}
	msg := request.Msg
	if err := validateWorkspace(msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateArtifactActor(msg.GetActorId()); err != nil {
		return nil, err
	}
	if err := validateArtifactID(msg.GetArtifactId()); err != nil {
		return nil, err
	}
	service, err := h.api()
	if err != nil {
		return nil, err
	}
	artifact, getErr := service.Get(ctx, organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()), artifacts.ArtifactID(msg.GetArtifactId()), "operator", msg.GetActorId())
	if getErr != nil {
		return nil, MapError(getErr)
	}
	return connectrpc.NewResponse(&driftv1.GetArtifactResponse{Artifact: artifactRecordProto(artifact)}), nil
}

func (h *ArtifactHandler) ReadArtifact(ctx context.Context, request *connectrpc.Request[driftv1.ReadArtifactRequest]) (*connectrpc.Response[driftv1.ReadArtifactResponse], error) {
	if request == nil {
		return nil, invalidArgument("artifact read request is required")
	}
	msg := request.Msg
	if err := validateWorkspace(msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateArtifactActor(msg.GetActorId()); err != nil {
		return nil, err
	}
	if err := validateArtifactID(msg.GetArtifactId()); err != nil {
		return nil, err
	}
	service, err := h.api()
	if err != nil {
		return nil, err
	}
	content, artifact, readErr := service.Read(ctx, organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()), artifacts.ArtifactID(msg.GetArtifactId()), "operator", msg.GetActorId())
	if readErr != nil {
		return nil, MapError(readErr)
	}
	return connectrpc.NewResponse(&driftv1.ReadArtifactResponse{
		Artifact: artifactRecordProto(artifact),
		Content:  content,
	}), nil
}

func (h *ArtifactHandler) DeleteArtifact(ctx context.Context, request *connectrpc.Request[driftv1.DeleteArtifactRequest]) (*connectrpc.Response[driftv1.DeleteArtifactResponse], error) {
	if request == nil {
		return nil, invalidArgument("artifact delete request is required")
	}
	msg := request.Msg
	if err := validateWorkspace(msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateArtifactActor(msg.GetActorId()); err != nil {
		return nil, err
	}
	if err := validateArtifactID(msg.GetArtifactId()); err != nil {
		return nil, err
	}
	service, err := h.api()
	if err != nil {
		return nil, err
	}
	outcome, deleteErr := service.Delete(ctx, organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()), artifacts.ArtifactID(msg.GetArtifactId()), "operator", msg.GetActorId())
	if deleteErr != nil {
		return nil, MapError(deleteErr)
	}
	return connectrpc.NewResponse(&driftv1.DeleteArtifactResponse{DeletionOutcome: string(outcome)}), nil
}

func (h *ArtifactHandler) GetStorageHealth(ctx context.Context, request *connectrpc.Request[driftv1.GetStorageHealthRequest]) (*connectrpc.Response[driftv1.GetStorageHealthResponse], error) {
	if request == nil {
		return nil, invalidArgument("storage health request is required")
	}
	msg := request.Msg
	if err := validateWorkspace(msg.GetWorkspace()); err != nil {
		return nil, err
	}
	if err := validateArtifactActor(msg.GetActorId()); err != nil {
		return nil, err
	}
	service, err := h.api()
	if err != nil {
		return nil, err
	}
	health, healthErr := service.Health(ctx, organizations.WorkspaceID(msg.GetWorkspace().GetWorkspaceId()), "operator", msg.GetActorId())
	if healthErr != nil {
		return nil, MapError(healthErr)
	}
	return connectrpc.NewResponse(&driftv1.GetStorageHealthResponse{
		WorkspaceId:        health.Workspace,
		CasRootConfigured:  health.CASRootConfigured,
		ObjectCount:        health.ObjectCount,
		TotalBytes:         health.TotalBytes,
		CleanupFailedCount: health.CleanupFailedCount,
		CheckedAt:          health.CheckedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}), nil
}

func (h *ArtifactHandler) api() (ArtifactAPI, error) {
	if h == nil || h.service == nil {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, &safeError{message: "artifact service is not configured"})
	}
	return h.service, nil
}

func validateArtifactActor(actorID string) error {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || len(actorID) > maxArtifactActorBytes {
		return invalidArgument("artifact actor_id is required and bounded")
	}
	return nil
}

func validateArtifactID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxArtifactIDBytes {
		return invalidArgument("artifact_id is required and bounded")
	}
	return nil
}

func artifactRecordProto(artifact artifacts.Artifact) *driftv1.ArtifactRecord {
	record := &driftv1.ArtifactRecord{
		ArtifactId:            string(artifact.ID),
		Workspace:             &driftv1.WorkspaceRef{WorkspaceId: string(artifact.Workspace)},
		ContentHash:           artifact.ContentHash,
		SizeBytes:             artifact.SizeBytes,
		MediaType:             artifact.MediaType,
		SchemaVersion:         uint32(artifact.SchemaVersion),
		RetentionClass:        string(artifact.RetentionClass),
		Category:              string(artifact.Category),
		State:                 string(artifact.State),
		CreatedAt:             artifact.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		DeletionOutcome:       string(artifact.DeletionOutcome),
		FailureClassification: string(artifact.FailureClassification),
		OmissionReason:        artifact.OmissionReason,
	}
	if artifact.UpdatedAt != nil {
		record.UpdatedAt = artifact.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if artifact.DeletedAt != nil {
		record.DeletedAt = artifact.DeletedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return record
}
