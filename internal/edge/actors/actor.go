// Package actors serializes all execution for one fake device. It is a
// process-local actor only; durable lease/fencing authority remains in the
// control-plane action service.
package actors

import (
	"context"
	"errors"
	"strings"
	"sync"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type Request struct {
	Intent     action.Intent
	Authorized bool
}

type Response struct {
	AttemptID        string
	DeviceID         string
	Outcome          action.Outcome
	Postcondition    action.PostconditionState
	FailureClass     domain.FailureClass
	Dispatched       bool
	ObservationToken string
	CleanupSucceeded bool
	IdempotentReplay bool
}

type Actor struct {
	deviceID string
	adapter  adapter.Adapter
	queue    chan request
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
	results  map[string]storedResult
}

type storedResult struct {
	hash     string
	response Response
}

type request struct {
	input Request
	hash  string
	// ctx is the submitting caller's context. The execution derives from it, so
	// cancelling the caller stops the in-flight device call instead of only
	// stopping this actor from waiting on it.
	ctx    context.Context
	result chan result
}

type result struct {
	response Response
	err      error
}

func New(deviceID string, edgeAdapter adapter.Adapter, queueSize int) (*Actor, error) {
	if strings.TrimSpace(deviceID) == "" || edgeAdapter == nil || queueSize <= 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "device ID, adapter, and positive queue size are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	actor := &Actor{deviceID: deviceID, adapter: edgeAdapter, queue: make(chan request, queueSize), ctx: ctx, cancel: cancel, done: make(chan struct{}), results: make(map[string]storedResult)}
	go actor.loop()
	return actor, nil
}

func (a *Actor) Submit(ctx context.Context, input Request) (Response, error) {
	var empty Response
	if ctx == nil || a == nil {
		return empty, platformerrors.New(platformerrors.CodeInvalidInput, "context and actor are required")
	}
	if !input.Authorized {
		return empty, platformerrors.New(platformerrors.CodePolicyDenied, "only authorized typed intents may execute")
	}
	if err := input.Intent.Validate(); err != nil {
		return empty, platformerrors.Wrap(platformerrors.CodeInvalidInput, "typed intent is invalid", err)
	}
	if input.Intent.DeviceID != a.deviceID {
		return empty, platformerrors.New(platformerrors.CodeLeaseConflict, "intent targets another device actor")
	}
	spec, ok := action.Lookup(input.Intent.Kind)
	if !ok || !action.Supports(a.adapter.Capabilities(), spec.RequiredCapabilities) {
		return empty, platformerrors.New(platformerrors.CodeCapabilityMismatch, "device actor cannot execute this action")
	}
	if strings.TrimSpace(input.Intent.IdempotencyKey) == "" {
		return empty, platformerrors.New(platformerrors.CodeInvalidInput, "idempotency key is required")
	}
	hash, err := action.RequestHash(input.Intent)
	if err != nil {
		return empty, platformerrors.Wrap(platformerrors.CodeInvalidInput, "hash typed intent", err)
	}
	if input.Intent.RequestHash != "" && input.Intent.RequestHash != hash {
		return empty, platformerrors.New(platformerrors.CodeConflict, "intent request hash does not match typed fields")
	}
	resultCh := make(chan result, 1)
	a.mu.Lock()
	if previous, ok := a.results[input.Intent.IdempotencyKey]; ok {
		if previous.hash != hash {
			a.mu.Unlock()
			return empty, platformerrors.New(platformerrors.CodeConflict, "actor idempotency key was reused with a different intent")
		}
		a.mu.Unlock()
		previous.response.IdempotentReplay = true
		return previous.response, nil
	}
	if a.closed {
		a.mu.Unlock()
		return empty, platformerrors.New(platformerrors.CodeUnavailable, "device actor is closed")
	}
	a.mu.Unlock()
	select {
	case a.queue <- request{input: input, hash: hash, ctx: ctx, result: resultCh}:
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-a.ctx.Done():
		return empty, platformerrors.New(platformerrors.CodeUnavailable, "device actor is closed")
	}
	select {
	case result := <-resultCh:
		return result.response, result.err
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-a.ctx.Done():
		return empty, platformerrors.New(platformerrors.CodeUnavailable, "device actor is closed")
	}
}

func (a *Actor) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	<-a.done
	return nil
}

func (a *Actor) loop() {
	defer close(a.done)
	for {
		select {
		case <-a.ctx.Done():
			return
		case request := <-a.queue:
			a.mu.Lock()
			if previous, ok := a.results[request.input.Intent.IdempotencyKey]; ok {
				a.mu.Unlock()
				if previous.hash != request.hash {
					request.result <- result{err: platformerrors.New(platformerrors.CodeConflict, "actor idempotency key was reused with a different intent")}
					continue
				}
				previous.response.IdempotentReplay = true
				request.result <- result{response: previous.response}
				continue
			}
			a.mu.Unlock()
			response, err := a.execute(request.ctx, request.input)
			a.mu.Lock()
			if err == nil || response.AttemptID != "" {
				a.results[request.input.Intent.IdempotencyKey] = storedResult{hash: request.hash, response: response}
			}
			a.mu.Unlock()
			request.result <- result{response: response, err: err}
		}
	}
}

// execute runs one authorized intent. The execution context derives from the
// submitting caller's context, falling back to the actor's own when a caller
// supplied none, so cancellation and deadlines propagate to the device call
// itself rather than only ending this actor's wait. Cleanup stays on the
// actor's context: cleanup must still run when a caller walked away.
func (a *Actor) execute(parent context.Context, input Request) (Response, error) {
	intent := input.Intent
	response := Response{AttemptID: intent.ID, DeviceID: intent.DeviceID}
	if parent == nil {
		parent = a.ctx
	}
	executionCtx, cancel := context.WithTimeout(parent, intent.Timeout)
	defer cancel()
	execution, err := a.adapter.Execute(executionCtx, intent)
	if err != nil {
		var executionErr *adapter.ExecutionError
		if errors.As(err, &executionErr) {
			response.Dispatched = executionErr.Dispatched
			if executionErr.Dispatched {
				response.Outcome = action.OutcomeIndeterminate
				response.Postcondition = action.PostconditionUnknown
				response.FailureClass = domain.FailureIndeterminate
			} else if errors.Is(err, context.DeadlineExceeded) {
				response.Outcome = action.OutcomeTimedOut
				response.Postcondition = action.PostconditionUnknown
				response.FailureClass = domain.FailureTimeout
			} else {
				response.Outcome = action.OutcomeFailed
				response.Postcondition = action.PostconditionUnknown
				response.FailureClass = executionErr.FailureClass
			}
		} else {
			response.Outcome = action.OutcomeFailed
			response.Postcondition = action.PostconditionUnknown
			response.FailureClass = domain.FailureTransport
		}
	} else {
		response = responseFromExecution(intent, execution)
	}
	cleanupErr := a.adapter.Cleanup(a.ctx, intent)
	response.CleanupSucceeded = cleanupErr == nil
	if cleanupErr != nil {
		return response, platformerrors.New(platformerrors.CodeCleanupFailed, "fake device cleanup failed")
	}
	return response, nil
}

func responseFromExecution(intent action.Intent, execution adapter.Execution) Response {
	response := Response{AttemptID: intent.ID, DeviceID: intent.DeviceID, Outcome: execution.Outcome, Postcondition: execution.Postcondition, FailureClass: execution.FailureClass, Dispatched: execution.Dispatched, ObservationToken: execution.ObservationToken}
	if execution.TransportLost && execution.Dispatched {
		response.Outcome = action.OutcomeIndeterminate
		response.Postcondition = action.PostconditionUnknown
		response.FailureClass = domain.FailureIndeterminate
		return response
	}
	if response.Outcome == "" {
		if response.Postcondition == action.PostconditionPassed {
			response.Outcome = action.OutcomeVerified
		} else {
			response.Outcome = action.OutcomeFailed
		}
	}
	if response.Outcome == action.OutcomeVerified && response.Postcondition != action.PostconditionPassed {
		response.Outcome = action.OutcomeFailed
		response.FailureClass = domain.FailurePostcondition
	}
	return response
}
