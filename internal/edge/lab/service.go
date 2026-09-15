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
	EnvLabToken = "DRIFT_P13_LAB_TOKEN"

	maxRetainedOutcomes    = 64
	maxSummaryLength       = 512
	maxOperatorIDLength    = 128
	maxDisplayNameLength   = 128
	maxReasonLength        = 512
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
// service always asks before touching an adapter, so authorization cannot be
// skipped by a transport caller.
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

// Service is the typed lab adapter boundary. A confirmed target lives only in
// this struct: the service never writes a device into the control-plane
// registry.
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

// WithMockCandidates replaces the deterministic mock discovery fixtures. It has
// no effect once WithLabAdapters has bound real adapters.
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

// Discover enumerates candidate transports. It only enumerates: it never
// confirms a target, and it never registers a canonical device.
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
		s.recordDiscoveryFailure(correlationID, class, enumerateErr)
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
		Summary:       fmt.Sprintf("enumerated %d candidate transports in %s mode; none were registered or confirmed", len(candidates), s.mode),
	})
	status := s.snapshotLocked()
	s.mu.Unlock()

	return status, nil
}

// ConfirmTarget binds one explicitly named candidate to this session.
//
// The serial must match an enumerated candidate exactly. When more than one
// candidate is attached, the confirmation text must be the serial itself: the
// generic CONFIRM phrase is refused so a target can never be inferred from
// list order, display name, address, or row position.
func (s *Service) ConfirmTarget(ctx context.Context, request ConfirmRequest) (Status, error) {
	if err := s.authorize(ctx, request.OperatorID, ActionConfirmTarget); err != nil {
		return s.Status(ctx), err
	}
	if err := s.devices.ValidateSerial(request.Serial); err != nil {
		return s.Status(ctx), platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab target serial is not a valid device serial", err)
	}
	if request.DisplayName != "" {
		if err := validateBoundedText(request.DisplayName, maxDisplayNameLength); err != nil {
			return s.Status(ctx), platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab target display name must be bounded and sanitized", err)
		}
	}
	// A confirmation is an auditable operator decision, so it must carry a
	// reason. An absent reason is refused rather than defaulted.
	if err := validateBoundedText(strings.TrimSpace(request.Reason), maxReasonLength); err != nil {
		return s.Status(ctx), platformerrors.Wrap(platformerrors.CodeInvalidInput, "lab confirmation requires a bounded, sanitized reason", err)
	}

	correlationID, err := s.newCorrelationID("")
	if err != nil {
		return s.Status(ctx), err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.captureTimeout)
	defer cancel()

	// Confirmation always re-enumerates: a cached list is not evidence that the
	// named target is still attached.
	candidates, enumerateErr := s.devices.Enumerate(callCtx)
	if enumerateErr != nil {
		class := adb.FailureClassOf(enumerateErr)
		s.recordDiscoveryFailure(correlationID, class, enumerateErr)
		return s.Status(ctx), classifiedError(class, "lab adapter could not enumerate devices", enumerateErr)
	}

	s.mu.Lock()
	s.state.Discovered = append([]adb.DiscoveredDevice(nil), candidates...)
	s.mu.Unlock()

	candidate, matchErr := matchCandidate(candidates, request)
	if matchErr != nil {
		s.recordConfirmationRejection(correlationID, request.Serial, matchErr)
		return s.Status(ctx), matchErr.platformError()
	}

	s.mu.Lock()
	s.appendEventLocked(Event{
		Name:          EventOperatorConfirmation,
		CorrelationID: correlationID,
		Serial:        candidate.Serial,
		OccurredAt:    s.clock.Now(),
		Summary:       fmt.Sprintf("operator %s typed a matching confirmation for %d attached candidates", redaction.RedactString(request.OperatorID), len(candidates)),
	})
	s.mu.Unlock()

	report, healthErr := s.devices.Health(callCtx, candidate.Serial)
	if healthErr != nil {
		class := adb.FailureClassOf(healthErr)
		s.recordConfirmationFailure(correlationID, candidate.Serial, class, "target health could not be observed")
		return s.Status(ctx), classifiedError(class, "lab target health could not be observed", healthErr)
	}
	if report.FailureClass != "" {
		s.recordConfirmationFailure(correlationID, candidate.Serial, report.FailureClass, "target is attached but not observable")
		return s.Status(ctx), classifiedError(report.FailureClass, "lab target is attached but not usable", nil)
	}

	observedAt := s.clock.Now()

	s.mu.Lock()
	s.state.ConfirmedSerial = candidate.Serial
	s.state.ConfirmedDisplayName = request.DisplayName
	s.state.StableIdentity = StableIdentityPrefix + candidate.Serial
	s.state.TransportID = transportIDOf(candidate, report)
	s.state.ConnectionState = string(candidate.State)
	s.state.ConnectionType = candidate.ConnectionType
	s.state.LastHealthAt = &observedAt
	s.state.CorrelationID = correlationID
	s.state.FailureClass = ""
	s.state.Indeterminate = false
	if report.PlatformToolsVersion != "" {
		s.state.PlatformToolsVersion = report.PlatformToolsVersion
	}
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventTargetConfirmation,
		CorrelationID: correlationID,
		Serial:        candidate.Serial,
		OccurredAt:    observedAt,
		Summary:       fmt.Sprintf("confirmed lab target %s as session identity %s; reason recorded; no canonical device was registered", candidate.Serial, s.state.StableIdentity),
	})
	status := s.snapshotLocked()
	s.mu.Unlock()

	return status, nil
}

// ClearTarget releases the confirmed target and resolves a stuck indeterminate
// readiness. Clearing is the operator's explicit acknowledgement that an
// unknown outcome has been reviewed.
func (s *Service) ClearTarget(ctx context.Context, operatorID string) (Status, error) {
	if err := s.authorize(ctx, operatorID, ActionClearTarget); err != nil {
		return s.Status(ctx), err
	}
	correlationID, err := s.newCorrelationID("")
	if err != nil {
		return s.Status(ctx), err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	released := s.state.ConfirmedSerial
	s.state.ConfirmedSerial = ""
	s.state.ConfirmedDisplayName = ""
	s.state.StableIdentity = ""
	s.state.TransportID = ""
	s.state.ConnectionState = ""
	s.state.ConnectionType = ""
	s.state.LastScreenshotHash = ""
	s.state.LastHierarchySummary = ""
	s.state.ObservationLatencyMs = 0
	s.state.LastObservationAt = nil
	s.state.LastHealthAt = nil
	s.state.Indeterminate = false
	s.state.FailureClass = ""
	s.state.CorrelationID = correlationID
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventCleanup,
		CorrelationID: correlationID,
		Serial:        released,
		OccurredAt:    s.clock.Now(),
		Summary:       fmt.Sprintf("operator %s released the confirmed lab target", redaction.RedactString(operatorID)),
	})
	return s.snapshotLocked(), nil
}

// CaptureObservation performs one bounded read-only observation: health, then a
// screenshot, then a view-hierarchy dump. It requires a confirmed target whose
// serial matches exactly.
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

	correlationID, err := s.newCorrelationID(request.CorrelationID)
	if err != nil {
		return ObservationBundle{}, err
	}

	s.captureMu.Lock()
	defer s.captureMu.Unlock()

	serial, stableIdentity, previousTransportID, err := s.authorizeCapture(request)
	if err != nil {
		return ObservationBundle{}, err
	}
	if bundle, replayErr, decided := s.replayOutcome(request.IdempotencyKey); decided {
		return bundle, replayErr
	}

	timeout := s.captureTimeout
	if request.Timeout > 0 {
		timeout = request.Timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := &captureRun{
		service:             s,
		correlationID:       correlationID,
		serial:              serial,
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

// authorizeCapture enforces the confirmed-target and readiness preconditions.
func (s *Service) authorizeCapture(request CaptureRequest) (serial string, stableIdentity string, transportID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.ConfirmedSerial == "" {
		return "", "", "", platformerrors.New(platformerrors.CodePreconditionFailed, "lab capture requires an explicitly confirmed target")
	}
	if request.Serial != s.state.ConfirmedSerial {
		return "", "", "", platformerrors.New(platformerrors.CodePreconditionFailed, "lab capture serial does not match the confirmed target")
	}
	switch s.state.Readiness {
	case ReadinessReady, ReadinessIndeterminate:
	case ReadinessUnavailable:
		return "", "", "", platformerrors.New(platformerrors.CodeUnavailable, "lab adapter is unavailable")
	case ReadinessBlocked, "":
		return "", "", "", platformerrors.New(platformerrors.CodePreconditionFailed, "lab adapter is not ready to observe")
	default:
		return "", "", "", platformerrors.New(platformerrors.CodePreconditionFailed, "lab adapter readiness is unknown")
	}
	return s.state.ConfirmedSerial, s.state.StableIdentity, s.state.TransportID, nil
}

// replayOutcome answers a repeated idempotency key without dispatching work. A
// completed key returns its stored bundle; a key whose outcome is unknown is
// refused so no observation is silently retried.
func (s *Service) replayOutcome(key string) (ObservationBundle, error, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	outcome, ok := s.outcomes[key]
	if !ok {
		return ObservationBundle{}, nil, false
	}
	if outcome.resolved {
		return outcome.bundle, nil, true
	}
	return ObservationBundle{Serial: s.state.ConfirmedSerial, IdempotencyKey: key, FailureClass: outcome.class, Indeterminate: true},
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
	// An unknown outcome is sticky. Only an explicit ClearTarget or a capture
	// whose postcondition actually verified may resolve it: a later determinate
	// failure says nothing about whether the earlier command reached the device.
	s.state.Indeterminate = bundle.Indeterminate || (s.state.Indeterminate && !bundle.PostconditionVerified)
	s.state.ObservationLatencyMs = bundle.LatencyMs
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
	case s.state.ConfirmedSerial == "":
		s.state.Readiness = ReadinessBlocked
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

func (s *Service) recordDiscoveryFailure(correlationID string, class domain.FailureClass, cause error) {
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

func (s *Service) recordConfirmationRejection(correlationID, serial string, rejection *confirmationRejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CorrelationID = correlationID
	s.state.FailureClass = rejection.class
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventTargetConfirmation,
		CorrelationID: correlationID,
		Serial:        serial,
		OccurredAt:    s.clock.Now(),
		FailureClass:  rejection.class,
		Summary:       "confirmation rejected: " + rejection.message,
	})
}

func (s *Service) recordConfirmationFailure(correlationID, serial string, class domain.FailureClass, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CorrelationID = correlationID
	s.state.FailureClass = class
	s.refreshReadinessLocked()
	s.appendEventLocked(Event{
		Name:          EventTargetConfirmation,
		CorrelationID: correlationID,
		Serial:        serial,
		OccurredAt:    s.clock.Now(),
		FailureClass:  class,
		Summary:       "confirmation rejected: " + summary,
	})
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

// confirmationRejection is a refused confirmation with both an audit
// classification and a safe operator-facing message.
type confirmationRejection struct {
	class   domain.FailureClass
	code    platformerrors.Code
	message string
}

func (r *confirmationRejection) Error() string { return r.message }

func (r *confirmationRejection) platformError() error {
	return platformerrors.New(r.code, r.message)
}

// matchCandidate resolves a confirmation request to exactly one enumerated
// candidate. Nothing is inferred from order, name, address, or row position.
func matchCandidate(candidates []adb.DiscoveredDevice, request ConfirmRequest) (adb.DiscoveredDevice, *confirmationRejection) {
	matches := make([]adb.DiscoveredDevice, 0, 1)
	for _, candidate := range candidates {
		if candidate.Serial == request.Serial {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return adb.DiscoveredDevice{}, &confirmationRejection{
			class:   domain.FailureDeviceOffline,
			code:    platformerrors.CodeNotFound,
			message: "the requested serial is not among the enumerated candidates",
		}
	case 1:
	default:
		return adb.DiscoveredDevice{}, &confirmationRejection{
			class:   domain.FailureAmbiguousTarget,
			code:    platformerrors.CodeAmbiguousTarget,
			message: "the requested serial matched more than one enumerated candidate",
		}
	}

	if len(candidates) > 1 && request.ConfirmationText != request.Serial {
		return adb.DiscoveredDevice{}, &confirmationRejection{
			class:   domain.FailureAmbiguousTarget,
			code:    platformerrors.CodeAmbiguousTarget,
			message: "more than one device is attached, so the confirmation text must be the exact serial",
		}
	}
	if request.ConfirmationText != request.Serial && request.ConfirmationText != ConfirmationLiteral {
		return adb.DiscoveredDevice{}, &confirmationRejection{
			class:   domain.FailurePolicyDenied,
			code:    platformerrors.CodeInvalidInput,
			message: "confirmation text must be the exact serial or the literal CONFIRM",
		}
	}

	candidate := matches[0]
	if !candidate.State.Usable() {
		return adb.DiscoveredDevice{}, &confirmationRejection{
			class:   domain.FailureTransport,
			code:    platformerrors.CodePreconditionFailed,
			message: "the requested candidate is attached but its transport state is not usable",
		}
	}
	return candidate, nil
}

func transportIDOf(candidate adb.DiscoveredDevice, report adb.HealthReport) string {
	if report.TransportID != "" {
		return report.TransportID
	}
	return candidate.TransportID
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
