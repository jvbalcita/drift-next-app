package lab

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	// DefaultCaptureTimeout bounds one observation when the caller supplies no
	// shorter deadline.
	DefaultCaptureTimeout = 30 * time.Second

	// MaxCaptureTimeout is the longest deadline an operator may request.
	MaxCaptureTimeout = 2 * time.Minute

	// MaxScreenshotPreviewBytes caps an opt-in inline preview. A capture larger
	// than this is reported as truncated with no preview rather than as a
	// partial image.
	MaxScreenshotPreviewBytes = 32768

	// DefaultEventBuffer bounds the retained audit ring buffer.
	DefaultEventBuffer = 256

	// ScreenshotMediaType is the only media type this slice observes.
	ScreenshotMediaType = "image/png"

	// EnvLabMode is the explicit operator opt-in for real-device lab mode.
	EnvLabMode = "DRIFT_P13_LAB_MODE"

	// EnvADBPath is the absolute adb executable path used in lab mode.
	EnvADBPath = "DRIFT_P13_ADB_PATH"

	// EnvLabToken is the shared secret a local caller must present to reach the
	// lab adapter route. Lab mode requires it; mock mode may omit it.
	EnvLabToken     = "DRIFT_P13_LAB_TOKEN"
	EnvRuntimeMode  = "DRIFT_RUNTIME_DEVICE_MODE"
	EnvRuntimeADB   = "DRIFT_RUNTIME_ADB_PATH"
	EnvRuntimeToken = "DRIFT_RUNTIME_SERVICE_TOKEN"

	maxRetainedOutcomes    = 64
	maxSummaryLength       = 512
	maxOperatorIDLength    = 128
	maxTargetSerialBytes   = 256
	maxIdempotencyKeyBytes = 128
	maxCorrelationIDBytes  = 128
)

var (
	// ErrAdaptersRequired reports a lab-mode construction without both a device
	// runner and a hierarchy observer.
	ErrAdaptersRequired = errors.New("lab mode requires both a device runner and a hierarchy observer")

	// ErrOperatorRequired reports a missing operator identity on an operation
	// that must be attributable.
	ErrOperatorRequired = errors.New("lab operations require an operator identity")
)

// DeviceRunner is the narrow read-only ADB surface this service needs.
// *adb.Adapter satisfies it; tests supply a deterministic fake.
type DeviceRunner interface {
	Version() string
	ValidateSerial(serial string) error
	PlatformToolsVersion(ctx context.Context) (string, error)
	Enumerate(ctx context.Context) ([]adb.DiscoveredDevice, error)
	Health(ctx context.Context, serial string) (adb.HealthReport, error)
	Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error)

	// ReattachReadOnly re-reads transport state after an observed transport
	// identity change. It is read-only and single-use per observed change, so
	// the service cannot turn a flapping transport into a reconnect loop.
	ReattachReadOnly(ctx context.Context, serial string, previousTransportID string) (adb.DiscoveredDevice, error)
}

// HierarchyObserver captures one bounded view hierarchy.
// *uiautomator.Adapter satisfies it.
type HierarchyObserver interface {
	Capture(ctx context.Context, serial string) (uiautomator.HierarchyCapture, error)
}

// EvidencePersister stores screenshot and UI-tree bytes through the artifact
// service only. Capture success must not depend on persistence; failures are
// isolated from command and workflow outcomes.
type EvidencePersister interface {
	PersistScreenshot(ctx context.Context, workspace, ownerID, actorID string, png []byte, contentHash string) (artifactID string, err error)
	PersistUITree(ctx context.Context, workspace, ownerID, actorID string, sanitizedJSON []byte) (artifactID string, err error)
}

// Authorizer decides whether an operator may perform one lab action. The
// service asks before every capture, so authorization cannot be skipped by a
// transport caller and is never inherited from an earlier call.
type Authorizer interface {
	Authorize(ctx context.Context, operatorID string, action Action) error
}

// OperatorRequired is the default policy: every action must carry a bounded,
// sanitized operator identity.
type OperatorRequired struct{}

// Authorize accepts any attributable operator.
func (OperatorRequired) Authorize(_ context.Context, operatorID string, _ Action) error {
	if err := validateBoundedText(operatorID, maxOperatorIDLength); err != nil {
		return fmt.Errorf("%w: %s", ErrOperatorRequired, err)
	}
	return nil
}

// captureOutcome records what happened for one idempotency key. An unresolved
// outcome is never replayed automatically: the operator must decide.
type captureOutcome struct {
	resolved bool
	bundle   ObservationBundle
	class    domain.FailureClass
	reason   string
}

// Service is the typed lab adapter boundary. It holds no target: a device is
// named inside one capture call, and the service never writes a device into the
// control-plane registry.
type Service struct {
	mode           Mode
	devices        DeviceRunner
	hierarchy      HierarchyObserver
	authorizer     Authorizer
	clock          clock.Clock
	ids            ids.IDGenerator
	captureTimeout time.Duration
	previewLimit   int
	eventLimit     int
	evidence       EvidencePersister

	// captureMu serializes observation so one device is never observed by two
	// concurrent captures. It is local serialization only, not ownership.
	captureMu sync.Mutex

	mu           sync.Mutex
	state        Status
	events       []Event
	outcomes     map[string]captureOutcome
	outcomeOrder []string

	// lastTransportID is the mutable transport identity observed for the most
	// recently resolved target. It is never identity and never a target: it
	// exists only so a transport change between two captures of the same named
	// device can be detected and reconciled once.
	lastTransportID string
}

// Option configures a Service at construction time.
type Option func(*Service) error

// WithClock replaces the audit clock.
func WithClock(source clock.Clock) Option {
	return func(s *Service) error {
		if source == nil {
			return errors.New("lab clock is required")
		}
		s.clock = source
		return nil
	}
}

// WithIDGenerator replaces the correlation-identifier source.
func WithIDGenerator(generator ids.IDGenerator) Option {
	return func(s *Service) error {
		if generator == nil {
			return errors.New("lab ID generator is required")
		}
		s.ids = generator
		return nil
	}
}

// WithAuthorizer replaces the default operator policy.
func WithAuthorizer(authorizer Authorizer) Option {
	return func(s *Service) error {
		if authorizer == nil {
			return errors.New("lab authorizer is required")
		}
		s.authorizer = authorizer
		return nil
	}
}

// WithLabAdapters is the explicit opt-in to real-device mode. Mock mode is the
// default precisely so no caller reaches a device by omission.
func WithLabAdapters(devices DeviceRunner, hierarchy HierarchyObserver) Option {
	return func(s *Service) error {
		if devices == nil || hierarchy == nil {
			return ErrAdaptersRequired
		}
		s.devices = devices
		s.hierarchy = hierarchy
		s.mode = ModeLab
		return nil
	}
}

// WithMockCandidates replaces the deterministic mock attached-device fixtures.
// It has no effect once WithLabAdapters has bound real adapters.
func WithMockCandidates(candidates ...adb.DiscoveredDevice) Option {
	return func(s *Service) error {
		if s.mode == ModeLab {
			return errors.New("mock candidates cannot be combined with lab adapters")
		}
		mock := newMockAdapter(candidates)
		s.devices = mock
		s.hierarchy = mock
		return nil
	}
}

// WithCaptureTimeout bounds one observation.
func WithCaptureTimeout(timeout time.Duration) Option {
	return func(s *Service) error {
		if timeout <= 0 || timeout > MaxCaptureTimeout {
			return fmt.Errorf("lab capture timeout must be positive and at most %s", MaxCaptureTimeout)
		}
		s.captureTimeout = timeout
		return nil
	}
}

// WithScreenshotPreview enables a bounded inline preview. A limit of zero, the
// default, disables previews entirely.
func WithScreenshotPreview(limitBytes int) Option {
	return func(s *Service) error {
		if limitBytes < 0 || limitBytes > MaxScreenshotPreviewBytes {
			return fmt.Errorf("lab screenshot preview limit must be between 0 and %d bytes", MaxScreenshotPreviewBytes)
		}
		s.previewLimit = limitBytes
		return nil
	}
}

// WithEventBuffer bounds the retained audit ring buffer.
func WithEventBuffer(size int) Option {
	return func(s *Service) error {
		if size <= 0 {
			return errors.New("lab event buffer must be positive")
		}
		s.eventLimit = size
		return nil
	}
}

// WithEvidencePersister routes capture screenshots/UI trees through the
// artifact service. Persistence failures never fail the observation itself.
func WithEvidencePersister(persister EvidencePersister) Option {
	return func(s *Service) error {
		s.evidence = persister
		return nil
	}
}

// NewService returns a lab service. It defaults to deterministic mock mode with
// no real runner, so constructing a service never touches a device.
func NewService(opts ...Option) (*Service, error) {
	mock := newMockAdapter(nil)
	service := &Service{
		mode:           ModeMock,
		devices:        mock,
		hierarchy:      mock,
		authorizer:     OperatorRequired{},
		clock:          clock.System{},
		ids:            ids.NewRandom(),
		captureTimeout: DefaultCaptureTimeout,
		eventLimit:     DefaultEventBuffer,
		outcomes:       make(map[string]captureOutcome),
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("lab option is required")
		}
		if err := opt(service); err != nil {
			return nil, err
		}
	}
	service.state = Status{
		Mode:           service.mode,
		Readiness:      ReadinessUnavailable,
		AdapterVersion: service.devices.Version(),
	}
	return service, nil
}

// Status returns a snapshot of the boundary. It performs no device work.
func (s *Service) Status(_ context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Events returns the retained audit records, oldest first.
func (s *Service) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// Discover enumerates the attached candidate transports that back the canonical
// scan. It only enumerates: it never confirms a target, never registers a
// canonical device, and exposes no operator-facing lifecycle. The Network
// Profile scan is the single discovery path; this is its transport source.
func (s *Service) Discover(ctx context.Context, operatorID string) (Status, error) {
	if err := s.authorize(ctx, operatorID, ActionDiscover); err != nil {
		return s.Status(ctx), err
	}

	correlationID, err := s.newCorrelationID("")
	if err != nil {
		return s.Status(ctx), err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.captureTimeout)
	defer cancel()

	candidates, enumerateErr := s.devices.Enumerate(callCtx)
	if enumerateErr != nil {
		class := adb.FailureClassOf(enumerateErr)
		s.recordEnumerationFailure(correlationID, class, enumerateErr)
		return s.Status(ctx), classifiedError(class, "lab adapter could not enumerate devices", enumerateErr)
	}

	version, versionErr := s.devices.PlatformToolsVersion(callCtx)

	s.mu.Lock()
	s.state.CorrelationID = correlationID
	s.state.Discovered = append([]adb.DiscoveredDevice(nil), candidates...)
	if versionErr == nil {
		s.state.PlatformToolsVersion = version
	}
	s.state.FailureClass = ""
	if versionErr != nil {
		s.state.FailureClass = domain.FailureObservation
	}
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventAdapterReadiness,
		CorrelationID: correlationID,
		OccurredAt:    s.clock.Now(),
		FailureClass:  s.state.FailureClass,
		Summary:       fmt.Sprintf("enumerated %d attached candidate transports in %s mode; none were registered", len(candidates), s.mode),
	})
	status := s.snapshotLocked()
	s.mu.Unlock()

	return status, nil
}

// CaptureObservation performs one bounded read-only observation of the device
// named in this call: health, then a screenshot, then a view-hierarchy dump.
//
// The serial is the whole of the operator's intent. Authorization resolves that
// exact name against the currently attached transports on every call, so no
// session state, list order, display name, address, or row position can select
// a device, and nothing is registered or confirmed.
//
// A timeout after a command may already have been dispatched yields an
// indeterminate outcome. The idempotency key is then recorded as unresolved and
// is never replayed automatically.
func (s *Service) CaptureObservation(ctx context.Context, request CaptureRequest) (ObservationBundle, error) {
	if err := s.authorize(ctx, request.OperatorID, ActionCapture); err != nil {
		return ObservationBundle{}, err
	}
	if err := validateBoundedText(request.IdempotencyKey, maxIdempotencyKeyBytes); err != nil {
		return ObservationBundle{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab capture requires a bounded idempotency key", err)
	}
	if request.Timeout < 0 || request.Timeout > MaxCaptureTimeout {
		return ObservationBundle{}, platformerrors.New(platformerrors.CodeInvalidInput, "lab capture timeout is out of range")
	}
	if err := validateBoundedText(request.Serial, maxTargetSerialBytes); err != nil {
		return ObservationBundle{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab capture requires an explicitly named target serial", err)
	}
	if err := s.devices.ValidateSerial(request.Serial); err != nil {
		return ObservationBundle{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab target serial is not a valid device serial", err)
	}

	correlationID, err := s.newCorrelationID(request.CorrelationID)
	if err != nil {
		return ObservationBundle{}, err
	}

	s.captureMu.Lock()
	defer s.captureMu.Unlock()

	// A repeated idempotency key is answered before any device work: a resolved
	// key returns its recorded bundle, an unresolved one is refused.
	if bundle, replayErr, decided := s.replayOutcome(request.Serial, request.IdempotencyKey); decided {
		return bundle, replayErr
	}

	timeout := s.captureTimeout
	if request.Timeout > 0 {
		timeout = request.Timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target, stableIdentity, previousTransportID, err := s.resolveTarget(callCtx, correlationID, request.Serial)
	if err != nil {
		return ObservationBundle{}, err
	}

	run := &captureRun{
		service:             s,
		correlationID:       correlationID,
		serial:              target.Serial,
		stableIdentity:      stableIdentity,
		previousTransportID: previousTransportID,
		request:             request,
		parent:              ctx,
	}
	bundle, captureErr := run.execute(callCtx)
	s.commitOutcome(request.IdempotencyKey, bundle, captureErr)
	s.commitStatus(bundle)
	return bundle, captureErr
}

// resolveTarget authorizes one capture against the device named in the request.
// It always re-enumerates: a cached list is not evidence that the named target
// is still attached, and it never falls back to a heuristic when the name is
// absent, ambiguous, or unusable.
func (s *Service) resolveTarget(ctx context.Context, correlationID, serial string) (adb.DiscoveredDevice, string, string, error) {
	candidates, enumerateErr := s.devices.Enumerate(ctx)
	if enumerateErr != nil {
		class := adb.FailureClassOf(enumerateErr)
		s.recordEnumerationFailure(correlationID, class, enumerateErr)
		return adb.DiscoveredDevice{}, "", "", classifiedError(class, "lab adapter could not enumerate attached devices", enumerateErr)
	}

	s.mu.Lock()
	s.state.Discovered = append([]adb.DiscoveredDevice(nil), candidates...)
	s.mu.Unlock()

	target, rejection := matchAttachedTarget(candidates, serial)
	if rejection != nil {
		s.recordTargetRejection(correlationID, serial, rejection, candidates)
		return adb.DiscoveredDevice{}, "", "", rejection.platformError()
	}

	s.mu.Lock()
	previousTransportID := s.lastTransportID
	s.state.ConnectionState = string(target.State)
	s.state.ConnectionType = target.ConnectionType
	s.state.CorrelationID = correlationID
	s.state.FailureClass = ""
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventAdapterReadiness,
		CorrelationID: correlationID,
		Serial:        target.Serial,
		OccurredAt:    s.clock.Now(),
		Summary:       fmt.Sprintf("resolved the explicitly named target among %d attached transports; no device was registered or confirmed", len(candidates)),
	})
	s.mu.Unlock()

	return target, StableIdentityPrefix + target.Serial, previousTransportID, nil
}

// replayOutcome answers a repeated idempotency key without dispatching work. A
// completed key returns its stored bundle; a key whose outcome is unknown is
// refused so no observation is silently retried.
func (s *Service) replayOutcome(serial, key string) (ObservationBundle, error, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	outcome, ok := s.outcomes[key]
	if !ok {
		return ObservationBundle{}, nil, false
	}
	if outcome.resolved {
		return outcome.bundle, nil, true
	}
	return ObservationBundle{Serial: serial, IdempotencyKey: key, FailureClass: outcome.class, Indeterminate: true},
		platformerrors.New(platformerrors.CodeIndeterminateCompletion, "a previous capture with this idempotency key ended with an unknown outcome and is never replayed automatically"),
		true
}

func (s *Service) commitOutcome(key string, bundle ObservationBundle, captureErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	outcome := captureOutcome{resolved: captureErr == nil, bundle: bundle, class: bundle.FailureClass}
	if bundle.Indeterminate {
		outcome.resolved = false
		outcome.reason = "capture outcome is unknown"
	}
	if _, exists := s.outcomes[key]; !exists {
		s.outcomeOrder = append(s.outcomeOrder, key)
		if len(s.outcomeOrder) > maxRetainedOutcomes {
			delete(s.outcomes, s.outcomeOrder[0])
			s.outcomeOrder = s.outcomeOrder[1:]
		}
	}
	s.outcomes[key] = outcome
}

func (s *Service) commitStatus(bundle ObservationBundle) {
	s.mu.Lock()
	defer s.mu.Unlock()

	observedAt := bundle.CapturedAt
	s.state.CorrelationID = bundle.CorrelationID
	s.state.FailureClass = bundle.FailureClass
	// An unknown outcome is sticky. Only a capture whose postcondition actually
	// verified may resolve it: a later determinate failure says nothing about
	// whether the earlier command reached the device.
	s.state.Indeterminate = bundle.Indeterminate || (s.state.Indeterminate && !bundle.PostconditionVerified)
	s.state.ObservationLatencyMs = bundle.LatencyMs
	if bundle.PlatformToolsVersion != "" {
		s.state.PlatformToolsVersion = bundle.PlatformToolsVersion
	}
	if bundle.ScreenshotHash != "" {
		s.state.LastScreenshotHash = bundle.ScreenshotHash
	}
	if bundle.HierarchySummary != "" {
		s.state.LastHierarchySummary = bundle.HierarchySummary
	}
	if bundle.HealthState != "" && !observedAt.IsZero() {
		healthAt := observedAt
		s.state.LastHealthAt = &healthAt
	}
	if bundle.PostconditionVerified && !observedAt.IsZero() {
		capturedAt := observedAt
		s.state.LastObservationAt = &capturedAt
	}
	s.refreshReadinessLocked()
}

// refreshReadinessLocked derives readiness from observed facts only.
func (s *Service) refreshReadinessLocked() {
	switch {
	case s.state.Indeterminate:
		s.state.Readiness = ReadinessIndeterminate
	case s.devices == nil:
		s.state.Readiness = ReadinessUnavailable
	case s.state.FailureClass == domain.FailureInfrastructure:
		s.state.Readiness = ReadinessUnavailable
	case len(s.state.Discovered) == 0:
		s.state.Readiness = ReadinessUnavailable
	case s.state.ConnectionState != "" && !adb.DeviceAuthState(s.state.ConnectionState).Usable():
		s.state.Readiness = ReadinessBlocked
	default:
		s.state.Readiness = ReadinessReady
	}
}

func (s *Service) snapshotLocked() Status {
	status := s.state
	status.Mode = s.mode
	status.Discovered = append([]adb.DiscoveredDevice(nil), s.state.Discovered...)
	if s.state.LastHealthAt != nil {
		healthAt := *s.state.LastHealthAt
		status.LastHealthAt = &healthAt
	}
	if s.state.LastObservationAt != nil {
		observedAt := *s.state.LastObservationAt
		status.LastObservationAt = &observedAt
	}
	return status
}

func (s *Service) appendEventLocked(event Event) Event {
	event.Summary = boundedSummary(event.Summary)
	s.events = append(s.events, event)
	if len(s.events) > s.eventLimit {
		s.events = s.events[len(s.events)-s.eventLimit:]
	}
	return event
}

func (s *Service) recordEnumerationFailure(correlationID string, class domain.FailureClass, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CorrelationID = correlationID
	s.state.FailureClass = class
	s.state.Discovered = nil
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventAdapterReadiness,
		CorrelationID: correlationID,
		OccurredAt:    s.clock.Now(),
		FailureClass:  class,
		Summary:       "adapter enumeration failed: " + safeDetail(cause),
	})
}

func (s *Service) recordTargetRejection(correlationID, serial string, rejection *targetRejection, candidates []adb.DiscoveredDevice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CorrelationID = correlationID
	s.state.FailureClass = rejection.class
	if rejection.class == domain.FailureTransport {
		// The device is attached but unusable, so its transport facts are still
		// observed evidence and readiness stays honest about them.
		for _, candidate := range candidates {
			if candidate.Serial == serial {
				s.state.ConnectionState = string(candidate.State)
				s.state.ConnectionType = candidate.ConnectionType
			}
		}
	}
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventObservationCapture,
		CorrelationID: correlationID,
		Serial:        serial,
		OccurredAt:    s.clock.Now(),
		FailureClass:  rejection.class,
		Summary:       "capture refused: " + rejection.message,
	})
}

// observeTransportID records the transport identity observed for the target of
// the capture that just ran. It is mutable observation state, never identity.
func (s *Service) observeTransportID(transportID string) {
	if transportID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTransportID = transportID
}

func (s *Service) authorize(ctx context.Context, operatorID string, action Action) error {
	if s.authorizer == nil {
		return platformerrors.New(platformerrors.CodePolicyDenied, "lab authorization is not configured")
	}
	if err := s.authorizer.Authorize(ctx, operatorID, action); err != nil {
		return platformerrors.Wrap(platformerrors.CodePolicyDenied, "operator is not permitted to perform this lab action", err)
	}
	return nil
}

func (s *Service) newCorrelationID(requested string) (string, error) {
	if requested != "" {
		if err := validateBoundedText(requested, maxCorrelationIDBytes); err != nil {
			return "", platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab correlation ID must be bounded and sanitized", err)
		}
		return requested, nil
	}
	generated, err := s.ids.NewID()
	if err != nil {
		return "", platformerrors.Wrap(platformerrors.CodeInternal, "lab correlation ID could not be generated", err)
	}
	return generated, nil
}

// targetRejection is a refused target resolution with both an audit
// classification and a safe operator-facing message.
type targetRejection struct {
	class   domain.FailureClass
	code    platformerrors.Code
	message string
}

func (r *targetRejection) Error() string { return r.message }

func (r *targetRejection) platformError() error {
	return platformerrors.New(r.code, r.message)
}

// matchAttachedTarget resolves an explicitly named serial to exactly one
// attached, usable device. Nothing is inferred from order, name, address, or
// row position.
func matchAttachedTarget(candidates []adb.DiscoveredDevice, serial string) (adb.DiscoveredDevice, *targetRejection) {
	matches := make([]adb.DiscoveredDevice, 0, 1)
	for _, candidate := range candidates {
		if candidate.Serial == serial {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return adb.DiscoveredDevice{}, &targetRejection{
			class:   domain.FailureDeviceOffline,
			code:    platformerrors.CodePreconditionFailed,
			message: "the named serial is not among the currently attached devices",
		}
	case 1:
	default:
		return adb.DiscoveredDevice{}, &targetRejection{
			class:   domain.FailureAmbiguousTarget,
			code:    platformerrors.CodeAmbiguousTarget,
			message: "the named serial matched more than one attached device",
		}
	}

	target := matches[0]
	if !target.State.Usable() {
		return adb.DiscoveredDevice{}, &targetRejection{
			class:   domain.FailureTransport,
			code:    platformerrors.CodePreconditionFailed,
			message: "the named device is attached but its transport state is not usable",
		}
	}
	return target, nil
}

// classifiedError maps a device failure class to a stable application code.
func classifiedError(class domain.FailureClass, message string, cause error) error {
	code := platformerrors.CodeInternal
	switch class {
	case domain.FailureTimeout:
		code = platformerrors.CodeTimeout
	case domain.FailureOperatorCancelled:
		code = platformerrors.CodeCanceled
	case domain.FailureIndeterminate:
		code = platformerrors.CodeIndeterminateCompletion
	case domain.FailureInfrastructure:
		code = platformerrors.CodeUnavailable
	case domain.FailureDeviceOffline, domain.FailureTransport, domain.FailureAgentUnhealthy:
		code = platformerrors.CodePreconditionFailed
	case domain.FailureCapabilityMismatch:
		code = platformerrors.CodeCapabilityMismatch
	case domain.FailureAmbiguousTarget:
		code = platformerrors.CodeAmbiguousTarget
	case domain.FailureStaleObservation:
		code = platformerrors.CodeStaleObservation
	case domain.FailureObservation, domain.FailureUnknownScreen, domain.FailurePostcondition:
		code = platformerrors.CodePostconditionFailed
	case domain.FailurePolicyDenied:
		code = platformerrors.CodePolicyDenied
	case domain.FailureCleanupFailed:
		code = platformerrors.CodeCleanupFailed
	case domain.FailureLeaseConflict:
		code = platformerrors.CodeLeaseConflict
	case domain.FailureInvalidTransition:
		code = platformerrors.CodeConflict
	}
	if cause == nil {
		return platformerrors.New(code, message)
	}
	return platformerrors.Wrap(code, message, cause)
}

// validateBoundedText rejects empty, padded, over-long, or credential-bearing
// operator input before it reaches an adapter or an audit record.
func validateBoundedText(value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value is required")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("value has surrounding whitespace")
	}
	if len(value) > limit {
		return fmt.Errorf("value exceeds %d bytes", limit)
	}
	if redaction.RedactString(value) != value {
		return errors.New("value contains sensitive material")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("value contains a control character")
		}
	}
	return nil
}

func boundedSummary(summary string) string {
	sanitized := redaction.RedactString(strings.TrimSpace(summary))
	if len(sanitized) > maxSummaryLength {
		sanitized = sanitized[:maxSummaryLength] + "…[truncated]"
	}
	return sanitized
}

// safeDetail renders a diagnostic cause as bounded, redacted prose.
func safeDetail(err error) string {
	if err == nil {
		return ""
	}
	return boundedSummary(adb.RedactOutput([]byte(err.Error())))
}

// encodePreview returns a bounded base64 preview. A payload larger than the
// configured cap yields no preview and a truncation flag, so a caller never
// receives a partial image it might treat as the observation.
func encodePreview(payload []byte, limit int) (string, bool) {
	if limit <= 0 || len(payload) == 0 {
		return "", false
	}
	if len(payload) > limit {
		return "", true
	}
	return base64.StdEncoding.EncodeToString(payload), false
}
