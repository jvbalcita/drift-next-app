package errors_test

import (
	"errors"
	"fmt"
	"testing"

	errplatform "drift.local/drift-next/internal/platform/errors"
)

func TestCodeTaxonomyIsStable(t *testing.T) {
	tests := []struct {
		name string
		code errplatform.Code
		want string
	}{
		{name: "invalid input", code: errplatform.CodeInvalidInput, want: "invalid_input"},
		{name: "not found", code: errplatform.CodeNotFound, want: "not_found"},
		{name: "conflict", code: errplatform.CodeConflict, want: "conflict"},
		{name: "unavailable", code: errplatform.CodeUnavailable, want: "unavailable"},
		{name: "timeout", code: errplatform.CodeTimeout, want: "timeout"},
		{name: "canceled", code: errplatform.CodeCanceled, want: "canceled"},
		{name: "deadline exceeded", code: errplatform.CodeDeadlineExceeded, want: "deadline_exceeded"},
		{name: "policy denied", code: errplatform.CodePolicyDenied, want: "policy_denied"},
		{name: "lease conflict", code: errplatform.CodeLeaseConflict, want: "lease_conflict"},
		{name: "migration dirty", code: errplatform.CodeMigrationDirty, want: "migration_dirty"},
		{name: "migration checksum mismatch", code: errplatform.CodeMigrationChecksumMismatch, want: "migration_checksum_mismatch"},
		{name: "migration locked", code: errplatform.CodeMigrationLocked, want: "migration_locked"},
		{name: "internal", code: errplatform.CodeInternal, want: "internal"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := string(test.code); got != test.want {
				t.Fatalf("Code = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTypedErrorWrapsCauseAndKeepsClientMessageSafe(t *testing.T) {
	cause := errors.New("driver detail includes TEST_ONLY_SECRET_SENTINEL")
	err := fmt.Errorf("operation failed: %w", errplatform.Wrap(errplatform.CodeConflict, "resource is already claimed", cause))

	if got := errplatform.CodeOf(err); got != errplatform.CodeConflict {
		t.Fatalf("CodeOf() = %q, want %q", got, errplatform.CodeConflict)
	}
	var typed *errplatform.Error
	if !errors.As(err, &typed) {
		t.Fatal("errors.As() did not find platform error")
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() did not find diagnostic cause")
	}
	if got := typed.ClientMessage(); got != "resource is already claimed" {
		t.Fatalf("ClientMessage() = %q, want safe message", got)
	}
	if got := err.Error(); got == typed.ClientMessage() || !contains(got, "TEST_ONLY_SECRET_SENTINEL") {
		t.Fatalf("Error() = %q, want diagnostic cause distinct from client message", got)
	}
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
