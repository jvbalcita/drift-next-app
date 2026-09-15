package lab_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type failingPersister struct{}

func (failingPersister) PersistScreenshot(context.Context, string, string, string, []byte, string) (string, error) {
	return "", errors.New("persist screenshot failed")
}

func (failingPersister) PersistUITree(context.Context, string, string, string, []byte) (string, error) {
	return "", errors.New("persist ui tree failed")
}

type omittingPersister struct{}

func (omittingPersister) PersistScreenshot(context.Context, string, string, string, []byte, string) (string, error) {
	return "artifact-omitted-shot", platformerrors.New(platformerrors.CodePolicyDenied, "screenshot admission omitted bytes")
}

func (omittingPersister) PersistUITree(context.Context, string, string, string, []byte) (string, error) {
	return "artifact-omitted-tree", platformerrors.New(platformerrors.CodePolicyDenied, "ui-tree admission omitted bytes")
}

func TestCaptureContinuesWhenEvidencePersistenceFails(t *testing.T) {
	service, err := lab.NewService(lab.WithEvidencePersister(failingPersister{}), lab.WithScreenshotPreview(0))
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Discover(context.Background(), "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Discovered) == 0 {
		t.Fatal("expected mock discoveries")
	}
	serial := status.Discovered[0].Serial
	if _, err := service.ConfirmTarget(context.Background(), lab.ConfirmRequest{
		Serial: serial, OperatorID: "operator-1", ConfirmationText: serial, Reason: "phase-15 capture isolation",
	}); err != nil {
		t.Fatal(err)
	}
	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial: serial, OperatorID: "operator-1", IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.PostconditionVerified {
		t.Fatalf("observation should succeed despite persist failure: %#v", bundle)
	}
	if !bundle.EvidencePersistFailed {
		t.Fatal("expected EvidencePersistFailed isolation flag")
	}
}

func TestCaptureAdmissionOmissionIsNotInfrastructureFailure(t *testing.T) {
	service, err := lab.NewService(lab.WithEvidencePersister(omittingPersister{}), lab.WithScreenshotPreview(0))
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Discover(context.Background(), "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Discovered) == 0 {
		t.Fatal("expected mock discoveries")
	}
	serial := status.Discovered[0].Serial
	if _, err := service.ConfirmTarget(context.Background(), lab.ConfirmRequest{
		Serial: serial, OperatorID: "operator-1", ConfirmationText: serial, Reason: "phase-15 admission omission",
	}); err != nil {
		t.Fatal(err)
	}
	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial: serial, OperatorID: "operator-1", IdempotencyKey: "idem-omit-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.PostconditionVerified {
		t.Fatalf("observation should succeed when admission omits bytes: %#v", bundle)
	}
	if bundle.EvidencePersistFailed {
		t.Fatal("policy omission must not set EvidencePersistFailed")
	}
	if bundle.ScreenshotArtifactID != "artifact-omitted-shot" || bundle.HierarchyArtifactID != "artifact-omitted-tree" {
		t.Fatalf("expected omission artifact ids, got shot=%q tree=%q", bundle.ScreenshotArtifactID, bundle.HierarchyArtifactID)
	}
	var sawPolicyDenied bool
	for _, event := range bundle.Events {
		if event.FailureClass == domain.FailureInfrastructure {
			t.Fatalf("policy omission must not emit infrastructure failure: %#v", event)
		}
		if event.FailureClass == domain.FailurePolicyDenied {
			sawPolicyDenied = true
		}
	}
	if !sawPolicyDenied {
		t.Fatal("expected policy_denied admission labeling on observation events")
	}
}
