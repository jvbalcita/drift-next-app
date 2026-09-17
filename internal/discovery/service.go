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
	// BeginScan opens a scan run for a saved profile, or — with an absent profile
	// ID — for a range an operator entered, which is not saved policy and so has
	// no profile reference for the run to record.
	BeginScan(context.Context, organizations.WorkspaceID, networkprofiles.NetworkProfileID, string, string, string) (ScanRun, bool, error)
	MarkScanRunning(context.Context, organizations.WorkspaceID, ScanRunID, string, string) error
	// FinishScan completes a running scan, upserting the observed devices and
	// their endpoints, and returns the observed devices with their identity.
	FinishScan(context.Context, organizations.WorkspaceID, ScanRunID, []ObservedDevice, string, string) (ScanRun, []ObservedDevice, error)
	// RecordArrivals upserts transports observed after launch through the same
	// serial-keyed upsert FinishScan uses, without opening a scan run.
	RecordArrivals(context.Context, organizations.WorkspaceID, []ObservedDevice, string, string) ([]ObservedDevice, error)
	// RecordDepartures records transports a watcher observed leaving, as an
	// observation fact about the endpoint each departure ends. It opens no scan
	// run, writes no lifecycle state, and leaves the device row, its identity and
	// its last positive observation alone.
	RecordDepartures(context.Context, organizations.WorkspaceID, []ObservedDevice, string, string) error
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
	return s.runScan(ctx, workspace, profile, profileID, key, actorType, actorID)
}

// StartRangeScan runs one bounded scan of a range an operator ENTERED in the
// console's OTG Setup tab. Its target is the entered range rather than a saved
// Network Profile, which is why this is a path of its own instead of a lookup:
// resolving an entered range to whichever saved profile happens to bound the same
// addresses would make the scan's target a saved resource and report a scan of
// policy nobody entered.
//
// The target is not saved policy, so the run it opens records no profile
// reference — the same nullable historical column a run keeps after its profile
// is deleted. Everything else is the scan path a saved profile takes, because an
// observation is an observation: the same scanner bounds it and the same
// serial-keyed upsert persists it.
func (s *Service) StartRangeScan(ctx context.Context, workspace organizations.WorkspaceID, addressPolicy string, port uint16, key, actorType, actorID string) (ScanRun, []ObservedDevice, error) {
	var zero ScanRun
	if s == nil || s.store == nil || s.scanner == nil {
		return zero, nil, fmt.Errorf("discovery service dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	target, err := networkprofiles.EnteredRange(workspace, addressPolicy, port)
	if err != nil {
		return zero, nil, err
	}
	return s.runScan(ctx, workspace, target, "", key, actorType, actorID)
}

// runScan is the one scan path. profileID names the saved profile the run
// scanned, and is absent for a run against a range an operator entered; the
// profile value carries the bounds the scanner applies either way.
func (s *Service) runScan(ctx context.Context, workspace organizations.WorkspaceID, profile networkprofiles.NetworkProfile, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (ScanRun, []ObservedDevice, error) {
	var zero ScanRun
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

// RecordArrivals persists transports an arrival watcher observed after launch.
// An arrival is an observation, not a scan: the watcher already enumerated the
// transports, so this path opens no scan run and re-runs no enumeration. It is
// deliberately the same serial-keyed upsert a scan uses, because routing an
// arrival anywhere else is what mints a duplicate identity for a device that
// already has one (the 22-cards-for-21-devices symptom). It mints no candidate
// queue and no approval transition.
//
// Every observation in the batch is validated before anything is persisted, so
// a batch carrying an unidentifiable transport is refused whole rather than
// half-applied. An empty batch is a no-op: a poll that found nothing new is not
// a failure.
func (s *Service) RecordArrivals(ctx context.Context, workspace organizations.WorkspaceID, observations []ObservedDevice, actorType, actorID string) ([]ObservedDevice, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("discovery service dependencies are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(observations) == 0 {
		return nil, nil
	}
	for _, observation := range observations {
		if !observation.Valid() {
			return nil, fmt.Errorf("arrival observation carries neither a serial nor a host")
		}
	}
	return s.store.RecordArrivals(ctx, workspace, observations, actorType, actorID)
}

// RecordDepartures persists the transports a watcher observed leaving. It is the
// absence fact that makes a device read as observed-before-and-not-observed-now
// rather than as present: the departure supersedes the current endpoint record of
// the transport that left, so a reader can tell a device that is gone from one
// that was never observed at all.
//
// It is deliberately not the arrival path. A departure must not refresh a
// device's last positive observation or upsert it as a sighting, because that is
// exactly what would revive the device it reports leaving; it records the fact
// about the transport and nothing about the device's identity. The device row
// survives, so a device that returns resolves to the same identity through the
// same serial-keyed upsert a scan uses.
//
// Every departure in the batch is validated before anything is persisted, so a
// batch carrying an unidentifiable transport is refused whole rather than
// half-applied. An empty batch is a no-op: a poll that saw nothing leave is not
// a failure.
func (s *Service) RecordDepartures(ctx context.Context, workspace organizations.WorkspaceID, departures []ObservedDevice, actorType, actorID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("discovery service dependencies are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(departures) == 0 {
		return nil
	}
	for _, departure := range departures {
		if !departure.Valid() {
			return fmt.Errorf("departure observation carries neither a serial nor a host")
		}
	}
	return s.store.RecordDepartures(ctx, workspace, departures, actorType, actorID)
}
