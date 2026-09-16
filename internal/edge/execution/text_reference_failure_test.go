package execution

import (
	"testing"

	"drift.local/drift-next/internal/domain"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// A reference that could not be released is not a transport failure. Nothing
// reached the transport on that path — the registry refused before any device
// command was built — so a record that calls it a transport error sends an
// operator to the device connection while the actual cause sits in the reference
// they named.
//
// The two must be distinguishable, and the classification must not depend on
// which code a failure happens to carry: both of these are unavailable to the
// caller, and one of them is about the reference while the other is about the
// device.
func TestAReferenceFailureIsDistinguishableFromATransportFailure(t *testing.T) {
	reference := textReferenceFailure(platformerrors.CodeUnavailable, "reference-1", textReferenceUnreleased)
	transport := platformerrors.New(platformerrors.CodeUnavailable, "the device command failed")

	if got := failureClassFor(reference); got != domain.FailureReferenceUnreleased {
		t.Fatalf("a reference that could not be released is classified %q, want %q", got, domain.FailureReferenceUnreleased)
	}
	if got := failureClassFor(reference); got == failureClassFor(transport) {
		t.Fatalf("a reference failure and a transport failure share the class %q", got)
	}
	if got := failureClassFor(transport); got != domain.FailureTransport {
		t.Fatalf("a transport failure is classified %q, want %q", got, domain.FailureTransport)
	}
	if !domain.FailureReferenceUnreleased.Valid() {
		t.Fatalf("%q is not part of the shared failure vocabulary", domain.FailureReferenceUnreleased)
	}
	if domain.FailureReferenceUnreleased == domain.FailureTransport {
		t.Fatal("the reference failure class is the transport failure class")
	}
}
