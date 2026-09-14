package discovery

import (
	"context"
	"fmt"

	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
)

// RegistryStore is the application seam for discovery orchestration. The
// SQLite adapter implements the transaction-heavy methods; tests can use a
// deterministic in-memory adapter without reimplementing scan policy.
type RegistryStore interface {
	GetNetworkProfile(context.Context, organizations.WorkspaceID, networkprofiles.NetworkProfileID) (networkprofiles.NetworkProfile, error)
	BeginScan(context.Context, organizations.WorkspaceID, networkprofiles.NetworkProfileID, string, string, string) (ScanRun, bool, error)
	MarkScanRunning(context.Context, organizations.WorkspaceID, ScanRunID, string, string) error
	FinishScan(context.Context, organizations.WorkspaceID, ScanRunID, []ObservedCandidate, string, string) (ScanRun, error)
	FailScan(context.Context, organizations.WorkspaceID, ScanRunID, string, string, error) (ScanRun, error)
	DecideCandidate(context.Context, organizations.WorkspaceID, ScanCandidateID, bool, string, string, string) (ScanCandidate, error)
	ExpireCandidate(context.Context, organizations.WorkspaceID, ScanCandidateID, string, string) (ScanCandidate, error)
	RegisterCandidate(context.Context, organizations.WorkspaceID, ScanCandidateID, string, string, string) (RegistrationEvent, ScanCandidate, error)
}

type Service struct {
	store   RegistryStore
	scanner Scanner
}

func NewService(store RegistryStore, scanner Scanner) *Service {
	return &Service{store: store, scanner: scanner}
}

func (s *Service) StartScan(ctx context.Context, workspace organizations.WorkspaceID, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (ScanRun, error) {
	var zero ScanRun
	if s == nil || s.store == nil || s.scanner == nil {
		return zero, fmt.Errorf("discovery service dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	profile, err := s.store.GetNetworkProfile(ctx, workspace, profileID)
	if err != nil {
		return zero, err
	}
	if err := profile.Validate(); err != nil {
		return zero, err
	}
	if profile.State != networkprofiles.Active {
		return zero, fmt.Errorf("network profile is not active")
	}
	run, created, err := s.store.BeginScan(ctx, workspace, profileID, key, actorType, actorID)
	if err != nil || !created {
		return run, err
	}
	if err := s.store.MarkScanRunning(ctx, workspace, run.ID, actorType, actorID); err != nil {
		return zero, err
	}
	observations, err := s.scanner.Scan(ctx, profile)
	if err != nil {
		failed, failErr := s.store.FailScan(ctx, workspace, run.ID, actorType, actorID, err)
		if failErr != nil {
			return zero, failErr
		}
		return failed, err
	}
	for _, observation := range observations {
		if !observation.Valid() {
			failed, failErr := s.store.FailScan(ctx, workspace, run.ID, actorType, actorID, fmt.Errorf("fake scanner returned an invalid candidate"))
			if failErr != nil {
				return zero, failErr
			}
			return failed, fmt.Errorf("fake scanner returned an invalid candidate")
		}
	}
	return s.store.FinishScan(ctx, workspace, run.ID, observations, actorType, actorID)
}

func (s *Service) DecideCandidate(ctx context.Context, workspace organizations.WorkspaceID, candidateID ScanCandidateID, approve bool, reason, actorType, actorID string) (ScanCandidate, error) {
	if s == nil || s.store == nil {
		return ScanCandidate{}, fmt.Errorf("discovery service store is required")
	}
	return s.store.DecideCandidate(ctx, workspace, candidateID, approve, reason, actorType, actorID)
}

func (s *Service) RegisterCandidate(ctx context.Context, workspace organizations.WorkspaceID, candidateID ScanCandidateID, displayName, actorType, actorID string) (RegistrationEvent, ScanCandidate, error) {
	if s == nil || s.store == nil {
		return RegistrationEvent{}, ScanCandidate{}, fmt.Errorf("discovery service store is required")
	}
	return s.store.RegisterCandidate(ctx, workspace, candidateID, displayName, actorType, actorID)
}

func (s *Service) ExpireCandidate(ctx context.Context, workspace organizations.WorkspaceID, candidateID ScanCandidateID, actorType, actorID string) (ScanCandidate, error) {
	if s == nil || s.store == nil {
		return ScanCandidate{}, fmt.Errorf("discovery service store is required")
	}
	return s.store.ExpireCandidate(ctx, workspace, candidateID, actorType, actorID)
}
