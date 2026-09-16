package transportconnect

import (
	"errors"
	"fmt"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The refusal contract at the transport boundary (card ARC-66).
//
// ARC-62 gave the dispatch boundary a refusal vocabulary that is deliberately
// finer grained than the kernel's: the kernel reports one lease/fence conflict
// while an operator needs to know whether the lease expired, the fencing token
// was fenced, the control session ended, or the transport is unusable. That
// vocabulary only helps a client if it survives the transport hop.
//
// It does not survive `MapError` alone. Several distinct reasons share one
// platform code (`lease_missing`, `lease_expired`, `lease_not_held`,
// `fence_stale` and `no_control_session` are all `lease_conflict`), so a
// code-only mapping collapses five different operator situations into one
// Connect code, and a caller cannot tell them apart. The reason therefore
// travels as contract: a typed refusal that names its own reason, carried on the
// error the caller receives.
//
// These tests are the contract's own guard. They are driven from
// `execution.RefusalDefinitions()`, the boundary's single reviewable table, so a
// reason added there without a discriminator here fails this file rather than
// reaching a client as "something went wrong".

func refusalFor(definition execution.RefusalDefinition) *execution.Refusal {
	return &execution.Refusal{
		Reason:       definition.Reason,
		Code:         definition.Code,
		FailureClass: definition.FailureClass,
		Message:      definition.Message,
	}
}

// wrappedRefusal is the shape the boundary's own error chain has: a classified
// refusal that a caller wrapped for diagnostics with %w. The transport must
// still resolve it.
func wrappedRefusal(definition execution.RefusalDefinition) error {
	return fmt.Errorf("dispatch device input: %w", refusalFor(definition))
}

// TestEveryRefusalReasonHasItsOwnTypedDiscriminator is the core of the card: no
// two refusal reasons may collapse into one thing a client can read.
func TestEveryRefusalReasonHasItsOwnTypedDiscriminator(t *testing.T) {
	definitions := execution.RefusalDefinitions()
	if len(definitions) == 0 {
		t.Fatal("the dispatch boundary reports no refusal vocabulary, so this test would pass vacuously")
	}

	seen := make(map[driftv1.DeviceInputRefusalReason]execution.RefusalReason, len(definitions))
	for _, definition := range definitions {
		refusal, ok := deviceInputRefusalFromError(wrappedRefusal(definition))
		if !ok {
			t.Fatalf("the refusal %q was not recognised through the error chain", definition.Reason)
		}
		if refusal.GetReason() == driftv1.DeviceInputRefusalReason_DEVICE_INPUT_REFUSAL_REASON_UNSPECIFIED {
			t.Fatalf("the refusal %q has no typed discriminator: a client cannot tell it from any other refusal", definition.Reason)
		}
		if other, clash := seen[refusal.GetReason()]; clash {
			t.Fatalf("the refusals %q and %q share one discriminator (%s): a client cannot tell them apart",
				other, definition.Reason, refusal.GetReason())
		}
		seen[refusal.GetReason()] = definition.Reason

		if refusal.GetCode() != string(definition.Code) {
			t.Fatalf("the refusal %q carries code %q, want %q", definition.Reason, refusal.GetCode(), definition.Code)
		}
		if refusal.GetFailureClass() != string(definition.FailureClass) {
			t.Fatalf("the refusal %q carries failure class %q, want %q", definition.Reason, refusal.GetFailureClass(), definition.FailureClass)
		}
		if refusal.GetMessage() != definition.Message {
			t.Fatalf("the refusal %q carries message %q, want the boundary's own stable message %q",
				definition.Reason, refusal.GetMessage(), definition.Message)
		}
	}
	if len(seen) != len(definitions) {
		t.Fatalf("the vocabulary has %d reasons but only %d distinct discriminators", len(definitions), len(seen))
	}
}

// TestAClassifiedRefusalNeverAnswersWithTheGenericMessage is the "never the
// generic internal fallback" requirement, asserted for every reason rather than
// for one example.
func TestAClassifiedRefusalNeverAnswersWithTheGenericMessage(t *testing.T) {
	for _, definition := range execution.RefusalDefinitions() {
		t.Run(string(definition.Reason), func(t *testing.T) {
			err := mapDeviceInputError(wrappedRefusal(definition))
			if err == nil {
				t.Fatal("a refused device input was reported as a success")
			}
			var connectErr *connectrpc.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("a refusal produced %T, want a Connect error", err)
			}
			if connectErr.Message() == genericInternalMessage {
				t.Fatalf("the refusal %q fell through to the generic internal message %q", definition.Reason, genericInternalMessage)
			}
			if connectErr.Message() != definition.Message {
				t.Fatalf("the refusal %q reports %q, want the boundary's own stable message %q",
					definition.Reason, connectErr.Message(), definition.Message)
			}

			details := connectErr.Details()
			if len(details) != 1 {
				t.Fatalf("the refusal %q carries %d error details, want exactly 1 typed refusal", definition.Reason, len(details))
			}
			value, valueErr := details[0].Value()
			if valueErr != nil {
				t.Fatalf("the refusal detail for %q does not resolve: %v", definition.Reason, valueErr)
			}
			refusal, ok := value.(*driftv1.DeviceInputRefusal)
			if !ok {
				t.Fatalf("the refusal detail for %q is %T, want *driftv1.DeviceInputRefusal", definition.Reason, value)
			}
			if refusal.GetReason() != deviceInputRefusalReason(definition.Reason) {
				t.Fatalf("the refusal detail for %q names %s", definition.Reason, refusal.GetReason())
			}
		})
	}
}

// TestTheRefusalDetailDoesNotChangeAnOrdinaryFailure pins the other half: the
// refusal path must not swallow or reinterpret a failure that is not a refusal.
// A classified failure that is not a refusal still maps through the boundary's
// existing mapper, and an unclassified one still answers with the generic
// message.
func TestTheRefusalDetailDoesNotChangeAnOrdinaryFailure(t *testing.T) {
	classified := platformerrors.New(platformerrors.CodePolicyDenied, "the policy denies this action")
	mapped := mapDeviceInputError(classified)
	var connectErr *connectrpc.Error
	if !errors.As(mapped, &connectErr) {
		t.Fatalf("a classified non-refusal failure produced %T, want a Connect error", mapped)
	}
	if connectErr.Code() != connectrpc.CodePermissionDenied {
		t.Fatalf("a policy denial mapped to %v, want %v", connectErr.Code(), connectrpc.CodePermissionDenied)
	}
	if details := connectErr.Details(); len(details) != 0 {
		t.Fatalf("a non-refusal failure carries %d refusal details, want none", len(details))
	}

	unclassified := errors.New("something nobody classified")
	mappedUnclassified := mapDeviceInputError(unclassified)
	if !errors.As(mappedUnclassified, &connectErr) {
		t.Fatalf("an unclassified failure produced %T, want a Connect error", mappedUnclassified)
	}
	if connectErr.Message() != genericInternalMessage {
		t.Fatalf("an unclassified failure reports %q, want the generic internal message %q", connectErr.Message(), genericInternalMessage)
	}
}
