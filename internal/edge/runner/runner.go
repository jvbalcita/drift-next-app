// Package runner connects a typed action attempt in the control plane to one
// serialized edge actor. The browser and adapter never share a direct call.
package runner

import (
	"context"
	"errors"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/actors"
)

var ErrUnavailable = errors.New("typed edge runner is unavailable")

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
	if actorErr != nil && response.AttemptID == "" {
		completed, err = r.control.Cancel(ctx, intent.Workspace, intent.ID, intent.HolderID, intent.FencingToken, actorType, actorID)
	} else if response.Outcome == action.OutcomeTimedOut {
		completed, err = r.control.Timeout(ctx, intent.Workspace, intent.ID, intent.HolderID, intent.FencingToken, actorType, actorID)
	} else if response.Outcome == action.OutcomeIndeterminate || response.Dispatched && actorErr != nil {
		completed, err = r.control.MarkIndeterminate(ctx, completion, actorType, actorID)
	} else {
		completed, err = r.control.Complete(ctx, completion, actorType, actorID)
	}
	if err != nil && completed.Attempt.ID == "" {
		return completed, err
	}
	if _, cleanupErr := r.control.Cleanup(ctx, intent.Workspace, intent.ID, actorType, actorID, response.CleanupSucceeded); cleanupErr != nil && err == nil {
		err = cleanupErr
	}
	if actorErr != nil && err == nil {
		err = actorErr
	}
	return completed, err
}
