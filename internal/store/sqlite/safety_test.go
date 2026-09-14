package sqlite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type advancingClock struct{ now time.Time }

func (c *advancingClock) Now() time.Time           { return c.now }
func (c *advancingClock) Advance(by time.Duration) { c.now = c.now.Add(by) }

func TestLeaseServiceFencesOwnersAndSerializesOneActiveLease(t *testing.T) {
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "safety-w", Name: "Safety", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessions := store.NewSessionService(db, time.Hour)
	leasesService := store.NewLeaseService(db, 10*time.Minute)
	firstSession, err := sessions.Open(ctx, workspace.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	firstLease, err := leasesService.Acquire(ctx, workspace.ID, "device-1", firstSession.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := sessions.Open(ctx, workspace.ID, "holder-b", "operator", "operator-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leasesService.Acquire(ctx, workspace.ID, "device-1", secondSession.ID, "holder-b", "operator", "operator-2"); platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("conflicting acquire code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
	if _, err := leasesService.Renew(ctx, workspace.ID, firstLease.ID, "holder-b", firstLease.FencingToken, "operator", "operator-2"); platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("wrong-holder renew code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
	if _, err := leasesService.Release(ctx, workspace.ID, firstLease.ID, "holder-a", firstLease.FencingToken, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	secondLease, err := leasesService.Acquire(ctx, workspace.ID, "device-1", secondSession.ID, "holder-b", "operator", "operator-2")
	if err != nil {
		t.Fatal(err)
	}
	if secondLease.FencingToken <= firstLease.FencingToken {
		t.Fatalf("fencing token = %d, want greater than %d", secondLease.FencingToken, firstLease.FencingToken)
	}
	if _, err := leasesService.Release(ctx, workspace.ID, firstLease.ID, "holder-a", firstLease.FencingToken, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("stale release code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
	var active int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM device_leases WHERE workspace_id=? AND device_id=? AND state='active'`, workspace.ID, "device-1").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active lease count = %d, want 1", active)
	}
}

func TestLeaseServiceExpiresAndRecordsLeaseBeforeReacquiring(t *testing.T) {
	controlled := &advancingClock{now: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC)}
	db, err := store.Open(context.Background(), t.TempDir()+"/safety.db", store.Options{Clock: controlled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "expiry-w", Name: "Expiry", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessions := store.NewSessionService(db, time.Hour)
	leasesService := store.NewLeaseService(db, time.Minute)
	firstSession, err := sessions.Open(ctx, workspace.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	firstLease, err := leasesService.Acquire(ctx, workspace.ID, "device-1", firstSession.ID, "holder-a", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	controlled.Advance(2 * time.Minute)
	secondSession, err := sessions.Open(ctx, workspace.ID, "holder-b", "operator", "operator-2")
	if err != nil {
		t.Fatal(err)
	}
	secondLease, err := leasesService.Acquire(ctx, workspace.ID, "device-1", secondSession.ID, "holder-b", "operator", "operator-2")
	if err != nil {
		t.Fatal(err)
	}
	if secondLease.FencingToken <= firstLease.FencingToken {
		t.Fatalf("reacquired fencing token = %d, want greater than %d", secondLease.FencingToken, firstLease.FencingToken)
	}
	var state string
	if err := store.SQLForTest(db).QueryRow(`SELECT state FROM device_leases WHERE id=?`, firstLease.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(leases.LeaseExpired) {
		t.Fatalf("expired lease state = %q, want expired", state)
	}
	var events int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM lease_events WHERE workspace_id=? AND lease_id=? AND state='expired'`, workspace.ID, firstLease.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("expired lease events = %d, want 1", events)
	}
	if _, err := leasesService.Renew(ctx, workspace.ID, firstLease.ID, "holder-a", firstLease.FencingToken, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("expired renew code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
}

func TestClosingSessionRevokesItsLeaseAndRecordsBothTransitions(t *testing.T) {
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "close-w", Name: "Close", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessionService := store.NewSessionService(db, time.Hour)
	leaseService := store.NewLeaseService(db, time.Minute)
	session, err := sessionService.Open(ctx, workspace.ID, "holder", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := leaseService.Acquire(ctx, workspace.ID, "device-1", session.ID, "holder", "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionService.Close(ctx, workspace.ID, session.ID, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := store.SQLForTest(db).QueryRow(`SELECT state FROM device_leases WHERE id=?`, lease.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(leases.LeaseReleased) {
		t.Fatalf("closed-session lease state = %q, want released", state)
	}
	var releasedEvents int
	if err := store.SQLForTest(db).QueryRow(`SELECT COUNT(*) FROM lease_events WHERE lease_id=? AND state='released'`, lease.ID).Scan(&releasedEvents); err != nil {
		t.Fatal(err)
	}
	if releasedEvents != 1 {
		t.Fatalf("released lease events = %d, want 1", releasedEvents)
	}
}

func TestConcurrentLeaseAcquisitionReturnsOneTypedConflict(t *testing.T) {
	db := openTestDB(t)
	workspace := organizations.Workspace{ID: "concurrent-w", Name: "Concurrent", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NewDeviceService(db).Create(context.Background(), devices.Device{ID: "device-1", Workspace: workspace.ID, DisplayName: "Fake", PlatformVersion: "fake", State: devices.Registered}, "operator", "operator-1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessionService := store.NewSessionService(db, time.Hour)
	first, err := sessionService.Open(ctx, workspace.ID, "holder-a", "operator", "operator-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sessionService.Open(ctx, workspace.ID, "holder-b", "operator", "operator-b")
	if err != nil {
		t.Fatal(err)
	}
	leaseService := store.NewLeaseService(db, time.Hour)
	type acquireResult struct{ err error }
	results := make(chan acquireResult, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, acquireErr := leaseService.Acquire(ctx, workspace.ID, "device-1", first.ID, "holder-a", "operator", "operator-a")
		results <- acquireResult{err: acquireErr}
	}()
	go func() {
		defer wait.Done()
		_, acquireErr := leaseService.Acquire(ctx, workspace.ID, "device-1", second.ID, "holder-b", "operator", "operator-b")
		results <- acquireResult{err: acquireErr}
	}()
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		if result.err == nil {
			successes++
		} else if platformerrors.CodeOf(result.err) == platformerrors.CodeLeaseConflict {
			conflicts++
		} else {
			t.Fatalf("concurrent acquire error = %v, want nil or lease_conflict", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent acquire outcomes = success:%d conflict:%d, want 1/1", successes, conflicts)
	}
}
