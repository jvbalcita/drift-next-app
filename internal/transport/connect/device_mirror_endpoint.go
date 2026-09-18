package transportconnect

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// The per-device stream endpoint the TCP transport is fetched from.
//
// It is a byte surface rather than an RPC, and it is deliberately small: one
// stream identity in, the container out. Everything a caller can learn from it
// is what a browser is allowed to know - the stream's own identity is the only
// thing it names, and no device address, adb serial or media-server URL is
// reachable through it.
//
// Two refusals happen before the response body begins, because a body that has
// started cannot be taken back: an identity this service is not carrying, and a
// stream that is not carried this way at all (a peer connection is negotiated,
// not fetched). Once the body has begun, the only ending available is the
// response ending - which is exactly what a browser needs to see, and the reason
// the console polls the stream's own state for why.
type MirrorStreamHTTPHandler struct {
	streams DeviceMirrors
}

// NewMirrorStreamHTTPHandler binds the endpoint to the live stream transport. It
// returns nil when the transport is absent - including a non-nil interface
// holding a nil pointer - so a caller cannot mount an endpoint that has nothing
// to serve, and the route is never mounted (AGENTS.md section 6).
func NewMirrorStreamHTTPHandler(streams DeviceMirrors) *MirrorStreamHTTPHandler {
	if isAbsentDeviceMirrors(streams) {
		return nil
	}
	return &MirrorStreamHTTPHandler{streams: streams}
}

func (h *MirrorStreamHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.streams == nil {
		http.Error(writer, "the live stream endpoint is not constructed", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "a live stream endpoint is fetched", http.StatusMethodNotAllowed)
		return
	}
	streamKey := strings.TrimSpace(request.URL.Query().Get("stream_id"))
	if streamKey == "" {
		http.Error(writer, "a live stream endpoint requires the stream identity it was given", http.StatusBadRequest)
		return
	}
	stream, live := h.streams.Stream(streamKey)
	if !live {
		// The refusal names the identity it was given and the identities this
		// service IS carrying, for the same reason the surface's own does: a
		// browser's fetch that fails has to be diagnosable from what it was told.
		http.Error(writer, noSuchStreamError(h.streams, streamKey).Error(), http.StatusNotFound)
		return
	}
	endpoint, carries := stream.(DeviceMirrorEndpointStream)
	if !carries {
		http.Error(writer, "that stream is negotiated as a peer connection and is not fetched from this endpoint", http.StatusNotFound)
		return
	}

	writer.Header().Set("Content-Type", "video/mp4")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, canFlush := writer.(http.Flusher)
	flush := func() error {
		if canFlush {
			flusher.Flush()
		}
		return nil
	}
	// The response starts before the stream's first fragment, because a browser's
	// fetch has to see a body before it can read one and the container's
	// initialisation segment travels with the first picture. The handler states
	// what it is about to send and then sends it: the alternative - buffering a
	// container to decide on a status code - is a live stream delayed by its own
	// first fragment.
	writer.WriteHeader(http.StatusOK)
	_ = flush()

	if err := endpoint.Serve(request.Context(), writer, flush); err != nil {
		// Nothing can be written after the body has begun, and nothing needs to
		// be: the console reads the stream's own reported state for why it
		// ended, and a browser that went away is not a failure to report to it.
		if !errors.Is(err, context.Canceled) {
			_ = err
		}
	}
}
