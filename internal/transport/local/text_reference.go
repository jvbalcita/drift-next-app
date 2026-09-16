// Package local carries the control plane's narrow local HTTP surface: the one
// boundary at which operator-typed content enters the process.
//
// It is deliberately not a Connect service, and that is the whole design. A
// generated message renders every populated field in its string, text, JSON and
// debug forms, so a value carried in a message field is rendered the first time
// anyone formats the request — a log line, an error, a panic dump or a debugger
// (AGENTS.md section 9). protobuf-go offers no per-field redaction to prevent
// that: the field option exists in the descriptor and the runtime reads nothing
// from it. So this boundary has no message at all. The value is the request
// body, the body is read once into a bounded buffer local to one call, the value
// is handed straight to the registry that owns it, and the buffer is cleared.
// There is no struct for anyone to format, which is a stronger property than any
// promise not to format one.
//
// What the boundary returns is the payload type below: an opaque handle and the
// length of what is held, and no field in which the value could travel. That it
// has no such field is asserted structurally rather than promised — see
// TestThePayloadTypeHasNoFieldTheValueCouldTravelIn.
package local

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Path is the route the control plane mounts this boundary at. It is a path of
// its own rather than an RPC: an RPC's request would be a generated message, and
// a generated message is exactly what the value must never be in.
const Path = "/drift/v1/local/text-reference"

const (
	// MaxValueBytes is the largest value this boundary will admit. It is the
	// typed input contract's own bound, read from the package that states it, so
	// the surface and the resolver cannot disagree about how long a value may
	// be. A larger body is refused, never truncated: a truncated value is
	// content the operator did not type.
	MaxValueBytes = execution.MaxTypedTextValueLength

	// workspaceQuery is the query parameter a registration names its workspace
	// in. The workspace is not secret and travels in the URL; the value never
	// does, because a URL is the first thing anything logs. The workspace's own
	// shape is validated by execution.ValidateTextReferenceWorkspace, the one
	// recogniser for it.
	workspaceQuery = "workspace"
)

// Fixed refusals. Every one of them is a sentence this boundary owns: none is
// built from the request, and none can carry a value by construction. The
// registry's own errors are never rendered here — only their classification is
// read — so a future error text cannot arrive in this answer either.
const (
	refusalNotConstructed = "the text reference surface is not constructed"
	refusalMethod         = "a text reference registration is submitted as a POST"
	refusalWorkspace      = "a text reference registration requires exactly one well-formed workspace"
	refusalEmpty          = "a text reference registration requires a non-empty value"
	refusalUnreadable     = "the text reference value could not be read"
	refusalTooLarge       = "the text reference value is larger than this boundary accepts"
	refusalNoHandle       = "a text reference handle could not be assigned, so nothing was registered"
	refusalUnregistered   = "the text reference could not be registered"
	refusalFull           = "the text reference registry is full"
	refusalConflict       = "the text reference handle is already held"
)

// TextReferenceRegistrar holds a value under a handle, for one workspace, and is
// the only thing this boundary hands a value to.
// *execution.TextReferenceRegistry satisfies it.
type TextReferenceRegistrar interface {
	Register(workspace, handle, value string) error
}

// HandleSource mints the opaque handle a value is registered under. An identity
// source is required rather than optional: a handle the boundary invented itself
// would be a second, weaker source of the same thing.
type HandleSource interface {
	NewID() (string, error)
}

// TextReferenceRegistration is the entire payload this boundary answers with: the
// opaque handle the value was registered under, and how long the held value is.
// Those are the two facts a caller needs to name the value in a typed-text
// reference, and they are all it gets.
//
// There is no field here in which the value could travel, and that is the
// property this type exists to have: it is not a promise that nothing renders
// the payload, it is the absence of anywhere for the value to be. A handle is
// opaque by the same pattern the contract already enforces, so the string field
// carries a reference and never content.
type TextReferenceRegistration struct {
	Handle string `json:"handle"`
	Length uint32 `json:"length"`
}

// TextReferenceHandler is the boundary where a typed-text value enters the
// process. It holds nothing of what it registers: the value is a local buffer
// for the duration of one call, the handler keeps no copy, no cache and no
// last-seen state, and nothing on this path logs.
type TextReferenceHandler struct {
	registrar TextReferenceRegistrar
	handles   HandleSource
}

// NewTextReferenceHandler binds the boundary to the registry that will hold the
// value and the source of the handle it is held under. It returns nil for either
// dependency being absent — including a typed nil, which `var r *Registry;
// NewTextReferenceHandler(r, ids)` would otherwise smuggle past a plain nil
// check — so the composition root mounts no route rather than a route that can
// only answer with a refusal.
func NewTextReferenceHandler(registrar TextReferenceRegistrar, handles HandleSource) *TextReferenceHandler {
	if absent(registrar) || absent(handles) {
		return nil
	}
	return &TextReferenceHandler{registrar: registrar, handles: handles}
}

// ServeHTTP reads one value from the request body and registers it.
//
// The order is the contract: the caller is identified by its workspace, the body
// is read under an explicit maximum, emptiness is refused, the value is bound to
// a freshly minted handle and handed to the registry, and the caller is answered
// with the handle and the length. The value is never returned, echoed, logged,
// or kept: the buffer it was read into is cleared before this returns, and the
// one copy that must exist is the one the registry holds.
func (h *TextReferenceHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.registrar == nil || h.handles == nil {
		refuse(writer, http.StatusServiceUnavailable, refusalNotConstructed)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		refuse(writer, http.StatusMethodNotAllowed, refusalMethod)
		return
	}
	workspace, workspaceRefusal := registrationWorkspace(request)
	if workspaceRefusal != "" {
		refuse(writer, http.StatusBadRequest, workspaceRefusal)
		return
	}

	// The value is the body and nothing else. It is read into a buffer that
	// belongs to this call — not a field of this handler, not a message, not a
	// cache — under a maximum the reader enforces by refusing rather than by
	// truncating.
	value, readErr := readBoundedValue(writer, request)
	defer zeroBytes(value)
	if readErr != nil {
		if errors.Is(readErr, errValueTooLarge) {
			refuse(writer, http.StatusRequestEntityTooLarge, refusalTooLarge)
			return
		}
		refuse(writer, http.StatusBadRequest, refusalUnreadable)
		return
	}
	if len(value) == 0 {
		refuse(writer, http.StatusBadRequest, refusalEmpty)
		return
	}

	handle, handleErr := h.handles.NewID()
	if handleErr != nil {
		refuse(writer, http.StatusServiceUnavailable, refusalNoHandle)
		return
	}
	if registerErr := h.registrar.Register(workspace, handle, string(value)); registerErr != nil {
		refuseForRegistrar(writer, registerErr)
		return
	}
	writeRegistration(writer, TextReferenceRegistration{Handle: handle, Length: uint32(len(value))})
}

// errValueTooLarge reports a body past the boundary's maximum. It is this
// package's own sentinel so that a read failure and an oversized body stay
// distinguishable refusals.
var errValueTooLarge = errors.New("text reference value exceeds the boundary maximum")

// readBoundedValue reads the request body under MaxValueBytes. A body past the
// maximum is an error, not a prefix: the caller gets a refusal rather than a
// silently shortened value.
func readBoundedValue(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	value, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, MaxValueBytes))
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			return value, errValueTooLarge
		}
		return value, readErr
	}
	if len(value) > MaxValueBytes {
		// Unreachable through the reader above, which refuses first, and kept as
		// the statement of the bound this boundary actually admits.
		return value, errValueTooLarge
	}
	return value, nil
}

// registrationWorkspace reads the one workspace this registration is made in and
// validates its shape with the recogniser the registry itself uses. A missing,
// malformed or repeated parameter is refused rather than resolved: more than one
// workspace would make the answer to "which workspace holds this" a guess, and a
// guessed scope is a value registered where the caller did not say.
func registrationWorkspace(request *http.Request) (string, string) {
	values, present := request.URL.Query()[workspaceQuery]
	if !present || len(values) != 1 {
		return "", refusalWorkspace
	}
	workspace := values[0]
	if execution.ValidateTextReferenceWorkspace(workspace) != nil {
		return "", refusalWorkspace
	}
	return workspace, ""
}

// refuseForRegistrar maps the registry's classification onto this boundary's
// answer. Only the code is read: the registry's sentence is not rendered, so a
// value can never arrive in this answer through an error text, now or later.
func refuseForRegistrar(writer http.ResponseWriter, err error) {
	switch platformerrors.CodeOf(err) {
	case platformerrors.CodeInvalidInput:
		refuse(writer, http.StatusBadRequest, refusalUnregistered)
	case platformerrors.CodeConflict:
		refuse(writer, http.StatusConflict, refusalConflict)
	case platformerrors.CodeUnavailable:
		refuse(writer, http.StatusServiceUnavailable, refusalFull)
	default:
		refuse(writer, http.StatusInternalServerError, refusalUnregistered)
	}
}

func writeRegistration(writer http.ResponseWriter, registration TextReferenceRegistration) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(registration)
}

func refuse(writer http.ResponseWriter, status int, message string) {
	http.Error(writer, message, status)
}

// zeroBytes clears a buffer this boundary allocated, so the plaintext does not
// outlive the call in the heap block it was read into. It bounds how long the
// bytes stay, and it claims nothing more than that: the copy the registry holds
// is the one that has to exist, and a copy the runtime made elsewhere is not
// something this can reach.
func zeroBytes(buffer []byte) {
	for index := range buffer {
		buffer[index] = 0
	}
}

// absent reports a dependency this boundary has nothing to call. The plain nil
// check is not enough for an interface: `var r *execution.TextReferenceRegistry;
// NewTextReferenceHandler(r, ids)` compiles, and would mount a boundary whose
// first request fails at the call site.
func absent(dependency any) bool {
	if dependency == nil {
		return true
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
