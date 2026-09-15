// Package lab is the typed application-service boundary for the real-device
// lab slice. It owns authorization, adapter selection, target confirmation,
// timeouts, cancellation, redaction, audit events, failure classification,
// indeterminate outcomes, sanitized evidence references, and postcondition
// verification for read-only Android observation.
//
// The package deliberately does two things it would be easy to skip. It keeps
// candidate discovery separate from operator approval, so nothing here creates
// or registers a canonical device. And it keeps a confirmed target in service
// memory only, so a lab session never mutates the control-plane registry.
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
	// no enumeration has succeeded yet.
	ReadinessUnavailable Readiness = "unavailable"

	// ReadinessReady means a confirmed target was observed usable.
	ReadinessReady Readiness = "ready"

	// ReadinessBlocked means the adapter answered but the session may not
	// observe: no confirmed target, or a target that is not usable.
	ReadinessBlocked Readiness = "blocked"

	// ReadinessIndeterminate means the outcome of the last operation is
	// unknown. It stays indeterminate until an operator clears the target or a
	// later capture with a new idempotency key succeeds.
	ReadinessIndeterminate Readiness = "indeterminate"
)

// EventName is the stable vocabulary of lab audit events.
type EventName string

const (
	EventTargetConfirmation   EventName = "target_confirmation"
	EventAdapterReadiness     EventName = "adapter_readiness"
	EventObservationCapture   EventName = "observation_capture"
	EventUITreeCapture        EventName = "ui_tree_capture"
	EventTransportChange      EventName = "transport_change"
	EventReadonlyReattach     EventName = "readonly_reattach"
	EventTimeout              EventName = "timeout"
	EventCancellation         EventName = "cancellation"
	EventCleanup              EventName = "cleanup"
	EventIndeterminateOutcome EventName = "indeterminate_outcome"
	EventOperatorConfirmation EventName = "operator_confirmation"
)

// Action names one authorized lab operation.
type Action string

const (
	ActionDiscover      Action = "discover"
	ActionConfirmTarget Action = "confirm_target"
	ActionClearTarget   Action = "clear_target"
	ActionCapture       Action = "capture_observation"
)

// ConfirmationLiteral is the generic confirmation phrase accepted only when
// exactly one candidate was discovered. With more than one candidate the
// operator must type the serial itself so no target can be inferred.
const ConfirmationLiteral = "CONFIRM"

// StableIdentityPrefix scopes lab-session identity. A lab identity is stable
// for the session and is never a transport identifier, row number, or address.
const StableIdentityPrefix = "lab:"

// Status is the sanitized projection of the adapter boundary. Timestamps are
// nil when the corresponding observation never happened.
type Status struct {
	Mode                 Mode
	Readiness            Readiness
	AdapterVersion       string
	PlatformToolsVersion string
	ConfirmedSerial      string
	ConfirmedDisplayName string
	StableIdentity       string
	TransportID          string
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
	Discovered           []adb.DiscoveredDevice
}

// Confirmed reports whether an operator has confirmed a target for this
// session. A status is never treated as confirmed because a serial appeared in
// discovery output.
func (s Status) Confirmed() bool { return s.ConfirmedSerial != "" }

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

// ConfirmRequest is an explicit operator approval of one enumerated candidate.
// Reason is required: a confirmation is an auditable decision, not a default.
type ConfirmRequest struct {
	Serial           string
	DisplayName      string
	ConfirmationText string
	OperatorID       string
	Reason           string
}

// CaptureRequest asks for one bounded read-only observation of the confirmed
// target. Serial must match the confirmed target exactly.
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
