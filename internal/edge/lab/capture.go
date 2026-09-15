package lab

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// captureRun is the per-observation scope. It accumulates the audit events for
// one capture so both the ring buffer and the returned bundle tell the same
// story.
type captureRun struct {
	service             *Service
	correlationID       string
	serial              string
	stableIdentity      string
	previousTransportID string
	request             CaptureRequest
	parent              context.Context

	events  []Event
	latency time.Duration
}

// execute observes health, then a bounded screenshot, then a bounded view
// hierarchy, and finally verifies the postcondition. Every step is read-only.
func (r *captureRun) execute(ctx context.Context) (ObservationBundle, error) {
	bundle := ObservationBundle{
		Serial:              r.serial,
		StableIdentity:      r.stableIdentity,
		CorrelationID:       r.correlationID,
		IdempotencyKey:      r.request.IdempotencyKey,
		CapturedAt:          r.service.clock.Now(),
		AdapterVersion:      r.service.devices.Version(),
		ScreenshotMediaType: ScreenshotMediaType,
	}

	report, healthErr := r.service.devices.Health(ctx, r.serial)
	r.latency += report.Latency
	if healthErr != nil {
		return r.fail(bundle, healthErr, "health observation failed")
	}
	bundle.HealthState = report.State
	bundle.PlatformToolsVersion = report.PlatformToolsVersion
	if report.FailureClass != "" {
		return r.blocked(bundle, report.FailureClass, "target is attached but not observable: state "+report.State)
	}
	r.emit(EventObservationCapture, "", "health observed with transport state "+report.State)

	if transportErr := r.reconcileTransport(ctx, report); transportErr != nil {
		return r.fail(bundle, transportErr, "transport identity could not be reconciled")
	}

	shot, screenshotErr := r.service.devices.Screenshot(ctx, r.serial)
	r.latency += shot.Latency
	if screenshotErr != nil {
		return r.fail(bundle, screenshotErr, "screenshot capture failed")
	}
	bundle.ScreenshotHash = shot.Hash
	bundle.ScreenshotBytes = len(shot.PNG)
	bundle.PreviewBase64, bundle.PreviewTruncated = encodePreview(shot.PNG, r.service.previewLimit)
	r.emit(EventObservationCapture, "", "captured a bounded screenshot referenced by content hash "+shot.Hash)

	tree, hierarchyErr := r.service.hierarchy.Capture(ctx, r.serial)
	r.latency += tree.Latency
	if tree.CleanupFailed {
		r.emit(EventCleanup, domain.FailureCleanupFailed, "device-side temporary hierarchy file could not be removed")
	}
	if hierarchyErr != nil {
		return r.fail(bundle, hierarchyErr, "view hierarchy capture failed")
	}
	bundle.NodeCount = tree.NodeCount
	bundle.MaxDepth = tree.MaxDepth
	bundle.HierarchyComplete = tree.Complete()
	bundle.HierarchyFreshnessToken = tree.FreshnessToken
	bundle.HierarchySummary = hierarchySummary(tree)
	r.emit(EventUITreeCapture, tree.FailureClass, "observed hierarchy: "+bundle.HierarchySummary)

	r.persistEvidence(ctx, &bundle, shot.PNG, shot.Hash, tree)

	bundle.LatencyMs = r.latency.Milliseconds()
	if class := postconditionClass(bundle, report, tree); class != "" {
		bundle.FailureClass = class
		r.emit(EventObservationCapture, class, "postcondition not satisfied for "+bundle.HierarchySummary)
		bundle.Events = r.commit()
		return bundle, classifiedError(class, "lab observation did not satisfy its postcondition", nil)
	}

	bundle.PostconditionVerified = true
	bundle.Events = r.commit()
	return bundle, nil
}

// persistEvidence stores screenshot/UI-tree bytes only through the configured
// artifact persister. Failures are isolated: observation success is unchanged.
func (r *captureRun) persistEvidence(ctx context.Context, bundle *ObservationBundle, png []byte, hash string, tree uiautomator.HierarchyCapture) {
	if r.service.evidence == nil || bundle == nil {
		return
	}
	workspace := ""
	ownerID := bundle.CorrelationID
	if ownerID == "" {
		ownerID = bundle.IdempotencyKey
	}
	actorID := r.request.OperatorID
	if shotID, err := r.service.evidence.PersistScreenshot(ctx, workspace, ownerID, actorID, png, hash); err != nil {
		r.recordEvidencePersist(bundle, "screenshot", shotID, err)
	} else {
		bundle.ScreenshotArtifactID = shotID
	}
	payload := []byte(fmt.Sprintf(`{"summary":%q,"nodes":%d,"depth":%d,"complete":%t,"freshness":%q}`, bundle.HierarchySummary, tree.NodeCount, tree.MaxDepth, tree.Complete(), tree.FreshnessToken))
	if treeID, err := r.service.evidence.PersistUITree(ctx, workspace, ownerID, actorID, payload); err != nil {
		r.recordEvidencePersist(bundle, "ui-tree", treeID, err)
	} else {
		bundle.HierarchyArtifactID = treeID
	}
}

// recordEvidencePersist isolates artifact write outcomes from observation success.
// Policy omissions that still return an artifact ID are admission decisions, not
// infrastructure failures.
func (r *captureRun) recordEvidencePersist(bundle *ObservationBundle, kind, artifactID string, err error) {
	if artifactID != "" && platformerrors.CodeOf(err) == platformerrors.CodePolicyDenied {
		switch kind {
		case "screenshot":
			bundle.ScreenshotArtifactID = artifactID
		case "ui-tree":
			bundle.HierarchyArtifactID = artifactID
		}
		r.emit(EventObservationCapture, domain.FailurePolicyDenied, kind+" artifact omitted by admission; observation continues")
		return
	}
	bundle.EvidencePersistFailed = true
	r.emit(EventObservationCapture, domain.FailureInfrastructure, kind+" artifact persistence failed; observation continues")
}

// reconcileTransport reacts to an observed transport-identity change with the
// adapter's single-use read-only reattach. It issues no connect, reconnect, or
// other mutating transport command, and the adapter itself caps the reattach at
// one per observed change.
func (r *captureRun) reconcileTransport(ctx context.Context, report adb.HealthReport) error {
	if report.TransportID == "" || r.previousTransportID == "" || report.TransportID == r.previousTransportID {
		return nil
	}
	r.emit(EventTransportChange, "", "transport identity changed while the stable session identity stayed "+r.stableIdentity)

	candidate, err := r.service.devices.ReattachReadOnly(ctx, r.serial, r.previousTransportID)
	if err != nil {
		return reattachFailure(candidate, err)
	}
	if !candidate.State.Usable() {
		return classifiedError(domain.FailureTransport, "lab target is no longer usable after its transport changed", nil)
	}

	r.service.mu.Lock()
	r.service.state.TransportID = report.TransportID
	r.service.state.ConnectionState = string(candidate.State)
	r.service.state.ConnectionType = candidate.ConnectionType
	r.service.mu.Unlock()
	r.emit(EventReadonlyReattach, "", "re-read transport state read-only after the identity change")
	return nil
}

// reattachFailure keeps the observed distinction between a target that came
// back unusable and one that did not come back at all. A reattach the adapter
// already spent is a transport failure, not a retry invitation.
func reattachFailure(candidate adb.DiscoveredDevice, err error) error {
	if errors.Is(err, adb.ErrDeviceNotFound) {
		return classifiedError(domain.FailureDeviceOffline, "lab target disappeared after its transport changed", err)
	}
	if candidate.Serial != "" && !candidate.State.Usable() {
		return classifiedError(domain.FailureTransport, "lab target is no longer usable after its transport changed", err)
	}
	return classifiedError(adb.FailureClassOf(err), "lab target transport could not be re-read read-only", err)
}

// blocked reports an observation that succeeded as an observation but found the
// target unusable. The bundle is never marked verified.
func (r *captureRun) blocked(bundle ObservationBundle, class domain.FailureClass, summary string) (ObservationBundle, error) {
	bundle.FailureClass = class
	bundle.LatencyMs = r.latency.Milliseconds()
	r.emit(EventObservationCapture, class, summary)
	bundle.Events = r.commit()
	return bundle, classifiedError(class, "lab target is not observable", nil)
}

// fail classifies a failed step. Operator cancellation is distinguished from a
// deadline: a deadline may have expired after a command was already dispatched,
// so the outcome is indeterminate and the idempotency key becomes unreplayable.
func (r *captureRun) fail(bundle ObservationBundle, cause error, summary string) (ObservationBundle, error) {
	class := adb.FailureClassOf(cause)
	bundle.LatencyMs = r.latency.Milliseconds()

	switch {
	case errors.Is(r.parent.Err(), context.Canceled):
		class = domain.FailureOperatorCancelled
		r.emit(EventCancellation, class, summary+": operator cancelled before the observation completed")
	case class == domain.FailureTimeout || errors.Is(cause, context.DeadlineExceeded):
		r.emit(EventTimeout, domain.FailureTimeout, summary+": deadline expired after the command may already have been dispatched")
		r.emit(EventIndeterminateOutcome, domain.FailureIndeterminate, "outcome is unknown; this idempotency key is never replayed automatically")
		class = domain.FailureIndeterminate
		bundle.Indeterminate = true
	default:
		r.emit(EventObservationCapture, class, summary+": "+safeDetail(cause))
	}

	bundle.FailureClass = class
	bundle.Events = r.commit()
	return bundle, classifiedError(class, "lab observation did not complete", cause)
}

func (r *captureRun) emit(name EventName, class domain.FailureClass, summary string) {
	event := Event{
		Name:          name,
		CorrelationID: r.correlationID,
		Serial:        r.serial,
		OccurredAt:    r.service.clock.Now(),
		FailureClass:  class,
		Summary:       summary,
	}
	r.service.mu.Lock()
	stored := r.service.appendEventLocked(event)
	r.service.mu.Unlock()
	r.events = append(r.events, stored)
}

func (r *captureRun) commit() []Event {
	return append([]Event(nil), r.events...)
}

// postconditionClass verifies that the bundle actually describes a complete
// observation. Absence of an error is never treated as success.
func postconditionClass(bundle ObservationBundle, report adb.HealthReport, tree uiautomator.HierarchyCapture) domain.FailureClass {
	if !report.Healthy() || !adb.DeviceAuthState(report.State).Usable() {
		return domain.FailurePostcondition
	}
	if bundle.ScreenshotHash == "" || bundle.ScreenshotBytes == 0 {
		return domain.FailurePostcondition
	}
	if !tree.Complete() || tree.NodeCount == 0 {
		return domain.FailurePostcondition
	}
	return ""
}

// hierarchySummary renders bounded prose about the observation. It reports
// completeness honestly rather than implying a full tree.
func hierarchySummary(tree uiautomator.HierarchyCapture) string {
	completeness := "complete"
	switch {
	case tree.Truncated:
		completeness = "truncated"
	case tree.Partial:
		completeness = "partial"
	case tree.FailureClass != "":
		completeness = "incomplete"
	}
	summary := fmt.Sprintf("%d nodes, depth %d, %s", tree.NodeCount, tree.MaxDepth, completeness)
	// Fixture adapters must not be read as measured device evidence.
	if strings.Contains(tree.AdapterVersion, "mock") {
		return summary + " (not measured)"
	}
	return summary
}
