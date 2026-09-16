package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	"drift.local/drift-next/internal/service"
	transportlocal "drift.local/drift-next/internal/transport/local"
)

// The registration surface is worth having only if an operator can reach it, and
// only if what comes back is the boundary's own answer. These tests exercise the
// mounted route over a real loopback HTTP server rather than calling the handler
// directly, because a handler that compiles proves nothing about reachability.
//
// The value is deliberately ordinary: no credential shape, no key name and no
// delimiter. A pattern matcher cannot recognize it as secret, so if any rendered
// form of the request carried it, this is the value that would appear.

// registrationValue is the fixture every test here registers. It is never
// printed by an assertion: a failure names the surface that leaked, not the
// value.
const registrationValue = "operator-typed-value-8f14e45fceea167a5a36dedd4bea2543"

const (
	registrationWorkspace = "workspace-alpha"
	otherWorkspace        = "workspace-beta"
	registrationToken     = "lab-token-value"
	dispatchSerial        = "mock-device-alpha"
)

// --- fakes ------------------------------------------------------------------

// capturedDeviceCalls records every argument array that reached the transport,
// so a test can assert the value arrived exactly once and everything else
// arrived not at all.
type capturedDeviceCalls struct {
	mutex sync.Mutex
	calls [][]string
}

func (c *capturedDeviceCalls) RunDeviceCommand(_ context.Context, _ string, args []string) (adb.Result, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.calls = append(c.calls, append([]string(nil), args...))
	return adb.Result{}, nil
}

func (c *capturedDeviceCalls) count() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.calls)
}

func (c *capturedDeviceCalls) call(index int) []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if index >= len(c.calls) {
		return nil
	}
	return c.calls[index]
}

// --- process output capture -------------------------------------------------

// lockedBuffer is a writer both loggers can share without a data race.
type lockedBuffer struct {
	mutex sync.Mutex
	buf   strings.Builder
}

func (b *lockedBuffer) Write(text []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.Write(text)
}

func (b *lockedBuffer) contents() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.String()
}

// capturingSlogHandler renders every attribute it is given in every form a
// handler could render it — value, values, and Go syntax — so a handler that
// leaked a field would leak it here too.
type capturingSlogHandler struct {
	output *lockedBuffer
}

func (h *capturingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingSlogHandler) Handle(_ context.Context, record slog.Record) error {
	h.output.Write([]byte("message=" + record.Message + "\n"))
	record.Attrs(func(attribute slog.Attr) bool {
		_, _ = h.output.Write([]byte("attribute=" + fmt.Sprintf("%v | %+v | %#v", attribute, attribute, attribute) + "\n"))
		return true
	})
	return nil
}

func (h *capturingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingSlogHandler) WithGroup(string) slog.Handler      { return h }

// capturedProcessOutput is everything this process can print from: the standard
// logger, slog's default handler, and standard error itself, which is where a
// panic dump and anything that bypasses both loggers lands.
type capturedProcessOutput struct {
	logs   *lockedBuffer
	stderr *lockedBuffer
}

// captureProcessOutput redirects all three and returns a restore that drains
// them. Restoring is idempotent, so a test can drain before it asserts and a
// deferred restore still runs after a failure.
func captureProcessOutput(t *testing.T) (*capturedProcessOutput, func()) {
	t.Helper()
	previousLogWriter := log.Writer()
	previousDefault := slog.Default()
	previousStderr := os.Stderr

	captured := &capturedProcessOutput{logs: &lockedBuffer{}, stderr: &lockedBuffer{}}
	pipeReader, pipeWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("redirect standard error: %v", pipeErr)
	}
	log.SetOutput(captured.logs)
	slog.SetDefault(slog.New(&capturingSlogHandler{output: captured.logs}))
	os.Stderr = pipeWriter

	var once sync.Once
	restore := func() {
		once.Do(func() {
			log.SetOutput(previousLogWriter)
			slog.SetDefault(previousDefault)
			os.Stderr = previousStderr
			_ = pipeWriter.Close()
			written, _ := io.ReadAll(pipeReader)
			_, _ = captured.stderr.Write(written)
			_ = pipeReader.Close()
		})
	}
	return captured, restore
}

// leakNames reports which captured surface carries the value, without ever
// rendering the value itself.
func (c *capturedProcessOutput) leakNames(value string) []string {
	leaks := make([]string, 0, 2)
	if strings.Contains(c.logs.contents(), value) {
		leaks = append(leaks, "the process loggers")
	}
	if strings.Contains(c.stderr.contents(), value) {
		leaks = append(leaks, "standard error")
	}
	return leaks
}

// --- the boundary, end to end ----------------------------------------------

// The whole boundary in one flow, on the real server, with the real registry:
// a value is registered over the wire and never appears in anything the process
// printed or answered with; another workspace cannot release it; the workspace
// that registered it releases it exactly once, into exactly one device
// argument; and the second dispatch is refused without reaching the device.
func TestTheRegistrationBoundaryKeepsTheValueOutOfEveryRenderedForm(t *testing.T) {
	registry := newBoundaryRegistry(t)
	route := service.TextReferenceRoute(transportlocal.NewTextReferenceHandler(registry, ids.NewRandom()), registrationToken)
	if route.Path != transportlocal.Path {
		t.Fatalf("route path = %q, want %q", route.Path, transportlocal.Path)
	}
	server := httptest.NewServer(service.NewHTTPServer("control-plane", "127.0.0.1:0", route).Handler)
	defer server.Close()

	captured, restore := captureProcessOutput(t)

	registration := registerValue(t, server.URL, registrationWorkspace, registrationToken, registrationValue)
	answer, readErr := io.ReadAll(registration.Body)
	_ = registration.Body.Close()
	if readErr != nil {
		t.Fatalf("read the registration answer: %v", readErr)
	}
	if registration.StatusCode != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d", registration.StatusCode, http.StatusCreated)
	}
	if strings.Contains(string(answer), registrationValue) {
		t.Fatal("the registration answer rendered the value")
	}
	var payload struct {
		Handle string `json:"handle"`
		Length uint32 `json:"length"`
	}
	if err := json.Unmarshal(answer, &payload); err != nil {
		t.Fatalf("the answer is not the payload this boundary declares: %v", err)
	}
	if payload.Length != uint32(len(registrationValue)) {
		t.Fatal("the answer does not carry the length of the value that was registered")
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1", held)
	}

	reference := execution.TextReference{Handle: payload.Handle, Length: payload.Length}
	device := &capturedDeviceCalls{}
	inputs, err := execution.NewInputs(device, registry, dispatchSerial)
	if err != nil {
		t.Fatalf("new inputs: %v", err)
	}

	// Another workspace cannot release it, and its refusal consumes nothing.
	otherCtx := execution.WithTextReferenceWorkspace(context.Background(), otherWorkspace)
	if err := inputs.TypeText(otherCtx, execution.TypeTextRequest{Text: reference}); err == nil {
		t.Fatal("another workspace released a value it did not register")
	}
	if calls := device.count(); calls != 0 {
		t.Fatalf("device calls = %d, want 0: a refused cross-workspace dispatch must reach no device", calls)
	}
	if held := registry.Held(); held != 1 {
		t.Fatalf("references held = %d, want 1 after a refused cross-workspace dispatch", held)
	}

	// The workspace that registered it releases it, exactly once.
	ctx := execution.WithTextReferenceWorkspace(context.Background(), registrationWorkspace)
	if err := inputs.TypeText(ctx, execution.TypeTextRequest{Text: reference}); err != nil {
		t.Fatalf("the registering workspace could not dispatch its own reference: %v", err)
	}
	if calls := device.count(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	args := device.call(0)
	if len(args) != 4 {
		t.Fatalf("argument count = %d, want 4: the shape is `shell input text <token>`", len(args))
	}
	if args[3] != registrationValue {
		t.Fatal("the released value did not become the single argument token")
	}

	if err := inputs.TypeText(ctx, execution.TypeTextRequest{Text: reference}); err == nil {
		t.Fatal("the same reference dispatched a second time")
	}
	if calls := device.count(); calls != 1 {
		t.Fatalf("device calls after the refused second dispatch = %d, want the count unchanged at 1", calls)
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0 after the release", held)
	}

	// Everything the process printed during all of that, from every sink it can
	// print to. Draining first so standard error is included.
	restore()
	if leaks := captured.leakNames(registrationValue); len(leaks) > 0 {
		t.Fatalf("the value was rendered into %s", strings.Join(leaks, " and "))
	}
}

// The route carries the same constant-time token check as the other local
// surfaces: loopback reachability alone is not authority for a hostile local
// caller. Without the token nothing is registered.
func TestTheRegistrationRouteRequiresTheLocalToken(t *testing.T) {
	registry := newBoundaryRegistry(t)
	route := service.TextReferenceRoute(transportlocal.NewTextReferenceHandler(registry, ids.NewRandom()), registrationToken)
	server := httptest.NewServer(service.NewHTTPServer("control-plane", "127.0.0.1:0", route).Handler)
	defer server.Close()

	response := registerValue(t, server.URL, registrationWorkspace, "", registrationValue)
	_ = response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		t.Fatal("an unauthenticated registration was accepted")
	}
	if held := registry.Held(); held != 0 {
		t.Fatalf("references held = %d, want 0: an unauthenticated caller registered a value", held)
	}
}

// No constructed boundary, no route. A route that could only answer with a
// refusal would be a control an operator surface renders and finds dead.
func TestTheRegistrationRouteIsNotMountedWithoutABoundary(t *testing.T) {
	route := service.TextReferenceRoute(nil, registrationToken)
	if route.Path != "" || route.Handler != nil {
		t.Fatalf("TextReferenceRoute(nil) = %#v, want an unmounted route", route)
	}
}

// The composition root constructs the route from the registry and the handle
// source it built, and a constructed boundary mounts at the boundary's own path.
func TestAConstructedBoundaryMountsTheRegistrationRoute(t *testing.T) {
	registry := newBoundaryRegistry(t)
	route := service.TextReferenceRoute(transportlocal.NewTextReferenceHandler(registry, ids.NewRandom()), registrationToken)
	if route.Path == "" || route.Handler == nil {
		t.Fatalf("a constructed registration boundary mounted no route: %#v", route)
	}
}

func newBoundaryRegistry(t *testing.T) *execution.TextReferenceRegistry {
	t.Helper()
	registry, err := execution.NewTextReferenceRegistry(
		clock.NewFixed(time.Date(2026, time.September, 17, 9, 0, 0, 0, time.UTC)),
		execution.DefaultTextReferenceTTL,
		execution.DefaultTextReferenceCapacity,
	)
	if err != nil {
		t.Fatalf("new text reference registry: %v", err)
	}
	return registry
}

func registerValue(t *testing.T, baseURL, workspace, token, value string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+transportlocal.Path+"?workspace="+workspace, strings.NewReader(value))
	if err != nil {
		t.Fatalf("build the registration request: %v", err)
	}
	if token != "" {
		request.Header.Set(service.LabTokenHeader, token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("submit the registration: %v", err)
	}
	return response
}
