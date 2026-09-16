package actors_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/actors"
	"drift.local/drift-next/internal/edge/adapter"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func actorIntent(id, key string) action.Intent {
	return action.Intent{
		ID:                id,
		Workspace:         "workspace-a",
		DeviceID:          "device-a",
		LeaseID:           "lease-a",
		HolderID:          "holder-a",
		FencingToken:      1,
		Kind:              action.Tap,
		Target:            action.SemanticTarget{ResourceID: "button"},
		IdempotencyKey:    key,
		ObservationToken:  "observation-a",
		InvocationSurface: action.SurfaceManual,
		Capabilities:      []action.Capability{action.CapabilityTap},
		Timeout:           time.Second,
	}
}

func TestActorSerializesExecutionAndReplaysDuplicateDelivery(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}, Delay: 5 * time.Millisecond})
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}, Delay: 5 * time.Millisecond})
	actor, err := actors.New("device-a", fake, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	ctx := context.Background()
	first, err := actor.Submit(ctx, actors.Request{Intent: actorIntent("attempt-a", "key-a"), Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != action.OutcomeVerified || !first.CleanupSucceeded {
		t.Fatalf("first response = %#v, want verified with cleanup", first)
	}
	secondIntent := actorIntent("attempt-b", "key-b")
	second, err := actor.Submit(ctx, actors.Request{Intent: secondIntent, Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Outcome != action.OutcomeVerified {
		t.Fatalf("second response = %#v, want verified", second)
	}
	replay, err := actor.Submit(ctx, actors.Request{Intent: actorIntent("different-attempt-id", "key-a"), Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.IdempotentReplay || replay.AttemptID != "attempt-a" {
		t.Fatalf("duplicate response = %#v, want replay of attempt-a", replay)
	}
	if fake.ExecuteCalls() != 2 || fake.MaxConcurrent() != 1 {
		t.Fatalf("execute calls/max concurrency = %d/%d, want 2/1", fake.ExecuteCalls(), fake.MaxConcurrent())
	}
}

func TestActorDoesNotExecuteConcurrentDuplicateDeliveryTwice(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true}, Delay: 10 * time.Millisecond})
	actor, err := actors.New("device-a", fake, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	intent := actorIntent("attempt-concurrent", "key-concurrent")
	responses := make(chan struct {
		response actors.Response
		err      error
	}, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			response, submitErr := actor.Submit(context.Background(), actors.Request{Intent: intent, Authorized: true})
			responses <- struct {
				response actors.Response
				err      error
			}{response: response, err: submitErr}
		}()
	}
	wait.Wait()
	close(responses)
	replays := 0
	for result := range responses {
		if result.err != nil || result.response.Outcome != action.OutcomeVerified {
			t.Fatalf("concurrent duplicate result = %#v, want verified", result)
		}
		if result.response.IdempotentReplay {
			replays++
		}
	}
	if replays != 1 || fake.ExecuteCalls() != 1 {
		t.Fatalf("concurrent duplicate replays/execute calls = %d/%d, want 1/1", replays, fake.ExecuteCalls())
	}
}

func TestActorKeepsPostDispatchTransportLossIndeterminate(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Outcome: action.OutcomeVerified, Postcondition: action.PostconditionPassed, Dispatched: true, TransportLost: true}})
	actor, err := actors.New("device-a", fake, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	response, err := actor.Submit(context.Background(), actors.Request{Intent: actorIntent("attempt-transport", "key-transport"), Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if response.Outcome != action.OutcomeIndeterminate || response.FailureClass != domain.FailureIndeterminate || response.Postcondition != action.PostconditionUnknown {
		t.Fatalf("transport-loss response = %#v, want indeterminate", response)
	}
}

func TestActorMapsTimeoutAndCleanupFailureWithoutHidingOutcome(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	fake.QueueExecution(adapter.ExecutionScript{Execution: adapter.Execution{Dispatched: false}, Delay: 20 * time.Millisecond})
	fake.QueueCleanupError(errors.New("fake cleanup failed"))
	actor, err := actors.New("device-a", fake, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	intent := actorIntent("attempt-timeout", "key-timeout")
	intent.Timeout = time.Millisecond
	response, err := actor.Submit(context.Background(), actors.Request{Intent: intent, Authorized: true})
	if platformerrors.CodeOf(err) != platformerrors.CodeCleanupFailed {
		t.Fatalf("timeout/cleanup error code = %v, want cleanup_failed", platformerrors.CodeOf(err))
	}
	if response.Outcome != action.OutcomeTimedOut || response.FailureClass != domain.FailureTimeout || response.CleanupSucceeded {
		t.Fatalf("timeout/cleanup response = %#v, want timed_out with failed cleanup", response)
	}
}

// blockingAdapter blocks until its context ends. It exists to prove that the
// actor's execution derives from the caller's context, so cancelling the caller
// stops the in-flight device call instead of only ending the caller's wait.
type blockingAdapter struct {
	capabilities []action.Capability
	entered      chan struct{}
	exited       chan struct{}
	once         sync.Once
}

func newBlockingAdapter(capability action.Capability) *blockingAdapter {
	return &blockingAdapter{capabilities: []action.Capability{capability}, entered: make(chan struct{}), exited: make(chan struct{})}
}

func (b *blockingAdapter) Capabilities() []action.Capability { return b.capabilities }

func (b *blockingAdapter) Observe(context.Context) (adapter.Observation, error) {
	return adapter.Observation{}, nil
}

func (b *blockingAdapter) Cleanup(context.Context, action.Intent) error { return nil }

func (b *blockingAdapter) Execute(ctx context.Context, _ action.Intent) (adapter.Execution, error) {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	close(b.exited)
	return adapter.Execution{Dispatched: true, Outcome: action.OutcomeIndeterminate, Postcondition: action.PostconditionUnknown, FailureClass: domain.FailureIndeterminate},
		&adapter.ExecutionError{Cause: ctx.Err(), Dispatched: true, FailureClass: domain.FailureIndeterminate}
}

func TestActorStopsTheInFlightCallWhenTheCallerCancels(t *testing.T) {
	blocking := newBlockingAdapter(action.CapabilityTap)
	actor, err := actors.New("device-a", blocking, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	intent := actorIntent("attempt-blocked", "key-blocked")
	intent.Timeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, submitErr := actor.Submit(ctx, actors.Request{Intent: intent, Authorized: true})
		done <- submitErr
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the adapter call was never entered")
	}
	cancel()
	select {
	case <-blocking.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the caller did not stop the in-flight adapter call")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the actor did not return after cancellation")
	}
}

func TestActorRejectsUntrustedOrIncompatibleRequests(t *testing.T) {
	fake := adapter.NewFakeAdapter(action.CapabilityTap)
	actor, err := actors.New("device-a", fake, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	intent := actorIntent("attempt-invalid", "key-invalid")
	if _, err := actor.Submit(context.Background(), actors.Request{Intent: intent}); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("unauthorized request code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
	wrongDevice := intent
	wrongDevice.ID = "attempt-wrong-device"
	wrongDevice.IdempotencyKey = "key-wrong-device"
	wrongDevice.DeviceID = "device-b"
	if _, err := actor.Submit(context.Background(), actors.Request{Intent: wrongDevice, Authorized: true}); platformerrors.CodeOf(err) != platformerrors.CodeLeaseConflict {
		t.Fatalf("wrong-device request code = %v, want lease_conflict", platformerrors.CodeOf(err))
	}
	unsupported := intent
	unsupported.ID = "attempt-unsupported"
	unsupported.IdempotencyKey = "key-unsupported"
	unsupported.Kind = action.TextInput
	unsupported.Capabilities = []action.Capability{action.CapabilityTextInput}
	unsupported.Target = action.SemanticTarget{ResourceID: "field"}
	if _, err := actor.Submit(context.Background(), actors.Request{Intent: unsupported, Authorized: true}); platformerrors.CodeOf(err) != platformerrors.CodeCapabilityMismatch {
		t.Fatalf("unsupported request code = %v, want capability_mismatch", platformerrors.CodeOf(err))
	}
	if fake.ExecuteCalls() != 0 {
		t.Fatalf("rejected requests executed %d times, want 0", fake.ExecuteCalls())
	}
}
