package execution_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
)

func launchTo(packageName string) execution.InputPayload {
	return execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: packageName}}
}

// A launch is idempotent on the target it names, and this is the proof that the
// target has to be in the request for that to be true.
//
// The request hash is what the store compares when an idempotency key is reused:
// the same key with the same hash is served from the recorded result without a
// second device call, and the same key with a different hash is refused as a
// reused key. While the intent carried no target, two launches of different
// packages hashed alike, so the second launch was answered as the first: the
// device was never asked to launch it, and nothing refused it, because as far as
// the kernel could tell it was the same request.
func TestALaunchIsIdempotentOnTheTargetItNames(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice)

	first := fixture.request("launch-1", "shared-key")
	first.Payload = launchTo("com.example.first")
	if _, err := fixture.dispatcher.Run(ctx, first, "operator", "operator-1"); err != nil {
		t.Fatalf("the first launch was refused: %v", err)
	}

	second := fixture.request("launch-2", "shared-key")
	second.Payload = launchTo("com.example.second")
	_, err := fixture.dispatcher.Run(ctx, second, "operator", "operator-1")
	refusal, ok := execution.RefusalOf(err)
	if !ok {
		t.Fatalf("a second launch of another package under the same key raised no refusal (err = %v): the target is not part of the request, so the second launch is answered as the first and the device is never asked to launch it", err)
	}
	if refusal.Reason != execution.RefusalDuplicateIdempotencyKey {
		t.Fatalf("refusal reason = %q, want %q: a reused key naming a different target is a different request and must be refused as reused rather than served as the earlier attempt", refusal.Reason, execution.RefusalDuplicateIdempotencyKey)
	}
}

// The other half of the same rule, asserted so a refusal cannot be passed by
// refusing everything: the same key naming the same target is the same request,
// and it is served from the recorded result with no second device call.
func TestTheSameLaunchUnderTheSameKeyIsTheSameRequest(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice)

	first := fixture.request("launch-1", "shared-key")
	first.Payload = launchTo("com.example.target")
	if _, err := fixture.dispatcher.Run(ctx, first, "operator", "operator-1"); err != nil {
		t.Fatalf("the first launch was refused: %v", err)
	}
	afterFirst := fixture.transport.invocationCount()

	second := fixture.request("launch-2", "shared-key")
	second.Payload = launchTo("com.example.target")
	if _, err := fixture.dispatcher.Run(ctx, second, "operator", "operator-1"); err != nil {
		t.Fatalf("the same launch under the same key was refused: %v", err)
	}
	if calls := fixture.transport.invocationCount(); calls != afterFirst {
		t.Fatalf("device calls after the repeated launch = %d, want %d unchanged: the same request under the same key is served from the recorded result", calls, afterFirst)
	}
}
