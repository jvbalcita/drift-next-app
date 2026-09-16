package execution

import (
	"context"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Defaults for held typed-text references.
const (
	// DefaultTextReferenceTTL is how long a registered reference stays
	// releasable. A reference is meant to be registered and dispatched within
	// one operator action, so the window is short on purpose.
	DefaultTextReferenceTTL = 5 * time.Minute
	// DefaultTextReferenceCapacity bounds how many values one workspace may hold
	// at once, so an operator cannot use the registry as an unbounded store.
	DefaultTextReferenceCapacity = 64
	// maxReferenceWorkspaceLength bounds the scope a reference is registered in.
	maxReferenceWorkspaceLength = 128
)

// TextReferenceRegistry holds the value a typed-text reference names, in memory,
// until it is released at dispatch.
//
// It is the boundary that owns the value. Nothing else in the process holds it:
// it is never written to a database, an artifact, a log or an error, it is
// released at most once, and it expires. A handle that was never registered, was
// already released, or has expired is refused with its own reason, so a typed
// text dispatch is never answered with a stale, substituted or replayed value.
//
// A reference is scoped to the workspace that registered it. A dispatch in
// another workspace cannot release it, and a resolve that names no workspace is
// refused rather than searching every one: an operator-supplied value belongs to
// the workspace whose operator supplied it, and a handle is only unique within
// that scope.
//
// This is deliberately not durable. A reference that survived a restart would
// need somewhere to survive in, and every such place is a place the content
// could be persisted in or rendered from. Losing a reference costs one
// re-entry; leaking one costs a credential.
type TextReferenceRegistry struct {
	mutex    sync.Mutex
	clock    clock.Clock
	ttl      time.Duration
	capacity int
	// values is keyed by workspace and then by handle, so a handle means one
	// thing inside its workspace and nothing at all outside it.
	values map[string]map[string]heldTextReference
}

// heldTextReference is one registered value and the instant it stops being
// releasable.
type heldTextReference struct {
	value     string
	expiresAt time.Time
}

// TextReferenceRegistry is the resolver the typed text primitive is given, so a
// registry that stopped satisfying it would fail to build rather than fail at a
// dispatch.
var _ TextResolver = (*TextReferenceRegistry)(nil)

// NewTextReferenceRegistry returns a registry that releases each value it holds
// at most once, no later than ttl after registration, and never holds more than
// capacity values at a time.
func NewTextReferenceRegistry(now clock.Clock, ttl time.Duration, capacity int) (*TextReferenceRegistry, error) {
	if now == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a text reference registry requires a clock")
	}
	if ttl <= 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a text reference registry requires a positive lifetime")
	}
	if capacity <= 0 {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a text reference registry requires a positive capacity")
	}
	return &TextReferenceRegistry{
		clock:    now,
		ttl:      ttl,
		capacity: capacity,
		values:   make(map[string]map[string]heldTextReference),
	}, nil
}

// Register holds value under handle, inside workspace, until it is released or
// expires.
//
// The handle is the same opaque bounded reference the contract admits, so a
// value is only reachable through a reference the boundary would accept, and the
// value is bounded to what a typed input could carry. A handle holds one value:
// registering it twice is refused rather than silently replacing a value an
// operator is about to dispatch. Capacity is counted per workspace, so one
// workspace cannot spend another's budget.
func (r *TextReferenceRegistry) Register(workspace, handle, value string) error {
	if err := validateReferenceWorkspace(workspace); err != nil {
		return err
	}
	if err := validateTextHandle(handle); err != nil {
		return err
	}
	if value == "" || len(value) > maxInputTypedTextLength {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference value must be non-empty and bounded")
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	held := r.values[workspace]
	if existing, ok := held[handle]; ok {
		if r.clock.Now().Before(existing.expiresAt) {
			return platformerrors.New(platformerrors.CodeConflict, "the text reference handle is already held")
		}
	}
	if len(held) >= r.capacity {
		return platformerrors.New(platformerrors.CodeUnavailable, "the text reference registry is full")
	}
	if held == nil {
		held = make(map[string]heldTextReference)
		r.values[workspace] = held
	}
	held[handle] = heldTextReference{value: value, expiresAt: r.clock.Now().Add(r.ttl)}
	return nil
}

// Resolve releases the value named by one reference in the workspace it belongs
// to. This is what the typed text primitive calls immediately before dispatch,
// and it is the only method that returns the value.
//
// A handle is released once. The second attempt is refused, so a replayed
// request cannot type content that was already typed. A reference registered in
// one workspace is not visible in another, and a resolve that names no workspace
// is refused rather than searching everywhere, so a dispatch cannot reach a value
// its workspace does not own.
//
// Every refusal is a fixed sentence that names no value: these errors are
// rendered into a refusal an operator reads and into the record of the attempt.
func (r *TextReferenceRegistry) Resolve(ctx context.Context, workspace string, reference TextReference) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if err := validateReferenceWorkspace(workspace); err != nil {
		return "", err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	held, ok := r.values[workspace][reference.Handle]
	if !ok {
		return "", platformerrors.New(platformerrors.CodeNotFound, "the text reference is not held")
	}
	delete(r.values[workspace], reference.Handle)
	if len(r.values[workspace]) == 0 {
		delete(r.values, workspace)
	}
	return held.value, nil
}

// Held reports how many references are currently releasable, across every
// workspace. Registering a value and releasing it are the only ways that count
// changes.
func (r *TextReferenceRegistry) Held() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	total := 0
	for _, held := range r.values {
		total += len(held)
	}
	return total
}

// releaseExpired drops every reference whose lifetime has passed. It is called
// under the lock before every read and write, so an expired value cannot be
// released by a dispatch that arrives late.
func (r *TextReferenceRegistry) releaseExpired() {
	now := r.clock.Now()
	for workspace, held := range r.values {
		for handle, reference := range held {
			if !now.Before(reference.expiresAt) {
				delete(held, handle)
			}
		}
		if len(held) == 0 {
			delete(r.values, workspace)
		}
	}
}

// validateReferenceWorkspace requires the scope a reference belongs to. It is a
// required input rather than an optional one: without it a reference would be
// reachable from every workspace, which is the property this scope exists to
// deny.
func validateReferenceWorkspace(workspace string) error {
	if strings.TrimSpace(workspace) == "" || len(workspace) > maxReferenceWorkspaceLength {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference must name the workspace it belongs to")
	}
	return nil
}
