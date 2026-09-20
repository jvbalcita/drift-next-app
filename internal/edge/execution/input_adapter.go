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
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// inputCapabilities are the capabilities this boundary can execute. They are
// the union of what the catalog entries it runs require, so the actor's
// capability check is a real check and not a formality.
func inputCapabilities() []action.Capability {
	return []action.Capability{action.CapabilityTap, action.CapabilityGesture, action.CapabilityTextInput, action.CapabilitySystemInput, action.CapabilityDeviceSettings}
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

	// departures is the port a reboot's departure is observed through, and
	// transferRoot is the host directory a push materializes its payload
	// under. Both are deployment inputs; a boundary with neither still
	// dispatches every kind whose operation does not need them, and refuses
	// the ones that do rather than acting without its postcondition.
	departures   DepartureObserver
	transferRoot string

	// archive stores the bytes one export pulled off a device. It is nil when
	// the deployment has no artifact store, and an export then refuses rather
	// than pulling bytes it cannot keep.
	archive DeviceFileArchive

	// deviceID names the device this adapter is bound to. It is what the live
	// session delivering an input is looked up by: a session is one device's,
	// and a serial may be a transport that moved.
	deviceID string

	// mirror is the live session this device's input travels when one exists.
	// It is nil until a caller binds one, and a nil mirror means every input
	// travels the argv path.
	mirror MirrorDelivery

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
// sink is per-attempt and never shared between attempts, and the actor is the
// one the attempt is dispatched on behalf of - an export writes the bytes it
// pulled into the artifact store ON THAT ACTOR'S BEHALF, so the store's audit
// trail names the operator or agent that ran the action rather than the plane.
type boundAttempt struct {
	payload   InputPayload
	sink      *attemptReportSink
	actorType string
	actorID   string
}

func newInputAdapter(transport InputTransport, resolver TextResolver, observer PostconditionObserver, deviceID, serial string, renderSizes RenderSizeSourceFactory, delivery MirrorDelivery, departures DepartureObserver, transferRoot string, archive DeviceFileArchive) *inputAdapter {
	return &inputAdapter{transport: transport, resolver: resolver, observer: observer, deviceID: deviceID, serial: serial, renderSizes: renderSizes, mirror: delivery, departures: departures, transferRoot: transferRoot, archive: archive, pending: make(map[string]boundAttempt)}
}

// carriesRenderCoordinate reports whether a kind dispatches a point that is only
// meaningful inside a render frame, so the cross-check applies to it and to
// nothing else.
func carriesRenderCoordinate(kind action.Kind) bool {
	return kind == action.Tap || kind == action.Swipe
}

// mirrorInputKinds are the kinds a live session can carry. It is deliberately
// not every kind this boundary executes:
//
//   - a catalogue settings operation is a device SETTING with a read-back, and
//     the session has no way to read a setting back - it stays on the argv path,
//     where its postcondition is evaluated against the device's own answer;
//   - an app launch is a package-manager operation rather than pointer input, and
//     the session's control socket carries no launch.
//
// Both remain fully dispatchable; they simply do not travel a mirror.
func mirrorInputKinds(kind action.Kind) bool {
	switch kind {
	case action.Tap, action.Swipe, action.TextInput, action.KeyEvent:
		return true
	default:
		return false
	}
}

// carriesThroughMirror reports whether THIS dispatch travels the device's live
// session: the kind has to be one a session can carry, a delivery has to be
// bound, and the device has to have a session right now. A device with no live
// session is not mirrored, and its input travels the argv path as before.
func (a *inputAdapter) carriesThroughMirror(kind action.Kind) bool {
	if a == nil || a.mirror == nil || !mirrorInputKinds(kind) {
		return false
	}
	return a.mirror.Mirrored(a.deviceID)
}

// deliverToMirror carries one authorized typed input to the device's live
// session. It is reached only after the kernel authorized the attempt and after
// the render-space cross-check above; the session independently refuses a
// coordinate whose frame is not the frame it is streaming, so a dispatched input
// satisfies both gates.
//
// A failure here is this boundary's failure, classified from the error's own
// code and never from its text: the class a render-space refusal keeps is the
// render-space one, so an operator is not told a tap failed at the transport
// when in fact no tap ever reached a device.
func (a *inputAdapter) deliverToMirror(ctx context.Context, intent action.Intent, payload InputPayload, kind action.Kind) error {
	deviceID := a.deviceID
	if deviceID == "" {
		return platformerrors.New(platformerrors.CodeUnavailable, "the live session cannot be addressed: this adapter was bound to no device")
	}
	if kind == action.TextInput {
		return a.deliverTextToMirror(ctx, intent, payload)
	}
	delivery := MirrorDeliveryInput{DeviceID: deviceID, Kind: kind, ObservationToken: intent.ObservationToken}
	switch kind {
	case action.Tap:
		delivery.Point, delivery.Frame = payload.Tap.Point, payload.Tap.Space
	case action.Swipe:
		delivery.Point, delivery.End = payload.Swipe.Start, payload.Swipe.End
		delivery.DurationMS, delivery.Frame = payload.Swipe.DurationMS, payload.Swipe.Space
	case action.KeyEvent:
		delivery.KeyCode, delivery.Repeat = payload.KeyEvent.KeyCode, payload.KeyEvent.Repeat
	default:
		return platformerrors.New(platformerrors.CodeInvalidInput, "device input kind has no live-session delivery")
	}
	callCtx, cancel := context.WithTimeout(ctx, a.inputTimeout(intent))
	defer cancel()
	if err := a.mirror.DeliverInput(callCtx, delivery); err != nil {
		if platformerrors.CodeOf(err) != platformerrors.CodeInternal {
			return err
		}
		return platformerrors.Wrap(platformerrors.CodeUnavailable, "device input did not reach the device's live session", errors.New(string(kind)+" did not reach the live session"))
	}
	return nil
}

// deliverTextToMirror releases a typed-text reference and hands the value
// straight to the session. The value is never returned, stored, rendered or
// logged, and every refusal names the opaque handle or a fixed reason instead:
// the released value exists only as the argument of one call.
func (a *inputAdapter) deliverTextToMirror(ctx context.Context, intent action.Intent, payload InputPayload) error {
	if payload.Text == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text input requires its reference")
	}
	if strings.TrimSpace(intent.Workspace) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "typed text must name the workspace its reference belongs to")
	}
	if err := validateTextReference(*payload.Text); err != nil {
		return err
	}
	if a.resolver == nil {
		return platformerrors.New(platformerrors.CodeUnavailable, "typed text has no reference resolver")
	}
	value, err := a.resolver.Resolve(ctx, intent.Workspace, *payload.Text)
	if err != nil {
		// The resolver's own error is deliberately dropped: it may quote the
		// value, and this error is rendered into evidence and logs.
		return textReferenceFailure(platformerrors.CodeUnavailable, payload.Text.Handle, textReferenceUnreleased)
	}
	if ctx.Err() != nil {
		return inputContextError(ctx)
	}
	if uint32(len(value)) != payload.Text.Length {
		return textReferenceFailure(platformerrors.CodeInvalidInput, payload.Text.Handle, textReferenceLengthMismatch)
	}
	callCtx, cancel := context.WithTimeout(ctx, a.inputTimeout(intent))
	defer cancel()
	if err := a.mirror.DeliverText(callCtx, a.deviceID, RenderSpace{}, value); err != nil {
		// The session's own error is dropped for the same reason the resolver's
		// is: a session that failed to encode a typed value can quote it.
		return textReferenceFailure(platformerrors.CodeUnavailable, payload.Text.Handle, textReferenceUnreleased)
	}
	return nil
}

// inputTimeout bounds one live-session delivery at the intent's own bound. The
// default applies to an intent that carries none, so a delivery cannot be held
// open by a session that stopped answering.
func (a *inputAdapter) inputTimeout(intent action.Intent) time.Duration {
	if intent.Timeout > 0 {
		return intent.Timeout
	}
	return DefaultInputTimeout
}

// inputOptions binds the boundary for one dispatch. A coordinate-bearing kind
// must be able to establish the size the device presents at: when the
// render-size source cannot be built, the input is refused here rather than
// being dispatched with no way to check its frame.
func (a *inputAdapter) inputOptions(intent action.Intent, kind action.Kind) ([]InputOption, error) {
	options := []InputOption{WithInputTimeout(intent.Timeout)}
	if a.departures != nil {
		// A reboot's departure is observed through the port, and a deployment
		// that bound one has it available to every dispatch that needs it.
		options = append(options, WithDepartureObserver(a.departures))
	}
	if strings.TrimSpace(a.transferRoot) != "" {
		options = append(options, WithTransferRoot(a.transferRoot))
	}
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
// into, and the actor the attempt runs on behalf of. A payload is single-use:
// Execute consumes it.
func (a *inputAdapter) bind(attemptID string, payload InputPayload, sink *attemptReportSink, actorType, actorID string) {
	if a == nil || attemptID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[attemptID] = boundAttempt{payload: payload, sink: sink, actorType: actorType, actorID: actorID}
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
	reached := false
	defer func() {
		if reached || (counted != nil && counted.attempted()) {
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
	readback, readBack, runErr := SettingReadback{}, false, error(nil)
	operation := OperationReadback{}
	operationRead := false
	if a.carriesThroughMirror(kind) {
		// The device has a live session, so this is the delivery. It is chosen
		// after the render-space cross-check above and after the kernel
		// authorized the attempt, and it is never fallen back from: an input
		// the session did not carry is refused rather than re-sent down the
		// argv path, because sending the same tap twice to a device is worse
		// than a tap that visibly did not land.
		runErr = a.deliverToMirror(ctx, intent, payload, kind)
		reached = runErr == nil
	} else if carriesOperationReadback(kind) {
		// A catalogued device OPERATION runs its own argument array and reads
		// its own postcondition back off the device: a departure for a reboot,
		// the device's own secure default for a keyboard switch, a size for a
		// file, a code path for a package. There is nothing for the observation
		// port to add to any of them.
		operation, runErr = a.runOperation(ctx, inputs, bound, payload, kind, intent.Workspace)
		operationRead = true
	} else {
		readback, readBack, runErr = runInput(ctx, inputs, payload, kind, intent.Workspace)
	}
	if runErr != nil {
		if operationRead {
			// The operation took a reading before it failed, and that reading
			// is what the failure is ABOUT: a device that listed no keyboard
			// to switch to, a size the device reported that does not match
			// what was sent, a file it answered with fewer bytes than it said
			// it held. Publishing it lets the row an operator reads state what
			// the DEVICE answered rather than only that the call did not
			// complete - and a reading that holds nothing is not published as
			// an observation, so "the device answered and it does not hold" and
			// "nothing was read" stay distinguishable.
			if operation.Read() {
				report.observation = PostconditionObservation{OperationReadback: &operation, Token: operation.Token()}
				report.observed = true
			}
		}
		// The typed payload and the transport's own diagnostics are never
		// echoed: a failing device command can quote what it was given.
		return adapter.Execution{}, &adapter.ExecutionError{
			Cause:        runErr,
			Dispatched:   counted.attempted(),
			FailureClass: failureClassFor(runErr),
		}
	}
	if operationRead {
		// A catalogued device operation read its own postcondition back off the
		// device, exactly as a settings operation does. The reading names
		// itself, because the kernel requires a completion to identify the
		// observation it was evaluated against and for an operation the
		// read-back IS that observation.
		//
		// A reboot that the transport never saw depart, a keyboard switch the
		// device's own default does not show, a file the device does not report
		// at the size that was sent and a package with no code path all fail
		// HERE, on the device's own answer, rather than on a command that
		// exited zero.
		token := operation.Token()
		observation := PostconditionObservation{OperationReadback: &operation, Token: token}
		report.observation = observation
		report.observed = true
		postcondition, failure, outcome := evaluatePostcondition(spec, intent, payload, observation)
		return adapter.Execution{
			Outcome:          outcome,
			Postcondition:    postcondition,
			FailureClass:     failure,
			Dispatched:       true,
			ObservationToken: token,
		}, nil
	}
	if readBack {
		// A catalogued settings operation read its own postcondition back off
		// the device. There is nothing for the observation port to add, and that
		// reading is what the action catalog's declared postcondition is
		// evaluated against - so a setting that was written and did not read back
		// as required fails here rather than being reported as applied.
		//
		// The reading names itself, because the kernel requires a completion to
		// identify the observation it was evaluated against and for a settings
		// operation the read-back IS that observation.
		token := readback.Token()
		observation := PostconditionObservation{SettingReadback: &readback, Token: token}
		report.observation = observation
		report.observed = true
		postcondition, failure, outcome := evaluatePostcondition(spec, intent, payload, observation)
		return adapter.Execution{
			Outcome:          outcome,
			Postcondition:    postcondition,
			FailureClass:     failure,
			Dispatched:       true,
			ObservationToken: token,
		}, nil
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
//
// The second return reports that the primitive read its own postcondition back
// off the device and hands that reading to the caller. The catalogued settings
// operations are the kinds that do: their postcondition is a device SETTING, so
// the read-back is part of the operation and there is nothing for the
// observation port to add. Every other kind returns false and is observed
// through the port as before.
func runInput(ctx context.Context, inputs *Inputs, payload InputPayload, kind action.Kind, workspace string) (SettingReadback, bool, error) {
	switch kind {
	case action.Tap:
		return SettingReadback{}, false, inputs.Tap(ctx, *payload.Tap)
	case action.Swipe:
		return SettingReadback{}, false, inputs.Swipe(ctx, *payload.Swipe)
	case action.TextInput:
		return SettingReadback{}, false, inputs.TypeText(ctx, TypeTextRequest{Text: *payload.Text, Workspace: workspace})
	case action.KeyEvent:
		return SettingReadback{}, false, inputs.KeyEvent(ctx, *payload.KeyEvent)
	case action.LaunchApp:
		return SettingReadback{}, false, inputs.LaunchApp(ctx, *payload.Launch)
	case action.RotationLock:
		readback, err := inputs.ApplyRotationLock(ctx)
		return readback, true, err
	case action.AutofillOff:
		readback, err := inputs.ApplyAutofillOff(ctx)
		return readback, true, err
	default:
		return SettingReadback{}, false, platformerrors.New(platformerrors.CodeInvalidInput, "device input kind is not dispatchable")
	}
}

// carriesOperationReadback reports whether a kind is a catalogued device
// OPERATION that reads its own postcondition back off the device rather than
// being observed through the observation port.
//
// The list is closed on purpose. An operation's postcondition is a device fact
// this boundary reads (a departure, the secure default input method, a file's
// size, a package's code path), so a kind that needs one and is missing from
// this list would fall through to the observation port and be evaluated against
// an observation that says nothing about it.
func carriesOperationReadback(kind action.Kind) bool {
	switch kind {
	case action.Reboot, action.KeyboardSwitch, action.ImportFile, action.ExportFile, action.InstallApk, action.AdvancedCommand:
		return true
	default:
		return false
	}
}

// DeviceFileArchive stores the bytes one export pulled off a device as an
// artifact this workspace holds.
//
// It is the write side of the two artifact ports an operation needs, and it is
// deliberately narrow: it takes bytes this boundary read off a device plus a
// bounded file name, and it answers the artifact's identity. It cannot read an
// artifact, so an export cannot be turned into an import, and it cannot be
// asked for anything by path.
type DeviceFileArchive interface {
	StorePulledFile(ctx context.Context, workspace, mediaType, fileName string, payload []byte, actorType, actorID string) (string, error)
}

// runOperation runs one catalogued device operation and answers the read-back it
// took. Every branch dispatches a fixed argument array built by the operation's
// own builder; none of them is reachable with caller-authored command text.
func (a *inputAdapter) runOperation(ctx context.Context, inputs *Inputs, bound boundAttempt, payload InputPayload, kind action.Kind, workspace string) (OperationReadback, error) {
	switch kind {
	case action.Reboot:
		return inputs.Reboot(ctx, 0)
	case action.KeyboardSwitch:
		return inputs.SwitchKeyboard(ctx)
	case action.ImportFile:
		if payload.Import == nil {
			return OperationReadback{}, platformerrors.New(platformerrors.CodeInvalidInput, "a file import requires its payload")
		}
		return inputs.ImportFile(ctx, *payload.Import)
	case action.InstallApk:
		if payload.Install == nil {
			return OperationReadback{}, platformerrors.New(platformerrors.CodeInvalidInput, "a package install requires its payload")
		}
		return inputs.InstallPackage(ctx, *payload.Install)
	case action.ExportFile:
		if payload.Export == nil {
			return OperationReadback{}, platformerrors.New(platformerrors.CodeInvalidInput, "a file export requires its payload")
		}
		readback, pulled, err := inputs.ExportFile(ctx, *payload.Export)
		if err != nil {
			return readback, err
		}
		if a.archive == nil {
			// The bytes are in hand and there is nowhere to keep them: the
			// export is refused rather than reported as an artifact this
			// workspace does not hold.
			return readback, platformerrors.New(platformerrors.CodeUnavailable, "this deployment has no artifact store, so a pulled file cannot be kept")
		}
		mediaType := payload.Export.MediaType
		if strings.TrimSpace(mediaType) == "" {
			mediaType = "application/octet-stream"
		}
		artifactID, err := a.archive.StorePulledFile(ctx, workspace, mediaType, payload.Export.FileName, pulled, bound.actorType, bound.actorID)
		if err != nil {
			return readback, err
		}
		readback.ArtifactID = artifactID
		return readback, nil
	case action.AdvancedCommand:
		if payload.Advanced == nil {
			return OperationReadback{}, platformerrors.New(platformerrors.CodeInvalidInput, "an advanced command requires its payload")
		}
		return inputs.AdvancedAnswer(ctx, *payload.Advanced)
	default:
		return OperationReadback{}, platformerrors.New(platformerrors.CodeInvalidInput, "device operation kind is not dispatchable")
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
