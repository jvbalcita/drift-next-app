package lab_test

import (
	"context"
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

func confirm(t *testing.T, service *lab.Service, serial string) lab.Status {
	t.Helper()
	ctx := context.Background()
	if _, err := service.Discover(ctx, operator); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	status, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
		Serial:           serial,
		DisplayName:      "Bench device",
		ConfirmationText: serial,
		OperatorID:       operator,
		Reason:           "phase 13 vertical slice",
	})
	if err != nil {
		t.Fatalf("ConfirmTarget() error = %v", err)
	}
	return status
}

func hasEvent(events []lab.Event, name lab.EventName) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
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
	if status.Confirmed() || len(status.Discovered) != 0 {
		t.Fatalf("Status() = %+v, want no confirmed target and no discovery before Discover", status)
	}
}

func TestDiscoverEnumeratesWithoutConfirmingOrRegisteringAnything(t *testing.T) {
	service := newMockService(t)

	status, err := service.Discover(context.Background(), operator)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(status.Discovered) != len(lab.DefaultMockCandidates()) {
		t.Fatalf("Discover() discovered %d candidates, want %d", len(status.Discovered), len(lab.DefaultMockCandidates()))
	}
	if status.Confirmed() {
		t.Fatalf("Discover() confirmed %q; discovery must never confirm a target", status.ConfirmedSerial)
	}
	if status.Readiness != lab.ReadinessBlocked {
		t.Fatalf("Discover() readiness = %q, want %q until a target is confirmed", status.Readiness, lab.ReadinessBlocked)
	}
	if !hasEvent(service.Events(), lab.EventAdapterReadiness) {
		t.Fatal("Discover() emitted no adapter_readiness event")
	}
}

func TestDiscoverAndCaptureRequireAnAttributableOperator(t *testing.T) {
	service := newMockService(t)

	if _, err := service.Discover(context.Background(), " "); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("Discover() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePolicyDenied)
	}
	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePolicyDenied)
	}
}

func TestCaptureObservationIsBlockedBeforeConfirmation(t *testing.T) {
	service := newMockService(t)
	if _, err := service.Discover(context.Background(), operator); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePreconditionFailed)
	}
}

func TestConfirmTargetRefusesGenericConfirmationWhenSeveralCandidatesAreAttached(t *testing.T) {
	service := newMockService(t)
	ctx := context.Background()
	if _, err := service.Discover(ctx, operator); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	status, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
		Serial:           "mock-device-alpha",
		ConfirmationText: lab.ConfirmationLiteral,
		OperatorID:       operator,
		Reason:           "phase 13 vertical slice",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeAmbiguousTarget {
		t.Fatalf("ConfirmTarget() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeAmbiguousTarget)
	}
	if status.Confirmed() {
		t.Fatalf("ConfirmTarget() confirmed %q despite an ambiguous confirmation", status.ConfirmedSerial)
	}
	if status.FailureClass != domain.FailureAmbiguousTarget {
		t.Fatalf("Status().FailureClass = %q, want %q", status.FailureClass, domain.FailureAmbiguousTarget)
	}
}

func TestConfirmTargetAcceptsTheGenericLiteralOnlyForASingleCandidate(t *testing.T) {
	single := lab.DefaultMockCandidates()[:1]
	service := newMockService(t, lab.WithMockCandidates(single...))
	ctx := context.Background()
	if _, err := service.Discover(ctx, operator); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	status, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
		Serial:           single[0].Serial,
		ConfirmationText: lab.ConfirmationLiteral,
		OperatorID:       operator,
		Reason:           "phase 13 vertical slice",
	})
	if err != nil {
		t.Fatalf("ConfirmTarget() error = %v", err)
	}
	if status.ConfirmedSerial != single[0].Serial {
		t.Fatalf("Status().ConfirmedSerial = %q, want %q", status.ConfirmedSerial, single[0].Serial)
	}
}

func TestConfirmTargetRejectsASerialThatWasNeverEnumerated(t *testing.T) {
	service := newMockService(t)
	ctx := context.Background()

	_, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
		Serial:           "mock-device-gamma",
		ConfirmationText: "mock-device-gamma",
		OperatorID:       operator,
		Reason:           "phase 13 vertical slice",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeNotFound {
		t.Fatalf("ConfirmTarget() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeNotFound)
	}
}

// A confirmation is an auditable operator decision, so an unexplained one is
// refused before any candidate is matched or any device is touched.
func TestConfirmTargetRequiresAReason(t *testing.T) {
	service := newMockService(t)
	ctx := context.Background()
	if _, err := service.Discover(ctx, operator); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	for name, reason := range map[string]string{"empty": "", "whitespace": "   "} {
		status, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
			Serial:           "mock-device-alpha",
			ConfirmationText: "mock-device-alpha",
			OperatorID:       operator,
			Reason:           reason,
		})
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Fatalf("ConfirmTarget(%s reason) code = %v, want %v", name, platformerrors.CodeOf(err), platformerrors.CodeInvalidInput)
		}
		if status.Confirmed() {
			t.Fatalf("ConfirmTarget(%s reason) confirmed %q without an audited reason", name, status.ConfirmedSerial)
		}
	}
}

func TestConfirmTargetRejectsAnUnusableTransportState(t *testing.T) {
	fake := newFakeAdapter(adb.DiscoveredDevice{
		Serial:         "fakeserial01",
		State:          adb.StateUnauthorized,
		TransportID:    "7",
		ConnectionType: adb.ConnectionUSB,
	})
	service := newLabService(t, fake)
	ctx := context.Background()

	_, err := service.ConfirmTarget(ctx, lab.ConfirmRequest{
		Serial:           "fakeserial01",
		ConfirmationText: "fakeserial01",
		OperatorID:       operator,
		Reason:           "phase 13 vertical slice",
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("ConfirmTarget() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePreconditionFailed)
	}
	if fake.callCount("health") != 0 {
		t.Fatal("ConfirmTarget() observed health for a candidate it had already refused")
	}
}

func TestConfirmTargetBindsASessionIdentityThatIsNotTheTransportIdentity(t *testing.T) {
	service := newMockService(t)
	status := confirm(t, service, "mock-device-beta")

	if status.StableIdentity != lab.StableIdentityPrefix+"mock-device-beta" {
		t.Fatalf("Status().StableIdentity = %q, want %q", status.StableIdentity, lab.StableIdentityPrefix+"mock-device-beta")
	}
	if status.StableIdentity == status.TransportID || status.TransportID == "" {
		t.Fatalf("Status() identity = %q must differ from transport identity %q", status.StableIdentity, status.TransportID)
	}
	if status.Readiness != lab.ReadinessReady {
		t.Fatalf("Status().Readiness = %q, want %q", status.Readiness, lab.ReadinessReady)
	}
	if !hasEvent(service.Events(), lab.EventOperatorConfirmation) || !hasEvent(service.Events(), lab.EventTargetConfirmation) {
		t.Fatalf("ConfirmTarget() events = %+v, want operator and target confirmation records", service.Events())
	}
}

func TestCaptureObservationRequiresTheConfirmedSerial(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")

	_, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-beta",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("CaptureObservation() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodePreconditionFailed)
	}
}

func TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
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
	confirm(t, service, "mock-device-alpha")

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
	if bundle.PreviewBase64 == "" || bundle.PreviewTruncated {
		t.Fatalf("bundle preview = %q truncated=%v, want a complete bounded preview", bundle.PreviewBase64, bundle.PreviewTruncated)
	}
}

func TestCaptureObservationTruncatesRatherThanReturningAPartialImage(t *testing.T) {
	service := newMockService(t, lab.WithScreenshotPreview(4))
	confirm(t, service, "mock-device-alpha")

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "mock-device-alpha",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
	if bundle.PreviewBase64 != "" || !bundle.PreviewTruncated {
		t.Fatalf("bundle preview = %q truncated=%v, want no preview and a truncation flag", bundle.PreviewBase64, bundle.PreviewTruncated)
	}
}

func TestCaptureObservationDeduplicatesACompletedIdempotencyKey(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)
	confirm(t, service, "fakeserial01")

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
	confirm(t, service, "fakeserial01")

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
	confirm(t, service, "fakeserial01")

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
	confirm(t, service, "fakeserial01")

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

func TestCaptureObservationReconcilesATransportChangeReadOnly(t *testing.T) {
	fake := newFakeAdapter()
	service := newLabService(t, fake)
	confirm(t, service, "fakeserial01")
	fake.healthTransport = "9"

	bundle, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
	if !hasEvent(bundle.Events, lab.EventTransportChange) || !hasEvent(bundle.Events, lab.EventReadonlyReattach) {
		t.Fatalf("bundle.Events = %+v, want transport change and read-only reattach records", bundle.Events)
	}
	status := service.Status(context.Background())
	if status.TransportID != "9" {
		t.Fatalf("Status().TransportID = %q, want the re-observed transport identity", status.TransportID)
	}
	if status.StableIdentity != lab.StableIdentityPrefix+"fakeserial01" {
		t.Fatalf("Status().StableIdentity = %q, want it unchanged by a transport change", status.StableIdentity)
	}
	// The adapter's single-use reattach is what caps the reconciliation; a bare
	// re-enumeration would let a flapping transport be re-read without limit.
	if fake.callCount("reattach") != 1 {
		t.Fatalf("reattach call count = %d, want exactly one adapter reattach", fake.callCount("reattach"))
	}
}

// An unknown outcome is only resolved by an operator clear or by a capture that
// actually verified its postcondition. A later determinate failure says nothing
// about whether the earlier command reached the device.
func TestIndeterminateReadinessSurvivesALaterDeterminateFailure(t *testing.T) {
	fake := newFakeAdapter()
	fake.screenshotDelay = time.Second
	service := newLabService(t, fake)
	confirm(t, service, "fakeserial01")

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
	verified, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-verified",
		OperatorID:     operator,
	})
	if err != nil {
		t.Fatalf("CaptureObservation() error = %v", err)
	}
	if !verified.PostconditionVerified {
		t.Fatalf("CaptureObservation() bundle = %+v, want a verified postcondition", verified)
	}
	if resolved := service.Status(context.Background()); resolved.Indeterminate || resolved.Readiness != lab.ReadinessReady {
		t.Fatalf("Status() = %+v, want a verified capture to resolve the unknown outcome", resolved)
	}
}

func TestClearTargetReleasesTheSessionAndResolvesIndeterminateReadiness(t *testing.T) {
	fake := newFakeAdapter()
	fake.screenshotDelay = time.Second
	service := newLabService(t, fake)
	confirm(t, service, "fakeserial01")

	if _, err := service.CaptureObservation(context.Background(), lab.CaptureRequest{
		Serial:         "fakeserial01",
		IdempotencyKey: "key-1",
		OperatorID:     operator,
		Timeout:        20 * time.Millisecond,
	}); err == nil {
		t.Fatal("CaptureObservation() unexpectedly succeeded with an expired deadline")
	}

	status, err := service.ClearTarget(context.Background(), operator)
	if err != nil {
		t.Fatalf("ClearTarget() error = %v", err)
	}
	if status.Confirmed() || status.Indeterminate || status.Readiness != lab.ReadinessBlocked {
		t.Fatalf("ClearTarget() status = %+v, want a released, determinate, blocked session", status)
	}
	if !hasEvent(service.Events(), lab.EventCleanup) {
		t.Fatalf("ClearTarget() events = %+v, want a cleanup record", service.Events())
	}
}

func TestEventSummariesAreBoundedAndRedacted(t *testing.T) {
	service := newMockService(t)
	confirm(t, service, "mock-device-alpha")

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

func TestDiscoverReportsAnUnavailableAdapterWithoutInventingCandidates(t *testing.T) {
	fake := newFakeAdapter()
	fake.enumerateErr = &adb.OperationError{Op: "enumerate", FailureClass: domain.FailureInfrastructure}
	service := newLabService(t, fake)

	status, err := service.Discover(context.Background(), operator)
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("Discover() code = %v, want %v", platformerrors.CodeOf(err), platformerrors.CodeUnavailable)
	}
	if len(status.Discovered) != 0 || status.Readiness != lab.ReadinessUnavailable {
		t.Fatalf("Discover() status = %+v, want no candidates and unavailable readiness", status)
	}
	if status.FailureClass != domain.FailureInfrastructure {
		t.Fatalf("Status().FailureClass = %q, want %q", status.FailureClass, domain.FailureInfrastructure)
	}
}
