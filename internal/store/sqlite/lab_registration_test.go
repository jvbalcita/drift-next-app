package sqlite_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestLabRegistrationDurableApproveRegisterCreatesOneDevice(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workspace := organizations.WorkspaceID("lab-reg-w")
	if err := store.NewWorkspaceService(db).Create(ctx, organizations.Workspace{
		ID: workspace, Name: "Lab Reg", State: organizations.WorkspaceActive,
	}, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	ready := registration.ProvisionReady{
		Serial: "LAB-1", TransportID: "usb:1", State: registration.StateProvisionVerified,
		Ready: true, CheckedAt: now, Notes: []string{"pairing authorized"},
	}
	target := registration.TargetIdentity{
		Serial: "LAB-1", TransportID: "usb:1", ConnectionType: "usb", ActorID: "op-1",
	}
	if err := db.SaveLabProvisioningCheck(ctx, workspace, ready, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RegisterLabDevice(ctx, workspace, registration.RegisterRequest{
		Serial: "LAB-1", DisplayName: "Lab Phone", ActorID: "op-1",
	}); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("register without approval code = %v", platformerrors.CodeOf(err))
	}
	if err := db.SaveLabRegistrationApproval(ctx, workspace, registration.Approval{
		Serial: "LAB-1", ActorID: "op-1", Reason: "authorized lab phone", DecidedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	first, err := db.RegisterLabDevice(ctx, workspace, registration.RegisterRequest{
		Serial: "LAB-1", DisplayName: "Lab Phone", ActorID: "op-1",
	})
	if err != nil || first.DeviceID == "" || first.EndpointID == "" {
		t.Fatalf("register = %#v err=%v", first, err)
	}
	again, err := db.RegisterLabDevice(ctx, workspace, registration.RegisterRequest{
		Serial: "LAB-1", DisplayName: "Lab Phone", ActorID: "op-1",
	})
	if err != nil || again.DeviceID != first.DeviceID {
		t.Fatalf("idempotent register = %#v err=%v", again, err)
	}

	// Re-verify for a second serial after approval of another would require check+approval;
	// one-device scope blocks a second registration in the same workspace.
	readyB := ready
	readyB.Serial = "LAB-2"
	readyB.TransportID = "usb:2"
	targetB := target
	targetB.Serial = "LAB-2"
	targetB.TransportID = "usb:2"
	if err := db.SaveLabProvisioningCheck(ctx, workspace, readyB, targetB); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveLabRegistrationApproval(ctx, workspace, registration.Approval{
		Serial: "LAB-2", ActorID: "op-1", Reason: "second phone", DecidedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RegisterLabDevice(ctx, workspace, registration.RegisterRequest{
		Serial: "LAB-2", DisplayName: "B", ActorID: "op-1",
	}); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("second device code = %v", platformerrors.CodeOf(err))
	}
}
