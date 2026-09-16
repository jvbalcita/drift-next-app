package execution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/execution"
)

// registryClock is a clock the test can hold still, so a reference's lifetime is
// spent deliberately rather than by waiting.
type registryClock struct{ now time.Time }

func (c *registryClock) Now() time.Time { return c.now }

func newRegistryForTest(t *testing.T) *execution.TextReferenceRegistry {
	t.Helper()
	registry, err := execution.NewTextReferenceRegistry(
		&registryClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)},
		execution.DefaultTextReferenceTTL,
		execution.DefaultTextReferenceCapacity,
	)
	if err != nil {
		t.Fatalf("new text reference registry: %v", err)
	}
	return registry
}

// What "typed text is dispatchable" means, composed: a registered reference is
// released into exactly one device argument, and it cannot be released a second
// time. Before this resolver existed the primitive had nothing to release a
// reference through, so the kind could only refuse.
func TestATypedTextDispatchReleasesItsValueOnceAsOneToken(t *testing.T) {
	ctx := context.Background()
	registry := newRegistryForTest(t)
	if err := registry.Register("reference-1", typedValueFixture); err != nil {
		t.Fatalf("register the reference: %v", err)
	}
	transport := newFakeDeviceTransport()
	inputs, err := execution.NewInputs(transport, registry, testSerial)
	if err != nil {
		t.Fatalf("new inputs: %v", err)
	}
	reference := execution.TextReference{Handle: "reference-1", Length: uint32(len(typedValueFixture))}

	if err := inputs.TypeText(ctx, execution.TypeTextRequest{Text: reference}); err != nil {
		t.Fatalf("the typed text dispatch was refused: %v", err)
	}
	if calls := transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	call := transport.invocation(0)
	if len(call.args) != 4 {
		t.Fatalf("argument count = %d, want 4: the shape is `shell input text <token>`", len(call.args))
	}
	if token := call.args[3]; token != typedValueFixture {
		t.Fatal("the released value did not become the single argument token")
	}

	if err := inputs.TypeText(ctx, execution.TypeTextRequest{Text: reference}); err == nil {
		t.Fatal("the same reference dispatched a second time")
	}
	if calls := transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls after the refused dispatch = %d, want the count unchanged at 1", calls)
	}
}

// The released value is one argument, never two. A space in typed text is
// carried in the device's own encoding for it, so a value cannot split into an
// extra token and reach the device as something the operator did not type.
func TestATypedTextValueCannotBecomeASecondArgument(t *testing.T) {
	ctx := context.Background()
	registry := newRegistryForTest(t)
	const spaced = "two words"
	if err := registry.Register("reference-spaced", spaced); err != nil {
		t.Fatalf("register the reference: %v", err)
	}
	transport := newFakeDeviceTransport()
	inputs, err := execution.NewInputs(transport, registry, testSerial)
	if err != nil {
		t.Fatalf("new inputs: %v", err)
	}

	reference := execution.TextReference{Handle: "reference-spaced", Length: uint32(len(spaced))}
	if err := inputs.TypeText(ctx, execution.TypeTextRequest{Text: reference}); err != nil {
		t.Fatalf("the typed text dispatch was refused: %v", err)
	}
	call := transport.invocation(0)
	if len(call.args) != 4 {
		t.Fatalf("argument count = %d, want 4: a space in the value must not become another argument", len(call.args))
	}
	if joined := strings.Join(call.args, " "); strings.Contains(joined, " words") || strings.Contains(joined, "words ") {
		t.Fatal("the value split into a second argument")
	}
	if token := call.args[3]; !strings.Contains(token, "two") || !strings.Contains(token, "words") {
		t.Fatal("the single argument did not carry both halves of the value")
	}
}
