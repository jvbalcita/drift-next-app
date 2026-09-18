package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/service"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// Typed text is dispatchable from this surface only because its value never
// travels in it. These tests exercise the MOUNTED route rather than the handler,
// because the property that matters is reachability: the RPC was absent until
// this slice, and a surface an operator can reach that carried the value in the
// message would be worse than the absence it replaces.

// acceptingDeviceInputs is a dispatcher that records what it was asked and
// answers with a verified outcome, so a case that reaches it is a case that
// reached a device in the kernel's sense.
type acceptingDeviceInputs struct {
	calls int
	last  transportconnect.DeviceInputTarget
}

func (f *acceptingDeviceInputs) Run(_ context.Context, input transportconnect.DeviceInputTarget, _, _ string) (action.Result, error) {
	f.calls++
	f.last = input
	return action.Result{Outcome: action.OutcomeVerified, PostconditionPassed: true}, nil
}

// typedTextBody is a TypeText call as a console makes it: the message names the
// reference, and the value the reference stands for appears nowhere in it,
// because the message has no field one could be written into.
func typedTextBody(handle string, length int) string {
	return `{"context":{"requestId":"req-text-1","idempotencyKey":"idem-text-1","actorId":"op-1"},` +
		`"workspace":{"workspaceId":"workspace-a"},` +
		`"deviceId":"device-a","leaseId":"lease-1","fencingToken":"7",` +
		`"text":{"handle":"` + handle + `","valueLength":` + strconv.Itoa(length) + `}}`
}

func postTypeText(t *testing.T, server *http.Server, token, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/"+driftv1connect.DeviceInputServiceName+"/TypeText", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(service.LabTokenHeader, token)
	}
	record := httptest.NewRecorder()
	server.Handler.ServeHTTP(record, request)
	return record.Code, record.Body.String()
}

func mountedInputRoute(inputs transportconnect.DeviceInputs) *http.Server {
	return service.NewHTTPServer("control-plane", "127.0.0.1:0", service.DeviceInputRoute(inputs, "lab-token-value"))
}

// TestTheMountedDeviceInputRouteDispatchesTypedTextByReference proves the path an
// operator's typed text takes: the route is mounted, the procedure resolves, the
// caller's reference reaches the dispatcher intact, and the value the reference
// stands for appears in neither the request nor the dispatcher's target —
// because it was never in the request to begin with.
func TestTheMountedDeviceInputRouteDispatchesTypedTextByReference(t *testing.T) {
	const (
		token   = "lab-token-value"
		handle  = "text-op-1-9f2c"
		value   = "correct horse battery staple"
		length  = len(value)
		routeID = "/" + driftv1connect.DeviceInputServiceName + "/"
	)
	inputs := &acceptingDeviceInputs{}
	route := service.DeviceInputRoute(inputs, token)
	if route.Path != routeID {
		t.Fatalf("DeviceInputRoute() path = %q, want %q", route.Path, routeID)
	}
	server := service.NewHTTPServer("control-plane", "127.0.0.1:0", route)

	body := typedTextBody(handle, length)
	if strings.Contains(body, value) {
		t.Fatalf("the request under test carries the value; this case would prove nothing: %s", body)
	}
	status, response := postTypeText(t, server, token, body)
	if status != http.StatusOK {
		t.Fatalf("a TypeText call = %d body=%s, want 200", status, response)
	}
	if inputs.calls != 1 {
		t.Fatalf("dispatcher calls = %d, want exactly 1", inputs.calls)
	}
	text := inputs.last.Payload.Text
	if text == nil {
		t.Fatalf("dispatched payload = %#v, want a typed text reference", inputs.last.Payload)
	}
	if text.Handle != handle || text.Length != uint32(length) {
		t.Fatalf("dispatched reference = %#v, want handle %q length %d", text, handle, length)
	}
	if inputs.last.ObservationToken != "" {
		// Typed content carries no coordinate, so there is nothing for it to be
		// measured in and nothing to cross-check. A token here would be a frame
		// the input does not have.
		t.Fatalf("dispatched observation token = %q, want none for a coordinate-free input", inputs.last.ObservationToken)
	}
	if inputs.last.Workspace != "workspace-a" || inputs.last.DeviceID != "device-a" {
		t.Fatalf("dispatched target = %#v, want workspace-a/device-a", inputs.last)
	}
	if inputs.last.LeaseID != "lease-1" || inputs.last.FencingToken != 7 || inputs.last.IdempotencyKey != "idem-text-1" {
		t.Fatalf("dispatched control tuple = %#v, want the caller's lease tuple", inputs.last)
	}
}

// TestTypedTextIsRefusedBeforeTheDispatcherWhenItsReferenceIsNotOne is the shape
// gate: every one of these is refused at the transport boundary, and the
// dispatcher — which is what opens a lease, evaluates policy and reaches a device
// — is never asked. A refusal here is cheap and reaches nothing.
func TestTypedTextIsRefusedBeforeTheDispatcherWhenItsReferenceIsNotOne(t *testing.T) {
	const contentShaped = "correct horse battery staple"
	cases := []struct {
		name string
		body string
	}{
		{name: "no reference at all", body: `{"context":{"requestId":"req-1","actorId":"op-1"},"workspace":{"workspaceId":"workspace-a"},"deviceId":"device-a","leaseId":"lease-1","fencingToken":"7"}`},
		{name: "an empty handle", body: typedTextBody("", 12)},
		{name: "a handle carrying content", body: typedTextBody(contentShaped, len(contentShaped))},
		{name: "a handle past the bound", body: typedTextBody(strings.Repeat("a", 129), 12)},
		{name: "a zero length", body: typedTextBody("text-op-1-9f2c", 0)},
		{name: "a length past the bound", body: typedTextBody("text-op-1-9f2c", (1<<20)+1)},
		{name: "no device", body: `{"context":{"requestId":"req-1","actorId":"op-1"},"workspace":{"workspaceId":"workspace-a"},"text":{"handle":"text-op-1-9f2c","valueLength":12}}`},
		{name: "no workspace", body: `{"context":{"requestId":"req-1","actorId":"op-1"},"deviceId":"device-a","text":{"handle":"text-op-1-9f2c","valueLength":12}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inputs := &acceptingDeviceInputs{}
			server := mountedInputRoute(inputs)
			status, response := postTypeText(t, server, "lab-token-value", testCase.body)
			if status == http.StatusOK {
				t.Fatalf("%s = %d body=%s, want a refusal", testCase.name, status, response)
			}
			if inputs.calls != 0 {
				t.Fatalf("%s reached the dispatcher %d times, want 0", testCase.name, inputs.calls)
			}
			if strings.Contains(response, contentShaped) {
				t.Fatalf("the refusal rendered the rejected handle: %s", response)
			}
		})
	}
}

// TestTypedTextIsUnreachableWithoutTheLocalToken keeps the new procedure behind
// the same guard as every other input: this surface is one procedure away from
// the boundary that admits operator content, and loopback reachability alone is
// not authority for it.
func TestTypedTextIsUnreachableWithoutTheLocalToken(t *testing.T) {
	inputs := &acceptingDeviceInputs{}
	status, _ := postTypeText(t, mountedInputRoute(inputs), "", typedTextBody("text-op-1-9f2c", 12))
	if status == http.StatusOK {
		t.Fatalf("an unauthenticated TypeText = %d, want a refusal", status)
	}
	if inputs.calls != 0 {
		t.Fatalf("an unauthenticated TypeText reached the dispatcher %d times, want 0", inputs.calls)
	}
}
