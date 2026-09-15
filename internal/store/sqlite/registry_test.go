package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/networkprofiles"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestFakeScanRequiresApprovalAndRegistrationIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "registry-w", Name: "Registry", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	profile := networkprofiles.NetworkProfile{ID: "profile-1", Workspace: "registry-w", Name: "Mock lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, IsDefault: true}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	scanner := discovery.NewFakeScanner([]discovery.ObservedCandidate{{CandidateKey: "candidate-1", Host: "192.0.2.5", Port: 5555, Serial: "mock-serial-1", Fingerprint: "sha256:fake", Evidence: map[string]string{"source": "fake"}}})
	svc := discovery.NewService(db, scanner)
	run, err := svc.StartScan(ctx, "registry-w", profile.ID, "scan-key-1", "operator", "op-1")
	if err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	if run.State != discovery.ScanCompleted {
		t.Fatalf("scan state = %q, want completed", run.State)
	}
	candidates, err := store.NewDiscoveryRepository(db).ListCandidates(ctx, "registry-w", discovery.CandidatePendingApproval)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].State != discovery.CandidatePendingApproval {
		t.Fatalf("candidates = %#v, want one pending candidate", candidates)
	}
	if got, err := store.NewDeviceRepository(db).List(ctx, "registry-w"); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatalf("scan registered devices implicitly: %#v", got)
	}
	if _, _, err := svc.RegisterCandidate(ctx, "registry-w", candidates[0].ID, "Mock Five", "operator", "op-1"); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unapproved registration code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
	approved, err := svc.DecideCandidate(ctx, "registry-w", candidates[0].ID, true, "approved for lab", "operator", "op-1")
	if err != nil || approved.State != discovery.CandidateApproved {
		t.Fatalf("approval = %#v, error = %v", approved, err)
	}
	registration, registered, err := svc.RegisterCandidate(ctx, "registry-w", approved.ID, "Mock Five", "operator", "op-1")
	if err != nil {
		t.Fatalf("RegisterCandidate() error = %v", err)
	}
	if registration.Outcome != "registered" || registered.State != discovery.CandidateRegistered || registration.DeviceID == "" || registration.EndpointID == "" {
		t.Fatalf("registration = %#v, candidate = %#v", registration, registered)
	}
	devicesAfterRegistration, err := store.NewDeviceRepository(db).List(ctx, "registry-w")
	if err != nil || len(devicesAfterRegistration) != 1 {
		t.Fatalf("devices after registration = %#v, error = %v", devicesAfterRegistration, err)
	}
	endpoints, err := store.NewEndpointRepository(db).ListCurrent(ctx, "registry-w", devices.DeviceID(registration.DeviceID))
	if err != nil || len(endpoints) != 1 || endpoints[0].Serial != "mock-serial-1" {
		t.Fatalf("current endpoints = %#v, error = %v", endpoints, err)
	}
	retryRun, err := svc.StartScan(ctx, "registry-w", profile.ID, "scan-key-1", "operator", "op-1")
	if err != nil || retryRun.ID != run.ID {
		t.Fatalf("idempotent scan retry = %#v, error = %v", retryRun, err)
	}
	retryCandidates, err := store.NewDiscoveryRepository(db).ListCandidates(ctx, "registry-w", "")
	if err != nil || len(retryCandidates) != 1 {
		t.Fatalf("candidate count after retry = %d, error = %v", len(retryCandidates), err)
	}
	retryRegistration, _, err := svc.RegisterCandidate(ctx, "registry-w", registered.ID, "Mock Five", "operator", "op-1")
	if err != nil || retryRegistration.ID != registration.ID || retryRegistration.DeviceID != registration.DeviceID {
		t.Fatalf("idempotent registration = %#v, error = %v", retryRegistration, err)
	}
}

func TestNetworkProfileUpdateRejectsNilContextWithoutRowVersionGuidance(t *testing.T) {
	db := openTestDB(t)
	profile := networkprofiles.NetworkProfile{ID: "profile-update", Workspace: "update-w", Name: "Lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}}
	err := store.NewNetworkProfileService(db).Update(nil, profile, "operator", "op-1")
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("Update(nil context) code = %v, want invalid_input", platformerrors.CodeOf(err))
	}
	if strings.Contains(err.Error(), "row version") {
		t.Fatalf("Update(nil context) error = %q, but network profiles no longer have a row version", err)
	}
}

func TestNetworkProfileDeleteSucceedsWithScanHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "delete-w", Name: "Delete", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	profile := networkprofiles.NetworkProfile{ID: "profile-delete", Workspace: "delete-w", Name: "Lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}, IsDefault: true}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner(nil))
	if _, err := svc.StartScan(ctx, "delete-w", profile.ID, "scan-key-delete", "operator", "op-1"); err != nil {
		t.Fatalf("StartScan() error = %v", err)
	}
	history, err := db.ListScanRuns(ctx, "delete-w")
	if err != nil || len(history) != 1 {
		t.Fatalf("scan history before delete = %#v, error = %v", history, err)
	}

	if err := store.NewNetworkProfileService(db).Delete(ctx, "delete-w", profile.ID, "operator", "op-1"); err != nil {
		t.Fatalf("Delete() error = %v, want deletion to succeed with scan history present", err)
	}

	remaining, err := db.ListScanRuns(ctx, "delete-w")
	if err != nil {
		t.Fatalf("ListScanRuns() after delete error = %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("scan history after profile deletion = %d, want 1 (history is immutable evidence)", len(remaining))
	}
}

func TestCandidateRejectionAndExpiryNeverCreateDevices(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{ID: "registry-w", Name: "Registry", State: organizations.WorkspaceActive}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	profile := networkprofiles.NetworkProfile{ID: "profile-1", Workspace: "registry-w", Name: "Mock lab", AddressPolicy: "192.0.2.0/28", Ports: []uint16{5555}}
	if err := store.NewNetworkProfileService(db).Create(ctx, profile, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	svc := discovery.NewService(db, discovery.NewFakeScanner([]discovery.ObservedCandidate{
		{CandidateKey: "candidate-reject", Host: "192.0.2.6", Port: 5555},
		{CandidateKey: "candidate-expire", Host: "192.0.2.7", Port: 5555},
	}))
	if _, err := svc.StartScan(ctx, "registry-w", profile.ID, "scan-key-2", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.NewDiscoveryRepository(db).ListCandidates(ctx, "registry-w", discovery.CandidatePendingApproval)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("pending candidates = %#v, error = %v", candidates, err)
	}
	if _, err := svc.DecideCandidate(ctx, "registry-w", candidates[0].ID, false, "not recognized", "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExpireCandidate(ctx, "registry-w", candidates[1].ID, "system", "expiry-worker"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewDeviceRepository(db).List(ctx, "registry-w"); err != nil || len(got) != 0 {
		t.Fatalf("rejected/expired candidates created devices: %#v, error = %v", got, err)
	}
}
