package product_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/product"
	store "drift.local/drift-next/internal/store/sqlite"
)

const (
	startupTestWorkspace = organizations.WorkspaceID("workspace-startup")
	startupTestProfile   = networkprofiles.NetworkProfileID("profile-default")
)

var startupTestStartedAt = time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)

func openStartupDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedStartupWorkspace(t *testing.T, db *store.DB) {
	t.Helper()
	err := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
		ID: startupTestWorkspace, Name: "Startup", State: organizations.WorkspaceActive,
	}, "system", "control-plane")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}

func seedStartupProfile(t *testing.T, db *store.DB, isDefault bool) {
	t.Helper()
	err := store.NewNetworkProfileService(db).Create(context.Background(), networkprofiles.NetworkProfile{
		ID: startupTestProfile, Workspace: startupTestWorkspace, Name: "Lab",
		AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, IsDefault: isDefault,
	}, "operator", "op-1")
	if err != nil {
		t.Fatalf("create network profile: %v", err)
	}
}

// countingScanner is the fake enumerator seam. It records how often the
// existing scan machinery actually reached device enumeration.
type countingScanner struct {
	calls          int
	devices        []discovery.ObservedDevice
	err            error
	blockUntilDone bool

	sawDeadline bool
	deadlineAt  time.Time
}

func (s *countingScanner) Scan(ctx context.Context, _ networkprofiles.NetworkProfile) ([]discovery.ObservedDevice, error) {
	s.calls++
	if deadline, ok := ctx.Deadline(); ok {
		s.sawDeadline, s.deadlineAt = true, deadline
	}
	if s.blockUntilDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return append([]discovery.ObservedDevice(nil), s.devices...), nil
}

func newStartupScanner(t *testing.T, db *store.DB, scanner discovery.Scanner, timeout time.Duration) *product.StartupAutoScanner {
	t.Helper()
	return product.NewStartupAutoScanner(product.StartupAutoScanConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Profiles:   store.NewNetworkProfileRepository(db),
		Recorder:   db,
		Runner:     discovery.NewService(db, scanner),
		StartedAt:  startupTestStartedAt,
		Timeout:    timeout,
	})
}

func scanRuns(t *testing.T, db *store.DB) []discovery.ScanRun {
	t.Helper()
	runs, err := db.ListScanRuns(context.Background(), startupTestWorkspace)
	if err != nil {
		t.Fatalf("ListScanRuns() error = %v", err)
	}
	return runs
}

func TestStartupAutoScanScansDefaultProfileAndPersistsDevices(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{
		Serial: "SERIAL-1", Model: "Pixel", State: discovery.LinkOnline,
	}}}

	outcome := newStartupScanner(t, db, scanner, time.Second).Run(context.Background())

	if outcome.State != product.AutoScanCompleted {
		t.Fatalf("outcome state = %q (err = %v), want completed", outcome.State, outcome.Err)
	}
	if scanner.calls != 1 {
		t.Fatalf("scanner calls = %d, want 1", scanner.calls)
	}
	if outcome.ProfileID != startupTestProfile {
		t.Fatalf("scanned profile = %q, want %q", outcome.ProfileID, startupTestProfile)
	}
	if len(outcome.Devices) != 1 || outcome.Devices[0].DeviceID == "" {
		t.Fatalf("observed devices = %#v, want one persisted device", outcome.Devices)
	}
	runs := scanRuns(t, db)
	if len(runs) != 1 {
		t.Fatalf("scan runs = %d, want exactly 1", len(runs))
	}
	if runs[0].State != discovery.ScanCompleted {
		t.Fatalf("scan run state = %q, want completed", runs[0].State)
	}
	if runs[0].ID != outcome.ScanRunID {
		t.Fatalf("recorded scan run = %q, reported %q", runs[0].ID, outcome.ScanRunID)
	}
	devices, err := store.NewDeviceRepository(db).List(context.Background(), startupTestWorkspace)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != outcome.Devices[0].DeviceID {
		t.Fatalf("persisted devices = %#v, want the observed device", devices)
	}
}

func TestStartupAutoScanWithoutDefaultProfileIsNoOp(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, false)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{Serial: "SERIAL-1", State: discovery.LinkOnline}}}

	outcome := newStartupScanner(t, db, scanner, time.Second).Run(context.Background())

	if outcome.State != product.AutoScanNoDefaultProfile {
		t.Fatalf("outcome state = %q (err = %v), want no_default_profile", outcome.State, outcome.Err)
	}
	if scanner.calls != 0 {
		t.Fatalf("scanner calls = %d, want 0", scanner.calls)
	}
	if runs := scanRuns(t, db); len(runs) != 0 {
		t.Fatalf("scan runs = %d, want 0", len(runs))
	}
}

func TestStartupAutoScanWithoutWorkspaceIsNoOp(t *testing.T) {
	db := openStartupDB(t)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{Serial: "SERIAL-1", State: discovery.LinkOnline}}}

	outcome := newStartupScanner(t, db, scanner, time.Second).Run(context.Background())

	if outcome.State != product.AutoScanNoWorkspace {
		t.Fatalf("outcome state = %q (err = %v), want no_workspace", outcome.State, outcome.Err)
	}
	if scanner.calls != 0 {
		t.Fatalf("scanner calls = %d, want 0", scanner.calls)
	}
}

func TestStartupAutoScanRecordsScannerFailureWithoutAbortingStartup(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{err: errors.New("enumerator unreachable")}

	outcome := newStartupScanner(t, db, scanner, time.Second).Run(context.Background())

	if outcome.State != product.AutoScanFailed {
		t.Fatalf("outcome state = %q, want failed", outcome.State)
	}
	if outcome.Err == nil {
		t.Fatal("outcome error = nil, want the recorded scanner failure")
	}
	runs := scanRuns(t, db)
	if len(runs) != 1 || runs[0].State != discovery.ScanFailed {
		t.Fatalf("scan runs = %#v, want one failed run", runs)
	}
	if report := outcome.Report(); report == "" {
		t.Fatal("outcome report is empty; a startup outcome must be recorded and reportable")
	}
}

func TestStartupAutoScanStartsAtMostOneScanPerStartup(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{Serial: "SERIAL-1", State: discovery.LinkOnline}}}
	autoScan := newStartupScanner(t, db, scanner, time.Second)

	first := autoScan.Run(context.Background())
	second := autoScan.Run(context.Background())

	if scanner.calls != 1 {
		t.Fatalf("scanner calls = %d, want 1 for one process startup", scanner.calls)
	}
	if runs := scanRuns(t, db); len(runs) != 1 {
		t.Fatalf("scan runs = %d, want 1", len(runs))
	}
	if second.State != first.State || second.ScanRunID != first.ScanRunID {
		t.Fatalf("second run outcome = %#v, want the first outcome %#v", second, first)
	}
}

func TestStartupAutoScanSkipsScanWhenStartupContextAlreadyCancelled(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{Serial: "SERIAL-1", State: discovery.LinkOnline}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := newStartupScanner(t, db, scanner, time.Second).Run(ctx)

	if outcome.State != product.AutoScanCancelled {
		t.Fatalf("outcome state = %q, want cancelled", outcome.State)
	}
	if scanner.calls != 0 {
		t.Fatalf("scanner calls = %d, want 0", scanner.calls)
	}
	if runs := scanRuns(t, db); len(runs) != 0 {
		t.Fatalf("scan runs = %d, want 0", len(runs))
	}
}

func TestStartupAutoScanBoundsTheScanContext(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{devices: []discovery.ObservedDevice{{Serial: "SERIAL-1", State: discovery.LinkOnline}}}
	timeout := 250 * time.Millisecond
	before := time.Now()

	outcome := newStartupScanner(t, db, scanner, timeout).Run(context.Background())

	if outcome.State != product.AutoScanCompleted {
		t.Fatalf("outcome state = %q (err = %v), want completed", outcome.State, outcome.Err)
	}
	if !scanner.sawDeadline {
		t.Fatal("scan context carried no deadline; a startup scan must be bounded")
	}
	if scanner.deadlineAt.After(before.Add(timeout + 50*time.Millisecond)) {
		t.Fatalf("scan deadline = %s, want no later than %s", scanner.deadlineAt, before.Add(timeout))
	}
}

func TestStartupAutoScanStopsAtTheBoundAndRecordsTheFailure(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{blockUntilDone: true}
	started := time.Now()

	outcome := newStartupScanner(t, db, scanner, 150*time.Millisecond).Run(context.Background())

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("startup scan took %s; it must honour its bound", elapsed)
	}
	if outcome.State != product.AutoScanFailed || outcome.Err == nil {
		t.Fatalf("outcome = %#v, want a recorded failure when the scan bound expires", outcome)
	}
	runs := scanRuns(t, db)
	if len(runs) != 1 {
		t.Fatalf("scan runs = %d, want 1", len(runs))
	}
	if runs[0].State != discovery.ScanFailed {
		t.Fatalf("scan run state = %q, want failed (a cancelled scan must not be left running)", runs[0].State)
	}
}

func TestStartupAutoScanCancelsWithProcessShutdownAndRecordsTheRun(t *testing.T) {
	db := openStartupDB(t)
	seedStartupWorkspace(t, db)
	seedStartupProfile(t, db, true)
	scanner := &countingScanner{blockUntilDone: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	outcome := newStartupScanner(t, db, scanner, 10*time.Second).Run(ctx)

	if outcome.State != product.AutoScanCancelled {
		t.Fatalf("outcome state = %q (err = %v), want cancelled", outcome.State, outcome.Err)
	}
	if outcome.Err == nil {
		t.Fatal("outcome error = nil, want the cancellation that ended the scan")
	}
	runs := scanRuns(t, db)
	if len(runs) != 1 {
		t.Fatalf("scan runs = %d, want 1", len(runs))
	}
	if runs[0].State != discovery.ScanFailed {
		t.Fatalf("scan run state = %q, want failed (a shut-down scan must not be left running)", runs[0].State)
	}
}

// failingWorkspaces is a store seam failure: it must be reported, never fatal.
type failingWorkspaces struct{ err error }

func (f failingWorkspaces) List(context.Context) ([]organizations.Workspace, error) {
	return nil, f.err
}

type emptyProfiles struct{}

func (emptyProfiles) List(context.Context, organizations.WorkspaceID) ([]networkprofiles.NetworkProfile, error) {
	return nil, nil
}

type unavailableRunner struct{ t *testing.T }

func (r unavailableRunner) StartScan(context.Context, organizations.WorkspaceID, networkprofiles.NetworkProfileID, string, string, string) (discovery.ScanRun, []discovery.ObservedDevice, error) {
	r.t.Fatal("scan runner must not be reached when workspace lookup fails")
	return discovery.ScanRun{}, nil, nil
}

func TestStartupAutoScanReportsStoreFailureWithoutAbortingStartup(t *testing.T) {
	autoScan := product.NewStartupAutoScanner(product.StartupAutoScanConfig{
		Workspaces: failingWorkspaces{err: errors.New("workspace listing unavailable")},
		Profiles:   emptyProfiles{},
		Runner:     unavailableRunner{t: t},
		StartedAt:  startupTestStartedAt,
	})

	outcome := autoScan.Run(context.Background())

	if outcome.State != product.AutoScanFailed || outcome.Err == nil {
		t.Fatalf("outcome = %#v, want a recorded failure when the store seam fails", outcome)
	}
}
