// Package domain contains only vocabulary shared by multiple domain models.
package domain

import "fmt"

// FailureClass is the stable, transport-independent classification recorded
// for an unsuccessful operation.
type FailureClass string

const (
	FailureDeviceOffline      FailureClass = "device_offline"
	FailureAgentUnhealthy     FailureClass = "agent_unhealthy"
	FailureLeaseConflict      FailureClass = "lease_conflict"
	FailureTimeout            FailureClass = "timeout"
	FailureUnknownScreen      FailureClass = "unknown_screen"
	FailurePostcondition      FailureClass = "postcondition_failed"
	FailurePolicyDenied       FailureClass = "policy_denied"
	FailureOperatorCancelled  FailureClass = "operator_cancelled"
	FailureCleanupFailed      FailureClass = "cleanup_failed"
	FailureTransport          FailureClass = "transport_error"
	FailureObservation        FailureClass = "observation_error"
	FailureInfrastructure     FailureClass = "infrastructure_error"
	FailureIndeterminate      FailureClass = "indeterminate_completion"
	FailureInvalidTransition  FailureClass = "invalid_transition"
	FailureAmbiguousTarget    FailureClass = "ambiguous_target"
	FailureStaleObservation   FailureClass = "stale_observation"
	FailureCapabilityMismatch FailureClass = "capability_mismatch"
)

// Valid reports whether the failure class is part of the shared vocabulary.
func (f FailureClass) Valid() bool {
	switch f {
	case FailureDeviceOffline, FailureAgentUnhealthy, FailureLeaseConflict,
		FailureTimeout, FailureUnknownScreen, FailurePostcondition,
		FailurePolicyDenied, FailureOperatorCancelled, FailureCleanupFailed,
		FailureTransport, FailureObservation, FailureInfrastructure,
		FailureIndeterminate, FailureInvalidTransition, FailureAmbiguousTarget,
		FailureStaleObservation, FailureCapabilityMismatch:
		return true
	default:
		return false
	}
}

// TransitionError identifies an illegal state-machine edge. It is safe to
// expose at a service boundary after mapping it to a stable error code.
type TransitionError struct {
	Resource string
	From     string
	To       string
}

func (e *TransitionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s cannot transition from %q to %q", e.Resource, e.From, e.To)
}

// InvalidTransition constructs the common error used by domain state
// machines. Keeping the error here avoids each model inventing a different
// invalid-transition vocabulary.
func InvalidTransition(resource, from, to string) error {
	return &TransitionError{Resource: resource, From: from, To: to}
}
