package transportconnect

import (
	"context"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/networkprofiles"
	store "drift.local/drift-next/internal/store/sqlite"
)

type DiscoveryHandler struct {
	service *discovery.Service
	db      *store.DB
}

func NewDiscoveryHandler(service *discovery.Service, db *store.DB) *DiscoveryHandler {
	return &DiscoveryHandler{service: service, db: db}
}

func (h *DiscoveryHandler) StartScan(ctx context.Context, request *connectrpc.Request[driftv1.StartScanRequest]) (*connectrpc.Response[driftv1.StartScanResponse], error) {
	if request == nil {
		return nil, invalidArgument("start scan request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, err := lookupWorkspace(ctx, h.db, request.Msg.GetWorkspace())
	if err != nil {
		return nil, err
	}
	profileID := networkprofiles.NetworkProfileID(request.Msg.GetNetworkProfileId())
	if profileID == "" {
		return nil, invalidArgument("network profile ID is required")
	}
	key := request.Msg.GetContext().GetIdempotencyKey()
	if key == "" {
		key = request.Msg.GetContext().GetRequestId()
	}
	run, scanErr := h.service.StartScan(ctx, workspace, profileID, key, actorType, actorID)
	if scanErr != nil {
		return nil, MapError(scanErr)
	}
	return connectrpc.NewResponse(&driftv1.StartScanResponse{ScanRun: scanRunProto(run)}), nil
}

func (h *DiscoveryHandler) DecideScanCandidate(ctx context.Context, request *connectrpc.Request[driftv1.DecideScanCandidateRequest]) (*connectrpc.Response[driftv1.DecideScanCandidateResponse], error) {
	if request == nil {
		return nil, invalidArgument("decide scan candidate request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, candidateID, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetCandidate())
	if err != nil {
		return nil, err
	}
	candidate, decideErr := h.service.DecideCandidate(ctx, workspace, discovery.ScanCandidateID(candidateID), request.Msg.GetApprove(), request.Msg.GetReason(), actorType, actorID)
	if decideErr != nil {
		return nil, MapError(decideErr)
	}
	return connectrpc.NewResponse(&driftv1.DecideScanCandidateResponse{Candidate: scanCandidateProto(candidate)}), nil
}

func (h *DiscoveryHandler) RegisterScanCandidate(ctx context.Context, request *connectrpc.Request[driftv1.RegisterScanCandidateRequest]) (*connectrpc.Response[driftv1.RegisterScanCandidateResponse], error) {
	if request == nil {
		return nil, invalidArgument("register scan candidate request is required")
	}
	actorType, actorID, err := requireActor(request.Msg.GetContext())
	if err != nil {
		return nil, err
	}
	workspace, candidateID, err := lookupResourceWorkspace(ctx, h.db, request.Msg.GetCandidate())
	if err != nil {
		return nil, err
	}
	event, candidate, registerErr := h.service.RegisterCandidate(ctx, workspace, discovery.ScanCandidateID(candidateID), request.Msg.GetDeviceDisplayName(), actorType, actorID)
	if registerErr != nil {
		return nil, MapError(registerErr)
	}
	return connectrpc.NewResponse(&driftv1.RegisterScanCandidateResponse{
		DeviceId:   string(event.DeviceID),
		EndpointId: event.EndpointID,
		Candidate:  scanCandidateProto(candidate),
	}), nil
}

func scanRunProto(run discovery.ScanRun) *driftv1.ScanRun {
	finished := run.CompletedAt
	return &driftv1.ScanRun{
		Id:               string(run.ID),
		Workspace:        workspaceRef(run.Workspace),
		NetworkProfileId: string(run.NetworkProfileID),
		State:            scanRunStateProto(run.State),
		RequestedAt:      formatTime(run.RequestedAt),
		FinishedAt:       formatTimePtr(finished),
		Failure:          failureProto(run.FailureClass),
	}
}

func scanCandidateProto(candidate discovery.ScanCandidate) *driftv1.ScanCandidate {
	return &driftv1.ScanCandidate{
		Id:           string(candidate.ID),
		Workspace:    workspaceRef(candidate.Workspace),
		ScanRunId:    string(candidate.ScanRunID),
		CandidateKey: candidate.CandidateKey,
		Host:         candidate.Host,
		Port:         uint32(candidate.Port),
		Serial:       candidate.Serial,
		Fingerprint:  candidate.Fingerprint,
		State:        scanCandidateStateProto(candidate.State),
		DiscoveredAt: formatTime(candidate.DiscoveredAt),
	}
}

func scanRunStateProto(state discovery.ScanRunState) driftv1.ScanRunState {
	switch state {
	case discovery.ScanRequested:
		return driftv1.ScanRunState_SCAN_RUN_STATE_REQUESTED
	case discovery.ScanRunning:
		return driftv1.ScanRunState_SCAN_RUN_STATE_RUNNING
	case discovery.ScanCompleted:
		return driftv1.ScanRunState_SCAN_RUN_STATE_COMPLETED
	case discovery.ScanFailed:
		return driftv1.ScanRunState_SCAN_RUN_STATE_FAILED
	case discovery.ScanCancelled:
		return driftv1.ScanRunState_SCAN_RUN_STATE_CANCELLED
	default:
		return driftv1.ScanRunState_SCAN_RUN_STATE_UNSPECIFIED
	}
}

func scanCandidateStateProto(state discovery.CandidateState) driftv1.ScanCandidateState {
	switch state {
	case discovery.CandidateDiscovered:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_DISCOVERED
	case discovery.CandidatePendingApproval:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_PENDING_APPROVAL
	case discovery.CandidateApproved:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_APPROVED
	case discovery.CandidateRejected:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_REJECTED
	case discovery.CandidateExpired:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_EXPIRED
	case discovery.CandidateRegistered:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_REGISTERED
	default:
		return driftv1.ScanCandidateState_SCAN_CANDIDATE_STATE_UNSPECIFIED
	}
}

func failureProto(class domain.FailureClass) *driftv1.Failure {
	if class == "" {
		return nil
	}
	return &driftv1.Failure{Message: string(class)}
}
