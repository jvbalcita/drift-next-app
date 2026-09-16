package execution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
)

// The composition proof for the workspace scope: a dispatcher carrying the real
// registry releases a typed-text reference in the workspace whose operator
// registered it, and a dispatch in another workspace naming the same handle is
// refused and reaches no device — and does not consume the value on the way.
func TestATypedTextReferenceIsConsumedOnlyInItsOwnWorkspace(t *testing.T) {
	ctx := context.Background()
	registry, err := execution.NewTextReferenceRegistry(
		&registryClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)},
		execution.DefaultTextReferenceTTL,
		execution.DefaultTextReferenceCapacity,
	)
	if err != nil {
		t.Fatalf("new text reference registry: %v", err)
	}
	const owningWorkspace, otherWorkspace = "workspace-owning", "workspace-other"
	if err := registry.Register(owningWorkspace, "reference-1", typedValueFixture); err != nil {
		t.Fatalf("register the reference: %v", err)
	}

	transport := newFakeDeviceTransport()
	dispatcher, err := execution.NewInputDispatcher(
		&fakeControl{},
		&fakeProbe{},
		&fakeObserver{observation: execution.PostconditionObservation{Token: postToken, FieldLength: len(typedValueFixture)}},
		transport,
		registry,
		execution.WithRenderSizeSourceFactory(testRenderSizeSource),
	)
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })

	payload := execution.InputPayload{Text: &execution.TextReference{Handle: "reference-1", Length: uint32(len(typedValueFixture))}}
	// A typed-text intent requires the field it types into. Set it on both
	// requests, so a refusal in the wrong place cannot pass for the right one.
	target := action.SemanticTarget{ResourceID: "composer-field"}

	// Another workspace naming the handle fails, and the value survives it: the
	// refusal is recorded as a failed attempt rather than returned as an error,
	// which is what the evidence contract does with a rejected dispatch.
	foreign := inputRequest("attempt-foreign", "key-foreign", payload)
	foreign.Workspace = otherWorkspace
	foreign.Target = target
	foreignResult, err := dispatcher.Run(ctx, foreign, "operator", "operator-1")
	if err != nil {
		t.Fatalf("a cross-workspace dispatch returned an error rather than a recorded failure: %v", err)
	}
	if foreignResult.Outcome == action.OutcomeVerified {
		t.Fatal("a workspace dispatched a reference another workspace registered")
	}
	if foreignResult.Outcome != action.OutcomeFailed {
		t.Fatalf("outcome = %q, want a failed attempt", foreignResult.Outcome)
	}
	// The class names the cause: this refusal made no device call, so it must not
	// be recorded as a failure of the device connection.
	if foreignResult.FailureClass == string(domain.FailureTransport) {
		t.Fatalf("a refusal that reached no device was recorded as %q", foreignResult.FailureClass)
	}
	if foreignResult.FailureClass != string(domain.FailureReferenceUnreleased) {
		t.Fatalf("failure class = %q, want %q", foreignResult.FailureClass, domain.FailureReferenceUnreleased)
	}
	if calls := transport.invocationCount(); calls != 0 {
		t.Fatalf("device calls = %d, want 0: a refused typed-text dispatch reaches no device", calls)
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1: the refused dispatch consumed the value", held)
	}

	// The owning workspace releases it into exactly one device argument.
	owned := inputRequest("attempt-owned", "key-owned", payload)
	owned.Workspace = owningWorkspace
	owned.Target = target
	ownedResult, err := dispatcher.Run(ctx, owned, "operator", "operator-1")
	if err != nil {
		t.Fatalf("the owning workspace's dispatch was refused: %v", err)
	}
	if ownedResult.Outcome != action.OutcomeVerified {
		t.Fatalf("outcome = %q, want verified", ownedResult.Outcome)
	}
	if calls := transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	call := transport.invocation(0)
	if len(call.args) != 4 || call.args[3] != typedValueFixture {
		t.Fatal("the released value did not become the single argument token")
	}
	if !strings.Contains(strings.Join(call.args, " "), "shell input text") {
		t.Fatalf("argument array = %v, want the typed text shape", call.args)
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: the value was released at dispatch", held)
	}
}
