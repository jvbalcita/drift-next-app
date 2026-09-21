package execution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The completion of one gesture, pinned through the path the console's own
// request travels: the real kernel (`store.ActionService`), the real readiness
// probe and the real dispatcher, over a disposable SQLite database and a fake
// device transport. No device and no adb process is involved.
//
// The kernel completes an attempt only against an observation taken AFTER it: an
// empty token, or the token the attempt was dispatched with, is refused as stale
// (`internal/store/sqlite/safety_actions.go`). That condition is the reason a
// gesture is not a gesture nobody re-read the device for, and it is deliberately
// NOT relaxed here - what is pinned instead is that the completion path presents
// the fresh reading, and that an attempt with no reading at all is left
// uncompleted rather than completed against a token nobody read.
func TestTheGestureCompletionPresentsAFreshReading(t *testing.T) {
	ctx := context.Background()

	t.Run("a completion against the dispatched observation is refused as stale", func(t *testing.T) {
		fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice)
		// The boundary's post-attempt reading is the observation the attempt was
		// dispatched with. The input DOES reach the device, and the completion is
		// still refused: the reading is not a reading taken after the action.
		fixture.observer.observation.Token = obsToken
		request := fixture.request("attempt-dispatched-observation", "dispatched-observation-key")
		if _, err := fixture.dispatcher.Run(ctx, request, "operator", "operator-1"); platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
			t.Fatalf("a completion naming the dispatched observation = %v, want stale_observation", err)
		}
		if fixture.transport.invocationCount() != 1 {
			t.Fatalf("device calls = %d, want exactly 1: the input reached the device and only its completion was refused", fixture.transport.invocationCount())
		}
		persisted, err := fixture.control.Get(ctx, string(fixture.workspace), request.IntentID)
		if err != nil {
			t.Fatalf("read persisted attempt: %v", err)
		}
		if persisted.Attempt.State != action.AttemptFailed || persisted.Attempt.FailureClass != string(domain.FailureStaleObservation) {
			t.Fatalf("persisted attempt = %#v, want failed with %s", persisted.Attempt, domain.FailureStaleObservation)
		}
	})

	t.Run("a completion against a fresh reading verifies the attempt", func(t *testing.T) {
		fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice)
		request := fixture.request("attempt-fresh-reading", "fresh-reading-key")
		result, err := fixture.dispatcher.Run(ctx, request, "operator", "operator-1")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if result.Outcome != action.OutcomeVerified || !result.PostconditionPassed {
			t.Fatalf("result = %#v, want verified with a passed postcondition", result)
		}
		if fixture.observer.observations != 1 {
			t.Fatalf("the attempt observed the device %d times, want exactly 1 reading taken after it", fixture.observer.observations)
		}
		if obsToken == postToken {
			t.Fatal("the fixture's dispatched and post-attempt observations are the same, so this case proves nothing")
		}
		persisted, err := fixture.control.Get(ctx, string(fixture.workspace), request.IntentID)
		if err != nil {
			t.Fatalf("read persisted attempt: %v", err)
		}
		if persisted.Attempt.State != action.AttemptVerified {
			t.Fatalf("persisted attempt = %#v, want verified", persisted.Attempt)
		}
		// The attempt keeps the observation it was dispatched with; the reading
		// the completion named is the one taken after it, which is what the
		// kernel accepted it against.
		if persisted.Attempt.ObservationToken != obsToken {
			t.Fatalf("the attempt was dispatched with %q, want the request's own observation %q", persisted.Attempt.ObservationToken, obsToken)
		}
	})

	t.Run("a completion with no reading is not submitted at all", func(t *testing.T) {
		// The device cannot be read at all: the render-space gate refuses the
		// coordinate, and the reading the completion would have to name cannot be
		// taken either. Nothing may be invented to fill the gap.
		fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice, execution.WithRenderSizeSourceFactory(
			func(execution.InputTransport, string) (execution.RenderSizeSource, error) {
				return &fakeRenderSizes{err: platformerrors.New(platformerrors.CodeUnavailable, "the device render size could not be read")}, nil
			}))
		fixture.observer.err = platformerrors.New(platformerrors.CodeUnavailable, "the device could not be observed")

		request := fixture.request("attempt-no-reading", "no-reading-key")
		_, err := fixture.dispatcher.Run(ctx, request, "operator", "operator-1")
		if platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion {
			t.Fatalf("a dispatch with no reading = %v, want indeterminate_completion", err)
		}
		if !strings.Contains(err.Error(), "no fresh observation of the device was taken after it") {
			t.Fatalf("the failure does not say the reading is missing: %v", err)
		}
		if fixture.transport.invocationCount() != 0 {
			t.Fatalf("device calls = %d, want 0: the coordinate was refused before it was sent", fixture.transport.invocationCount())
		}
		persisted, err := fixture.control.Get(ctx, string(fixture.workspace), request.IntentID)
		if err != nil {
			t.Fatalf("read persisted attempt: %v", err)
		}
		if persisted.Attempt.State != action.AttemptIndeterminate {
			t.Fatalf("persisted attempt = %#v, want indeterminate: an attempt nothing read is left for reconciliation rather than completed", persisted.Attempt)
		}
	})
}
