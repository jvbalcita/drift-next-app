// The edge adapter for the five typed device inputs.
//
// This adapter is what the serialized per-device actor executes. It refuses to
// run without a typed payload bound to the attempt by the dispatcher, so the
// actor cannot inject input for an intent this boundary did not build. It
// reaches a device only through the narrow `Inputs` primitives, which accept
// typed parameters and build their own argument arrays; there is no shell, no
// exec, no argv and no command text at this boundary either.
package execution

import (
	"context"
	"errors"
	"sync"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// inputCapabilities are the capabilities this boundary can execute. They are
// the union of what the five catalog entries require, so the actor's capability
// check is a real check and not a formality.
func inputCapabilities() []action.Capability {
	return []action.Capability{action.CapabilityTap, action.CapabilityGesture, action.CapabilityTextInput, action.CapabilitySystemInput}
}

// countingTransport records whether the device was actually reached. It exists
// so the adapter can tell "the call never reached the device" from "the call
// reached the device and its outcome is uncertain" - the single distinction the
// kernel needs to keep an uncertain completion indeterminate.
type countingTransport struct {
	inner InputTransport

	mu       sync.Mutex
	attempts int
}

func (c *countingTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	c.mu.Lock()
	c.attempts++
	c.mu.Unlock()
	return c.inner.RunDeviceCommand(ctx, serial, args)
}

func (c *countingTransport) attempted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts > 0
}

// inputAdapter implements adapter.Adapter for the five typed device inputs.
type inputAdapter struct {
	transport InputTransport
	resolver  TextResolver
	observer  PostconditionObserver
	serial    string

	mu      sync.Mutex
	pending map[string]InputPayload
}

func newInputAdapter(transport InputTransport, resolver TextResolver, observer PostconditionObserver, serial string) *inputAdapter {
	return &inputAdapter{transport: transport, resolver: resolver, observer: observer, serial: serial, pending: make(map[string]InputPayload)}
}

func (a *inputAdapter) Capabilities() []action.Capability { return inputCapabilities() }

// Observe refuses: this boundary injects typed input and never observes. The
// read side of the contract is the postcondition observer.
func (a *inputAdapter) Observe(context.Context) (adapter.Observation, error) {
	return adapter.Observation{}, platformerrors.New(platformerrors.CodeCapabilityMismatch, "the device input boundary does not observe")
}

// Cleanup reports that there is nothing to clean up: the five primitives create
// no device-side artifact, so no temporary file, spool entry or session
// survives a dispatch. The kernel still records the cleanup transition.
func (a *inputAdapter) Cleanup(context.Context, action.Intent) error { return nil }

// bind attaches one typed payload to one attempt for the duration of a
// dispatch. A payload is single-use: Execute consumes it.
func (a *inputAdapter) bind(attemptID string, payload InputPayload) {
	if a == nil || attemptID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[attemptID] = payload
}

func (a *inputAdapter) release(attemptID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, attemptID)
}

func (a *inputAdapter) take(attemptID string) (InputPayload, bool) {
	if a == nil {
		return InputPayload{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	payload, ok := a.pending[attemptID]
	delete(a.pending, attemptID)
	return payload, ok
}

// Execute runs one authorized typed input and then evaluates the catalog's
// declared postcondition for it. It fails closed when no typed payload is bound
// to the attempt or when the payload belongs to another kind, so the actor
// cannot be driven into injecting input the dispatcher did not build.
func (a *inputAdapter) Execute(ctx context.Context, intent action.Intent) (adapter.Execution, error) {
	if a == nil {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("device input adapter is not configured"), FailureClass: domain.FailureCapabilityMismatch}
	}
	payload, ok := a.take(intent.ID)
	if !ok {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("no typed device input payload is bound to this attempt"), FailureClass: domain.FailureInvalidTransition}
	}
	kind, err := payload.Kind()
	if err != nil || kind != intent.Kind {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("the bound payload is not the payload of this action kind"), FailureClass: domain.FailureInvalidTransition}
	}
	spec, ok := action.Lookup(intent.Kind)
	if !ok {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("action kind is not a complete catalog entry"), FailureClass: domain.FailureCapabilityMismatch}
	}
	counted := &countingTransport{inner: a.transport}
	inputs, err := NewInputs(counted, a.resolver, a.serial, WithInputTimeout(intent.Timeout))
	if err != nil {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: err, FailureClass: domain.FailureInfrastructure}
	}
	if err := runInput(ctx, inputs, payload, kind); err != nil {
		// The typed payload and the transport's own diagnostics are never
		// echoed: a failing device command can quote what it was given.
		return adapter.Execution{}, &adapter.ExecutionError{
			Cause:        err,
			Dispatched:   counted.attempted(),
			FailureClass: failureClassFor(err),
		}
	}
	observation, observeErr := a.observer.ObservePostcondition(ctx, intent, payload)
	if observeErr != nil {
		// The input reached the device and the outcome could not be observed:
		// that is indeterminate, never success.
		return adapter.Execution{
			Outcome:       action.OutcomeIndeterminate,
			Postcondition: action.PostconditionUnknown,
			FailureClass:  domain.FailureIndeterminate,
			Dispatched:    true,
		}, nil
	}
	postcondition, failure, outcome := evaluatePostcondition(spec, intent, payload, observation)
	return adapter.Execution{
		Outcome:          outcome,
		Postcondition:    postcondition,
		FailureClass:     failure,
		Dispatched:       true,
		ObservationToken: observation.Token,
	}, nil
}

// runInput dispatches to exactly the primitive the payload names.
func runInput(ctx context.Context, inputs *Inputs, payload InputPayload, kind action.Kind) error {
	switch kind {
	case action.Tap:
		return inputs.Tap(ctx, *payload.Tap)
	case action.Swipe:
		return inputs.Swipe(ctx, *payload.Swipe)
	case action.TextInput:
		return inputs.TypeText(ctx, TypeTextRequest{Text: *payload.Text})
	case action.KeyEvent:
		return inputs.KeyEvent(ctx, *payload.KeyEvent)
	case action.LaunchApp:
		return inputs.LaunchApp(ctx, *payload.Launch)
	default:
		return platformerrors.New(platformerrors.CodeInvalidInput, "device input kind is not dispatchable")
	}
}

// failureClassFor classifies a failed device input. It reads the classified
// error code rather than the message, so a refusal keeps its stable class.
func failureClassFor(err error) domain.FailureClass {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodeCanceled:
		return domain.FailureOperatorCancelled
	case platformerrors.CodeTimeout, platformerrors.CodeDeadlineExceeded:
		return domain.FailureTimeout
	case platformerrors.CodeInvalidInput:
		return domain.FailureInvalidTransition
	case platformerrors.CodeUnavailable:
		return domain.FailureTransport
	default:
		return domain.FailureInfrastructure
	}
}
