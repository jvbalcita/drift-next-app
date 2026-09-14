package fanout_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/actors"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/fanout"
	"drift.local/drift-next/internal/edge/runner"
	"drift.local/drift-next/internal/platform/errors"
)

type control struct{ fail bool }

func (c control) Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	if c.fail {
		return action.Result{}, errors.New(errors.CodePolicyDenied, "follower policy denied")
	}
	return action.Result{}, nil
}
func (control) Complete(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: completion.AttemptID}, Outcome: action.OutcomeVerified}, nil
}
func (control) MarkIndeterminate(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: completion.AttemptID}, Outcome: action.OutcomeIndeterminate}, nil
}
func (control) Timeout(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: attemptID}, Outcome: action.OutcomeTimedOut}, nil
}
func (control) Cancel(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	return action.Result{Attempt: action.Attempt{ID: attemptID}, Outcome: action.OutcomeCancelled}, nil
}
func (control) Cleanup(context.Context, string, string, string, string, bool) (action.Result, error) {
	return action.Result{}, nil
}

func target(device, lease, key string, controlPlane runner.ControlPlane) (fanout.Target, error) {
	adapterFake := adapter.NewFakeAdapter(action.CapabilityTap)
	adapterFake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true, ObservationToken: "after-" + device}})
	actor, err := actors.New(device, adapterFake, 1)
	if err != nil {
		return fanout.Target{}, err
	}
	intent := action.Intent{ID: "attempt-" + device, Workspace: "workspace-a", DeviceID: device, LeaseID: lease, HolderID: "holder-" + device, FencingToken: 1, Kind: action.Tap, Target: action.SemanticTarget{ResourceID: "same-semantic-button"}, IdempotencyKey: key, ObservationToken: "before-" + device, InvocationSurface: action.SurfaceMirror, Capabilities: []action.Capability{action.CapabilityTap}, Timeout: time.Second}
	return fanout.Target{DeviceID: device, Intent: intent, Runner: runner.New(controlPlane, actor)}, nil
}

func TestFanoutExcludesSourceAndKeepsFollowerResultsIndependent(t *testing.T) {
	first, err := target("device-b", "lease-b", "key-b", control{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := target("device-c", "lease-c", "key-c", control{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Runner.Close()
	defer second.Runner.Close()
	results, err := (fanout.Session{SourceDeviceID: "device-a", FailurePolicy: fanout.Continue, Targets: []fanout.Target{first, second}}).Run(context.Background(), "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].DeviceID != "device-b" || results[1].DeviceID != "device-c" {
		t.Fatalf("fanout results = %#v, want sorted independent followers", results)
	}
	for _, result := range results {
		if result.Err != nil || result.Action.Outcome != action.OutcomeVerified {
			t.Fatalf("follower result = %#v, want verified", result)
		}
	}
	if _, err := (fanout.Session{SourceDeviceID: "device-a", FailurePolicy: fanout.Continue, Targets: []fanout.Target{{DeviceID: "device-a", Intent: action.Intent{DeviceID: "device-a"}}}}).Run(context.Background(), "operator", "operator-1"); errors.CodeOf(err) != errors.CodeInvalidInput {
		t.Fatalf("source follower validation code = %v, want invalid_input", errors.CodeOf(err))
	}
}

func TestFanoutContinueKeepsOneFollowerFailureSeparate(t *testing.T) {
	succeeded, err := target("device-b", "lease-b", "key-b", control{})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := target("device-c", "lease-c", "key-c", control{fail: true})
	if err != nil {
		t.Fatal(err)
	}
	defer succeeded.Runner.Close()
	defer denied.Runner.Close()
	results, err := (fanout.Session{SourceDeviceID: "device-a", FailurePolicy: fanout.Continue, Targets: []fanout.Target{succeeded, denied}}).Run(context.Background(), "operator", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Err != nil || results[0].Action.Outcome != action.OutcomeVerified || results[1].Err == nil {
		t.Fatalf("independent failure results = %#v, want success and separate failure", results)
	}
	if errors.CodeOf(results[1].Err) != errors.CodePolicyDenied {
		t.Fatalf("follower failure code = %v, want policy_denied", errors.CodeOf(results[1].Err))
	}
}
