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
	// FinishScan completes a running scan, upserting the observed devices and
	// their endpoints, and returns the observed devices with their identity.
	FinishScan(context.Context, organizations.WorkspaceID, ScanRunID, []ObservedDevice, string, string) (ScanRun, []ObservedDevice, error)
	FailScan(context.Context, organizations.WorkspaceID, ScanRunID, string, string, error) (ScanRun, error)
}

type Service struct {
	store   RegistryStore
	scanner Scanner
}

func NewService(store RegistryStore, scanner Scanner) *Service {
	return &Service{store: store, scanner: scanner}
}

// StartScan runs one bounded scan and returns the devices it observed. A scan
// is an observation: each observed device upserts its canonical device and its
// current endpoint. There is no intermediate candidate state and no approval
// step between observing a device and it being visible.
func (s *Service) StartScan(ctx context.Context, workspace organizations.WorkspaceID, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (ScanRun, []ObservedDevice, error) {
	var zero ScanRun
	if s == nil || s.store == nil || s.scanner == nil {
		return zero, nil, fmt.Errorf("discovery service dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	profile, err := s.store.GetNetworkProfile(ctx, workspace, profileID)
	if err != nil {
		return zero, nil, err
	}
	if err := profile.Validate(); err != nil {
		return zero, nil, err
	}

	run, created, err := s.store.BeginScan(ctx, workspace, profileID, key, actorType, actorID)
	if err != nil || !created {
		return run, nil, err
	}
	if err := s.store.MarkScanRunning(ctx, workspace, run.ID, actorType, actorID); err != nil {
		return zero, nil, err
	}
	observations, err := s.scanner.Scan(ctx, profile)
	if err != nil {
		failed, failErr := s.store.FailScan(ctx, workspace, run.ID, actorType, actorID, err)
		if failErr != nil {
			return zero, nil, failErr
		}
		return failed, nil, err
	}
	for _, observation := range observations {
		if !observation.Valid() {
			cause := fmt.Errorf("scanner returned an observation with no serial or host")
			failed, failErr := s.store.FailScan(ctx, workspace, run.ID, actorType, actorID, cause)
			if failErr != nil {
				return zero, nil, failErr
			}
			return failed, nil, cause
		}
	}
	completed, devices, err := s.store.FinishScan(ctx, workspace, run.ID, observations, actorType, actorID)
	return completed, devices, err
}
