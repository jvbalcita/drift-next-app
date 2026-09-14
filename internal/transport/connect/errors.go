package transportconnect

import (
	connectrpc "connectrpc.com/connect"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// MapError converts a transport-independent platform failure into a stable
// Connect code and safe client message. Wrapped diagnostic causes are never
// returned to callers.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	code := connectrpc.CodeInternal
	message := "request could not be completed"
	if typed, ok := err.(*platformerrors.Error); ok {
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
		}
	}
	return connectrpc.NewError(code, &safeError{message: message})
}
