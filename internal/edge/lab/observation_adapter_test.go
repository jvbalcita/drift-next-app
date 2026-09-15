package lab_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/lab"
)

func TestObservationAdapterCapturesTheConfirmedSerial(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")
	bound, err := lab.NewObservationAdapter(service, "mock-device-alpha", operator)
	if err != nil {
		t.Fatal(err)
	}
	result, err := bound.Execute(context.Background(), action.Intent{
		ID: "attempt-observe-1", Kind: action.Capture, IdempotencyKey: "capture-alpha", Timeout: time.Second,
		Capabilities: []action.Capability{action.CapabilityCapture},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != action.OutcomeVerified || !result.Dispatched {
		t.Fatalf("execution = %#v, want a verified dispatched observation", result)
	}
}

func TestObservationAdapterRefusesAMutatingKind(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")
	bound, err := lab.NewObservationAdapter(service, "mock-device-alpha", operator)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bound.Execute(context.Background(), action.Intent{Kind: action.Tap, IdempotencyKey: "tap-1", Timeout: time.Second})
	executionErr, ok := adapter.IsExecutionError(err)
	if !ok || executionErr.FailureClass == "" || executionErr.Dispatched {
		t.Fatalf("mutating execute err = %v, want an undispatched capability mismatch", err)
	}
}

func TestObservationAdapterRefusesAMismatchedConfirmedSerial(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")
	bound, err := lab.NewObservationAdapter(service, "mock-device-beta", operator)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bound.Execute(context.Background(), action.Intent{
		Kind: action.Capture, IdempotencyKey: "capture-beta", Timeout: time.Second,
		Capabilities: []action.Capability{action.CapabilityCapture},
	})
	if _, ok := adapter.IsExecutionError(err); !ok {
		t.Fatalf("mismatched serial err = %v, want an execution error", err)
	}
}
