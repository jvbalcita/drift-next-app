// Package errors defines stable application error codes and safe client
// messages independent of any transport protocol.
package errors

import (
	stderrors "errors"
	"fmt"
)

// Code classifies an application failure at a transport-independent boundary.
type Code string

const (
	CodeInvalidInput              Code = "invalid_input"
	CodeNotFound                  Code = "not_found"
	CodeConflict                  Code = "conflict"
	CodeUnavailable               Code = "unavailable"
	CodeTimeout                   Code = "timeout"
	CodeCanceled                  Code = "canceled"
	CodeDeadlineExceeded          Code = "deadline_exceeded"
	CodePolicyDenied              Code = "policy_denied"
	CodeLeaseConflict             Code = "lease_conflict"
	CodeStaleObservation          Code = "stale_observation"
	CodeAmbiguousTarget           Code = "ambiguous_target"
	CodeCapabilityMismatch        Code = "capability_mismatch"
	CodePreconditionFailed        Code = "precondition_failed"
	CodePostconditionFailed       Code = "postcondition_failed"
	CodeIndeterminateCompletion   Code = "indeterminate_completion"
	CodeEmergencyStopped          Code = "emergency_stopped"
	CodeCleanupFailed             Code = "cleanup_failed"
	CodeMigrationDirty            Code = "migration_dirty"
	CodeMigrationChecksumMismatch Code = "migration_checksum_mismatch"
	CodeMigrationLocked           Code = "migration_locked"
	CodeInternal                  Code = "internal"
)

// Error is a classified failure with a safe client-facing message and an
// optional diagnostic cause. The cause is available to internal callers via
// errors.Is/errors.As but is never included by ClientMessage.
type Error struct {
	code          Code
	clientMessage string
	cause         error
}

// New creates a classified error without a diagnostic cause.
func New(code Code, clientMessage string) *Error {
	return &Error{code: code, clientMessage: clientMessage}
}

// Wrap creates a classified error while retaining cause for diagnostics.
func Wrap(code Code, clientMessage string, cause error) *Error {
	return &Error{code: code, clientMessage: clientMessage, cause: cause}
}

// Error returns a diagnostic string for logs and internal error reports.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause == nil {
		return fmt.Sprintf("%s: %s", e.code, e.clientMessage)
	}
	return fmt.Sprintf("%s: %s: %v", e.code, e.clientMessage, e.cause)
}

// Unwrap exposes the diagnostic cause to errors.Is and errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Code returns the stable classification.
func (e *Error) Code() Code {
	if e == nil {
		return ""
	}
	return e.code
}

// ClientMessage returns only the safe message intended for an operator or
// API client.
func (e *Error) ClientMessage() string {
	if e == nil {
		return ""
	}
	return e.clientMessage
}

// CodeOf extracts the first platform error from err. Unclassified failures
// are intentionally mapped to internal so callers fail closed.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var typed *Error
	if stderrors.As(err, &typed) {
		return typed.Code()
	}
	return CodeInternal
}
