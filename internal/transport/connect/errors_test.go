package transportconnect_test

import (
	"bytes"
	"errors"
	"fmt"
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

// platformErrorCodeMapping pins one platform classification to the Connect
// code an operator console already branches on.
type platformErrorCodeMapping struct {
	code platformerrors.Code
	want connectrpc.Code
}

// Every classified platform failure keeps its exact Connect code and safe
// message. The migration codes have no dedicated Connect mapping today and
// intentionally stay internal; they are pinned here so a later change to that
// mapping is deliberate rather than accidental.
//
// The table is shared with the wrapped-error case so a mapping cannot be
// corrected in one place and silently diverged in the other.
func platformErrorCodeMappings() []platformErrorCodeMapping {
	return []platformErrorCodeMapping{
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
}

// Every unwrapped classification still yields its exact current Connect code
// and client message. This is the regression table for the mapping: the
// wrapped-error case below must agree with it entry for entry.
func TestMapErrorPreservesEveryPlatformErrorCodeMapping(t *testing.T) {
	for _, test := range platformErrorCodeMappings() {
		t.Run(string(test.code), func(t *testing.T) {
			message := "safe operator message for " + string(test.code)
			mapped := transportconnect.MapError(platformerrors.Wrap(test.code, message, errors.New("diagnostic: [REDACTED] private detail")))
			if got := connectrpc.CodeOf(mapped); got != test.want {
				t.Fatalf("MapError(%q) code = %v, want %v", test.code, got, test.want)
			}
			if got, want := mapped.Error(), connectCodeAndMessage(test.want, message); got != want {
				t.Fatalf("MapError(%q) message = %q, want %q", test.code, got, want)
			}
			if strings.Contains(mapped.Error(), "private detail") {
				t.Fatalf("MapError(%q) leaked its diagnostic cause: %q", test.code, mapped.Error())
			}
		})
	}
}

// A platform failure that a caller wrapped for diagnostics with
// fmt.Errorf("...: %w", err) keeps the code and safe message it already carried.
// A direct type assertion does not see through the wrapper, so the failure
// silently degrades to the generic internal answer and the operator loses a
// classification the error actually had.
func TestMapErrorKeepsTypedCodeAndMessageForWrappedPlatformError(t *testing.T) {
	for _, test := range platformErrorCodeMappings() {
		t.Run(string(test.code), func(t *testing.T) {
			message := "safe operator message for " + string(test.code)
			classified := platformerrors.Wrap(test.code, message, errors.New("diagnostic: [REDACTED] private detail"))
			wrapped := fmt.Errorf("list accounts: %w", classified)

			mapped := transportconnect.MapError(wrapped)

			if got := connectrpc.CodeOf(mapped); got != test.want {
				t.Fatalf("MapError(wrapped %q) code = %v, want %v", test.code, got, test.want)
			}
			if got, want := mapped.Error(), connectCodeAndMessage(test.want, message); got != want {
				t.Fatalf("MapError(wrapped %q) message = %q, want %q", test.code, got, want)
			}
			if strings.Contains(mapped.Error(), "private detail") {
				t.Fatalf("MapError(wrapped %q) leaked its diagnostic cause: %q", test.code, mapped.Error())
			}
		})
	}
}

// Wrapping more than once must not change the answer either: the mapper
// resolves the classification wherever it sits in the chain.
func TestMapErrorResolvesPlatformErrorThroughNestedWrappers(t *testing.T) {
	classified := platformerrors.New(platformerrors.CodePolicyDenied, "operator is not authorized for this device")
	nested := fmt.Errorf("dispatch action: %w", fmt.Errorf("actor refused execution: %w", classified))

	mapped := transportconnect.MapError(nested)

	if got := connectrpc.CodeOf(mapped); got != connectrpc.CodePermissionDenied {
		t.Fatalf("MapError(nested wrapped) code = %v, want %v", got, connectrpc.CodePermissionDenied)
	}
	if got, want := mapped.Error(), connectCodeAndMessage(connectrpc.CodePermissionDenied, "operator is not authorized for this device"); got != want {
		t.Fatalf("MapError(nested wrapped) message = %q, want %q", got, want)
	}
}

// A classified failure is not an unclassified one: resolving it through the
// chain must not also record it as a transport-unmapped failure.
func TestMapErrorDoesNotReportClassifiedWrappedFailureAsUnmapped(t *testing.T) {
	var logged bytes.Buffer
	captureServerLog(t, &logged)

	transportconnect.MapError(fmt.Errorf("list accounts: %w", platformerrors.New(platformerrors.CodeNotFound, "account source was not found")))

	if entry := logged.String(); strings.Contains(entry, "event=transport_unmapped_error") {
		t.Fatalf("server-side diagnostic = %q, want no unmapped-failure record for a classified failure", entry)
	}
}

// An unclassified failure stays unclassified even when it is itself wrapped:
// it keeps the generic client message and is still recorded server-side with
// its error class and a redacted, bounded diagnostic.
func TestMapErrorKeepsGenericFallbackForUnclassifiedWrappedFailure(t *testing.T) {
	var logged bytes.Buffer
	captureServerLog(t, &logged)

	mapped := transportconnect.MapError(fmt.Errorf("list accounts: %w", errors.New("SQL logic error: no such column: state (1)")))

	if got := connectrpc.CodeOf(mapped); got != connectrpc.CodeInternal {
		t.Fatalf("MapError(unclassified wrapped) code = %v, want %v", got, connectrpc.CodeInternal)
	}
	if !strings.Contains(mapped.Error(), "request could not be completed") {
		t.Fatalf("MapError(unclassified wrapped) message = %q, want the generic safe message", mapped.Error())
	}
	if strings.Contains(mapped.Error(), "no such column") {
		t.Fatalf("MapError(unclassified wrapped) leaked the persistence failure to the client: %q", mapped.Error())
	}
	entry := logged.String()
	for _, want := range []string{
		"event=transport_unmapped_error",
		"class=*fmt.wrapError",
		"no such column: state",
	} {
		if !strings.Contains(entry, want) {
			t.Fatalf("server-side diagnostic = %q, want it to contain %q", entry, want)
		}
	}
}

// connectCodeAndMessage is the exact client-visible string of a mapped
// failure: the Connect error text is "code: message".
func connectCodeAndMessage(code connectrpc.Code, message string) string {
	return code.String() + ": " + message
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
