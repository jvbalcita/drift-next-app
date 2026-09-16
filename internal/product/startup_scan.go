package product

import (
	"context"
	"fmt"
	"sync"
	"time"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
)

const (
	// defaultStartupScanTimeout bounds the single scan a control plane performs
	// on startup. It is a convenience scan, never a reason for a process to hang
	// or to keep working after shutdown began.
	defaultStartupScanTimeout = 30 * time.Second
	// startupScanRecordTimeout bounds the bookkeeping written after a scan was
	// interrupted. Recording happens on a context detached from the cancelled
	// scan so the outcome still lands, but it stays bounded.
	startupScanRecordTimeout = 5 * time.Second

	// autoScanActorType and autoScanActorID identify startup-initiated work in
	// the same audit trail an operator-initiated scan uses.
	autoScanActorType = "system"
	autoScanActorID   = "control-plane"
)

// AutoScanState is the recorded outcome of the one startup scan. Every state is
// a normal startup outcome: none of them aborts the process.
type AutoScanState string

const (
	AutoScanCompleted          AutoScanState = "completed"
	AutoScanFailed             AutoScanState = "failed"
	AutoScanCancelled          AutoScanState = "cancelled"
	AutoScanNoWorkspace        AutoScanState = "no_workspace"
	AutoScanAmbiguousWorkspace AutoScanState = "ambiguous_workspace"
	AutoScanNoDefaultProfile   AutoScanState = "no_default_profile"
)

// AutoScanOutcome is what the control plane records and logs about its startup
// scan. Err carries the scan failure; RecordErr reports bookkeeping that could
// not be written. Neither is fatal to startup.
type AutoScanOutcome struct {
	State     AutoScanState
	Workspace organizations.WorkspaceID
	ProfileID networkprofiles.NetworkProfileID
	ScanRunID discovery.ScanRunID
	Devices   []discovery.ObservedDevice
	Err       error
	RecordErr error
}

// Report renders the startup record line. It carries identifiers and counts
// only, never device evidence or profile addresses.
func (o AutoScanOutcome) Report() string {
	switch o.State {
	case AutoScanCompleted:
		return fmt.Sprintf("startup auto-scan completed on network profile %s: %d device(s) observed (scan run %s)", o.ProfileID, len(o.Devices), o.ScanRunID)
	case AutoScanFailed:
		return fmt.Sprintf("startup auto-scan failed on network profile %s: %v (scan run %s)", o.ProfileID, o.Err, o.ScanRunID)
	case AutoScanCancelled:
		return fmt.Sprintf("startup auto-scan cancelled on network profile %s: %v", o.ProfileID, o.Err)
	case AutoScanNoDefaultProfile:
		return fmt.Sprintf("startup auto-scan skipped: workspace %s has no default network profile", o.Workspace)
	case AutoScanAmbiguousWorkspace:
		return "startup auto-scan skipped: more than one workspace is configured"
	default:
		return "startup auto-scan skipped: no workspace exists yet"
	}
}

// WorkspaceLister and NetworkProfileLister are the read seams the startup scan
// needs to find what an operator would scan: the single workspace and its
// default network profile.
type WorkspaceLister interface {
	List(context.Context) ([]organizations.Workspace, error)
}

type NetworkProfileLister interface {
	List(context.Context, organizations.WorkspaceID) ([]networkprofiles.NetworkProfile, error)
}

// StartupScanRunner is the existing scan machinery. The startup scan runs the
// same orchestration a manual scan runs; it does not re-implement any of it.
type StartupScanRunner interface {
	StartScan(ctx context.Context, workspace organizations.WorkspaceID, profileID networkprofiles.NetworkProfileID, key, actorType, actorID string) (discovery.ScanRun, []discovery.ObservedDevice, error)
}

// ScanRunRecorder closes out a scan run the scan machinery could not record
// itself: a scan cancelled mid-flight cannot write on its own context, so the
// run would otherwise stay 'running' forever.
type ScanRunRecorder interface {
	ListScanRuns(context.Context, organizations.WorkspaceID) ([]discovery.ScanRun, error)
	FailScan(ctx context.Context, workspace organizations.WorkspaceID, id discovery.ScanRunID, actorType, actorID string, cause error) (discovery.ScanRun, error)
}

// StartupAutoScanConfig wires the startup scan to the control plane's store and
// scan machinery.
type StartupAutoScanConfig struct {
	Workspaces WorkspaceLister
	Profiles   NetworkProfileLister
	Runner     StartupScanRunner
	// Recorder is optional: without it an interrupted scan is still reported,
	// only its bookkeeping is skipped.
	Recorder ScanRunRecorder
	// StartedAt makes the scan's idempotency key unique per process start.
	StartedAt time.Time
	// Timeout bounds the scan; a zero value uses defaultStartupScanTimeout.
	Timeout time.Duration
}

// StartupAutoScanner performs exactly one bounded scan of the default network
// profile when a control plane starts. There is no timer, no scheduler, and no
// repeated work: Run scans at most once per process start, and every outcome -
// including "nothing to scan" and "the scan failed" - is returned as a report
// for the caller to log. Nothing here is fatal to startup.
type StartupAutoScanner struct {
	workspaces WorkspaceLister
	profiles   NetworkProfileLister
	runner     StartupScanRunner
	recorder   ScanRunRecorder
	startedAt  time.Time
	timeout    time.Duration

	once    sync.Once
	outcome AutoScanOutcome
}

func NewStartupAutoScanner(cfg StartupAutoScanConfig) *StartupAutoScanner {
	startedAt := cfg.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultStartupScanTimeout
	}
	return &StartupAutoScanner{
		workspaces: cfg.Workspaces,
		profiles:   cfg.Profiles,
		runner:     cfg.Runner,
		recorder:   cfg.Recorder,
		startedAt:  startedAt.UTC(),
		timeout:    timeout,
	}
}

// Run performs the startup scan. It is safe to call more than once: later calls
// return the first outcome instead of scanning again.
func (a *StartupAutoScanner) Run(ctx context.Context) AutoScanOutcome {
	if a == nil {
		return AutoScanOutcome{State: AutoScanFailed, Err: fmt.Errorf("startup auto-scan is not configured")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.once.Do(func() { a.outcome = a.scan(ctx) })
	return a.outcome
}

func (a *StartupAutoScanner) scan(ctx context.Context) AutoScanOutcome {
	if err := ctx.Err(); err != nil {
		return AutoScanOutcome{State: AutoScanCancelled, Err: err}
	}
	workspace, outcome := a.defaultProfileWorkspace(ctx)
	if outcome != nil {
		return *outcome
	}
	profileID, outcome := a.defaultProfile(ctx, workspace)
	if outcome != nil {
		return *outcome
	}
	if err := ctx.Err(); err != nil {
		return AutoScanOutcome{State: AutoScanCancelled, Workspace: workspace, ProfileID: profileID, Err: err}
	}

	// The scan runs on a context bounded by the process context: shutdown
	// cancels it, and the bound guarantees it cannot outlive its welcome.
	key := fmt.Sprintf("control-plane-startup-scan:%d", a.startedAt.UnixNano())
	scanCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	run, devices, scanErr := a.runner.StartScan(scanCtx, workspace, profileID, key, autoScanActorType, autoScanActorID)
	result := AutoScanOutcome{
		State:     AutoScanCompleted,
		Workspace: workspace,
		ProfileID: profileID,
		ScanRunID: run.ID,
		Devices:   devices,
	}
	if scanErr == nil {
		return result
	}
	result.Err = scanErr
	result.State = AutoScanFailed
	if ctx.Err() != nil {
		// Shutdown ended the scan.
		result.State = AutoScanCancelled
	} else if scanCtx.Err() == nil {
		// The scan machinery already recorded this failure on its own context.
		return result
	}
	result.RecordErr = a.recordInterruptedRun(ctx, workspace, key, scanErr)
	return result
}

// recordInterruptedRun closes out a run whose context ended before the scan
// could finish. Writing happens on a detached but still bounded context, so a
// cancelled scan leaves recorded evidence instead of a run stuck in 'running'.
func (a *StartupAutoScanner) recordInterruptedRun(ctx context.Context, workspace organizations.WorkspaceID, key string, cause error) error {
	if a.recorder == nil {
		return nil
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startupScanRecordTimeout)
	defer cancel()
	runs, err := a.recorder.ListScanRuns(recordCtx, workspace)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.IdempotencyKey != key || (run.State != discovery.ScanRequested && run.State != discovery.ScanRunning) {
			continue
		}
		if _, failErr := a.recorder.FailScan(recordCtx, workspace, run.ID, autoScanActorType, autoScanActorID, cause); failErr != nil {
			return failErr
		}
	}
	return nil
}

// defaultProfileWorkspace resolves the workspace a startup scan may use. One
// workspace is the workspace; with none, or with several, there is no
// unambiguous workspace to scan and startup simply records that.
func (a *StartupAutoScanner) defaultProfileWorkspace(ctx context.Context) (organizations.WorkspaceID, *AutoScanOutcome) {
	if a.workspaces == nil {
		outcome := AutoScanOutcome{State: AutoScanFailed, Err: fmt.Errorf("workspace listing is not configured")}
		return "", &outcome
	}
	workspaces, err := a.workspaces.List(ctx)
	if err != nil {
		outcome := AutoScanOutcome{State: AutoScanFailed, Err: err}
		return "", &outcome
	}
	switch len(workspaces) {
	case 0:
		outcome := AutoScanOutcome{State: AutoScanNoWorkspace}
		return "", &outcome
	case 1:
		return workspaces[0].ID, nil
	default:
		outcome := AutoScanOutcome{State: AutoScanAmbiguousWorkspace}
		return "", &outcome
	}
}

// defaultProfile finds the workspace's default network profile. Startup scans
// the default only; a manual scan can still name any profile.
func (a *StartupAutoScanner) defaultProfile(ctx context.Context, workspace organizations.WorkspaceID) (networkprofiles.NetworkProfileID, *AutoScanOutcome) {
	if a.profiles == nil {
		outcome := AutoScanOutcome{State: AutoScanFailed, Workspace: workspace, Err: fmt.Errorf("network profile listing is not configured")}
		return "", &outcome
	}
	profiles, err := a.profiles.List(ctx, workspace)
	if err != nil {
		outcome := AutoScanOutcome{State: AutoScanFailed, Workspace: workspace, Err: err}
		return "", &outcome
	}
	for _, profile := range profiles {
		if profile.IsDefault {
			return profile.ID, nil
		}
	}
	outcome := AutoScanOutcome{State: AutoScanNoDefaultProfile, Workspace: workspace}
	return "", &outcome
}
