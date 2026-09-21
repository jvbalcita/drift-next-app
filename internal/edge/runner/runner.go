// Package runner connects a typed action attempt in the control plane to one
// serialized edge actor. The browser and adapter never share a direct call.
package runner

import (
	"context"
	"errors"
	"strings"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/actors"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

var ErrUnavailable = errors.New("typed edge runner is unavailable")

// errNoFreshObservation is what a caller reads when an attempt could not be
// completed because nothing re-read the device after it. The attempt is left
// uncompleted for reconciliation, and the sentence names the missing READING
// rather than the bookkeeping: an action whose outcome nobody read is not one
// this runner may report as verified, and reporting it as stale would hide why.
var errNoFreshObservation = errors.New("the action was not completed: no fresh observation of the device was taken after it, so the attempt awaits reconciliation")

// readingMissing is the failure a caller is handed when an attempt was left
// uncompleted because nothing re-read the device after it. The kernel's own
// sentence for that state is kept as the cause, so the transition that was made
// is still recorded, but the sentence the operator reads names the reading.
func readingMissing(cause error) error {
	if cause != nil {
		return platformerrors.Wrap(platformerrors.CodeIndeterminateCompletion, errNoFreshObservation.Error(), cause)
	}
	return platformerrors.New(platformerrors.CodeIndeterminateCompletion, errNoFreshObservation.Error())
}

type ControlPlane interface {
	Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Complete(context.Context, action.Completion, string, string) (action.Result, error)
	MarkIndeterminate(context.Context, action.Completion, string, string) (action.Result, error)
	Timeout(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Cancel(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Cleanup(context.Context, string, string, string, string, bool) (action.Result, error)
}

type Runner struct {
	control ControlPlane
	actor   *actors.Actor
}

func New(control ControlPlane, actor *actors.Actor) *Runner {
	return &Runner{control: control, actor: actor}
}

func (r *Runner) Close() error {
	if r == nil || r.actor == nil {
		return nil
	}
	return r.actor.Close()
}

func (r *Runner) Run(ctx context.Context, intent action.Intent, actorType, actorID string) (action.Result, error) {
	if r == nil || r.control == nil || r.actor == nil {
		return action.Result{}, ErrUnavailable
	}
	dispatched, err := r.control.Dispatch(ctx, intent.Workspace, intent.ID, intent.HolderID, intent.FencingToken, actorType, actorID)
	if err != nil {
		return dispatched, err
	}
	// The attempt was already terminal when this delivery arrived. Re-running
	// the device action would double-act on a duplicate delivery, so the
	// recorded outcome is returned instead.
	if dispatched.IdempotentReplay && dispatched.Attempt.State != action.AttemptDispatched {
		return dispatched, nil
	}
	response, actorErr := r.actor.Submit(ctx, actors.Request{Intent: intent, Authorized: true})
	completion := action.Completion{Workspace: intent.Workspace, AttemptID: intent.ID, DeviceID: intent.DeviceID, LeaseID: intent.LeaseID, HolderID: intent.HolderID, FencingToken: intent.FencingToken, Postcondition: response.Postcondition, ObservationToken: response.ObservationToken, FailureClass: string(response.FailureClass)}
	var completed action.Result
	switch {
	case actorErr != nil && response.AttemptID == "":
		completed, err = r.control.Cancel(ctx, intent.Workspace, intent.ID, intent.HolderID, intent.FencingToken, actorType, actorID)
	case response.Outcome == action.OutcomeTimedOut:
		completed, err = r.control.Timeout(ctx, intent.Workspace, intent.ID, intent.HolderID, intent.FencingToken, actorType, actorID)
	case response.Outcome == action.OutcomeIndeterminate || response.Dispatched && actorErr != nil:
		completed, err = r.control.MarkIndeterminate(ctx, completion, actorType, actorID)
		// The device was reached and the READING is what failed, so the caller is
		// told that rather than that the transport went quiet: the input may well
		// have landed, and "the device could not be read afterwards" is the fact
		// the operator can act on.
		if response.FailureClass == domain.FailureObservation {
			err = readingMissing(err)
		}
	case strings.TrimSpace(completion.ObservationToken) == "":
		// A completion names the reading it was evaluated against, and this
		// runner has none: the attempt was dispatched carrying a token, nothing
		// re-read the device after it, and the kernel refuses an empty token as
		// stale. Submitting it anyway would replace the reason the input failed
		// with a sentence about an observation nobody took, so the attempt is
		// left indeterminate for reconciliation instead - uncompleted - and the
		// caller is told that the reading is what is missing.
		var transitionErr error
		completed, transitionErr = r.control.MarkIndeterminate(ctx, completion, actorType, actorID)
		err = readingMissing(transitionErr)
	default:
		completed, err = r.control.Complete(ctx, completion, actorType, actorID)
	}
	if err != nil && completed.Attempt.ID == "" {
		return completed, err
	}
	if _, cleanupErr := r.control.Cleanup(ctx, intent.Workspace, intent.ID, actorType, actorID, response.CleanupSucceeded); cleanupErr != nil && err == nil {
		err = cleanupErr
	}
	// What an operator reads when the input never reached the device is the
	// boundary's OWN failure, not the kernel's sentence about the completion:
	// "the declared render space does not match the device render size" is a
	// fact about the input, and "transport outcome is indeterminate and requires
	// reconciliation" is a fact about the bookkeeping. The kernel's sentence
	// stands where the device WAS reached and the outcome is genuinely unknown.
	if actorErr != nil && (err == nil || !response.Dispatched) {
		err = actorErr
	}
	return completed, err
}
