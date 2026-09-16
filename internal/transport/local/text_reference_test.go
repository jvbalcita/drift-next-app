package local_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	transportlocal "drift.local/drift-next/internal/transport/local"
)

// boundaryValue is a distinctive value with no credential shape, no key name and
// no delimiter: exactly the case that a pattern-matcher cannot recognize as
// secret. If any rendered form of the request carried it, this is the value that
// would show up.
const boundaryValue = "operator-typed-value-8f14e45fceea167a5a36dedd4bea2543"

const boundaryWorkspace = "workspace-alpha"

// recordingRegistrar stands in for the registry. It records what it was asked to
// hold, and never holds it beyond the call.
type recordingRegistrar struct {
	workspace string
	handle    string
	value     string
	calls     int
	err       error
}

func (r *recordingRegistrar) Register(workspace, handle, value string) error {
	r.calls++
	r.workspace, r.handle, r.value = workspace, handle, value
	return r.err
}

type fixedHandles struct {
	id  string
	err error
}

func (h fixedHandles) NewID() (string, error) { return h.id, h.err }

func newHandler(t *testing.T, registrar transportlocal.TextReferenceRegistrar, handles transportlocal.HandleSource) *transportlocal.TextReferenceHandler {
	t.Helper()
	handler := transportlocal.NewTextReferenceHandler(registrar, handles)
	if handler == nil {
		t.Fatal("the boundary was not constructed from the dependencies it was given")
	}
	return handler
}

func post(handler http.Handler, target, value string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(value))
	record := httptest.NewRecorder()
	handler.ServeHTTP(record, request)
	return record
}

// What the boundary returns is the whole of what it can say about a value it
// holds: an opaque handle and a length. This asserts that structurally over the
// type rather than by formatting an instance and hoping — a field added later
// that could carry the value fails here, which is the point of building the
// boundary as a route rather than as a message.
func TestThePayloadTypeHasNoFieldTheValueCouldTravelIn(t *testing.T) {
	allowed := map[string]reflect.Kind{ // field name -> the only kind it may have
		"Handle": reflect.String,
		"Length": reflect.Uint32,
	}
	payload := reflect.TypeOf(transportlocal.TextReferenceRegistration{})
	if payload.NumField() != len(allowed) {
		t.Fatalf("payload fields = %d, want %d: every additional field is somewhere the value could travel", payload.NumField(), len(allowed))
	}
	for index := 0; index < payload.NumField(); index++ {
		field := payload.Field(index)
		want, declared := allowed[field.Name]
		if !declared {
			t.Fatalf("payload field %q is not one of the two facts this boundary answers with", field.Name)
		}
		if field.Type.Kind() != want {
			t.Fatalf("payload field %q has kind %s, want %s", field.Name, field.Type.Kind(), want)
		}
	}

	// The handler holds nothing of what it registers either: no field of it is
	// capable of holding a value, because there is no string and no byte slice
	// on it at all. The one string the boundary keeps is the handle, and it is
	// minted per registration rather than stored.
	handler := reflect.TypeOf(&transportlocal.TextReferenceHandler{})
	for index := 0; index < handler.Elem().NumField(); index++ {
		field := handler.Elem().Field(index)
		switch field.Type.Kind() {
		case reflect.String, reflect.Slice, reflect.Array:
			t.Fatalf("handler field %q can hold content: a boundary that retains nothing must have nowhere to retain it", field.Name)
		}
	}
}

// The whole path, once: a value goes in, an opaque handle and its length come
// back, and the registry is the only thing that ends up holding it.
func TestTheBoundaryRegistersAValueAndAnswersWithTheHandleAndLength(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, boundaryValue)
	if record.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d", record.Code, http.StatusCreated)
	}
	if registrar.calls != 1 {
		t.Fatalf("registrations = %d, want exactly 1", registrar.calls)
	}
	if registrar.workspace != boundaryWorkspace {
		t.Fatalf("registered workspace = %q, want the workspace the caller named", registrar.workspace)
	}
	if registrar.value != boundaryValue {
		t.Fatal("the registry was not handed the value the caller submitted")
	}

	var payload map[string]any
	if err := json.Unmarshal(record.Body.Bytes(), &payload); err != nil {
		t.Fatalf("the answer is not the payload type this boundary declares: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("answer fields = %d, want 2: the handle and the length", len(payload))
	}
	if handle, _ := payload["handle"].(string); handle != registrar.handle {
		t.Fatal("the answer does not carry the handle the value was registered under")
	}
	if length, _ := payload["length"].(float64); int(length) != len(boundaryValue) {
		t.Fatal("the answer does not carry the length of the value that was registered")
	}
	// The value itself is nowhere in the answer, in any form.
	for _, rendered := range []string{record.Body.String(), record.Header().Get("Content-Type")} {
		if strings.Contains(rendered, boundaryValue) {
			t.Fatal("the answer renders the value it registered")
		}
	}
}

// The handle is a reference, not content: it carries the same shape the contract
// admits, so nothing about the value can be read out of it.
func TestTheHandleIsOpaqueAndBounded(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, ids.NewRandom())

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, boundaryValue)
	if record.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d", record.Code, http.StatusCreated)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`).MatchString(registrar.handle) {
		t.Fatal("the minted handle is not the opaque bounded reference the contract admits")
	}
	if len(registrar.handle) > 128 {
		t.Fatalf("handle length = %d, want it bounded", len(registrar.handle))
	}
}

// A body past the maximum is refused, and nothing is registered. A truncated
// value would be content the operator did not type, so the boundary must not
// fall back to the prefix it read.
func TestTheBoundaryRefusesABodyPastItsMaximum(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, strings.Repeat("v", transportlocal.MaxValueBytes+1))
	if record.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want %d", record.Code, http.StatusRequestEntityTooLarge)
	}
	if registrar.calls != 0 {
		t.Fatalf("registrations = %d, want 0: an oversized body must register nothing", registrar.calls)
	}

	// The same maximum, exactly, is admitted: the bound is a maximum and not an
	// off-by-one.
	record = post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, strings.Repeat("v", transportlocal.MaxValueBytes))
	if record.Code != http.StatusCreated {
		t.Fatalf("body at the maximum status = %d, want %d", record.Code, http.StatusCreated)
	}
	if registrar.calls != 1 {
		t.Fatalf("registrations = %d, want 1: a value at the maximum is admissible", registrar.calls)
	}
}

// The bound this boundary enforces is the typed input contract's own, read from
// the package that states it. Two copies of the number would be two chances for
// the surface to admit a value the resolver then refuses.
func TestTheMaximumIsTheContractsOwnBound(t *testing.T) {
	if transportlocal.MaxValueBytes != execution.MaxTypedTextValueLength {
		t.Fatalf("boundary maximum = %d, contract maximum = %d", transportlocal.MaxValueBytes, execution.MaxTypedTextValueLength)
	}
}

// An empty value registers nothing: a reference to nothing would dispatch a
// keystroke sequence for nothing.
func TestTheBoundaryRefusesAnEmptyValue(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, "")
	if record.Code != http.StatusBadRequest {
		t.Fatalf("empty body status = %d, want %d", record.Code, http.StatusBadRequest)
	}
	if registrar.calls != 0 {
		t.Fatalf("registrations = %d, want 0", registrar.calls)
	}
}

// The workspace is what the value is filed under, so a registration that names
// none, names two, or names something that is not an identifier is refused. A
// guessed scope would register the value somewhere the caller did not say.
func TestTheBoundaryRequiresExactlyOneWellFormedWorkspace(t *testing.T) {
	cases := map[string]string{
		"none":         transportlocal.Path,
		"empty":        transportlocal.Path + "?workspace=",
		"repeated":     transportlocal.Path + "?workspace=" + boundaryWorkspace + "&workspace=workspace-beta",
		"padded":       transportlocal.Path + "?workspace=%20" + boundaryWorkspace + "%20",
		"not an ident": transportlocal.Path + "?workspace=workspace%20beta",
		"over long":    transportlocal.Path + "?workspace=" + strings.Repeat("w", 129),
	}
	for name, target := range cases {
		registrar := &recordingRegistrar{}
		handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})
		record := post(handler, target, boundaryValue)
		if record.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want %d", name, record.Code, http.StatusBadRequest)
		}
		if registrar.calls != 0 {
			t.Fatalf("%s: registrations = %d, want 0", name, registrar.calls)
		}
	}
}

// Registration is a POST and nothing else: a GET that registered a value would
// put it in a URL, which is the first thing anything logs.
func TestTheBoundaryRefusesAnythingButAPost(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead} {
		record := httptest.NewRecorder()
		handler.ServeHTTP(record, httptest.NewRequest(method, transportlocal.Path+"?workspace="+boundaryWorkspace, nil))
		if record.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", method, record.Code, http.StatusMethodNotAllowed)
		}
		if record.Header().Get("Allow") != http.MethodPost {
			t.Fatalf("%s Allow header = %q, want %q", method, record.Header().Get("Allow"), http.MethodPost)
		}
	}
	if registrar.calls != 0 {
		t.Fatalf("registrations = %d, want 0", registrar.calls)
	}
}

// The registry's classifications become this boundary's statuses, and its
// sentence is never rendered. This is the hostile case: an error that quotes the
// value must still not put it in the answer, because a future registry error
// text is not something this boundary can audit.
func TestTheBoundaryAnswersWithItsOwnSentenceNeverTheRegistrars(t *testing.T) {
	cases := map[platformerrors.Code]int{
		platformerrors.CodeInvalidInput: http.StatusBadRequest,
		platformerrors.CodeConflict:     http.StatusConflict,
		platformerrors.CodeUnavailable:  http.StatusServiceUnavailable,
		platformerrors.CodeInternal:     http.StatusInternalServerError,
	}
	for code, want := range cases {
		registrar := &recordingRegistrar{err: platformerrors.New(code, "the value "+boundaryValue+" was rejected")}
		handler := newHandler(t, registrar, fixedHandles{id: "reference-abc"})

		record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, boundaryValue)
		if record.Code != want {
			t.Fatalf("code %q status = %d, want %d", code, record.Code, want)
		}
		if strings.Contains(record.Body.String(), boundaryValue) {
			t.Fatalf("code %q: the answer rendered the registry's message, which carried the value", code)
		}
	}
}

// A boundary missing either half of what it needs is not constructed, so the
// composition root mounts no route rather than a route that can only refuse.
func TestTheBoundaryIsNotConstructedWithoutBothDependencies(t *testing.T) {
	if handler := transportlocal.NewTextReferenceHandler(nil, fixedHandles{id: "reference-abc"}); handler != nil {
		t.Fatal("a boundary with no registrar was constructed")
	}
	registrar := &recordingRegistrar{}
	if handler := transportlocal.NewTextReferenceHandler(registrar, nil); handler != nil {
		t.Fatal("a boundary with no handle source was constructed")
	}
	var typedNil *execution.TextReferenceRegistry
	if handler := transportlocal.NewTextReferenceHandler(typedNil, fixedHandles{id: "reference-abc"}); handler != nil {
		t.Fatal("a boundary over a typed-nil registry was constructed")
	}
	var typedNilHandles *ids.RandomGenerator
	if handler := transportlocal.NewTextReferenceHandler(registrar, typedNilHandles); handler != nil {
		t.Fatal("a boundary over a typed-nil handle source was constructed")
	}
}

// A handle source that cannot mint a handle registers nothing: an attempt with
// no identity is not an attempt.
func TestTheBoundaryRegistersNothingWhenNoHandleCanBeMinted(t *testing.T) {
	registrar := &recordingRegistrar{}
	handler := newHandler(t, registrar, fixedHandles{err: platformerrors.New(platformerrors.CodeUnavailable, "no entropy")})

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, boundaryValue)
	if record.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", record.Code, http.StatusServiceUnavailable)
	}
	if registrar.calls != 0 {
		t.Fatalf("registrations = %d, want 0", registrar.calls)
	}
}

// The real registry, through the real boundary: the value is held under the
// workspace that registered it, and nowhere else.
func TestTheBoundaryHandsTheValueToTheRegistryItWasGiven(t *testing.T) {
	registry, err := execution.NewTextReferenceRegistry(clock.NewFixed(time.Date(2026, time.September, 17, 9, 0, 0, 0, time.UTC)), execution.DefaultTextReferenceTTL, execution.DefaultTextReferenceCapacity)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	handler := newHandler(t, registry, ids.NewRandom())

	record := post(handler, transportlocal.Path+"?workspace="+boundaryWorkspace, boundaryValue)
	if record.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d", record.Code, http.StatusCreated)
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1", held)
	}

	var payload struct {
		Handle string `json:"handle"`
		Length uint32 `json:"length"`
	}
	if err := json.Unmarshal(record.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	reference := execution.TextReference{Handle: payload.Handle, Length: payload.Length}

	// Another workspace cannot release it, and the refusal does not consume it.
	if _, err := registry.Resolve(execution.WithTextReferenceWorkspace(t.Context(), "workspace-beta"), reference); err == nil {
		t.Fatal("another workspace released a value it did not register")
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1 after a refused cross-workspace release", held)
	}
	value, err := registry.Resolve(execution.WithTextReferenceWorkspace(t.Context(), boundaryWorkspace), reference)
	if err != nil {
		t.Fatalf("the registering workspace could not release its own value: %v", err)
	}
	if value != boundaryValue {
		t.Fatal("the registry released a different value than the one registered")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0 after the release", held)
	}
}
