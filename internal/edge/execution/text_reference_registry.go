package execution

import (
	"context"
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
	// DefaultTextReferenceCapacity bounds how many values the registry holds at
	// once, so an operator cannot use it as an unbounded store.
	DefaultTextReferenceCapacity = 64
)

// TextReferenceRegistry holds the value a typed-text reference names, in memory,
// until it is released at dispatch.
//
// It is the boundary that owns the value. Nothing else in the process holds it:
// it is never written to a database, an artifact, a log or an error, it is
// released at most once, and it expires. A handle that was never registered, was
// already released, has expired, or belongs to another workspace is refused, so
// a typed text dispatch is never answered with a stale, substituted, replayed or
// another workspace's value.
//
// Every held value is filed under the workspace that registered it together with
// the handle it was registered under. The workspace is therefore a boundary of
// the registry itself and not only of the request that reads it: a dispatch made
// in one workspace cannot release a value another workspace registered, even
// holding the handle, because the lookup is made inside the dispatching
// workspace. That refusal is the same sentence an unknown handle gets, so a
// caller in the wrong workspace cannot tell a handle that exists from one that
// does not.
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
	values   map[textReferenceKey]heldTextReference
}

// textReferenceKey is what one held value is filed under: the workspace that
// registered it, and the handle it was registered under. It is a struct rather
// than a joined string, so no two workspace/handle pairs can be made to
// collide by a handle that happens to contain a delimiter.
type textReferenceKey struct {
	workspace string
	handle    string
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
		values:   make(map[textReferenceKey]heldTextReference),
	}, nil
}

// Register holds value under handle, in the workspace that will be allowed to
// release it, until it is released or expires.
//
// The handle is the same opaque bounded reference the contract admits, so a
// value is only reachable through a reference the boundary would accept, and the
// value is bounded to what a typed input could carry. A handle holds one value
// per workspace: registering it twice there is refused rather than silently
// replacing a value an operator is about to dispatch.
//
// The workspace is required, and it is validated with the same rule the handle
// is: it is an identifier of the same bounded, unpadded shape, never content.
// A registry entry that named no workspace would be releasable from every one.
func (r *TextReferenceRegistry) Register(workspace, handle, value string) error {
	if err := ValidateTextReferenceWorkspace(workspace); err != nil {
		return err
	}
	if err := validateTextHandle(handle); err != nil {
		return err
	}
	if value == "" || len(value) > maxInputTypedTextLength {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference value must be non-empty and bounded")
	}
	key := textReferenceKey{workspace: workspace, handle: handle}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	if _, held := r.values[key]; held {
		return platformerrors.New(platformerrors.CodeConflict, "the text reference handle is already held")
	}
	if len(r.values) >= r.capacity {
		return platformerrors.New(platformerrors.CodeUnavailable, "the text reference registry is full")
	}
	r.values[key] = heldTextReference{value: value, expiresAt: r.clock.Now().Add(r.ttl)}
	return nil
}

// Resolve releases the value named by one reference, inside the workspace the
// caller's context is scoped to. This is what the typed text primitive calls
// immediately before dispatch, and it is the only method that returns the value.
//
// The workspace scope is not optional and is not defaulted: a caller that names
// no workspace is refused rather than served, so a future call path that forgot
// to say which workspace it is dispatching in fails closed instead of releasing
// whichever value happens to carry the handle. The dispatcher scopes every
// attempt to the workspace of the request it is dispatching (see WithTextReferenceWorkspace).
//
// A handle is released once. The second attempt is refused, so a replayed request
// cannot type content that was already typed, and an expired reference is dropped
// before it is looked for, so a dispatch arriving after the window is refused
// rather than served. A release attempted in another workspace is refused before
// anything is dropped: a refused caller must not be able to consume, or even to
// learn about, another workspace's value.
//
// Every refusal is a fixed sentence that names no value: these errors are
// rendered into a refusal an operator reads and into the record of the attempt.
func (r *TextReferenceRegistry) Resolve(ctx context.Context, reference TextReference) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	workspace, scoped := textReferenceWorkspaceFrom(ctx)
	if !scoped {
		return "", platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference can only be released inside the workspace it was registered in")
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	held, ok := r.values[textReferenceKey{workspace: workspace, handle: reference.Handle}]
	if !ok {
		return "", platformerrors.New(platformerrors.CodeNotFound, "the text reference is not held")
	}
	delete(r.values, textReferenceKey{workspace: workspace, handle: reference.Handle})
	return held.value, nil
}

// Held reports how many references are currently releasable, across every
// workspace. Registering a value and releasing it are the only ways that count
// changes.
func (r *TextReferenceRegistry) Held() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.releaseExpired()
	return len(r.values)
}

// releaseExpired drops every reference whose lifetime has passed. It is called
// under the lock before every read and write, so an expired value cannot be
// released by a dispatch that arrives late.
func (r *TextReferenceRegistry) releaseExpired() {
	now := r.clock.Now()
	for key, held := range r.values {
		if !now.Before(held.expiresAt) {
			delete(r.values, key)
		}
	}
}

// validateTextReferenceWorkspace checks the workspace a value is registered in.
//
// The workspace is an identifier of the same shape as a handle — bounded,
// unpadded, with no whitespace — so it is validated with the same rule rather
// than a second copy that could drift from it. It is exported because the
// boundary that admits a registration validates the workspace it was given with
// this same recogniser: one rule, applied where the value enters the process and
// again where it is filed.
func ValidateTextReferenceWorkspace(workspace string) error {
	if workspace == "" || len(workspace) > maxInputTextHandleLength || !textHandlePattern.MatchString(workspace) {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a typed text reference requires a well-formed workspace identity")
	}
	return nil
}

// textReferenceWorkspaceKey scopes a context to the workspace a dispatch is made
// in, so the one registry can hold each workspace's values apart without a
// second registry per workspace and without a value ever being addressed by a
// handle alone.
type textReferenceWorkspaceKey struct{}

// WithTextReferenceWorkspace marks ctx as a request made in workspace. The
// dispatcher applies it to every attempt it runs, from the workspace the request
// names, before anything that could release a value.
func WithTextReferenceWorkspace(ctx context.Context, workspace string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, textReferenceWorkspaceKey{}, workspace)
}

// textReferenceWorkspaceFrom reads the workspace a context is scoped to. An
// unscoped context reports false rather than the empty workspace, so callers
// refuse instead of looking up an empty scope.
func textReferenceWorkspaceFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	workspace, ok := ctx.Value(textReferenceWorkspaceKey{}).(string)
	if !ok || workspace == "" {
		return "", false
	}
	return workspace, true
}
