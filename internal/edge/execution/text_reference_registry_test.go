package execution

import (
	"context"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// movingClock is a clock the test advances, so a reference's lifetime can be
// spent without waiting for it.
type movingClock struct {
	now time.Time
}

func (c *movingClock) Now() time.Time           { return c.now }
func (c *movingClock) advance(by time.Duration) { c.now = c.now.Add(by) }

const (
	registryHandle = "reference-1"
	registryValue  = "a-registered-value"

	// registryWorkspace is the workspace a registered value belongs to, and
	// otherWorkspace is one that must not be able to release it.
	registryWorkspace = "workspace-alpha"
	otherWorkspace    = "workspace-beta"
)

func newRegistryFixture(t *testing.T) (*TextReferenceRegistry, *movingClock) {
	t.Helper()
	now := &movingClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)}
	registry, err := NewTextReferenceRegistry(now, DefaultTextReferenceTTL, DefaultTextReferenceCapacity)
	if err != nil {
		t.Fatalf("new text reference registry: %v", err)
	}
	return registry, now
}

func referenceTo(handle, value string) TextReference {
	return TextReference{Handle: handle, Length: uint32(len(value))}
}

// scoped is the context a dispatch made in a workspace runs under. The
// dispatcher applies it to every attempt from the workspace of the request it is
// dispatching.
func scoped(workspace string) context.Context {
	return WithTextReferenceWorkspace(context.Background(), workspace)
}

// A reference is released once. The second release is refused rather than served
// again, so a replayed request cannot type content that was already typed.
func TestAReferenceIsReleasedOnce(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	value, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue))
	if err != nil {
		t.Fatalf("the first release was refused: %v", err)
	}
	if value != registryValue {
		t.Fatal("the released value is not the value that was registered")
	}
	if _, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue)); err == nil {
		t.Fatal("the same reference was released twice")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references still held = %d, want 0", held)
	}
}

// The card's unresolvable reference: a handle nobody registered is refused, and
// it is refused with its own code rather than as a generic failure.
func TestAnUnregisteredReferenceIsRefused(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	_, err := registry.Resolve(scoped(registryWorkspace), referenceTo("never-registered", registryValue))
	if err == nil {
		t.Fatal("an unregistered reference was released")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeNotFound {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeNotFound)
	}
}

// A reference is held inside the workspace that registered it. Another
// workspace cannot release it, even holding the handle, and the refusal must not
// consume it: a refused caller must not be able to spend another workspace's
// value, nor to tell a handle that exists from one that does not.
func TestAReferenceCannotBeReleasedInAnotherWorkspace(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, crossWorkspaceErr := registry.Resolve(scoped(otherWorkspace), referenceTo(registryHandle, registryValue))
	if crossWorkspaceErr == nil {
		t.Fatal("another workspace released a value it did not register")
	}
	_, unknownHandleErr := registry.Resolve(scoped(otherWorkspace), referenceTo("never-registered", registryValue))
	if unknownHandleErr == nil {
		t.Fatal("an unknown handle was released")
	}
	if crossWorkspaceErr.Error() != unknownHandleErr.Error() {
		t.Fatalf("a cross-workspace refusal = %q, an unknown-handle refusal = %q: they must be indistinguishable",
			crossWorkspaceErr.Error(), unknownHandleErr.Error())
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references still held = %d, want 1: a refused cross-workspace release must not consume the value", held)
	}
	value, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue))
	if err != nil {
		t.Fatalf("the owning workspace could not release its own value: %v", err)
	}
	if value != registryValue {
		t.Fatal("the released value is not the value that was registered")
	}
}

// The workspace a release happens in is required, not defaulted. A call path
// that forgot to say which workspace it is dispatching in fails closed instead
// of releasing whichever value happens to carry the handle.
func TestAReleaseWithoutAWorkspaceIsRefused(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	for name, ctx := range map[string]context.Context{"bare context": context.Background(), "no context": nil} {
		if _, err := registry.Resolve(ctx, referenceTo(registryHandle, registryValue)); err == nil {
			t.Fatalf("%s: an unscoped release was served", name)
		}
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references still held = %d, want 1: an unscoped release must not consume the value", held)
	}
}

// A value is filed under a workspace, so a registration that names none — or one
// that is not an identifier — is refused rather than filed somewhere every
// workspace can reach.
func TestARegistrationRequiresAWorkspace(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	for _, workspace := range []string{"", "   ", "with space", "bad;workspace", strings.Repeat("a", maxInputTextHandleLength+1)} {
		if err := registry.Register(workspace, registryHandle, registryValue); err == nil {
			t.Fatalf("a value was registered under workspace %q", workspace)
		}
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: a refused registration holds nothing", held)
	}
}

// A reference that has spent its lifetime is refused, so a dispatch that arrives
// after the operator's action cannot release it late.
func TestAnExpiredReferenceIsRefused(t *testing.T) {
	registry, now := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	now.advance(DefaultTextReferenceTTL)
	if _, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue)); err == nil {
		t.Fatal("an expired reference was released")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references still held = %d, want 0", held)
	}
}

// A dispatch that was cancelled before it dispatched has not used the
// reference: the value stays held, so cancelling does not silently consume it.
func TestACancelledDispatchDoesNotConsumeTheReference(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(scoped(registryWorkspace))
	cancel()
	if _, err := registry.Resolve(ctx, referenceTo(registryHandle, registryValue)); err == nil {
		t.Fatal("a cancelled release was served")
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references still held = %d, want 1: a cancelled dispatch must not consume the value", held)
	}
}

// The refusal is what an operator reads and what the attempt records, so the
// value must not be in it. The handle may be, because only the value is secret.
func TestARefusalNeverCarriesTheValue(t *testing.T) {
	registry, now := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue)); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, releasedErr := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue))

	if err := registry.Register(registryWorkspace, "reference-2", registryValue); err != nil {
		t.Fatalf("register the expiring reference: %v", err)
	}
	now.advance(DefaultTextReferenceTTL)
	_, expiredErr := registry.Resolve(scoped(registryWorkspace), referenceTo("reference-2", registryValue))

	_, neverRegisteredErr := registry.Resolve(scoped(registryWorkspace), referenceTo("never-registered", registryValue))

	_, otherWorkspaceErr := registry.Resolve(scoped(otherWorkspace), referenceTo("never-registered", registryValue))

	for name, err := range map[string]error{"released": releasedErr, "expired": expiredErr, "never registered": neverRegisteredErr, "wrong workspace": otherWorkspaceErr} {
		if err == nil {
			t.Fatalf("%s: want a refusal", name)
		}
		if strings.Contains(err.Error(), registryValue) {
			t.Fatalf("%s: the refusal carries the value it names", name)
		}
	}
}

// The handle rule is the contract's own, so a handle the contract admits is one
// the registry holds and a handle the contract refuses is one it refuses. Two
// copies of this rule would be two chances to disagree about which handles exist.
func TestTheRegistryAgreesWithTheContractAboutHandles(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	handles := []string{
		"reference-1", "ref_2", "a.b:c", "A1",
		"", "   ", "with space", "bad;handle", "slash/handle", "quote'handle", "dollar$handle",
		strings.Repeat("a", maxInputTextHandleLength+1),
	}
	for _, handle := range handles {
		registered := registry.Register(registryWorkspace, handle, registryValue)
		admitted := validateTextReference(TextReference{Handle: handle, Length: 1})
		switch {
		case registered == nil && admitted != nil:
			t.Fatalf("handle %q was registered but the contract refuses it", handle)
		case registered != nil && admitted == nil:
			t.Fatalf("handle %q was refused by the registry but the contract admits it", handle)
		}
	}
}

// A value is bounded to what a typed input could carry, and it cannot be empty:
// an empty reference would dispatch a keystroke sequence for nothing.
func TestTheRegistryRefusesAnEmptyOrUnboundedValue(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, ""); err == nil {
		t.Fatal("an empty value was registered")
	}
	if err := registry.Register(registryWorkspace, registryHandle, strings.Repeat("x", maxInputTypedTextLength+1)); err == nil {
		t.Fatal("a value beyond the typed text bound was registered")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: a refused registration holds nothing", held)
	}
}

// A handle holds one value. Re-registering it is refused rather than silently
// replacing the value an operator is about to dispatch.
func TestAHandleHoldsOneValue(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := registry.Register(registryWorkspace, registryHandle, "a-different-value")
	if err == nil {
		t.Fatal("the same handle was registered twice")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeConflict {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeConflict)
	}
	value, err := registry.Resolve(scoped(registryWorkspace), referenceTo(registryHandle, registryValue))
	if err != nil {
		t.Fatalf("the held reference was lost by the refused registration: %v", err)
	}
	if value != registryValue {
		t.Fatal("the refused registration replaced the held value")
	}
}

// A handle another workspace already holds is not a collision: the two values
// are filed apart, and each workspace releases its own.
func TestTheSameHandleInTwoWorkspacesHoldsTwoValues(t *testing.T) {
	registry, _ := newRegistryFixture(t)
	if err := registry.Register(registryWorkspace, registryHandle, registryValue); err != nil {
		t.Fatalf("register in the first workspace: %v", err)
	}
	if err := registry.Register(otherWorkspace, registryHandle, "the-other-workspaces-value"); err != nil {
		t.Fatalf("register in the second workspace: %v", err)
	}
	if held := registry.Held(); held != 2 {
		t.Fatalf("references held = %d, want 2: each workspace's value is its own", held)
	}
	value, err := registry.Resolve(scoped(otherWorkspace), referenceTo(registryHandle, "the-other-workspaces-value"))
	if err != nil {
		t.Fatalf("release in the second workspace: %v", err)
	}
	if value != "the-other-workspaces-value" {
		t.Fatal("a workspace released the other workspace's value")
	}
}

// The registry cannot be used as an unbounded store, and releasing a value makes
// room for the next one.
func TestTheRegistryStaysWithinItsCapacity(t *testing.T) {
	now := &movingClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)}
	registry, err := NewTextReferenceRegistry(now, DefaultTextReferenceTTL, 2)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(registryWorkspace, "reference-1", registryValue); err != nil {
		t.Fatalf("register the first: %v", err)
	}
	if err := registry.Register(registryWorkspace, "reference-2", registryValue); err != nil {
		t.Fatalf("register the second: %v", err)
	}
	err = registry.Register(registryWorkspace, "reference-3", registryValue)
	if err == nil {
		t.Fatal("the registry grew past its capacity")
	}
	if code := platformerrors.CodeOf(err); code != platformerrors.CodeUnavailable {
		t.Fatalf("refusal code = %q, want %q", code, platformerrors.CodeUnavailable)
	}
	if _, err := registry.Resolve(scoped(registryWorkspace), referenceTo("reference-1", registryValue)); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := registry.Register(registryWorkspace, "reference-3", registryValue); err != nil {
		t.Fatalf("a released reference did not free its capacity: %v", err)
	}
}

// A registry nobody can construct safely is a registry that fails at the first
// dispatch instead of at startup.
func TestTheRegistryRequiresAClockALifetimeAndACapacity(t *testing.T) {
	now := &movingClock{now: time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)}
	cases := map[string]struct {
		clock    clock.Clock
		ttl      time.Duration
		capacity int
	}{
		"no clock":       {clock: nil, ttl: time.Minute, capacity: 1},
		"no lifetime":    {clock: now, ttl: 0, capacity: 1},
		"no capacity":    {clock: now, ttl: time.Minute, capacity: 0},
		"negative ttl":   {clock: now, ttl: -time.Second, capacity: 1},
		"negative slots": {clock: now, ttl: time.Minute, capacity: -1},
	}
	for name, test := range cases {
		if _, err := NewTextReferenceRegistry(test.clock, test.ttl, test.capacity); err == nil {
			t.Fatalf("%s: an unusable registry was constructed", name)
		}
	}
}
