package lab_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/uiautomator"
)

// captureSerialResolver resolves the device the observer names to the serial the
// capture was taken for.
type captureSerialResolver struct{ serial string }

func (r captureSerialResolver) CurrentSerial(context.Context, string, string) (string, error) {
	return r.serial, nil
}

// TestALaunchPostconditionGetsTheCapturedPackageToBeEvaluatedAgainst walks the
// chain this card completes: the device read, the bundle a capture builds, the
// observation an adapter hands over, and the postcondition observation the
// kernel evaluates a launch against. A launch postcondition can only be decided
// at the last link, so before the capture named a package every launch
// postcondition in production was indeterminate.
func TestALaunchPostconditionGetsTheCapturedPackageToBeEvaluatedAgainst(t *testing.T) {
	fake := newFakeAdapter()
	fake.hierarchy = uiautomator.HierarchyCapture{
		Serial:            "fakeserial01",
		NodeCount:         3,
		MaxDepth:          2,
		FreshnessToken:    "sha256:capture",
		ForegroundPackage: "com.example.target",
	}
	service := newLabService(t, fake)

	observer, err := execution.NewObservationPostconditionObserver(
		captureSerialResolver{serial: "fakeserial01"},
		func(serial string) (execution.ObservationSource, error) {
			return lab.NewObservationAdapter(service, serial, "operator-1")
		},
	)
	if err != nil {
		t.Fatalf("NewObservationPostconditionObserver() = %v", err)
	}

	observation, err := observer.ObservePostcondition(
		context.Background(),
		action.Intent{Kind: action.LaunchApp, Workspace: "ws-1", DeviceID: "device-1", ObservationToken: "sha256:before"},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.target"}},
	)
	if err != nil {
		t.Fatalf("ObservePostcondition() = %v", err)
	}
	if observation.Partial {
		t.Fatalf("observation = %+v, want a complete observation: a launch postcondition must be decidable, not left indeterminate", observation)
	}
	if observation.ForegroundPackage != "com.example.target" {
		t.Fatalf("ForegroundPackage = %q, want the package the device's focused node reported", observation.ForegroundPackage)
	}
	if observation.Token != "sha256:capture" {
		t.Fatalf("Token = %q, want the capture's own freshness token", observation.Token)
	}
}

// TestAnUndeterminedForegroundPackageLeavesTheLaunchIndeterminate is the other
// half: a device that reports no focused package must leave the postcondition
// undecidable rather than failed, which is what keeps a launch that probably
// succeeded from being recorded as one that failed.
func TestAnUndeterminedForegroundPackageLeavesTheLaunchIndeterminate(t *testing.T) {
	fake := newFakeAdapter()
	fake.hierarchy = uiautomator.HierarchyCapture{
		Serial:         "fakeserial01",
		NodeCount:      2,
		MaxDepth:       2,
		FreshnessToken: "sha256:capture",
	}
	service := newLabService(t, fake)

	observer, err := execution.NewObservationPostconditionObserver(
		captureSerialResolver{serial: "fakeserial01"},
		func(serial string) (execution.ObservationSource, error) {
			return lab.NewObservationAdapter(service, serial, "operator-1")
		},
	)
	if err != nil {
		t.Fatalf("NewObservationPostconditionObserver() = %v", err)
	}

	observation, err := observer.ObservePostcondition(
		context.Background(),
		action.Intent{Kind: action.LaunchApp, Workspace: "ws-1", DeviceID: "device-1", ObservationToken: "sha256:before"},
		execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.target"}},
	)
	if err != nil {
		t.Fatalf("ObservePostcondition() = %v", err)
	}
	if observation.ForegroundPackage != "" {
		t.Fatalf("ForegroundPackage = %q, want it left unstated", observation.ForegroundPackage)
	}
	if !observation.Partial {
		t.Fatal("an observation with no foreground package must be partial, or a launch that was not observed would be failed")
	}
}
