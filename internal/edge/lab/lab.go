// Package lab is the typed application-service boundary for the real-device
// lab slice. It owns authorization, adapter selection, target resolution,
// timeouts, cancellation, redaction, audit events, failure classification,
// indeterminate outcomes, sanitized evidence references, and postcondition
// verification for read-only Android observation.
//
// Capture is device-scoped. Every capture call names the one device it observes
// and is authorized on its own, so the boundary holds no confirmed-target
// lifecycle and no mutable "current target" session state. The service still
// keeps candidates separate from approval: resolving a target enumerates
// attached transports but creates and registers nothing, so a lab session never
// mutates the control-plane registry.
package lab

import (
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
)

// Mode reports which adapter the service is bound to.
type Mode string

const (
	// ModeMock answers from deterministic fixtures and never executes adb.
	ModeMock Mode = "mock"

	// ModeLab answers from a real device through an explicitly configured
	// executable and an explicit operator opt-in.
	ModeLab Mode = "lab"
)

// Readiness is the observed readiness of the adapter boundary. It is never
// inferred from the absence of an error.
type Readiness string

const (
	// ReadinessUnavailable means the adapter itself could not be reached, or
	// no attached candidate has been observed yet.
	ReadinessUnavailable Readiness = "unavailable"

	// ReadinessReady means the adapter answered and the last resolved target
	// was observed usable.
	ReadinessReady Readiness = "ready"

	// ReadinessBlocked means the adapter answered but the last observed
	// transport state of the named target is not usable.
	ReadinessBlocked Readiness = "blocked"

	// ReadinessIndeterminate means the outcome of the last operation is
	// unknown. It stays indeterminate until a later capture with a new
	// idempotency key verifies its postcondition.
	ReadinessIndeterminate Readiness = "indeterminate"
)

// EventName is the stable vocabulary of lab audit events.
type EventName string

const (
	EventAdapterReadiness     EventName = "adapter_readiness"
	EventObservationCapture   EventName = "observation_capture"
	EventUITreeCapture        EventName = "ui_tree_capture"
	EventTransportChange      EventName = "transport_change"
	EventReadonlyReattach     EventName = "readonly_reattach"
	EventTimeout              EventName = "timeout"
	EventCancellation         EventName = "cancellation"
	EventCleanup              EventName = "cleanup"
	EventIndeterminateOutcome EventName = "indeterminate_outcome"
)

// Action names one authorized lab operation.
type Action string

const (
	// ActionDiscover is the read-only enumeration the canonical Network Profile
	// scan uses as its transport source.
	ActionDiscover Action = "discover"

	// ActionCapture is one read-only observation of an explicitly named device.
	ActionCapture Action = "capture_observation"
)

// StableIdentityPrefix scopes lab-session identity. A lab identity is derived
// from the explicitly named serial and is never a transport identifier, row
// number, address, or display name.
const StableIdentityPrefix = "lab:"

// Status is the sanitized projection of the adapter boundary. Timestamps are
// nil when the corresponding observation never happened. It carries no target
// state: a target exists only inside the call that names it.
type Status struct {
	Mode                 Mode
	Readiness            Readiness
	AdapterVersion       string
	PlatformToolsVersion string
	ConnectionState      string
	ConnectionType       string
	LastHealthAt         *time.Time
	LastObservationAt    *time.Time
	LastScreenshotHash   string
	LastHierarchySummary string
	ObservationLatencyMs int64
	FailureClass         domain.FailureClass
	Indeterminate        bool
	CorrelationID        string
	// Discovered is the attached candidate set observed while resolving the
	// most recent target. It is read-only evidence and is never a device
	// registry.
	Discovered []adb.DiscoveredDevice
}

// Event is one sanitized audit record. Summary is bounded redacted prose and
// never carries raw command output, raw hierarchy XML, or credentials.
type Event struct {
	Name          EventName
	CorrelationID string
	Serial        string
	OccurredAt    time.Time
	FailureClass  domain.FailureClass
	Summary       string
}

// CaptureRequest asks for one bounded read-only observation of one explicitly
// named device. Serial is required and must name exactly one attached, usable
// device: the service never infers a target from ambient state, list order,
// display name, address, or row position.
type CaptureRequest struct {
	Serial         string
	IdempotencyKey string
	Timeout        time.Duration
	OperatorID     string
	CorrelationID  string
}

// ObservationBundle is the sanitized result of one capture. Screenshot bytes
// are represented by a content hash; a bounded base64 preview appears only
// when previews are explicitly enabled and the whole image fits the cap.
type ObservationBundle struct {
	Serial                  string
	StableIdentity          string
	CorrelationID           string
	IdempotencyKey          string
	CapturedAt              time.Time
	AdapterVersion          string
	PlatformToolsVersion    string
	HealthState             string
	ScreenshotHash          string
	ScreenshotBytes         int
	ScreenshotMediaType     string
	ScreenshotArtifactID    string
	PreviewBase64           string
	PreviewTruncated        bool
	HierarchySummary        string
	HierarchyArtifactID     string
	NodeCount               int
	MaxDepth                int
	HierarchyComplete       bool
	HierarchyFreshnessToken string
	EvidencePersistFailed   bool
	LatencyMs               int64
	FailureClass            domain.FailureClass
	Indeterminate           bool
	PostconditionVerified   bool
	Events                  []Event
}
