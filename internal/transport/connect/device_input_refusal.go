package transportconnect

import (
	"log"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/execution"
)

// The refusal contract at the transport boundary (card ARC-66).
//
// ARC-62 gave the dispatch boundary a refusal vocabulary deliberately finer
// grained than the kernel's: the kernel reports one lease/fence conflict, while
// an operator needs to know whether the lease expired, the fencing token was
// fenced, the control session ended, or the transport is unusable. That
// vocabulary only helps a client if it survives the transport hop.
//
// It does not survive `MapError` alone. Five distinct reasons share one platform
// code — `lease_missing`, `lease_expired`, `lease_not_held`, `fence_stale` and
// `no_control_session` are all `lease_conflict` — so a code-only mapping
// collapses five different operator situations into one Connect code. The reason
// therefore travels as contract: a typed refusal that names its own reason,
// carried as an error detail on the error the caller receives.

// deviceInputRefusalReasons binds the dispatch boundary's refusal vocabulary to
// the contract's typed discriminator.
//
// It is a table rather than a switch so that a reason added to the boundary
// shows up as a missing entry to a test walking the whole vocabulary, rather
// than answering UNSPECIFIED and reaching a client as "refused, somehow".
var deviceInputRefusalReasons = map[execution.RefusalReason]driftv1.DeviceInputRefusalReason{
	execution.RefusalLeaseMissing:            driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_LEASE_MISSING,
	execution.RefusalLeaseExpired:            driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_LEASE_EXPIRED,
	execution.RefusalLeaseReleased:           driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_LEASE_RELEASED,
	execution.RefusalLeaseNotHeld:            driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_LEASE_NOT_HELD,
	execution.RefusalFenceStale:              driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_FENCE_STALE,
	execution.RefusalNoControlSession:        driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_NO_CONTROL_SESSION,
	execution.RefusalLeaseConflict:           driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_LEASE_CONFLICT,
	execution.RefusalEmergencyStop:           driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_EMERGENCY_STOP,
	execution.RefusalPolicyDenied:            driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_POLICY_DENIED,
	execution.RefusalCapabilityMismatch:      driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_CAPABILITY_MISMATCH,
	execution.RefusalDeviceOffline:           driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_DEVICE_OFFLINE,
	execution.RefusalDeviceUnauthorized:      driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_DEVICE_UNAUTHORIZED,
	execution.RefusalDeviceUnavailable:       driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_DEVICE_UNAVAILABLE,
	execution.RefusalDuplicateIdempotencyKey: driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_DUPLICATE_IDEMPOTENCY_KEY,
}

// deviceInputRefusalReason resolves one boundary reason to its typed
// discriminator. An unknown reason resolves to UNSPECIFIED, which the mapping
// test refuses to accept for any reason the boundary actually publishes.
func deviceInputRefusalReason(reason execution.RefusalReason) driftv1.DeviceInputRefusalReason {
	if mapped, ok := deviceInputRefusalReasons[reason]; ok {
		return mapped
	}
	return driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_UNSPECIFIED
}

// deviceInputRefusalFromError resolves a refused device input through the whole
// error chain — a refusal wrapped for diagnostics with `%w` still resolves — and
// renders it as the contract's typed refusal. It reports false for every error
// that is not a refusal.
func deviceInputRefusalFromError(err error) (*driftv1.DeviceInputRefusal, bool) {
	refusal, ok := execution.RefusalOf(err)
	if !ok {
		return nil, false
	}
	return deviceInputRefusalMessage(refusal), true
}

// deviceInputRefusalMessage renders one refusal in the contract's terms. The code
// and failure class mirror the boundary's own stable vocabulary, and the message
// is the boundary's fixed operator-facing sentence for that reason — never a
// device, transport or stack-trace diagnostic.
func deviceInputRefusalMessage(refusal *execution.Refusal) *driftv1.DeviceInputRefusal {
	if refusal == nil {
		return nil
	}
	return &driftv1.DeviceInputRefusal{
		Reason:       deviceInputRefusalReason(refusal.Reason),
		Code:         string(refusal.Code),
		FailureClass: string(refusal.FailureClass),
		Message:      refusal.Message,
	}
}

// mapDeviceInputError is the error mapper a device input handler returns
// through. A refusal keeps its own stable code, its own message and its own
// reason, and never answers with the generic internal message; every other
// failure goes through the shared mapper unchanged, so this boundary adds
// nothing to how an ordinary failure is reported.
func mapDeviceInputError(err error) error {
	if err == nil {
		return nil
	}
	refusal, ok := execution.RefusalOf(err)
	if !ok {
		return MapError(err)
	}
	connectErr := connectrpc.NewError(connectCodeFor(refusal.Code), &safeError{message: refusal.Message})
	detail, detailErr := connectrpc.NewErrorDetail(deviceInputRefusalMessage(refusal))
	if detailErr != nil {
		// The refusal keeps its own code and its own stable message; only the
		// typed detail could not be attached. That is recorded rather than
		// swallowed, and it is never a downgrade to the generic internal message:
		// a client still sees a refusal and its code, just without the reason.
		log.Printf("event=device_input_refusal_detail_failed reason=%s diagnostic=%q", refusal.Reason, detailErr.Error())
		return connectErr
	}
	connectErr.AddDetail(detail)
	return connectErr
}
