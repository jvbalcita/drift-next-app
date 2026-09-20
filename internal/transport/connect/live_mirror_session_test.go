package transportconnect_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/media"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The live stream this surface opens must outlive the request that opened it.
//
// Everything here is production wiring except the device: the real handler, the
// real stream transport, the real mirror engine and the real HTTP surface, over a
// real net/http server. The dialer is the only fake, because a device is not what
// this is about - what it is about is the lifetime of the session the opening
// request started, and that is decided by the context the request handed down.
//
// The failure this reproduces is the one an operator saw on the running plane:
// StartMirrorStream answers with a stream identity and the transport line states
// WebRTC, and the very next request - the negotiation, which attaches that
// browser to the stream it was just given - is refused with "no live stream with
// that identity is being carried". The console's own two-message rendering of
// that refusal is exactly what the frame showed.

// mirrorPort adapts the real stream transport to the surface's own port, which is
// what the composition root does (cmd/control-plane/main.go's mirrorStreamPort).
type mirrorPort struct{ transport *media.StreamTransport }

func (p mirrorPort) Open(ctx context.Context, deviceID, serial string, transport media.MirrorTransportKind, purpose media.MirrorViewerPurpose) (transportconnect.DeviceMirrorStream, error) {
	carrier, err := p.transport.Open(ctx, deviceID, serial, transport, purpose)
	if err != nil {
		return nil, err
	}
	return carrier, nil
}

func (p mirrorPort) Stream(streamKey string) (transportconnect.DeviceMirrorStream, bool) {
	carrier, live := p.transport.Stream(streamKey)
	if !live {
		return nil, false
	}
	return carrier, true
}

func (p mirrorPort) Carrying() []string {
	peers := p.transport.Peers()
	keys := make([]string, 0, len(peers))
	for _, peer := range peers {
		keys = append(keys, peer.StreamKey)
	}
	return keys
}

var _ transportconnect.DeviceMirrors = mirrorPort{}

// scriptedStream is one device's live stream as the dialer returns it: it reports
// a frame size, and then reads until its own context ends, which is what a device
// that is streaming a still screen looks like.
type scriptedStream struct {
	frames chan media.StreamFrame
	closed chan struct{}
}

func newScriptedStream() *scriptedStream {
	return &scriptedStream{frames: make(chan media.StreamFrame), closed: make(chan struct{})}
}

func (s *scriptedStream) FrameSize(context.Context) (int, int, error) { return 1080, 2280, nil }

func (s *scriptedStream) ReadFrame(ctx context.Context) (media.StreamFrame, error) {
	select {
	case frame := <-s.frames:
		return frame, nil
	case <-ctx.Done():
		// The session's own context ended, so the read stops. A session whose
		// context is cancelled by the request that opened it ends here, before
		// any browser has attached to it.
		return media.StreamFrame{}, ctx.Err()
	}
}

func (*scriptedStream) SendInput(context.Context, media.MirrorInput) error { return nil }

func (*scriptedStream) RequestKeyframe(context.Context) error { return nil }

func (s *scriptedStream) Close(context.Context) error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

// scriptedDialer starts one scripted stream per device.
type scriptedDialer struct {
	streams []*scriptedStream
	ctxs    []context.Context
}

func (d *scriptedDialer) Dial(ctx context.Context, _, _ string) (media.MirrorStream, error) {
	stream := newScriptedStream()
	d.streams = append(d.streams, stream)
	d.ctxs = append(d.ctxs, ctx)
	return stream, nil
}

// liveSurface wires the production stream path over a real HTTP surface and
// returns the client an operator's console is.
func liveSurface(t *testing.T) (driftv1connect.DeviceMirrorServiceClient, *scriptedDialer, *httptest.Server) {
	t.Helper()
	dialer := &scriptedDialer{}
	engine, err := media.NewMirrorEngine(media.MirrorEngineConfig{Dialer: dialer})
	if err != nil {
		t.Fatalf("the mirror engine was not constructed: %v", err)
	}
	transport, err := media.NewStreamTransport(media.StreamTransportConfig{Mirror: engine})
	if err != nil {
		t.Fatalf("the stream transport was not constructed: %v", err)
	}
	handler := transportconnect.NewDeviceMirrorHandler(mirrorPort{transport: transport}, &fixedSerials{serial: mirrorSerial}, nil, nil)
	if handler == nil {
		t.Fatal("the live mirror handler was not constructed")
	}
	path, surface := driftv1connect.NewDeviceMirrorServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, surface)
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = transport.Close(stopCtx)
		_ = engine.Stop(stopCtx)
	})
	return driftv1connect.NewDeviceMirrorServiceClient(server.Client(), server.URL), dialer, server
}

func startOverSurface(t *testing.T, client driftv1connect.DeviceMirrorServiceClient) *driftv1.MirrorStream {
	t.Helper()
	response, err := client.StartMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StartMirrorStreamRequest{
		Context:   mirrorRequestContext(),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: mirrorWorkspace},
		DeviceId:  mirrorDevice,
		Transport: driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
	}))
	if err != nil {
		t.Fatalf("StartMirrorStream refused: %v", err)
	}
	if response.Msg.GetStream() == nil {
		t.Fatal("StartMirrorStream answered without a stream")
	}
	return response.Msg.GetStream()
}

// TestAStreamOpenedOverTheSurfaceIsStillCarriedAfterTheOpeningRequestReturns is
// the reproduction: the identity the opening request was given must still be a
// live stream when the browser attaches to it, because attaching is the very next
// request the console sends.
func TestAStreamOpenedOverTheSurfaceIsStillCarriedAfterTheOpeningRequestReturns(t *testing.T) {
	client, dialer, _ := liveSurface(t)
	opened := startOverSurface(t, client)
	if opened.GetStreamId() == "" {
		t.Fatal("the opening request answered with no stream identity")
	}
	if len(dialer.streams) != 1 {
		t.Fatalf("the opening request dialed %d stream(s), want 1", len(dialer.streams))
	}

	// Everything the opening request handed down has returned by now, and the
	// console's next requests are these: the poll it runs while the frame is
	// open, and the handshake that attaches this browser to the stream it was
	// just given.
	polled, err := client.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: opened.GetStreamId()}))
	if err != nil {
		if connectrpc.CodeOf(err) == connectrpc.CodeNotFound {
			t.Fatalf("the stream %s the surface had just opened was no longer carried: %v\n"+
				"a session bound to the lifetime of the request that opened it cannot outlive that request",
				opened.GetStreamId(), err)
		}
		t.Fatalf("GetMirrorStream refused: %v", err)
	}
	if polled.Msg.GetStream().GetStreamId() != opened.GetStreamId() {
		t.Fatalf("the poll answered for stream %q, want the identity the opening request returned (%q)", polled.Msg.GetStream().GetStreamId(), opened.GetStreamId())
	}

	// The offer below is deliberately not a real one: what this asserts is not
	// that the handshake succeeds, but that it REACHED the stream - a negotiation
	// refused because the identity is unknown is the failure, and any other
	// refusal means the identity resolved and the stream was there to negotiate.
	if _, err := client.NegotiateMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.NegotiateMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: opened.GetStreamId(),
		OfferSdp: "v=0\r\n",
	})); err != nil && connectrpc.CodeOf(err) == connectrpc.CodeNotFound {
		t.Fatalf("the stream %s was no longer carried when the browser attached to it: %v", opened.GetStreamId(), err)
	}
}

// TestTheLiveStreamOutlivesTheRequestThatOpenedIt pins the property directly: the
// session the opening request started is not ended by the return of that request.
// The device's own stream is still there, and the engine is still carrying the
// device - a session that ended itself here would be one the surface can no
// longer attach a browser to.
func TestTheLiveStreamOutlivesTheRequestThatOpenedIt(t *testing.T) {
	client, dialer, _ := liveSurface(t)
	opened := startOverSurface(t, client)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := client.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: opened.GetStreamId()}))
		if err != nil {
			if connectrpc.CodeOf(err) == connectrpc.CodeNotFound {
				t.Fatalf("the stream %s stopped being carried after the request that opened it returned: %v", opened.GetStreamId(), err)
			}
			t.Fatalf("GetMirrorStream refused: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(dialer.streams) != 1 {
		t.Fatalf("the device's stream was dialed %d time(s), want exactly one session", len(dialer.streams))
	}
	select {
	case <-dialer.streams[0].closed:
		t.Fatal("the device's stream was released while a browser was attached to it")
	default:
	}
	if len(dialer.ctxs) != 1 || dialer.ctxs[0].Err() != nil {
		t.Fatalf("the session's context was cancelled by the request that opened it (err=%v): the session outlives that request", firstErr(dialer.ctxs))
	}
}

func firstErr(ctxs []context.Context) error {
	for _, ctx := range ctxs {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
