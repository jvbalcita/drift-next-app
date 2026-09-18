package transportconnect_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"drift.local/drift-next/internal/media"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The per-device stream endpoint a browser fetches when the transport is TCP.
//
// The cases below are about the two things this surface can be got wrong: what it
// refuses BEFORE a response body begins (because a started body cannot be taken
// back), and what it hands a browser (the container, and no way around this
// service to the device).

// fetchStream performs one fetch of the stream endpoint.
func fetchStream(t *testing.T, mirrors transportconnect.DeviceMirrors, streamKey string) *httptest.ResponseRecorder {
	t.Helper()
	handler := transportconnect.NewMirrorStreamHTTPHandler(mirrors)
	if handler == nil {
		t.Fatal("a constructed transport produced no stream endpoint handler")
	}
	request := httptest.NewRequest(http.MethodGet, transportconnect.MirrorStreamPath+"?stream_id="+streamKey, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// TestAStreamEndpointFetchIsServedTheStreamsContainer: the positive half. The body
// is what the stream served, and the response says what it is.
func TestAStreamEndpointFetchIsServedTheStreamsContainer(t *testing.T) {
	endpoint := &fakeEndpointStream{fakeMirrorStream: &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice}, body: []byte("ftyp-container")}
	recorder := fetchStream(t, newFakeMirrors(endpoint), mirrorStreamID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the fetch answered %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != "ftyp-container" {
		t.Fatalf("the fetch carried %q, want the stream's own bytes", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("the response states content type %q, want video/mp4", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("the response states cache control %q, want no-store: a buffered live stream is a delayed one", got)
	}
	if endpoint.served != 1 {
		t.Fatalf("the stream was served %d times, want once", endpoint.served)
	}
}

// TestAStreamEndpointRefusesBeforeTheBodyBegins: an identity this service is not
// carrying, a stream that is not carried this way, a fetch with no identity and a
// method that is not a fetch are all answered before a byte of body - so a browser
// is never handed a response that opened and then had to stop.
func TestAStreamEndpointRefusesBeforeTheBodyBegins(t *testing.T) {
	// A stream carried over a peer connection rather than from this endpoint.
	peer := &fakeMirrorStream{key: "drift-device-beta-2abc1234", deviceID: "device-beta"}
	endpoint := &fakeEndpointStream{fakeMirrorStream: &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice}, body: []byte("ftyp-container")}
	mirrors := newFakeMirrors(peer, endpoint)
	handler := transportconnect.NewMirrorStreamHTTPHandler(mirrors)
	if handler == nil {
		t.Fatal("a constructed transport produced no stream endpoint handler")
	}

	cases := []struct {
		name     string
		method   string
		target   string
		wantCode int
		wantBody string
	}{
		{
			name: "an unknown stream", method: http.MethodGet,
			target:   transportconnect.MirrorStreamPath + "?stream_id=drift-elsewhere-00000000",
			wantCode: http.StatusNotFound, wantBody: "no live stream with that identity is being carried",
		},
		{
			name: "a stream carried as a peer connection", method: http.MethodGet,
			target:   transportconnect.MirrorStreamPath + "?stream_id=" + peer.key,
			wantCode: http.StatusNotFound, wantBody: "that stream is negotiated as a peer connection",
		},
		{
			name: "no stream identity", method: http.MethodGet,
			target:   transportconnect.MirrorStreamPath,
			wantCode: http.StatusBadRequest, wantBody: "requires the stream identity",
		},
		{
			name: "a method that is not a fetch", method: http.MethodPost,
			target:   transportconnect.MirrorStreamPath + "?stream_id=" + mirrorStreamID,
			wantCode: http.StatusMethodNotAllowed, wantBody: "is fetched",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
			if recorder.Code != test.wantCode {
				t.Fatalf("the fetch answered %d, want %d", recorder.Code, test.wantCode)
			}
			if body := recorder.Body.String(); !strings.Contains(body, test.wantBody) {
				t.Fatalf("the refusal reads %q, want it to say %q", strings.TrimSpace(body), test.wantBody)
			}
			if strings.Contains(recorder.Body.String(), mirrorSerial) {
				t.Fatal("a refusal names the device's transport serial")
			}
		})
	}
	if endpoint.served != 0 || peer.offers != nil {
		t.Fatal("a refused fetch reached the stream transport")
	}
}

// TestAStreamEndpointWithoutATransportIsNotConstructed: the mount gate. A handler
// over nothing is nil, so the route is empty rather than mounted and answering
// every request with a refusal - the rule every other surface in this service
// already applies.
func TestAStreamEndpointWithoutATransportIsNotConstructed(t *testing.T) {
	if handler := transportconnect.NewMirrorStreamHTTPHandler(nil); handler != nil {
		t.Fatal("a stream endpoint was constructed over no transport")
	}
	var typedNil *fakeMirrors
	if handler := transportconnect.NewMirrorStreamHTTPHandler(typedNil); handler != nil {
		t.Fatal("a stream endpoint was constructed over a typed-nil transport")
	}
}

// The endpoint the transport serves must satisfy the port the surface fetches it
// through: if this stops compiling, a real stream endpoint has no route again.
var _ transportconnect.DeviceMirrorEndpointStream = (*media.MirrorEndpoint)(nil)

// The transport narrows its carrier to the port the surface calls in the
// composition root's own adapter, so what is asserted here is the half that has
// no adapter: the endpoint serves one browser, and the endpoint type is what the
// container is written by.
var _ interface {
	Serve(context.Context, io.Writer, func() error) error
} = (*media.MirrorEndpoint)(nil)
