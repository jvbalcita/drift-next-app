// Device input dispatch: the kernel wiring for the five typed device inputs.
//
// This file binds the five typed inputs (tap, swipe, typed text by reference,
// key event and app launch) to the P7 control kernel. It does not decide
// anything the kernel already decides: authorization, leases, fencing tokens,
// idempotency, the policy and capability decision and the emergency stop belong
// to `ActionService`, and this boundary adds exactly two things.
//
//   - A read-only readiness probe that runs before dispatch, so the cases the
//     kernel cannot name are refused with their own reason and with no device
//     call: an offline transport, an unauthorized transport, and the several
//     distinct ways a control tuple can be unusable.
//   - A typed refusal that keeps those cases apart. Every refusal carries its
//     own reason, its own stable code and its own failure classification, and
//     none of them is reported as a generic internal error.
//
// The payload is typed and bound to one attempt for the duration of one
// dispatch. There is no shell, no exec, no argv, no command string and no free
// form payload anywhere in this file: what reaches the device is the argument
// array the five narrow builders in device_input.go produced, and only after
// the kernel authorized the attempt.
package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/actors"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/runner"
	"drift.local/drift-next/internal/leases"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/sessions"
	store "drift.local/drift-next/internal/store/sqlite"
)

// --- the typed payload ------------------------------------------------------

// InputPayload is the typed payload of exactly one device input kind. Exactly
// one member must be present, and the member must match the kind being
// authorized: a payload that is absent, that names two kinds, or that is
// incomplete is refused, so no mutating input can ever be authorized on a zero
// value.
type InputPayload struct {
	Tap      *TapRequest
	Swipe    *SwipeRequest
	Text     *TextReference
	KeyEvent *KeyEventRequest
	Launch   *LaunchAppRequest
}

// Kind reports the single kind this payload addresses, after refusing an
// absent, doubled or incomplete payload.
func (p InputPayload) Kind() (action.Kind, error) {
	members := 0
	kind := action.Kind("")
	if p.Tap != nil {
		kind, members = action.Tap, members+1
	}
	if p.Swipe != nil {
		kind, members = action.Swipe, members+1
	}
	if p.Text != nil {
		kind, members = action.TextInput, members+1
	}
	if p.KeyEvent != nil {
		kind, members = action.KeyEvent, members+1
	}
	if p.Launch != nil {
		kind, members = action.LaunchApp, members+1
	}
	switch {
	case members == 0:
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "a device input requires exactly one typed payload")
	case members > 1:
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "a device input payload names more than one kind")
	}
	if err := p.validate(); err != nil {
		return "", err
	}
	return kind, nil
}

// observationToken names the observation a coordinate-bearing payload was
// measured from. It is empty for an input that carries no coordinate.
func (p InputPayload) observationToken() string {
	switch {
	case p.Tap != nil:
		return p.Tap.Space.ObservationToken
	case p.Swipe != nil:
		return p.Swipe.Space.ObservationToken
	default:
		return ""
	}
}

// renderSpace reports the render frame a coordinate-bearing payload declares.
// An input that carries no coordinate has none, and the render-space cross-check
// does not apply to it.
func (p InputPayload) renderSpace() (RenderSpace, bool) {
	switch {
	case p.Tap != nil:
		return p.Tap.Space, true
	case p.Swipe != nil:
		return p.Swipe.Space, true
	default:
		return RenderSpace{}, false
	}
}

func (p InputPayload) validate() error {
	switch {
	case p.Tap != nil:
		return validateRenderPoint(p.Tap.Point, p.Tap.Space)
	case p.Swipe != nil:
		if p.Swipe.DurationMS == 0 || p.Swipe.DurationMS > maxInputSwipeDurationMS {
			return platformerrors.New(platformerrors.CodeInvalidInput, "a swipe duration must be between 1 and 300000 milliseconds")
		}
		if err := validateRenderPoint(p.Swipe.Start, p.Swipe.Space); err != nil {
			return err
		}
		return validateRenderPoint(p.Swipe.End, p.Swipe.Space)
	case p.Text != nil:
		return validateTextReference(*p.Text)
	case p.KeyEvent != nil:
		if p.KeyEvent.KeyCode == 0 || p.KeyEvent.KeyCode > maxInputKeyCode {
			return platformerrors.New(platformerrors.CodeInvalidInput, "a key event requires a bounded key code")
		}
		if p.KeyEvent.Repeat == 0 || p.KeyEvent.Repeat > maxInputKeyRepeat {
			return platformerrors.New(platformerrors.CodeInvalidInput, "a key event requires a repeat count between 1 and 32")
		}
		return nil
	case p.Launch != nil:
		if err := validatePackageName(p.Launch.PackageName); err != nil {
			return err
		}
		if p.Launch.ActivityName == "" {
			return nil
		}
		return validateActivityName(p.Launch.ActivityName)
	default:
		return platformerrors.New(platformerrors.CodeInvalidInput, "a device input requires exactly one typed payload")
	}
}

// --- typed refusals ---------------------------------------------------------

// RefusalReason is this boundary's stable identity for a refused device input.
// It is deliberately finer grained than the kernel's error vocabulary: the
// kernel reports one lease/fence conflict, while an operator needs to know
// whether the lease expired, the token was fenced, the control session ended or
// the transport is unusable.
type RefusalReason string

const (
	RefusalLeaseMissing            RefusalReason = "lease_missing"
	RefusalLeaseExpired            RefusalReason = "lease_expired"
	RefusalLeaseNotHeld            RefusalReason = "lease_not_held"
	RefusalFenceStale              RefusalReason = "fence_stale"
	RefusalNoControlSession        RefusalReason = "no_control_session"
	RefusalLeaseConflict           RefusalReason = "lease_conflict"
	RefusalEmergencyStop           RefusalReason = "emergency_stop"
	RefusalPolicyDenied            RefusalReason = "policy_denied"
	RefusalCapabilityMismatch      RefusalReason = "capability_mismatch"
	RefusalDeviceOffline           RefusalReason = "device_offline"
	RefusalDeviceUnauthorized      RefusalReason = "device_unauthorized"
	RefusalDeviceUnavailable       RefusalReason = "device_unavailable"
	RefusalDuplicateIdempotencyKey RefusalReason = "duplicate_idempotency_key"
)

// refusalDefinition is the single reviewable place where a refusal reason is
// bound to its stable code and its failure classification. Two reasons may
// share a code (the published vocabulary has one lease/fence conflict), but no
// two reasons share a reason or a message, and none of them is `internal`.
type refusalDefinition struct {
	code    platformerrors.Code
	failure domain.FailureClass
	message string
}

var refusalDefinitions = map[RefusalReason]refusalDefinition{
	RefusalLeaseMissing:            {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "a device input requires an active device lease"},
	RefusalLeaseExpired:            {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "the device lease has expired"},
	RefusalLeaseNotHeld:            {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "the device lease is held by another controller"},
	RefusalFenceStale:              {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "the fencing token is stale or revoked"},
	RefusalNoControlSession:        {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "the device input requires an active control session"},
	RefusalLeaseConflict:           {platformerrors.CodeLeaseConflict, domain.FailureLeaseConflict, "the control tuple was refused after the readiness probe"},
	RefusalEmergencyStop:           {platformerrors.CodeEmergencyStopped, domain.FailureOperatorCancelled, "emergency stop is engaged"},
	RefusalPolicyDenied:            {platformerrors.CodePolicyDenied, domain.FailurePolicyDenied, "policy denies this device input"},
	RefusalCapabilityMismatch:      {platformerrors.CodeCapabilityMismatch, domain.FailureCapabilityMismatch, "the device input requires an unavailable capability"},
	RefusalDeviceOffline:           {platformerrors.CodeUnavailable, domain.FailureDeviceOffline, "the device is offline"},
	RefusalDeviceUnauthorized:      {platformerrors.CodeUnavailable, domain.FailureTransport, "the device is unauthorized"},
	RefusalDeviceUnavailable:       {platformerrors.CodeUnavailable, domain.FailureCapabilityMismatch, "the device transport is not usable"},
	RefusalDuplicateIdempotencyKey: {platformerrors.CodeConflict, domain.FailureInvalidTransition, "the idempotency key was reused with a different request"},
}

// Refusal is a refused device input: what was refused, under which stable code,
// and with which failure classification. It stays in the classified error chain
// so an operator surface resolves the code rather than guessing at it.
type Refusal struct {
	Reason       RefusalReason
	Code         platformerrors.Code
	FailureClass domain.FailureClass
	Message      string

	classified *platformerrors.Error
}

func refusalFor(reason RefusalReason, cause error) *Refusal {
	definition, ok := refusalDefinitions[reason]
	if !ok {
		definition = refusalDefinition{
			code:    platformerrors.CodeUnavailable,
			failure: domain.FailureCapabilityMismatch,
			message: "the device input was refused",
		}
	}
	var classified *platformerrors.Error
	if cause == nil {
		classified = platformerrors.New(definition.code, definition.message)
	} else {
		classified = platformerrors.Wrap(definition.code, definition.message, cause)
	}
	return &Refusal{Reason: reason, Code: definition.code, FailureClass: definition.failure, Message: definition.message, classified: classified}
}

func (r *Refusal) Error() string {
	if r == nil {
		return "<nil>"
	}
	return string(r.Reason) + ": " + r.Message
}

// Unwrap keeps the classified platform error in the chain, so CodeOf resolves
// this refusal's stable code instead of failing closed to `internal`.
func (r *Refusal) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.classified
}

// RefusalOf extracts a typed refusal from an error chain.
func RefusalOf(err error) (*Refusal, bool) {
	var refusal *Refusal
	if err != nil && errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}

// classifyAuthorizeRefusal names the refusals the authorize step can produce.
// At that step a conflict is the idempotency guard and nothing else: the two
// conflict sources in `ActionService.Authorize` are both "this key was reused
// with a different request".
func classifyAuthorizeRefusal(err error) error {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodeEmergencyStopped:
		return refusalFor(RefusalEmergencyStop, err)
	case platformerrors.CodePolicyDenied:
		return refusalFor(RefusalPolicyDenied, err)
	case platformerrors.CodeCapabilityMismatch:
		return refusalFor(RefusalCapabilityMismatch, err)
	case platformerrors.CodeConflict:
		return refusalFor(RefusalDuplicateIdempotencyKey, err)
	case platformerrors.CodeLeaseConflict:
		return refusalFor(RefusalLeaseConflict, err)
	default:
		return err
	}
}

// classifyDispatchRefusal names the refusals the dispatch step can produce.
// Everything else the dispatch step reports is already classified by the
// kernel and is passed through unchanged.
func classifyDispatchRefusal(err error) error {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodeEmergencyStopped:
		return refusalFor(RefusalEmergencyStop, err)
	case platformerrors.CodeLeaseConflict:
		return refusalFor(RefusalLeaseConflict, err)
	default:
		return err
	}
}

// --- the kernel and readiness surfaces --------------------------------------

// ControlPlane is the control-kernel surface a device input needs. It is the
// whole P7 contract: authorize, dispatch, complete or reconcile the outcome,
// time out, cancel and clean up. `*store.ActionService` satisfies it.
type ControlPlane interface {
	Authorize(context.Context, action.Intent, string, string) (action.Result, error)
	Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Complete(context.Context, action.Completion, string, string) (action.Result, error)
	MarkIndeterminate(context.Context, action.Completion, string, string) (action.Result, error)
	Timeout(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Cancel(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	Cleanup(context.Context, string, string, string, string, bool) (action.Result, error)
}

// ControlProbe reports whether a device input may be attempted at all. It runs
// before authorization and answers with an empty reason when the tuple is
// ready. It is read-only and it never touches the device input transport: a
// probe that refuses guarantees no input reaches a device.
type ControlProbe interface {
	ProbeControl(context.Context, InputRequest) (RefusalReason, error)
}

// DeviceTransportObserver reports the transport-level authorization state of one
// explicitly named serial. It is read-only observation, never stable device
// identity.
type DeviceTransportObserver interface {
	TransportState(context.Context, string) (adb.DeviceAuthState, error)
}

// DeviceTransportObserverFunc adapts a function to DeviceTransportObserver.
type DeviceTransportObserverFunc func(context.Context, string) (adb.DeviceAuthState, error)

func (f DeviceTransportObserverFunc) TransportState(ctx context.Context, serial string) (adb.DeviceAuthState, error) {
	return f(ctx, serial)
}

// PostconditionObservation is a fresh read-only observation taken after the
// input reached the device. It is what the catalog's declared postcondition is
// evaluated against; it carries no typed-text content, only the addressed
// field's length.
type PostconditionObservation struct {
	// Token is the freshness token of this observation. A token equal to the
	// one the action was resolved against means nothing changed, which is not
	// evidence that the postcondition holds.
	Token string
	// ForegroundPackage is the package currently in the foreground, for a kind
	// whose postcondition names a package.
	ForegroundPackage string
	// FieldLength is the character count of the addressed field's content, for
	// a kind whose postcondition names a referenced field. It is a count, never
	// the value.
	FieldLength int
	// FailureClass is the classification of a failed observation.
	FailureClass domain.FailureClass
	// Partial reports an observation that could not be completed.
	Partial bool
}

// PostconditionObserver takes the fresh observation a postcondition is
// evaluated against. It is the read side of the slice; it never injects input.
type PostconditionObserver interface {
	ObservePostcondition(context.Context, action.Intent, InputPayload) (PostconditionObservation, error)
}

// PostconditionObserverFunc adapts a function to PostconditionObserver.
type PostconditionObserverFunc func(context.Context, action.Intent, InputPayload) (PostconditionObservation, error)

func (f PostconditionObserverFunc) ObservePostcondition(ctx context.Context, intent action.Intent, payload InputPayload) (PostconditionObservation, error) {
	return f(ctx, intent, payload)
}

// --- the request ------------------------------------------------------------

// InputRequest is one caller's typed device input. The control metadata names
// the attempt, the device, the transport serial, the lease tuple the kernel
// must validate, the idempotency key and the observation the input was resolved
// against. The payload is the typed input itself.
type InputRequest struct {
	IntentID          string
	Workspace         string
	DeviceID          string
	Serial            string
	LeaseID           string
	HolderID          string
	FencingToken      uint64
	IdempotencyKey    string
	ObservationToken  string
	InvocationSurface action.InvocationSurface
	ApprovalGranted   bool
	Timeout           time.Duration
	Target            action.SemanticTarget
	Payload           InputPayload
}

// --- the dispatcher ---------------------------------------------------------

// RenderSizeSourceFactory builds the source of one device's actual render size
// from the narrow transport that device is reached through. It is the seam
// between the two gates: the dispatcher authorizes and dispatches, and the
// render-space cross-check asks the device what size it presents at before a
// coordinate is sent.
//
// The factory receives the device's own unfiltered transport, never a counting
// wrapper around it: a render-size read is a read-only precondition check, not
// the input itself, and must not be counted as one.
type RenderSizeSourceFactory func(transport InputTransport, serial string) (RenderSizeSource, error)

// DefaultRenderSizeSource is the composition this boundary dispatches with: the
// real `WmSizeReader` over the same narrow transport the input primitives use,
// so the render-size read goes through the ADB allow-list like every other
// device call.
//
// Until the allow-list admits that read (card ARC-75) the real transport
// refuses `shell wm size` with `adb.ErrArgvNotAllowlisted`, the reader reports
// the device render size as unavailable, and a coordinate-bearing input is
// refused. That is the required fail-closed behaviour of this composition, not
// a fallback to another frame.
func DefaultRenderSizeSource(transport InputTransport, serial string) (RenderSizeSource, error) {
	return NewWmSizeReader(transport, serial)
}

// DispatcherOption configures the dispatcher at construction time.
type DispatcherOption func(*InputDispatcher) error

// WithRenderSizeSourceFactory replaces how the dispatcher resolves the render
// size a device presents at. It exists so a test can state that size directly,
// and so the production composition stays one reviewable function.
func WithRenderSizeSourceFactory(factory RenderSizeSourceFactory) DispatcherOption {
	return func(dispatcher *InputDispatcher) error {
		if factory == nil {
			return errors.New("a device render-size source factory is required")
		}
		dispatcher.renderSizes = factory
		return nil
	}
}

// InputDispatcher runs one typed device input through the whole P7 contract. It
// owns one serialized actor per device, so two inputs for the same device never
// run concurrently, and it holds a typed payload only for the duration of the
// dispatch that bound it.
type InputDispatcher struct {
	control   ControlPlane
	probe     ControlProbe
	observer  PostconditionObserver
	transport InputTransport
	resolver  TextResolver

	// renderSizes is how a coordinate-bearing dispatch learns the size the
	// device actually presents at. It defaults to the real reader over the same
	// narrow transport; a coordinate is refused when no source can be built.
	renderSizes RenderSizeSourceFactory

	mu      sync.Mutex
	devices map[string]*deviceInput
}

type deviceInput struct {
	serial  string
	actor   *actors.Actor
	adapter *inputAdapter
}

// NewInputDispatcher binds the dispatcher to the control kernel, the readiness
// probe, the postcondition observer and the narrow device transport. A nil
// resolver is permitted: typed text then fails closed until a resolver exists.
//
// The render-size source defaults to `DefaultRenderSizeSource`, so a
// coordinate-bearing input is cross-checked against the device by the real
// reader unless a caller replaces the seam explicitly.
func NewInputDispatcher(control ControlPlane, probe ControlProbe, observer PostconditionObserver, transport InputTransport, resolver TextResolver, options ...DispatcherOption) (*InputDispatcher, error) {
	if control == nil || probe == nil || observer == nil || transport == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "device input dispatch requires a control kernel, a readiness probe, a postcondition observer and a transport")
	}
	dispatcher := &InputDispatcher{control: control, probe: probe, observer: observer, transport: transport, resolver: resolver, renderSizes: DefaultRenderSizeSource, devices: make(map[string]*deviceInput)}
	for _, option := range options {
		if option == nil {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a device input dispatcher option is required")
		}
		if err := option(dispatcher); err != nil {
			return nil, err
		}
	}
	return dispatcher, nil
}

// Close releases every per-device actor this dispatcher owns.
func (d *InputDispatcher) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var first error
	for deviceID, bound := range d.devices {
		if err := bound.actor.Close(); err != nil && first == nil {
			first = err
		}
		delete(d.devices, deviceID)
	}
	return first
}

// Run performs one typed device input. The order is the contract:
//
//  1. the typed payload is validated and turned into a kernel intent;
//  2. a read-only readiness probe refuses a tuple the probe can prove unusable,
//     before anything is written and before any device call;
//  3. the kernel authorizes, which validates the intent, the lease tuple, the
//     policy and capability decision, the control session, the emergency stop
//     and the idempotency key;
//  4. a duplicate delivery returns the recorded result instead of acting again;
//  5. the kernel dispatches, and only then does the device receive the typed
//     input, serialized per device;
//  6. the declared postcondition is observed and evaluated;
//  7. the kernel records the completion (or the indeterminate outcome), and the
//     cleanup is recorded separately, so a failed cleanup stays visible.
func (d *InputDispatcher) Run(ctx context.Context, request InputRequest, actorType, actorID string) (action.Result, error) {
	if d == nil || d.control == nil || ctx == nil {
		return action.Result{}, platformerrors.New(platformerrors.CodeInvalidInput, "context and device input dispatcher are required")
	}
	intent, err := d.intentFor(request)
	if err != nil {
		return action.Result{}, err
	}
	reason, probeErr := d.probe.ProbeControl(ctx, request)
	if probeErr != nil {
		return action.Result{}, probeErr
	}
	if reason != "" {
		return action.Result{}, refusalFor(reason, nil)
	}
	authorized, err := d.control.Authorize(ctx, intent, actorType, actorID)
	if err != nil {
		return action.Result{}, classifyAuthorizeRefusal(err)
	}
	// A duplicate delivery returns what the first delivery recorded. An attempt
	// that is still merely authorized was never dispatched, so it continues.
	if authorized.IdempotentReplay && authorized.Attempt.State != action.AttemptAuthorized {
		return authorized, nil
	}
	device, err := d.deviceFor(request.DeviceID, request.Serial)
	if err != nil {
		return action.Result{}, err
	}
	device.adapter.bind(intent.ID, request.Payload)
	defer device.adapter.release(intent.ID)
	result, runErr := runner.New(d.control, device.actor).Run(ctx, intent, actorType, actorID)
	if runErr != nil {
		return result, classifyDispatchRefusal(runErr)
	}
	return result, nil
}

func (d *InputDispatcher) deviceFor(deviceID, serial string) (*deviceInput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if bound, ok := d.devices[deviceID]; ok {
		if bound.serial == serial {
			return bound, nil
		}
		_ = bound.actor.Close()
		delete(d.devices, deviceID)
	}
	boundAdapter := newInputAdapter(d.transport, d.resolver, d.observer, serial, d.renderSizes)
	actor, err := actors.New(deviceID, boundAdapter, 8)
	if err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInternal, "create device input actor", err)
	}
	bound := &deviceInput{serial: serial, actor: actor, adapter: boundAdapter}
	d.devices[deviceID] = bound
	return bound, nil
}

// intentFor turns one typed request into the kernel's intent, so the kernel
// validates the same facts the input boundary will execute. It refuses a
// payload whose coordinate frame names another observation instead of scaling.
func (d *InputDispatcher) intentFor(request InputRequest) (action.Intent, error) {
	kind, err := request.Payload.Kind()
	if err != nil {
		return action.Intent{}, err
	}
	spec, ok := action.Lookup(kind)
	if !ok {
		return action.Intent{}, platformerrors.New(platformerrors.CodeInvalidInput, "device input kind is not a complete action catalog entry")
	}
	if err := adb.ValidateSerial(request.Serial); err != nil {
		return action.Intent{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "device input serial is invalid", err)
	}
	if token := request.Payload.observationToken(); token != "" && token != request.ObservationToken {
		return action.Intent{}, platformerrors.New(platformerrors.CodeInvalidInput, "a device coordinate frame must name the observation the action was resolved against")
	}
	intent := action.Intent{
		ID:                request.IntentID,
		Workspace:         request.Workspace,
		DeviceID:          request.DeviceID,
		LeaseID:           request.LeaseID,
		HolderID:          request.HolderID,
		FencingToken:      request.FencingToken,
		Kind:              kind,
		Target:            request.Target,
		IdempotencyKey:    request.IdempotencyKey,
		ObservationToken:  request.ObservationToken,
		InvocationSurface: request.InvocationSurface,
		Capabilities:      append([]action.Capability(nil), spec.RequiredCapabilities...),
		ApprovalGranted:   request.ApprovalGranted,
		Timeout:           request.Timeout,
	}
	switch kind {
	case action.Tap:
		intent.CoordinateFallback = &action.CoordinateFallback{Start: renderCoordinate(request.Payload.Tap.Point, request.Payload.Tap.Space), Confirmed: true}
	case action.Swipe:
		intent.Gesture = &action.GesturePath{
			Points:     []action.Coordinate{renderCoordinate(request.Payload.Swipe.Start, request.Payload.Swipe.Space), renderCoordinate(request.Payload.Swipe.End, request.Payload.Swipe.Space)},
			DurationMs: int64(request.Payload.Swipe.DurationMS),
		}
	case action.TextInput:
		intent.ValueLength = int(request.Payload.Text.Length)
	case action.KeyEvent:
		intent.KeyCode = int(request.Payload.KeyEvent.KeyCode)
	}
	if err := intent.Validate(); err != nil {
		return action.Intent{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "device input intent is invalid", err)
	}
	return intent, nil
}

// renderCoordinate places a point in the render frame that travelled with it.
// The label follows the observation convention and names the render size, never
// a physical panel size and never a downscaled frame.
func renderCoordinate(point Point, space RenderSpace) action.Coordinate {
	return action.Coordinate{Space: fmt.Sprintf("display:%dx%d", space.Width, space.Height), X: int(point.X), Y: int(point.Y)}
}

// --- the store-backed readiness probe ---------------------------------------

// StoreControlProbe answers the readiness question from the control plane's own
// persisted state plus a read-only transport observation. It is a second,
// fail-closed gate: the kernel still validates the same tuple before dispatch,
// and this probe never replaces that validation.
type StoreControlProbe struct {
	leases   *store.LeaseService
	sessions *store.SessionService
	devices  DeviceTransportObserver
	clock    clock.Clock
}

// NewStoreControlProbe returns a probe over one SQLite store. The transport
// observer may be nil, in which case a device-scoped refusal is reported rather
// than a device being assumed usable.
func NewStoreControlProbe(db *store.DB, devices DeviceTransportObserver) *StoreControlProbe {
	if db == nil {
		return nil
	}
	return &StoreControlProbe{
		leases:   store.NewLeaseService(db, 0),
		sessions: store.NewSessionService(db, 0),
		devices:  devices,
		clock:    db.Clock(),
	}
}

// ProbeControl reports why this device input cannot be attempted, or an empty
// reason when it may. It reads; it never writes and never reaches the input
// transport.
func (p *StoreControlProbe) ProbeControl(ctx context.Context, request InputRequest) (RefusalReason, error) {
	if p == nil || p.leases == nil || p.sessions == nil {
		return "", platformerrors.New(platformerrors.CodeUnavailable, "control readiness probe is not configured")
	}
	workspace := organizations.WorkspaceID(request.Workspace)
	lease, err := p.leases.Get(ctx, workspace, leases.DeviceLeaseID(request.LeaseID))
	if err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodeNotFound {
			return RefusalLeaseMissing, nil
		}
		return "", err
	}
	if lease.HolderID != request.HolderID {
		return RefusalLeaseNotHeld, nil
	}
	now := p.clock.Now().UTC()
	if lease.State != leases.LeaseActive || !now.Before(lease.ExpiresAt) {
		return RefusalLeaseExpired, nil
	}
	if lease.FencingToken != request.FencingToken {
		return RefusalFenceStale, nil
	}
	if string(lease.DeviceID) != request.DeviceID {
		return RefusalLeaseConflict, nil
	}
	session, err := p.sessions.Get(ctx, workspace, lease.SessionID)
	if err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodeNotFound {
			return RefusalNoControlSession, nil
		}
		return "", err
	}
	if session.State != sessions.Active || !now.Before(session.ExpiresAt) {
		return RefusalNoControlSession, nil
	}
	if p.devices == nil {
		return RefusalDeviceUnavailable, nil
	}
	state, err := p.devices.TransportState(ctx, request.Serial)
	if err != nil {
		return RefusalDeviceUnavailable, nil
	}
	switch state {
	case adb.StateDevice:
		return "", nil
	case adb.StateUnauthorized, adb.StateAuthorizing, adb.StateNoPermissions:
		return RefusalDeviceUnauthorized, nil
	case adb.StateOffline, adb.StateConnecting, adb.StateUnknown:
		return RefusalDeviceOffline, nil
	default:
		return RefusalDeviceUnavailable, nil
	}
}

// ADBTransportObserver reads the transport state of one serial from the
// allow-listed, read-only ADB enumeration. It issues no mutating command.
type ADBTransportObserver struct{ adapter *adb.Adapter }

func NewADBTransportObserver(adapter *adb.Adapter) *ADBTransportObserver {
	return &ADBTransportObserver{adapter: adapter}
}

// TransportState reports the observed state of one serial. A serial the host
// does not list is reported offline rather than assumed reachable.
func (o *ADBTransportObserver) TransportState(ctx context.Context, serial string) (adb.DeviceAuthState, error) {
	if o == nil || o.adapter == nil {
		return "", errors.New("adb transport observer is not configured")
	}
	devices, err := o.adapter.Enumerate(ctx)
	if err != nil {
		return "", err
	}
	for _, device := range devices {
		if device.Serial == serial {
			return device.State, nil
		}
	}
	return adb.StateOffline, nil
}

// ADBInputTransport adapts the allow-listed ADB runner to the device input
// transport port. It adds no surface of its own: the only operation it exposes
// is the one the five primitives build an argument array for, and the adapter
// underneath refuses every array that is not on its allow-list.
type ADBInputTransport struct{ adapter *adb.Adapter }

func NewADBInputTransport(adapter *adb.Adapter) *ADBInputTransport {
	return &ADBInputTransport{adapter: adapter}
}

// RunDeviceCommand runs one argument array the input primitives built. The
// array is not trusted: the ADB adapter re-derives whether it is allow-listed.
func (t *ADBInputTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	if t == nil || t.adapter == nil {
		return adb.Result{}, platformerrors.New(platformerrors.CodeUnavailable, "adb input transport is not configured")
	}
	return t.adapter.RunAllowlisted(ctx, serial, args)
}

// --- postcondition evaluation ----------------------------------------------

// evaluatePostcondition reads the entry's declared postcondition and decides
// whether the fresh observation satisfies it. It returns the state the kernel
// records, the failure classification, and the outcome, so a postcondition that
// did not hold can never be reported as success.
//
// The value behind a typed-text reference has no representation here: the
// referenced postcondition is evaluated against the addressed field's length,
// never against its content.
func evaluatePostcondition(spec action.Specification, intent action.Intent, payload InputPayload, observation PostconditionObservation) (action.PostconditionState, domain.FailureClass, action.Outcome) {
	if strings.TrimSpace(string(spec.Postcondition)) == "" {
		return action.PostconditionUnknown, domain.FailureInvalidTransition, action.OutcomeIndeterminate
	}
	if observation.FailureClass != "" || observation.Partial {
		return action.PostconditionUnknown, domain.FailureIndeterminate, action.OutcomeIndeterminate
	}
	if observation.Token == "" || observation.Token == intent.ObservationToken {
		// Nothing observable changed, or nothing was observed: the entry's
		// postcondition is not satisfied.
		return action.PostconditionFailed, domain.FailurePostcondition, action.OutcomeFailed
	}
	switch intent.Kind {
	case action.LaunchApp:
		if payload.Launch == nil || observation.ForegroundPackage != payload.Launch.PackageName {
			return action.PostconditionFailed, domain.FailurePostcondition, action.OutcomeFailed
		}
	case action.TextInput:
		if payload.Text == nil || observation.FieldLength != int(payload.Text.Length) {
			return action.PostconditionFailed, domain.FailurePostcondition, action.OutcomeFailed
		}
	}
	return action.PostconditionPassed, "", action.OutcomeVerified
}
