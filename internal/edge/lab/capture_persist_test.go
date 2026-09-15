package lab_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/edge/lab"
)

type failingPersister struct{}

func (failingPersister) PersistScreenshot(context.Context, string, string, string, []byte, string) (string, error) {
	return "", errors.New("persist screenshot failed")
}

func (failingPersister) PersistUITree(context.Context, string, string, string, []byte) (string, error) {
	return "", errors.New("persist ui tree failed")
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
