package transportconnect

import (
	"errors"
	"log"
	"runtime"
	"strings"
	"unicode/utf8"

	connectrpc "connectrpc.com/connect"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	// unmappedErrorEvent is the stable event name recorded for an unclassified
	// failure that can only answer with the generic internal message.
	unmappedErrorEvent = "transport_unmapped_error"

	// maxDiagnosticErrorBytes bounds one server-side diagnostic record so a
	// single failure cannot flood the operator log.
	maxDiagnosticErrorBytes = 512
)

// MapError converts a transport-independent platform failure into a stable
// Connect code and safe client message. Wrapped diagnostic causes are never
// returned to callers.
//
// The classification is resolved through the error chain rather than by a
// direct type assertion: a platform failure that a caller wrapped for
// diagnostics with fmt.Errorf("...: %w", err) still carries a code and a safe
// client message, and losing them would silently downgrade a classified
// failure to the generic internal answer. The marker error in the chain is a
// *platformerrors.Error, whose type is fixed by the platform errors package,
// so a wrapped error can never forge a classification.
//
// A failure with no platform error in its chain still answers with the generic
// internal message, but it is also recorded server-side with its error class,
// the calling operation, and a redacted, bounded diagnostic. Without that
// record an unmapped persistence failure is indistinguishable from an operator
// mistake and costs a full diagnosis cycle.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	code := connectrpc.CodeInternal
	message := "request could not be completed"
	var typed *platformerrors.Error
	if errors.As(err, &typed) {
		message = typed.ClientMessage()
		switch typed.Code() {
		case platformerrors.CodeInvalidInput:
			code = connectrpc.CodeInvalidArgument
		case platformerrors.CodeNotFound:
			code = connectrpc.CodeNotFound
		case platformerrors.CodeConflict, platformerrors.CodeLeaseConflict:
			code = connectrpc.CodeAlreadyExists
		case platformerrors.CodeUnavailable, platformerrors.CodeMigrationLocked:
			code = connectrpc.CodeUnavailable
		case platformerrors.CodeTimeout, platformerrors.CodeDeadlineExceeded:
			code = connectrpc.CodeDeadlineExceeded
		case platformerrors.CodeCanceled:
			code = connectrpc.CodeCanceled
		case platformerrors.CodePolicyDenied:
			code = connectrpc.CodePermissionDenied
		case platformerrors.CodeCapabilityMismatch,
			platformerrors.CodePreconditionFailed,
			platformerrors.CodePostconditionFailed:
			code = connectrpc.CodeFailedPrecondition
		case platformerrors.CodeStaleObservation, platformerrors.CodeAmbiguousTarget:
			code = connectrpc.CodeFailedPrecondition
		case platformerrors.CodeEmergencyStopped:
			code = connectrpc.CodeAborted
		case platformerrors.CodeIndeterminateCompletion:
			code = connectrpc.CodeUnknown
		case platformerrors.CodeCleanupFailed:
			code = connectrpc.CodeFailedPrecondition
		}
	} else {
		logUnmappedError(err)
	}
	return connectrpc.NewError(code, &safeError{message: message})
}

// logUnmappedError records an unclassified failure at the transport boundary.
// The record carries the operation that produced it, the Go error class, and a
// redacted, length-bounded diagnostic. It never carries a request or device
// payload, and the client still receives only the generic safe message.
//
// The operation name comes from this function's own stack walk rather than a
// second helper: an inlined caller is still reported as its own logical frame,
// so the skip count stays correct for every build.
func logUnmappedError(err error) {
	// Skip this helper's frame and MapError's frame so the record names the
	// operation that produced the failure instead of the mapper.
	operation := "unknown"
	if programCounter, _, _, ok := runtime.Caller(2); ok {
		if function := runtime.FuncForPC(programCounter); function != nil {
			operation = function.Name()
			if index := strings.LastIndex(operation, "/"); index >= 0 {
				operation = operation[index+1:]
			}
		}
	}
	log.Printf("event=%s operation=%s class=%T diagnostic=%q", unmappedErrorEvent, operation, err, boundedDiagnostic(err))
}

// boundedDiagnostic redacts recognized credential material from the diagnostic
// text and truncates it to maxDiagnosticErrorBytes without splitting a rune.
func boundedDiagnostic(err error) string {
	diagnostic := redaction.RedactString(err.Error())
	if len(diagnostic) <= maxDiagnosticErrorBytes {
		return diagnostic
	}
	truncated := diagnostic[:maxDiagnosticErrorBytes]
	for trimmed := 0; trimmed < utf8.UTFMax && len(truncated) > 0 && !utf8.ValidString(truncated); trimmed++ {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "..."
}
