package transportconnect_test

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestMapErrorUsesSafeClientMessageWithoutDiagnosticCause(t *testing.T) {
	mapped := transportconnect.MapError(platformerrors.Wrap(
		platformerrors.CodeInternal,
		"request could not be completed",
		errors.New("diagnostic: [REDACTED] private subprocess output"),
	))

	if connectrpc.CodeOf(mapped) != connectrpc.CodeInternal {
		t.Fatalf("MapError() code = %v, want %v", connectrpc.CodeOf(mapped), connectrpc.CodeInternal)
	}
	if strings.Contains(mapped.Error(), "private subprocess") {
		t.Fatalf("MapError() leaked diagnostic cause: %q", mapped.Error())
	}
}

// Every classified platform failure keeps its exact Connect code and safe
// message. The migration codes have no dedicated Connect mapping today and
// intentionally stay internal; they are pinned here so a later change to that
// mapping is deliberate rather than accidental.
func TestMapErrorPreservesEveryPlatformErrorCodeMapping(t *testing.T) {
	tests := []struct {
		code platformerrors.Code
		want connectrpc.Code
	}{
		{code: platformerrors.CodeInvalidInput, want: connectrpc.CodeInvalidArgument},
		{code: platformerrors.CodeNotFound, want: connectrpc.CodeNotFound},
		{code: platformerrors.CodeConflict, want: connectrpc.CodeAlreadyExists},
		{code: platformerrors.CodeLeaseConflict, want: connectrpc.CodeAlreadyExists},
		{code: platformerrors.CodeUnavailable, want: connectrpc.CodeUnavailable},
		{code: platformerrors.CodeMigrationLocked, want: connectrpc.CodeUnavailable},
		{code: platformerrors.CodeTimeout, want: connectrpc.CodeDeadlineExceeded},
		{code: platformerrors.CodeDeadlineExceeded, want: connectrpc.CodeDeadlineExceeded},
		{code: platformerrors.CodeCanceled, want: connectrpc.CodeCanceled},
		{code: platformerrors.CodePolicyDenied, want: connectrpc.CodePermissionDenied},
		{code: platformerrors.CodeCapabilityMismatch, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodePreconditionFailed, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodePostconditionFailed, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodeStaleObservation, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodeAmbiguousTarget, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodeCleanupFailed, want: connectrpc.CodeFailedPrecondition},
		{code: platformerrors.CodeEmergencyStopped, want: connectrpc.CodeAborted},
		{code: platformerrors.CodeIndeterminateCompletion, want: connectrpc.CodeUnknown},
		{code: platformerrors.CodeInternal, want: connectrpc.CodeInternal},
		{code: platformerrors.CodeMigrationDirty, want: connectrpc.CodeInternal},
		{code: platformerrors.CodeMigrationChecksumMismatch, want: connectrpc.CodeInternal},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			message := "safe operator message for " + string(test.code)
			mapped := transportconnect.MapError(platformerrors.Wrap(test.code, message, errors.New("diagnostic: [REDACTED] private detail")))
			if got := connectrpc.CodeOf(mapped); got != test.want {
				t.Fatalf("MapError(%q) code = %v, want %v", test.code, got, test.want)
			}
			if !strings.Contains(mapped.Error(), message) {
				t.Fatalf("MapError(%q) message = %q, want it to contain %q", test.code, mapped.Error(), message)
			}
			if strings.Contains(mapped.Error(), "private detail") {
				t.Fatalf("MapError(%q) leaked its diagnostic cause: %q", test.code, mapped.Error())
			}
		})
	}
}

// An unclassified persistence failure — the shape of the incident where a
// stale binary wrote columns a newer migration had removed — still returns the
// generic client message, but it must leave a diagnosable server-side record.
func TestMapErrorReportsUnmappedFailureServerSideWithoutLeakingItToTheClient(t *testing.T) {
	var logged bytes.Buffer
	captureServerLog(t, &logged)

	mapped := unmappedPersistenceFailure()

	if got := connectrpc.CodeOf(mapped); got != connectrpc.CodeInternal {
		t.Fatalf("MapError() code = %v, want %v", got, connectrpc.CodeInternal)
	}
	if !strings.Contains(mapped.Error(), "request could not be completed") {
		t.Fatalf("MapError() message = %q, want the generic safe message", mapped.Error())
	}
	if strings.Contains(mapped.Error(), "no such column") {
		t.Fatalf("MapError() leaked the persistence failure to the client: %q", mapped.Error())
	}

	entry := logged.String()
	for _, want := range []string{
		"event=transport_unmapped_error",
		"class=*errors.errorString",
		"no such column: state",
	} {
		if !strings.Contains(entry, want) {
			t.Fatalf("server-side diagnostic = %q, want it to contain %q", entry, want)
		}
	}
	operation := diagnosticField(entry, "operation=")
	if operation == "" {
		t.Fatalf("server-side diagnostic = %q, want an operation field", entry)
	}
	if !strings.Contains(operation, "connect_test.") || strings.Contains(operation, "MapError") {
		t.Fatalf("operation = %q, want the caller that produced the failure, not the mapper itself", operation)
	}
}

func TestMapErrorRedactsAndBoundsServerSideDiagnostics(t *testing.T) {
	var logged bytes.Buffer
	captureServerLog(t, &logged)

	transportconnect.MapError(errors.New("driver failure token=TEST_ONLY_TOKEN_SENTINEL " + strings.Repeat("x", 4096)))

	entry := logged.String()
	if strings.Contains(entry, "TEST_ONLY_TOKEN_SENTINEL") {
		t.Fatalf("server-side diagnostic leaked a credential-shaped value: %q", entry)
	}
	if !strings.Contains(entry, redaction.Replacement) {
		t.Fatalf("server-side diagnostic = %q, want redacted credential material", entry)
	}
	if len(entry) > 1024 {
		t.Fatalf("server-side diagnostic is unbounded: %d bytes", len(entry))
	}
}

// unmappedPersistenceFailure maps the raw driver failure that reached the
// transport boundary during the incident: shaped like a write against columns a
// newer migration removed, and not a classified platform error.
func unmappedPersistenceFailure() error {
	return transportconnect.MapError(errors.New("SQL logic error: no such column: state (1)"))
}

// captureServerLog redirects the standard library logger that carries
// server-side diagnostics into buffer for the duration of one test.
func captureServerLog(t *testing.T, buffer *bytes.Buffer) {
	t.Helper()
	original := log.Writer()
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(original) })
}

// diagnosticField returns the value of one key=value field in a diagnostic
// line, or an empty string when the field is absent.
func diagnosticField(line string, key string) string {
	index := strings.Index(line, key)
	if index < 0 {
		return ""
	}
	value := line[index+len(key):]
	if end := strings.IndexAny(value, " \n"); end >= 0 {
		value = value[:end]
	}
	return value
}
