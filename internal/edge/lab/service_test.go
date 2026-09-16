package lab_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const operator = "operator-1"

func fixedClock() clock.Clock {
	return clock.NewFixed(time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC))
}

func newMockService(t *testing.T, opts ...lab.Option) *lab.Service {
	t.Helper()
	service, err := lab.NewService(append([]lab.Option{lab.WithClock(fixedClock())}, opts...)...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func newLabService(t *testing.T, fake *fakeAdapter, opts ...lab.Option) *lab.Service {
	t.Helper()
	service, err := lab.NewService(append([]lab.Option{
		lab.WithLabAdapters(fake, fake),
		lab.WithClock(fixedClock()),
	}, opts...)...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// captureFor names the target device explicitly in the same call that observes
// it. There is no discovery or confirmation step in front of it any more.
func captureFor(t *testing.T, service *lab.Service, serial, idempotencyKey string) lab.ObservationBundle {
	t.Helper()
	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         serial,
		IdempotencyKey: idempotencyKey,
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation(%q) error = %v", serial, err)
	}
	return bundle
}

func hasEvent(events []lab.Event, name lab.EventName) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

// recordingAuthorizer observes what the service asked for, so per-call
// attribution can be asserted rather than assumed.
type recordingAuthorizer struct {
	calls []string
	deny  bool
}

func (a *recordingAuthorizer) Authorize(_ context.Context, operatorID string, action lab.Action) error {
	a.calls = append(a.calls, operatorID+"|"+string(action))
	if a.deny {
		return errors.New("operator is not permitted")
	}
	return nil
}

func TestNewServiceDefaultsToMockModeWithoutAnyDeviceWork(t *testing.T) {
	service := newMockService(t)

	status := service.Status(context.Background())
	if status.Mode != lab.ModeMock {
		t.Fatalf("Status().Mode = %q, want %q", status.Mode, lab.ModeMock)
	}
	if status.Readiness != lab.ReadinessUnavailable {
		t.Fatalf("Status().Readiness = %q, want %q", status.Readiness, lab.ReadinessUnavailable)
	}
	if status.AdapterVersion != lab.MockAdapterVersion {
		t.Fatalf("Status().AdapterVersion = %q, want %q", status.AdapterVersion, lab.MockAdapterVersion)
	}
	if len(status.Discovered) != 0 {
		t.Fatalf("Status() = %+v, want no discovery before a capture resolves a target", status)
	}
}

// The canonical Network Profile scan uses the adapter's read-only enumeration as
// its transport source. It enumerates and nothing else: no confirmation step
// exists and no canonical device is created.
func TestDiscoverEnumeratesWithoutConfirmingOrRegisteringAnything(t *testing.T) {
	service := newMockService(t)

	status, err := service.Discover(context.Background(), operator)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(status.Discovered) != len(lab.DefaultMockCandidates()) {
		t.Fatalf("Discover() discovered %d candidates, want %d", len(status.Discovered), len(lab.DefaultMockCandidates()))
	}
	if status.Readiness != lab.ReadinessReady {
		t.Fatalf("Discover() readiness = %q, want %q once the adapter has answered with attached candidates", status.Readiness, lab.ReadinessReady)
	}
	if !hasEvent(service.Events(), lab.EventAdapterReadiness) {
		t.Fatal("Discover() emitted no adapter_readiness event")
	}
}

func TestCaptureObservationRequiresAnAttributableOperator(t *testing.T) {
	service := newMockService(t)

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePolicyDenied)
	}
}

// Each capture is authorized on its own, with the operator and the action the
// call actually performs. Attribution is per call, not per session.
func TestCaptureObservationAuthorizesEveryCallByOperator(t *testing.T) {
	authorizer := &recordingAuthorizer{}
	service := newMockService(t, lab.WithAuthorizer(authorizer))

	for _, key := range []string{"key-1", "key-2"} {
		if _, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
			Serial:         "mock-device-alpha",
			IdempotencyKey: key,
			OperatorID:     operator,
		}); err != nil {
			t.Fatalf("CaptureObservation(%s) error = %v", key, err)
		}
	}
	if len(authorizer.calls) != 2 {
		t.Fatalf("authorizer calls = %v, want one authorization per capture", authorizer.calls)
	}
	for _, call := range authorizer.calls {
		if call != operator+"|"+string(lab.ActionCapture) {
			t.Fatalf("authorizer call = %q, want the per-call operator and capture action", call)
		}
	}
}

func TestCaptureObservationDeniesAnUnauthorizedOperator(t *testing.T) {
	service := newMockService(t, lab.WithAuthorizer(&recordingAuthorizer{deny: true}))

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePolicyDenied)
	}
}

// A capture that names no device is refused before any adapter is touched: the
// target is never inferred from ambient state or from list order.
func TestCaptureObservationRefusesAnUnnamedTargetWithoutTouchingTheAdapter(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)

	for name, serial := range map[string]string{"empty": "", "whitespace": "   "} {
		_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
			Serial:         serial,
			IdempotencyKey: "key-" + name,
			OperatorID:     operator,
		})
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Fatalf("CaptureObservation(%s serial) code = %v, want %v", name, platformerrors.CodeOf(err), platformerrors.CodeInvalidInput)
		}
	}
	if len(fake.recordedCalls()) != 0 {
		t.Fatalf("adapter calls = %v, want none for a capture that names no device", fake.recordedCalls())
	}
}

func TestCaptureObservationRefusesASerialThatIsNotAttached(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial02",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v; err=%v", platformerrors.CodeOf(err), platformerrors.CodePreconditionFailed, err)
	}
	if fake.callCount("health") != 0 || fake.callCount("screenshot") != 0 {
		t.Fatal("CaptureObservation() observed a device it had already refused")
	}
}

func TestCaptureObservationRefusesAnAmbiguousTarget(t *testing.T) {
	fake := newFakeAdapter(
		adb.DiscoveredDevice{Serial: "fakeserial01", State: adb.StateDevice, TransportID: "7", ConnectionType: adb.ConnectionUSB},
		adb.DiscoveredDevice{Serial: "fakeserial01", State: adb.StateDevice, TransportID: "8", ConnectionType: adb.ConnectionTCP},
	)
	service := newLabService(t, fake)

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeAmbiguousTarget {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeAmbiguousTarget)
	}
}

func TestCaptureObservationRefusesAnUnusableTarget(t *testing.T) {
	fake := newFakeAdapter(adb.DiscoveredDevice{
		Serial:         "fakeserial01",
		State:          adb.StateUnauthorized,
		TransportID:    "7",
		ConnectionType: adb.ConnectionUSB,
	})
	service := newLabService(t, fake)

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePreconditionFailed)
	}
	if fake.callCount("health") != 0 {
		t.Fatal("CaptureObservation() observed health for a target it had already refused")
	}
	if status := service.Status(context.Background()); status.Readiness != lab.ReadinessBlocked {
		t.Fatalf("Status().Readiness = %q, want %q for an attached but unusable target", status.Readiness, lab.ReadinessBlocked)
	}
}

func TestCaptureObservationRequiresABoundedIdempotencyKey(t *testing.T) {
	service := newMockService(t)

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:     "mock-device-alpha",
		OperatorID: operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeInvalidInput)
	}
}

// The stable lab identity is derived from the explicitly named serial, and it is
// never a mutable transport fact.
func TestCaptureObservationBindsAStableIdentityThatIsNotTheTransportIdentity(t *testing.T) {
	service := newMockService(t)
	bundle := captureFor(t, service, "mock-device-beta", "key-1")

	if bundle.StableIdentity != lab.StableIdentityPrefix+"mock-device-beta" {
		t.Fatalf("bundle.StableIdentity = %q, want %q", bundle.StableIdentity, lab.StableIdentityPrefix+"mock-device-beta")
	}
	status := service.Status(context.Background())
	if len(status.Discovered) == 0 {
		t.Fatal("Status() recorded no attached candidates for the resolved target")
	}
	for _, candidate := range status.Discovered {
		if candidate.Serial == bundle.Serial && candidate.TransportID == bundle.StableIdentity {
			t.Fatalf("stable identity %q must never be the transport identity", bundle.StableIdentity)
		}
	}
}

// Resolving a target records the attached candidates it observed without
// creating or registering anything.
func TestCaptureObservationRecordsTheAttachedCandidatesItResolved(t *testing.T) {
	service := newMockService(t)
	bundle := captureFor(t, service, "mock-device-alpha", "key-1")

	status := service.Status(context.Background())
	if len(status.Discovered) != len(lab.DefaultMockCandidates()) {
		t.Fatalf("Status().Discovered = %d candidates, want the %d attached fixtures", len(status.Discovered), len(lab.DefaultMockCandidates()))
	}
	var named bool
	for _, candidate := range status.Discovered {
		if candidate.Serial == bundle.Serial {
			named = true
		}
	}
	if !named {
		t.Fatalf("Status().Discovered = %+v, want the explicitly named serial to be observed as attached", status.Discovered)
	}
	if status.Readiness != lab.ReadinessReady {
		t.Fatalf("Status().Readiness = %q, want %q after a verified capture", status.Readiness, lab.ReadinessReady)
	}
}

// Capture is observation only: it may enumerate, read health, take a bounded
// screenshot, dump the hierarchy, and re-read a changed transport read-only. It
// must never issue a state-changing device action.
func TestCaptureObservationIssuesOnlyReadOnlyAdapterCalls(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)
	captureFor(t, service, "fakeserial01", "key-1")

	readOnly := map[string]bool{
		"version": true, "enumerate": true, "health": true,
		"screenshot": true, "hierarchy": true, "reattach": true,
	}
	calls := fake.recordedCalls()
	if len(calls) == 0 {
		t.Fatal("CaptureObservation() issued no adapter calls at all")
	}
	for _, call := range calls {
		if !readOnly[call] {
			t.Fatalf("adapter call %q is not a read-only observation", call)
		}
	}
}

func TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode(t *testing.T) {
	service := newMockService(t)
	bundle := captureFor(t, service, "mock-device-alpha", "key-1")

	if !bundle.PostconditionVerified || bundle.FailureClass != "" || bundle.Indeterminate {
		t.Fatalf("CaptureObservation() bundle = %+v, want a verified determinate observation", bundle)
	}
	if !strings.HasPrefix(bundle.ScreenshotHash, "sha256:") || bundle.ScreenshotBytes == 0 {
		t.Fatalf("bundle screenshot = %q/%d, want a content hash and a non-empty payload size", bundle.ScreenshotHash, bundle.ScreenshotBytes)
	}
	if bundle.PreviewBase64 != "" {
		t.Fatal("bundle exposed an inline preview even though previews are disabled by default")
	}
	if bundle.HierarchySummary != "3 nodes, depth 2, complete (not measured)" {
		t.Fatalf("bundle.HierarchySummary = %q, want a bounded node/depth/completeness summary", bundle.HierarchySummary)
	}
	if !hasEvent(bundle.Events, lab.EventObservationCapture) || !hasEvent(bundle.Events, lab.EventUITreeCapture) {
		t.Fatalf("bundle.Events = %+v, want observation and hierarchy records", bundle.Events)
	}

	status := service.Status(context.Background())
	if status.LastObservationAt == nil || status.LastScreenshotHash != bundle.ScreenshotHash {
		t.Fatalf("Status() = %+v, want the observation recorded by hash only", status)
	}
}

func TestCaptureObservationEmitsABoundedPreviewOnlyWhenEnabled(t *testing.T) {
	service := newMockService(t, lab.WithScreenshotPreview(lab.MaxScreenshotPreviewBytes))
	bundle := captureFor(t, service, "mock-device-alpha", "key-1")

	if bundle.PreviewBase64 == "" || bundle.PreviewTruncated {
		t.Fatalf("bundle preview = %q truncated=%v, want a complete bounded preview", bundle.PreviewBase64, bundle.PreviewTruncated)
	}
}

func TestCaptureObservationTruncatesRatherThanReturningAPartialImage(t *testing.T) {
	service := newMockService(t, lab.WithScreenshotPreview(4))
	bundle := captureFor(t, service, "mock-device-alpha", "key-1")

	if bundle.PreviewBase64 != "" || !bundle.PreviewTruncated {
		t.Fatalf("bundle preview = %q truncated=%v, want no preview and a truncation flag", bundle.PreviewBase64, bundle.PreviewTruncated)
	}
}

func TestCaptureObservationDeduplicatesACompletedIdempotencyKey(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)

	request := lab.CaptureRequest{Serial: "fakeserial01", IdempotencyKey: "key-1", OperatorID: operator}
	first, err := service.CaptureObservation(context.Background(), request)
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
	before := fake.callCount("screenshot")

	second, err := service.CaptureObservation(context.Background(), request)
	if err != nil {
		t.Fatalf("CaptureObservation() replay error = %v", err)
	}
	if second.ScreenshotHash != first.ScreenshotHash || second.CorrelationID != first.CorrelationID {
		t.Fatalf("replay returned a different observation: %+v vs %+v", second, first)
	}
	if fake.callCount("screenshot") != before {
		t.Fatal("replay of a completed idempotency key dispatched another device command")
	}
}

func TestCaptureObservationTimeoutIsIndeterminateAndIsNeverReplayed(t *testing.T) {
	fake := newFakeAdapter()
	fake.screenshotDelay = time.Second
	service := newLabService(t, fake)

	request := lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
		Timeout:        20 * time.Millisecond,
	}
	bundle, err := service.CaptureObservation(context.Background(), request)
	if platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeIndeterminateCompletion)
	}
	if !bundle.Indeterminate || bundle.FailureClass != domain.FailureIndeterminate || bundle.PostconditionVerified {
		t.Fatalf("CaptureObservation() bundle = %+v, want an unverified indeterminate outcome", bundle)
	}
	if !hasEvent(bundle.Events, lab.EventTimeout) || !hasEvent(bundle.Events, lab.EventIndeterminateOutcome) {
		t.Fatalf("bundle.Events = %+v, want timeout and indeterminate records", bundle.Events)
	}
	if status := service.Status(context.Background()); status.Readiness != lab.ReadinessIndeterminate || !status.Indeterminate {
		t.Fatalf("Status() = %+v, want a sticky indeterminate readiness", status)
	}

	before := fake.callCount("screenshot")
	replayed, replayErr := service.CaptureObservation(context.Background(), request)
	if platformerrors.CodeOf(replayErr) != platformerrors.CodeIndeterminateCompletion {
		t.Fatalf("replay code = %v, want %v", platformerrors.CodeOf(replayErr), platformerrors.CodeIndeterminateCompletion)
	}
	if !replayed.Indeterminate {
		t.Fatalf("replay bundle = %+v, want it to stay indeterminate", replayed)
	}
	if fake.callCount("screenshot") != before {
		t.Fatal("replay of an unresolved idempotency key dispatched another device command")
	}
}

func TestCaptureObservationClassifiesOperatorCancellation(t *testing.T) {
	fake := newFakeAdapter()
	fake.screenshotDelay = time.Second
	service := newLabService(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	bundle, err := service.CaptureObservation(ctx, lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeCanceled {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeCanceled)
	}
	if bundle.FailureClass != domain.FailureOperatorCancelled || bundle.Indeterminate {
		t.Fatalf("CaptureObservation() bundle = %+v, want a cancelled, non-indeterminate outcome", bundle)
	}
	if !hasEvent(bundle.Events, lab.EventCancellation) {
		t.Fatalf("bundle.Events = %+v, want a cancellation record", bundle.Events)
	}
}

func TestCaptureObservationFailsThePostconditionForAnIncompleteHierarchy(t *testing.T) {
	fake := newFakeAdapter()
	fake.hierarchy.Truncated = true
	fake.hierarchy.Partial = true
	fake.hierarchy.FailureClass = domain.FailureObservation
	service := newLabService(t, fake)

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePostconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePostconditionFailed)
	}
	if bundle.PostconditionVerified || bundle.FailureClass != domain.FailurePostcondition {
		t.Fatalf("CaptureObservation() bundle = %+v, want an unverified postcondition failure", bundle)
	}
	if !strings.Contains(bundle.HierarchySummary, "truncated") {
		t.Fatalf("bundle.HierarchySummary = %q, want it to report truncation honestly", bundle.HierarchySummary)
	}
}

// A transport change is detected against the transport identity observed for
// this device on the previous capture, and it is reconciled read-only.
func TestCaptureObservationReconcilesATransportChangeReadOnly(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)
	captureFor(t, service, "fakeserial01", "key-1")
	fake.healthTransport = "9"

	bundle := captureFor(t, service, "fakeserial01", "key-2")
	if !hasEvent(bundle.Events, lab.EventTransportChange) || !hasEvent(bundle.Events, lab.EventReadonlyReattach) {
		t.Fatalf("bundle.Events = %+v, want transport change and read-only reattach records", bundle.Events)
	}
	if bundle.StableIdentity != lab.StableIdentityPrefix+"fakeserial01" {
		t.Fatalf("bundle.StableIdentity = %q, want it unchanged by a transport change", bundle.StableIdentity)
	}
	// The adapter's single-use reattach is what caps the reconciliation; a bare
	// re-enumeration would let a flapping transport be re-read without limit.
	if fake.callCount("reattach") != 1 {
		t.Fatalf("reattach call count = %d, want exactly one adapter reattach", fake.callCount("reattach"))
	}
}

// An unknown outcome is only resolved by a capture that actually verified its
// postcondition. A later determinate failure says nothing about whether the
// earlier command reached the device.
func TestIndeterminateReadinessSurvivesALaterDeterminateFailure(t *testing.T) {
	fake := newFakeAdapter()
	fake.screenshotDelay = time.Second
	service := newLabService(t, fake)

	if _, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-timeout",
		OperatorID:     operator,
		Timeout:        20 * time.Millisecond,
	}); platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeIndeterminateCompletion)
	}

	fake.screenshotDelay = 0
	fake.hierarchy.Truncated = true
	fake.hierarchy.FailureClass = domain.FailureObservation
	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-postcondition",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePostconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePostconditionFailed)
	}
	if bundle.Indeterminate {
		t.Fatalf("bundle.Indeterminate = true, want the determinate failure reported as determinate")
	}
	status := service.Status(context.Background())
	if !status.Indeterminate || status.Readiness != lab.ReadinessIndeterminate {
		t.Fatalf("Status() = %+v, want the session to stay indeterminate after a determinate failure", status)
	}

	fake.hierarchy.Truncated = false
	fake.hierarchy.FailureClass = ""
	verified := captureFor(t, service, "fakeserial01", "key-verified")
	if !verified.PostconditionVerified {
		t.Fatalf("CaptureObservation() bundle = %+v, want a verified postcondition", verified)
	}
	if resolved := service.Status(context.Background()); resolved.Indeterminate || resolved.Readiness != lab.ReadinessReady {
		t.Fatalf("Status() = %+v, want a verified capture to resolve the unknown outcome", resolved)
	}
}

func TestEventSummariesAreBoundedAndRedacted(t *testing.T) {
	service := newMockService(t)
	captureFor(t, service, "mock-device-alpha", "key-1")

	for _, event := range service.Events() {
		if len(event.Summary) > 512+len("…[truncated]") {
			t.Fatalf("event %q summary is unbounded: %d bytes", event.Name, len(event.Summary))
		}
		if strings.Contains(strings.ToLower(event.Summary), "password") {
			t.Fatalf("event %q summary leaked credential material: %q", event.Name, event.Summary)
		}
		if event.OccurredAt.IsZero() {
			t.Fatalf("event %q has no timestamp", event.Name)
		}
	}
}

func TestLabModeRequiresBothAnOptInAndAnExecutablePath(t *testing.T) {
	cases := map[string]map[string]string{
		"no opt in":     {lab.EnvADBPath: "/usr/local/bin/adb"},
		"no executable": {lab.EnvLabMode: "1"},
		"empty":         {},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			}
			if lab.LabModeRequested(lookup) {
				t.Fatal("LabModeRequested() = true, want false without both an opt-in and an executable path")
			}
			service, err := lab.NewServiceFromEnv(lookup)
			if err != nil {
				t.Fatalf("NewServiceFromEnv() error = %v", err)
			}
			if status := service.Status(context.Background()); status.Mode != lab.ModeMock {
				t.Fatalf("NewServiceFromEnv() mode = %q, want %q", status.Mode, lab.ModeMock)
			}
		})
	}
}

func TestLabModeRequestedAcceptsAnExplicitOptIn(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case lab.EnvLabMode:
			return "1", true
		case lab.EnvADBPath:
			return "/usr/local/bin/adb", true
		default:
			return "", false
		}
	}
	if !lab.LabModeRequested(lookup) {
		t.Fatal("LabModeRequested() = false, want true for an explicit opt-in with an executable path")
	}
}

func TestCaptureObservationReportsAnUnavailableAdapterWithoutInventingCandidates(t *testing.T) {
	fake := newFakeAdapter()
	fake.enumerateErr = &adb.OperationError{Op: "enumerate", FailureClass: domain.FailureInfrastructure}
	service := newLabService(t, fake)

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeUnavailable)
	}
	if bundle.PostconditionVerified || bundle.Indeterminate {
		t.Fatalf("CaptureObservation() bundle = %+v, want no observation attempted for an unavailable adapter", bundle)
	}
	status := service.Status(context.Background())
	if len(status.Discovered) != 0 || status.Readiness != lab.ReadinessUnavailable {
		t.Fatalf("Status() = %+v, want no candidates and unavailable readiness", status)
	}
	if status.FailureClass != domain.FailureInfrastructure {
		t.Fatalf("Status().FailureClass = %q, want %q", status.FailureClass, domain.FailureInfrastructure)
	}
}
