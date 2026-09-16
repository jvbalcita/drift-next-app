package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/platform/clock"
)

// distinctiveValue is the content a registration carries in these cases. It has
// no credential shape and no sensitive name beside it, which is exactly why a
// pattern-matching redactor cannot be the thing that keeps it out of a log: the
// assertion below is that nothing renders it at all.
const distinctiveValue = "correct horse battery staple"

const (
	testWorkspace  = "workspace-1"
	testHandle     = "reference-1"
	testLabToken   = "local-lab-token"
	oversizeSlack  = 1
	maxTestBodyLen = 1 << 20
)

func newRegistrationRegistry(t *testing.T) *execution.TextReferenceRegistry {
	t.Helper()
	registry, err := execution.NewTextReferenceRegistry(
		clock.NewFixed(time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)),
		execution.DefaultTextReferenceTTL,
		execution.DefaultTextReferenceCapacity,
	)
	if err != nil {
		t.Fatalf("new text reference registry: %v", err)
	}
	return registry
}

func registrationRequest(method, handle, workspace, value string) *http.Request {
	request := httptest.NewRequest(method, TextReferencePath+handle, strings.NewReader(value))
	if workspace != "" {
		request.Header.Set(TextReferenceWorkspaceHeader, workspace)
	}
	return request
}

// The surface registers the value, and the registry is the only holder of it.
func TestTheRegistrationSurfaceRegistersAValueForItsWorkspace(t *testing.T) {
	registry := newRegistrationRegistry(t)
	recorder := httptest.NewRecorder()

	NewTextReferenceHandler(registry).ServeHTTP(recorder, registrationRequest(http.MethodPost, testHandle, testWorkspace, distinctiveValue))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %q, want 204", recorder.Code, recorder.Body.String())
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1", held)
	}
	value, err := registry.Resolve(context.Background(), testWorkspace, execution.TextReference{Handle: testHandle, Length: uint32(len(distinctiveValue))})
	if err != nil {
		t.Fatalf("the registered value could not be released in its own workspace: %v", err)
	}
	if value != distinctiveValue {
		t.Fatal("the registered value is not the value that was sent")
	}
	if _, err := registry.Resolve(context.Background(), testWorkspace, execution.TextReference{Handle: testHandle, Length: uint32(len(distinctiveValue))}); err == nil {
		t.Fatal("the registered reference was released twice")
	}
}

// A body past the bound is refused, never truncated: a truncated body would
// register a prefix of what the operator typed, and a prefix is the wrong
// content delivered silently.
func TestTheRegistrationSurfaceRefusesABodyLargerThanTheBound(t *testing.T) {
	registry := newRegistrationRegistry(t)
	oversize := strings.Repeat("x", maxTestBodyLen+oversizeSlack)
	recorder := httptest.NewRecorder()

	NewTextReferenceHandler(registry).ServeHTTP(recorder, registrationRequest(http.MethodPost, testHandle, testWorkspace, oversize))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: an oversize body must register nothing at all", held)
	}
	if _, err := registry.Resolve(context.Background(), testWorkspace, execution.TextReference{Handle: testHandle, Length: uint32(len(oversize))}); err == nil {
		t.Fatal("a prefix of an oversize body was registered")
	}
}

// A caller that declares an oversize body is refused before the body is read, so
// a declared-too-large registration costs no memory and registers nothing.
func TestTheRegistrationSurfaceRefusesADeclaredOversizeBody(t *testing.T) {
	registry := newRegistrationRegistry(t)
	request := registrationRequest(http.MethodPost, testHandle, testWorkspace, distinctiveValue)
	request.ContentLength = maxTestBodyLen + oversizeSlack
	recorder := httptest.NewRecorder()

	NewTextReferenceHandler(registry).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0", held)
	}
}

// Every piece of the request is required, and a refusal registers nothing.
func TestTheRegistrationSurfaceRefusesAnIncompleteRequest(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		handle    string
		workspace string
		value     string
		want      int
	}{
		{name: "no workspace", method: http.MethodPost, handle: testHandle, value: distinctiveValue, want: http.StatusBadRequest},
		{name: "a blank workspace", method: http.MethodPost, handle: testHandle, workspace: "   ", value: distinctiveValue, want: http.StatusBadRequest},
		{name: "no value", method: http.MethodPost, handle: testHandle, workspace: testWorkspace, want: http.StatusBadRequest},
		{name: "no handle", method: http.MethodPost, handle: "", workspace: testWorkspace, value: distinctiveValue, want: http.StatusBadRequest},
		{name: "a handle with a slash", method: http.MethodPost, handle: "reference/1", workspace: testWorkspace, value: distinctiveValue, want: http.StatusBadRequest},
		{name: "a handle that is not a handle", method: http.MethodPost, handle: "bad!handle", workspace: testWorkspace, value: distinctiveValue, want: http.StatusBadRequest},
		{name: "a read", method: http.MethodGet, handle: testHandle, workspace: testWorkspace, value: distinctiveValue, want: http.StatusMethodNotAllowed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := newRegistrationRegistry(t)
			recorder := httptest.NewRecorder()

			NewTextReferenceHandler(registry).ServeHTTP(recorder, registrationRequest(test.method, test.handle, test.workspace, test.value))

			if recorder.Code != test.want {
				t.Fatalf("status = %d body = %q, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
			if held := registry.Held(); held != 0 {
				t.Fatalf("references held = %d, want 0: a refused registration holds nothing", held)
			}
		})
	}
}

// The route is guarded like every other local surface: this is the one boundary
// that admits operator content, and reachability on loopback is not authority.
func TestTheRegistrationRouteRefusesACallerWithoutTheToken(t *testing.T) {
	registry := newRegistrationRegistry(t)
	route := TextReferenceRoute(registry, testLabToken)
	if route.Path != TextReferencePath || route.Handler == nil {
		t.Fatalf("route = %#v, want the registration surface mounted", route)
	}

	unauthenticated := httptest.NewRecorder()
	route.Handler.ServeHTTP(unauthenticated, registrationRequest(http.MethodPost, testHandle, testWorkspace, distinctiveValue))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", unauthenticated.Code)
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: an unauthenticated registration holds nothing", held)
	}

	authenticated := httptest.NewRecorder()
	request := registrationRequest(http.MethodPost, testHandle, testWorkspace, distinctiveValue)
	request.Header.Set(LabTokenHeader, testLabToken)
	route.Handler.ServeHTTP(authenticated, request)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %q, want 204", authenticated.Code, authenticated.Body.String())
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1", held)
	}
}

// The payload type carries the handle, the workspace and the length, and has no
// field a value could travel in. Asserted on the type rather than promised in a
// comment, because "we do not log the value" is only true while no field can
// hold one.
func TestTheRegistrationPayloadHasNoFieldForTheValue(t *testing.T) {
	payload := reflect.TypeOf(textReferenceRegistration{})
	want := map[string]string{"Handle": "string", "Workspace": "string", "Length": "int"}
	if payload.NumField() != len(want) {
		t.Fatalf("payload has %d fields, want exactly %d: %v", payload.NumField(), len(want), payload)
	}
	for index := 0; index < payload.NumField(); index++ {
		field := payload.Field(index)
		if expected, ok := want[field.Name]; !ok || field.Type.String() != expected {
			t.Fatalf("payload field %q %s is not one of the recorded fields %v", field.Name, field.Type, want)
		}
	}

	// And a payload built from a request that carried a value renders none of it,
	// in every form this repo formats a struct.
	registration := textReferenceRegistration{Handle: testHandle, Workspace: testWorkspace, Length: len(distinctiveValue)}
	encoded, err := json.Marshal(registration)
	if err != nil {
		t.Fatalf("marshal the registration payload: %v", err)
	}
	for name, rendering := range map[string]string{
		"form":         fmt.Sprintf("%v", registration),
		"debug":        fmt.Sprintf("%+v", registration),
		"go syntax":    fmt.Sprintf("%#v", registration),
		"json":         string(encoded),
		"pointer form": fmt.Sprintf("%v", &registration),
	} {
		if strings.Contains(rendering, distinctiveValue) {
			t.Fatalf("the %s rendering of the registration payload carried the value", name)
		}
	}
}

// Nothing the surface writes carries the value: not the default logger, and not
// the response body an operator reads for either outcome.
func TestTheRegistrationSurfaceRendersTheValueNowhere(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	registry := newRegistrationRegistry(t)
	handler := NewTextReferenceHandler(registry)
	responses := []string{}

	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, registrationRequest(http.MethodPost, testHandle, testWorkspace, distinctiveValue))
	responses = append(responses, accepted.Body.String())

	oversize := httptest.NewRecorder()
	handler.ServeHTTP(oversize, registrationRequest(http.MethodPost, "reference-2", testWorkspace, strings.Repeat(distinctiveValue, 2)))
	responses = append(responses, oversize.Body.String())

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, registrationRequest(http.MethodPost, "bad!handle", testWorkspace, distinctiveValue))
	responses = append(responses, invalid.Body.String())

	if strings.Contains(logged.String(), distinctiveValue) {
		t.Fatal("the surface logged the value it was given")
	}
	for index, body := range responses {
		if strings.Contains(body, distinctiveValue) {
			t.Fatalf("response %d rendered the value it was given", index)
		}
	}
}
