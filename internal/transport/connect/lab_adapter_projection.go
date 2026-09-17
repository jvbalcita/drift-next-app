package transportconnect

import (
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/lab"
)

// labEvidenceSchemaVersion versions the sanitized evidence reference shape this
// transport emits for lab observations.
const labEvidenceSchemaVersion = 1

// labStatusProto projects the lab status onto the wire contract. It exposes
// hashes, bounded summaries, and mutable transport facts as separate fields so
// no caller can mistake a transport identifier for device identity.
func labStatusProto(status lab.Status) *driftv1.LabStatus {
	projected := &driftv1.LabStatus{
		Mode:                 labModeProto(status.Mode),
		Readiness:            labReadinessProto(status.Readiness),
		AdapterVersion:       status.AdapterVersion,
		PlatformToolsVersion: status.PlatformToolsVersion,
		ConnectionState:      status.ConnectionState,
		ConnectionType:       status.ConnectionType,
		LastHealthAt:         labTimestamp(status.LastHealthAt),
		LastObservationAt:    labTimestamp(status.LastObservationAt),
		LastScreenshotHash:   status.LastScreenshotHash,
		LastHierarchySummary: status.LastHierarchySummary,
		ObservationLatencyMs: status.ObservationLatencyMs,
		FailureClass:         string(status.FailureClass),
		Indeterminate:        status.Indeterminate,
		CorrelationId:        status.CorrelationID,
		Discovered:           make([]*driftv1.LabDiscoveredDevice, 0, len(status.Discovered)),
	}
	for _, candidate := range status.Discovered {
		projected.Discovered = append(projected.Discovered, &driftv1.LabDiscoveredDevice{
			Serial:          candidate.Serial,
			ConnectionState: string(candidate.State),
			ConnectionType:  candidate.ConnectionType,
			TransportId:     candidate.TransportID,
			Model:           candidate.Model,
			DeviceName:      candidate.DeviceName,
			Product:         candidate.Product,
			Usable:          candidate.State.Usable(),
		})
	}
	return projected
}

// labObservationProto projects one observation bundle. Screen bytes are never
// projected: only a content hash and, when explicitly enabled, a bounded
// preview the service already capped.
func labObservationProto(bundle lab.ObservationBundle) *driftv1.LabObservationBundle {
	projected := &driftv1.LabObservationBundle{
		Serial:                  bundle.Serial,
		StableIdentity:          bundle.StableIdentity,
		CorrelationId:           bundle.CorrelationID,
		IdempotencyKey:          bundle.IdempotencyKey,
		CapturedAt:              labTimestampValue(bundle.CapturedAt),
		AdapterVersion:          bundle.AdapterVersion,
		PlatformToolsVersion:    bundle.PlatformToolsVersion,
		HealthState:             bundle.HealthState,
		ScreenshotBytes:         boundedUint32(bundle.ScreenshotBytes),
		PreviewBase64:           bundle.PreviewBase64,
		PreviewTruncated:        bundle.PreviewTruncated,
		HierarchySummary:        bundle.HierarchySummary,
		NodeCount:               boundedUint32(bundle.NodeCount),
		MaxDepth:                boundedUint32(bundle.MaxDepth),
		HierarchyComplete:       bundle.HierarchyComplete,
		HierarchyFreshnessToken: bundle.HierarchyFreshnessToken,
		LatencyMs:               bundle.LatencyMs,
		FailureClass:            string(bundle.FailureClass),
		Indeterminate:           bundle.Indeterminate,
		PostconditionVerified:   bundle.PostconditionVerified,
		Events:                  labEventsProto(bundle.Events),
	}
	if bundle.ScreenshotHash != "" {
		projected.Screenshot = &driftv1.ArtifactReference{
			ContentHash:   bundle.ScreenshotHash,
			MediaType:     bundle.ScreenshotMediaType,
			SchemaVersion: labEvidenceSchemaVersion,
		}
	}
	return projected
}

func labEventsProto(events []lab.Event) []*driftv1.LabEvent {
	projected := make([]*driftv1.LabEvent, 0, len(events))
	for _, event := range events {
		projected = append(projected, &driftv1.LabEvent{
			Name:          labEventNameProto(event.Name),
			CorrelationId: event.CorrelationID,
			Serial:        event.Serial,
			OccurredAt:    labTimestampValue(event.OccurredAt),
			FailureClass:  string(event.FailureClass),
			Summary:       event.Summary,
		})
	}
	return projected
}

func labModeProto(mode lab.Mode) driftv1.LabMode {
	switch mode {
	case lab.ModeMock:
		return driftv1.LabMode_LAB_MODE_MOCK
	case lab.ModeLab:
		return driftv1.LabMode_LAB_MODE_LAB
	default:
		return driftv1.LabMode_LAB_MODE_UNSPECIFIED
	}
}

func labReadinessProto(readiness lab.Readiness) driftv1.LabReadiness {
	switch readiness {
	case lab.ReadinessUnavailable:
		return driftv1.LabReadiness_LAB_READINESS_UNAVAILABLE
	case lab.ReadinessReady:
		return driftv1.LabReadiness_LAB_READINESS_READY
	case lab.ReadinessBlocked:
		return driftv1.LabReadiness_LAB_READINESS_BLOCKED
	case lab.ReadinessIndeterminate:
		return driftv1.LabReadiness_LAB_READINESS_INDETERMINATE
	default:
		return driftv1.LabReadiness_LAB_READINESS_UNSPECIFIED
	}
}

func labEventNameProto(name lab.EventName) driftv1.LabEventName {
	switch name {
	case lab.EventAdapterReadiness:
		return driftv1.LabEventName_LAB_EVENT_NAME_ADAPTER_READINESS
	case lab.EventObservationCapture:
		return driftv1.LabEventName_LAB_EVENT_NAME_OBSERVATION_CAPTURE
	case lab.EventUITreeCapture:
		return driftv1.LabEventName_LAB_EVENT_NAME_UI_TREE_CAPTURE
	case lab.EventTransportChange:
		return driftv1.LabEventName_LAB_EVENT_NAME_TRANSPORT_CHANGE
	case lab.EventReadonlyReattach:
		return driftv1.LabEventName_LAB_EVENT_NAME_READONLY_REATTACH
	case lab.EventTimeout:
		return driftv1.LabEventName_LAB_EVENT_NAME_TIMEOUT
	case lab.EventCancellation:
		return driftv1.LabEventName_LAB_EVENT_NAME_CANCELLATION
	case lab.EventCleanup:
		return driftv1.LabEventName_LAB_EVENT_NAME_CLEANUP
	case lab.EventIndeterminateOutcome:
		return driftv1.LabEventName_LAB_EVENT_NAME_INDETERMINATE_OUTCOME
	default:
		return driftv1.LabEventName_LAB_EVENT_NAME_UNSPECIFIED
	}
}

// labTimestamp renders an optional observation instant. An event that never
// happened stays empty rather than being reported as the zero time.
func labTimestamp(at *time.Time) string {
	if at == nil {
		return ""
	}
	return labTimestampValue(*at)
}

func labTimestampValue(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func boundedUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}
	return uint32(value)
}
