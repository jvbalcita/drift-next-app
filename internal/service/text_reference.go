package service

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

const (
	// TextReferencePath is where the local registration surface is mounted. The
	// handle is the path's last segment.
	TextReferencePath = "/local/text-references/"
	// TextReferenceWorkspaceHeader names the workspace the value belongs to. A
	// reference is only releasable inside the scope that registered it, so the
	// surface will not register one without it.
	TextReferenceWorkspaceHeader = "X-Drift-Workspace"
	// maxTextReferenceBodyBytes bounds the value one registration may carry. It
	// mirrors the registry's own bound, so a value this surface accepts is one the
	// registry can hold.
	maxTextReferenceBodyBytes = 1 << 20
)

// textReferenceRegistration is everything a registration carries apart from the
// value: the handle the value is registered under, the workspace that owns it,
// and the length it declares.
//
// It has no field for the content, and that is the point rather than an
// oversight. The value travels as the request body, so no struct holds it, which
// is why no rendering of this request — a log line, an error, a panic dump or a
// debug print — can emit it. Redaction has to recognise a value to hide it; a
// type that cannot hold one has nothing to hide.
type textReferenceRegistration struct {
	Handle    string
	Workspace string
	Length    int
}

// NewTextReferenceHandler returns the local surface that registers a typed-text
// value with the reference registry, or nil when no registry was constructed, so
// a deployment without one exposes no surface at all rather than a control an
// operator would render and then find dead.
//
// The handler never logs the request, and every refusal it writes is a fixed
// sentence: the body is operator content, and an error quoting it would put it
// in the operator's terminal and in whatever collects stderr.
func NewTextReferenceHandler(registry *execution.TextReferenceRegistry) http.Handler {
	if registry == nil {
		return nil
	}
	return &textReferenceHandler{registry: registry}
}

type textReferenceHandler struct {
	registry *execution.TextReferenceRegistry
}

func (h *textReferenceHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "a text reference is registered by POST with the value as the body", http.StatusMethodNotAllowed)
		return
	}
	handle := strings.TrimPrefix(request.URL.Path, TextReferencePath)
	if handle == "" || strings.Contains(handle, "/") {
		http.Error(writer, "a registration names one opaque handle in its path", http.StatusBadRequest)
		return
	}
	workspace := strings.TrimSpace(request.Header.Get(TextReferenceWorkspaceHeader))
	if workspace == "" {
		http.Error(writer, "a registration names the workspace its value belongs to", http.StatusBadRequest)
		return
	}
	// A body larger than the bound is refused, never truncated. A truncated body
	// would register a prefix of what the operator typed, and typing a prefix is
	// worse than typing nothing: it is the wrong content, delivered silently.
	if request.ContentLength > maxTextReferenceBodyBytes {
		http.Error(writer, "the value is larger than this surface registers", http.StatusRequestEntityTooLarge)
		return
	}
	value, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxTextReferenceBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(writer, "the value is larger than this surface registers", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(writer, "the value could not be read", http.StatusBadRequest)
		return
	}
	if len(value) == 0 {
		http.Error(writer, "a registration carries a non-empty value", http.StatusBadRequest)
		return
	}
	// The registration names the handle, the workspace and the length — and never
	// the value, which is handed straight to the registry.
	registration := textReferenceRegistration{Handle: handle, Workspace: workspace, Length: len(value)}
	if err := h.registry.Register(registration.Workspace, registration.Handle, string(value)); err != nil {
		http.Error(writer, registrationRefusalMessage(platformerrors.CodeOf(err)), statusForRegistrationRefusal(platformerrors.CodeOf(err)))
		return
	}
	// The buffer is cleared once the registry owns the value, so the only copy
	// left is the one that will be released at dispatch.
	for index := range value {
		value[index] = 0
	}
	writer.WriteHeader(http.StatusNoContent)
}

// statusForRegistrationRefusal maps the registry's classified refusals onto the
// surface's statuses. The mapping is exhaustive on the codes the registry emits,
// and an unclassified failure is a 500 rather than a refusal: a refusal an
// operator reads must mean something the registry decided.
func statusForRegistrationRefusal(code platformerrors.Code) int {
	switch code {
	case platformerrors.CodeInvalidInput:
		return http.StatusBadRequest
	case platformerrors.CodeNotFound:
		return http.StatusNotFound
	case platformerrors.CodeConflict:
		return http.StatusConflict
	case platformerrors.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// registrationRefusalMessage is the fixed sentence for a refusal. None of these
// names the value, and none echoes the body.
func registrationRefusalMessage(code platformerrors.Code) string {
	switch code {
	case platformerrors.CodeInvalidInput:
		return "a registration must name a bounded handle, a workspace and a bounded value"
	case platformerrors.CodeNotFound:
		return "an operation names a reference that is not held"
	case platformerrors.CodeConflict:
		return "the handle is already held"
	case platformerrors.CodeUnavailable:
		return "this surface is not registering references right now"
	default:
		return "the reference could not be registered"
	}
}
