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

// attemptReport is what the executing adapter reports back about one attempt: it
// is the one place that knows whether the transport was actually invoked and
// what observation was taken, and it is what makes the evidence record able to
// say "the input reached the device" rather than "the kernel dispatched it".
type attemptReport struct {
	// reached reports that a device call was made for this attempt.
	reached bool
	// observation is the observation the attempt resulted in.
	observation PostconditionObservation
	// observed reports that an observation was taken.
	observed bool
}

// attemptReportSink is the per-attempt mailbox between the executing adapter and
// the dispatcher that records evidence. The adapter writes it inside the actor;
// the dispatcher reads it after the actor has finished, so there is no shared
// mutable state beyond this one value.
type attemptReportSink struct {
	mu     sync.Mutex
	report attemptReport
}

// publish records what the adapter did. A nil sink is not an error: a caller that
// bound no report is a caller with nothing to report.
func (s *attemptReportSink) publish(report attemptReport) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report = report
}

// value returns the report the adapter published, or the zero report when it
// published none.
func (s *attemptReportSink) value() attemptReport {
	if s == nil {
		return attemptReport{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

// inputAdapter implements adapter.Adapter for the five typed device inputs.
type inputAdapter struct {
	transport InputTransport
	resolver  TextResolver
	observer  PostconditionObserver
	serial    string

	// renderSizes builds the source of the size this device actually presents
	// at. It is consulted for the coordinate-bearing kinds only, and it is given
	// the device's own transport rather than the counting wrapper above it: a
	// render-size read is a read-only precondition check, not the input, and
	// must not be counted as the input having reached the device.
	renderSizes RenderSizeSourceFactory

	mu      sync.Mutex
	pending map[string]boundAttempt
}

// boundAttempt is one attempt's typed payload together with its report sink. The
// sink is per-attempt and never shared between attempts.
type boundAttempt struct {
	payload InputPayload
	sink    *attemptReportSink
}

func newInputAdapter(transport InputTransport, resolver TextResolver, observer PostconditionObserver, serial string, renderSizes RenderSizeSourceFactory) *inputAdapter {
	return &inputAdapter{transport: transport, resolver: resolver, observer: observer, serial: serial, renderSizes: renderSizes, pending: make(map[string]boundAttempt)}
}

// carriesRenderCoordinate reports whether a kind dispatches a point that is only
// meaningful inside a render frame, so the cross-check applies to it and to
// nothing else.
func carriesRenderCoordinate(kind action.Kind) bool {
	return kind == action.Tap || kind == action.Swipe
}

// inputOptions binds the boundary for one dispatch. A coordinate-bearing kind
// must be able to establish the size the device presents at: when the
// render-size source cannot be built, the input is refused here rather than
// being dispatched with no way to check its frame.
func (a *inputAdapter) inputOptions(intent action.Intent, kind action.Kind) ([]InputOption, error) {
	options := []InputOption{WithInputTimeout(intent.Timeout)}
	if !carriesRenderCoordinate(kind) {
		return options, nil
	}
	if a.renderSizes == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the boundary has no source of the device render size, so a coordinate cannot be dispatched")
	}
	source, err := a.renderSizes(a.transport, a.serial)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the device render size could not be established, so a coordinate cannot be dispatched")
	}
	return append(options, WithRenderSizeSource(source)), nil
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
// dispatch, together with the sink the executing adapter reports what it did
// into. A payload is single-use: Execute consumes it.
func (a *inputAdapter) bind(attemptID string, payload InputPayload, sink *attemptReportSink) {
	if a == nil || attemptID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[attemptID] = boundAttempt{payload: payload, sink: sink}
}

func (a *inputAdapter) release(attemptID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, attemptID)
}

func (a *inputAdapter) take(attemptID string) (boundAttempt, bool) {
	if a == nil {
		return boundAttempt{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	bound, ok := a.pending[attemptID]
	delete(a.pending, attemptID)
	return bound, ok
}

// Execute runs one authorized typed input and then evaluates the catalog's
// declared postcondition for it. It fails closed when no typed payload is bound
// to the attempt or when the payload belongs to another kind, so the actor
// cannot be driven into injecting input the dispatcher did not build.
func (a *inputAdapter) Execute(ctx context.Context, intent action.Intent) (adapter.Execution, error) {
	if a == nil {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("device input adapter is not configured"), FailureClass: domain.FailureCapabilityMismatch}
	}
	bound, ok := a.take(intent.ID)
	if !ok {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("no typed device input payload is bound to this attempt"), FailureClass: domain.FailureInvalidTransition}
	}
	payload := bound.payload
	kind, err := payload.Kind()
	if err != nil || kind != intent.Kind {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("the bound payload is not the payload of this action kind"), FailureClass: domain.FailureInvalidTransition}
	}
	spec, ok := action.Lookup(intent.Kind)
	if !ok {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: errors.New("action kind is not a complete catalog entry"), FailureClass: domain.FailureCapabilityMismatch}
	}
	// Everything this attempt did is reported back to the dispatcher, whichever
	// way it ends, so the evidence record states what happened rather than what
	// was intended. Only the argument array itself reaches the transport: the
	// payload, its resolver and the transport's own diagnostics are never part
	// of the report.
	var counted *countingTransport
	report := attemptReport{}
	defer func() {
		if counted != nil && counted.attempted() {
			report.reached = true
		}
		bound.sink.publish(report)
	}()
	counted = &countingTransport{inner: a.transport}
	options, err := a.inputOptions(intent, kind)
	if err != nil {
		// The render-size source could not be established for a kind whose
		// frame has to be checked. No device call was made, so this is a
		// refusal before the input, not a failure of the input.
		return adapter.Execution{}, &adapter.ExecutionError{Cause: err, FailureClass: domain.FailureInfrastructure}
	}
	inputs, err := NewInputs(counted, a.resolver, a.serial, options...)
	if err != nil {
		return adapter.Execution{}, &adapter.ExecutionError{Cause: err, FailureClass: domain.FailureInfrastructure}
	}
	// The render-space cross-check runs here, explicitly, before the input is
	// sent: a coordinate whose frame the device does not present at is refused
	// as this boundary's own decision, so the refusal keeps the render-space
	// gate's classification instead of being flattened into whatever class a
	// device command happens to fail with. The primitives cross-check again below
	// - through the same reader, which caches its reading, so this costs no
	// second device read - and that inner check is what keeps a direct `Inputs`
	// caller from dispatching an unverified frame.
	if space, ok := payload.renderSpace(); ok {
		if err := inputs.checkRenderSpace(ctx, space); err != nil {
			// The class is derived here rather than from the error alone: this
			// call site is the render-space gate, so its refusal is named as
			// such even when the reader's own code (an unreadable device) is one
			// the generic mapping would otherwise report as a transport failure.
			return adapter.Execution{}, &adapter.ExecutionError{Cause: err, FailureClass: renderSpaceFailureClass(err)}
		}
	}
	if err := runInput(ctx, inputs, payload, kind, intent.Workspace); err != nil {
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
		// that is indeterminate, never success. The evidence record says so,
		// and carries no observation rather than an invented one.
		return adapter.Execution{
			Outcome:       action.OutcomeIndeterminate,
			Postcondition: action.PostconditionUnknown,
			FailureClass:  domain.FailureIndeterminate,
			Dispatched:    true,
		}, nil
	}
	report.observation = observation
	report.observed = true
	postcondition, failure, outcome := evaluatePostcondition(spec, intent, payload, observation)
	return adapter.Execution{
		Outcome:          outcome,
		Postcondition:    postcondition,
		FailureClass:     failure,
		Dispatched:       true,
		ObservationToken: observation.Token,
	}, nil
}

// runInput dispatches to exactly the primitive the payload names. A typed-text
// payload also carries the workspace the reference belongs to, because the value
// can only be released into the workspace that registered it.
func runInput(ctx context.Context, inputs *Inputs, payload InputPayload, kind action.Kind, workspace string) error {
	switch kind {
	case action.Tap:
		return inputs.Tap(ctx, *payload.Tap)
	case action.Swipe:
		return inputs.Swipe(ctx, *payload.Swipe)
	case action.TextInput:
		return inputs.TypeText(ctx, TypeTextRequest{Text: *payload.Text, Workspace: workspace})
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
	if isRenderSpaceRefusal(err) {
		return renderSpaceFailureClass(err)
	}
	if isTextReferenceFailure(err) {
		return domain.FailureReferenceUnreleased
	}
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

// isTextReferenceFailure reports whether a failure came from releasing a
// typed-text reference rather than from a device command. The registry and the
// primitive that calls it produce their own typed error, and the generic mapping
// would otherwise flatten "the reference could not be released" into "the input's
// device command failed at the transport". The first made no device call at all,
// and the two send an operator to different places.
func isTextReferenceFailure(err error) bool {
	var reference *TextReferenceError
	return errors.As(err, &reference)
}

// isRenderSpaceRefusal reports whether a refusal came from the render-space
// cross-check rather than from the device command itself. ARC-63's gate produces
// its own typed errors and its own codes, and the generic mapping below would
// otherwise flatten "the frame could not be checked" into "the input's device
// command failed at the transport" - two very different operational facts.
func isRenderSpaceRefusal(err error) bool {
	var mismatch *RenderSpaceMismatchError
	var stale *StaleRenderSizeError
	if errors.As(err, &mismatch) || errors.As(err, &stale) {
		return true
	}
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodePreconditionFailed, platformerrors.CodeStaleObservation:
		return true
	default:
		return false
	}
}

// renderSpaceFailureClass names why a coordinate was refused before it reached a
// device. The declared frame is not the size the device presents at:
//
//   - a mismatch, or a reading too old to refresh, means the frame the
//     coordinate was measured in is stale against the device;
//   - a reading that cannot be established means the device's render size could
//     not be observed at all.
//
// Neither is a transport failure of the input, because the input was never sent:
// an operator reading the classification must not be told that a tap failed when
// in fact no tap was ever dispatched.
func renderSpaceFailureClass(err error) domain.FailureClass {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodePreconditionFailed, platformerrors.CodeStaleObservation:
		return domain.FailureStaleObservation
	case platformerrors.CodeUnavailable:
		return domain.FailureObservation
	default:
		return domain.FailureInfrastructure
	}
}
