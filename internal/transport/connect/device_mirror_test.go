package transportconnect_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The live mirror surface's own boundary cases. Every one of them runs against a
// fake transport: no device, no peer connection, no socket.

const (
	mirrorWorkspace = "workspace-lab-local"
	mirrorDevice    = "device-alpha"
	mirrorSerial    = "SERIAL-alpha"
	mirrorStreamID  = "drift-device-alpha-2abc1234"
)

// fakeMirrorStream is one browser's stream as the surface sees it.
type fakeMirrorStream struct {
	key       string
	deviceID  string
	serial    string
	frames    uint64
	keyFrames uint64
	failure   string
	// endClass is how the stream's session ended, in the plane's own vocabulary.
	// It is what tells an ENDED stream from a FAILED one, and it is stated
	// separately from failure on purpose: the surface must never read the state
	// out of the presence of a sentence.
	endClass media.MirrorEndClass
	width    int
	height   int

	answer    string
	answerErr error
	closeErr  error
	offers    []string
	closes    int
}

func (s *fakeMirrorStream) StreamKey() string { return s.key }

func (s *fakeMirrorStream) Answer(_ context.Context, offer string) (string, error) {
	s.offers = append(s.offers, offer)
	if s.answerErr != nil {
		return "", s.answerErr
	}
	return s.answer, nil
}

func (s *fakeMirrorStream) Stats() media.StreamStats {
	return media.StreamStats{
		StreamKey:    s.key,
		DeviceID:     s.deviceID,
		Serial:       s.serial,
		RenderWidth:  s.width,
		RenderHeight: s.height,
		Frames:       s.frames,
		KeyFrames:    s.keyFrames,
		Failure:      s.failure,
		EndClass:     s.endClass,
	}
}

func (s *fakeMirrorStream) Close() error {
	s.closes++
	return s.closeErr
}

// fakeEndpointStream is one stream carried over the service's own stream
// endpoint: the surface sees it as a stream, and a fetch of its endpoint is
// served by it. It is what makes "which transport is this" observable rather than
// asserted.
type fakeEndpointStream struct {
	*fakeMirrorStream

	mu     sync.Mutex
	served int
	body   []byte
}

func (s *fakeEndpointStream) Serve(_ context.Context, writer io.Writer, flush func() error) error {
	s.mu.Lock()
	s.served++
	body := append([]byte(nil), s.body...)
	s.mu.Unlock()
	if len(body) > 0 {
		if _, err := writer.Write(body); err != nil {
			return err
		}
	}
	if flush != nil {
		return flush()
	}
	return nil
}

// The compiler is what proves the endpoint stream is a stream this surface can
// carry: if this stops compiling, the surface has no TCP transport again.
var (
	_ transportconnect.DeviceMirrorEndpointStream = (*fakeEndpointStream)(nil)
	_ transportconnect.DeviceMirrorStream         = (*fakeEndpointStream)(nil)
)

// fakeMirrors is the stream transport the surface calls.
type fakeMirrors struct {
	streams    map[string]transportconnect.DeviceMirrorStream
	openErr    error
	opened     []string
	transports []string
	// purposes is what each open said it was: the operator's own frame or one of
	// the console's ambient tiles. It is recorded because the purpose is what the
	// plane's capacity is spent against, so a surface that dropped it would be
	// recorded here as a grid that may spend the operator's place.
	purposes []string
	// previews is the workspace's preview setting each open stated, which is what
	// the device's encoder is bounded by. It is recorded for the same reason: a
	// surface that dropped it would be a stream bounded by a number nobody chose.
	previews []media.MirrorPreview
	openFunc func(deviceID, serial string) *fakeMirrorStream
}

func newFakeMirrors(streams ...transportconnect.DeviceMirrorStream) *fakeMirrors {
	byKey := make(map[string]transportconnect.DeviceMirrorStream, len(streams))
	for _, stream := range streams {
		byKey[stream.StreamKey()] = stream
	}
	return &fakeMirrors{streams: byKey}
}

func (m *fakeMirrors) Open(_ context.Context, deviceID, serial string, transport media.MirrorTransportKind, purpose media.MirrorViewerPurpose, preview media.MirrorPreview) (transportconnect.DeviceMirrorStream, error) {
	m.opened = append(m.opened, deviceID+"/"+serial)
	m.transports = append(m.transports, string(transport))
	m.purposes = append(m.purposes, string(purpose))
	m.previews = append(m.previews, preview)
	if m.openErr != nil {
		return nil, m.openErr
	}
	stream := &fakeMirrorStream{
		key:      "drift-" + deviceID + "-2abc1234",
		deviceID: deviceID,
		serial:   serial,
		width:    1080,
		height:   2280,
		answer:   "v=0\r\na=answer\r\n",
	}
	if m.openFunc != nil {
		stream = m.openFunc(deviceID, serial)
	}
	// A stream opened over TCP is carried from the stream endpoint, and that is
	// what the surface has to see: the transport it reports and the handle it
	// hands out both follow from it.
	if transport == media.TransportTCP {
		endpoint := &fakeEndpointStream{fakeMirrorStream: stream, body: []byte("ftyp-container")}
		m.streams[stream.key] = endpoint
		return endpoint, nil
	}
	m.streams[stream.key] = stream
	return stream, nil
}

func (m *fakeMirrors) Stream(streamKey string) (transportconnect.DeviceMirrorStream, bool) {
	stream, live := m.streams[streamKey]
	if !live {
		return nil, false
	}
	return stream, true
}

// Carrying reports the identities the fake transport holds, sorted, so a refusal
// that names them names the same ones every time.
func (m *fakeMirrors) Carrying() []string {
	keys := make([]string, 0, len(m.streams))
	for key := range m.streams {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mirrorRequestContext() *driftv1.RequestContext {
	return &driftv1.RequestContext{
		RequestId:      "mirror-request-1",
		ActorId:        "operator-1",
		IdempotencyKey: "mirror-key-1",
	}
}

func mirrorHandler(t *testing.T, mirrors transportconnect.DeviceMirrors) *transportconnect.DeviceMirrorHandler {
	t.Helper()
	handler := transportconnect.NewDeviceMirrorHandler(mirrors, &fixedSerials{serial: mirrorSerial}, nil, nil)
	if handler == nil {
		t.Fatal("the live mirror handler was not constructed")
	}
	return handler
}

func startRequest(workspace, device string, transport driftv1.MirrorTransport) *connectrpc.Request[driftv1.StartMirrorStreamRequest] {
	return connectrpc.NewRequest(&driftv1.StartMirrorStreamRequest{
		Context:   mirrorRequestContext(),
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: workspace},
		DeviceId:  device,
		Transport: transport,
	})
}

// TestANewDeviceMirrorHandlerWithNothingToCallIsNil: a surface whose only
// possible answer is a refusal is a control an operator renders and then finds
// dead, so the route is never mounted without a transport - including the
// typed-nil shape a plain nil check misses.
func TestANewDeviceMirrorHandlerWithNothingToCallIsNil(t *testing.T) {
	serials := &fixedSerials{serial: mirrorSerial}
	if handler := transportconnect.NewDeviceMirrorHandler(nil, serials, nil, nil); handler != nil {
		t.Fatal("a handler with no stream transport was constructed")
	}
	var typedNil *fakeMirrors
	if handler := transportconnect.NewDeviceMirrorHandler(typedNil, serials, nil, nil); handler != nil {
		t.Fatal("a handler over a typed-nil stream transport was constructed")
	}
	if handler := transportconnect.NewDeviceMirrorHandler(newFakeMirrors(), nil, nil, nil); handler != nil {
		t.Fatal("a handler with no device resolver was constructed")
	}
}

// TestStartMirrorStreamRefusesBeforeAnythingIsOpened: shape and transport are the
// transport layer's business, and every refusal here happens with nothing opened.
func TestStartMirrorStreamRefusesBeforeAnythingIsOpened(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		device    string
		transport driftv1.MirrorTransport
		wantCode  connectrpc.Code
	}{
		{name: "no workspace", workspace: "", device: mirrorDevice, wantCode: connectrpc.CodeInvalidArgument},
		{name: "no device", workspace: mirrorWorkspace, device: "", wantCode: connectrpc.CodeInvalidArgument},
		{
			name: "a transport that does not exist", workspace: mirrorWorkspace, device: mirrorDevice,
			transport: driftv1.MirrorTransport(99),
			wantCode:  connectrpc.CodeInvalidArgument,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mirrors := newFakeMirrors()
			handler := mirrorHandler(t, mirrors)
			_, err := handler.StartMirrorStream(context.Background(), startRequest(test.workspace, test.device, test.transport))
			if err == nil {
				t.Fatal("the request was accepted")
			}
			if code := connectrpc.CodeOf(err); code != test.wantCode {
				t.Fatalf("refusal code = %v, want %v (%v)", code, test.wantCode, err)
			}
			if len(mirrors.opened) != 0 {
				t.Fatalf("a refused request opened %v", mirrors.opened)
			}
		})
	}
}

// TestStartMirrorStreamOpensTheDevicesCurrentTransportAndNamesNothingElse: the
// caller names a device and this surface resolves it to the transport that device
// is currently reachable at. What comes back names the stream and the frame it is
// encoded at - never the serial, never an address a browser could reach the device
// at directly.
func TestStartMirrorStreamOpensTheDevicesCurrentTransportAndNamesNothingElse(t *testing.T) {
	mirrors := newFakeMirrors()
	handler := mirrorHandler(t, mirrors)

	response, err := handler.StartMirrorStream(context.Background(), startRequest(mirrorWorkspace, mirrorDevice, driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED))
	if err != nil {
		t.Fatalf("start the stream: %v", err)
	}
	if len(mirrors.opened) != 1 || mirrors.opened[0] != mirrorDevice+"/"+mirrorSerial {
		t.Fatalf("opened %v, want the device resolved to its current transport", mirrors.opened)
	}
	stream := response.Msg.GetStream()
	if stream.GetStreamId() != "drift-"+mirrorDevice+"-2abc1234" {
		t.Fatalf("stream id = %q, want the transport's own stream identity", stream.GetStreamId())
	}
	if stream.GetDeviceId() != mirrorDevice {
		t.Fatalf("device = %q, want %q", stream.GetDeviceId(), mirrorDevice)
	}
	if stream.GetTransport() != driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC {
		t.Fatalf("transport = %v, want WebRTC", stream.GetTransport())
	}
	if stream.GetRenderWidth() != 1080 || stream.GetRenderHeight() != 2280 {
		t.Fatalf("render size = %dx%d, want the size the stream is encoded at - it is the coordinate frame an input must be measured in", stream.GetRenderWidth(), stream.GetRenderHeight())
	}
	if stream.GetState() != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_STARTING {
		t.Fatalf("state = %v, want STARTING for a stream that has carried no picture yet: connected is not live", stream.GetState())
	}
	// The one thing a browser must never be handed: something it could use to
	// reach the device around this service.
	if rendered := fmt.Sprintf("%v", stream); strings.Contains(rendered, mirrorSerial) {
		t.Fatalf("the stream descriptor carries the device's transport serial: %s", rendered)
	}
}

// TestStartMirrorStreamCarriesTheWorkspacePreviewSetting is the wire half of
// ARC-227: the two settings cross the contract ADDITIVELY, on the request the
// console already makes, and an unstated or unrecognised one is read as "the plane's
// own setting" rather than as an encoder nobody bounded.
//
// The fields are stated for an ambient viewer and for the operator's own frame
// alike: what they BOUND is the plane's decision (an ambient stream only), and the
// boundary's job is to carry what the caller said rather than to decide it. A level
// this plane does not know and a rate no control offers are not applied - the plane
// answers them with its own setting, which is itself a cap.
func TestStartMirrorStreamCarriesTheWorkspacePreviewSetting(t *testing.T) {
	cases := []struct {
		name        string
		purpose     driftv1.MirrorViewerPurpose
		quality     driftv1.MirrorPreviewQuality
		frameRate   uint32
		wantPurpose media.MirrorViewerPurpose
		wantPreview media.MirrorPreview
	}{
		{
			name:        "an ambient tile states both settings",
			purpose:     driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT,
			quality:     driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_LOW,
			frameRate:   8,
			wantPurpose: media.PurposeAmbient,
			wantPreview: media.MirrorPreview{Quality: media.PreviewLow, FrameRate: 8},
		},
		{
			name:        "an ambient tile that states nothing",
			purpose:     driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT,
			quality:     driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_UNSPECIFIED,
			frameRate:   0,
			wantPurpose: media.PurposeAmbient,
			wantPreview: media.MirrorPreview{},
		},
		{
			name:        "an ambient tile stating a level this plane does not know",
			purpose:     driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT,
			quality:     driftv1.MirrorPreviewQuality(99),
			frameRate:   12,
			wantPurpose: media.PurposeAmbient,
			wantPreview: media.MirrorPreview{FrameRate: 12},
		},
		{
			name:        "an ambient tile stating a rate no control offers",
			purpose:     driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT,
			quality:     driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_HIGH,
			frameRate:   300,
			wantPurpose: media.PurposeAmbient,
			wantPreview: media.MirrorPreview{Quality: media.PreviewHigh},
		},
		{
			name:        "the operator's own frame",
			purpose:     driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR,
			quality:     driftv1.MirrorPreviewQuality_MIRROR_PREVIEW_QUALITY_LOW,
			frameRate:   4,
			wantPurpose: media.PurposeOperator,
			wantPreview: media.MirrorPreview{Quality: media.PreviewLow, FrameRate: 4},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mirrors := newFakeMirrors()
			handler := mirrorHandler(t, mirrors)
			request := startRequest(mirrorWorkspace, mirrorDevice, driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC)
			request.Msg.Purpose = test.purpose
			request.Msg.PreviewQuality = test.quality
			request.Msg.FrameRate = test.frameRate
			if _, err := handler.StartMirrorStream(context.Background(), request); err != nil {
				t.Fatalf("start the stream: %v", err)
			}
			if len(mirrors.previews) != 1 {
				t.Fatalf("the request opened %d streams, want 1", len(mirrors.previews))
			}
			if mirrors.purposes[0] != string(test.wantPurpose) {
				t.Fatalf("purpose = %q, want %q", mirrors.purposes[0], test.wantPurpose)
			}
			if mirrors.previews[0] != test.wantPreview {
				t.Fatalf("preview = %+v, want %+v: an unstated or unrecognised setting must reach the plane as unstated, so the plane's own bound applies rather than none", mirrors.previews[0], test.wantPreview)
			}
		})
	}
}

// TestStartMirrorStreamCarriesTheTransportTheOperatorAskedFor: both transports are
// carried, and a stream over the TCP transport is given the per-device endpoint a
// browser fetches its bytes from. A peer-connection stream states no endpoint,
// because it does not serve one.
func TestStartMirrorStreamCarriesTheTransportTheOperatorAskedFor(t *testing.T) {
	cases := []struct {
		name          string
		requested     driftv1.MirrorTransport
		wantTransport driftv1.MirrorTransport
		wantCarried   media.MirrorTransportKind
		wantURL       bool
	}{
		{
			name:          "unspecified selects the default",
			requested:     driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED,
			wantTransport: driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
			wantCarried:   media.TransportWebRTC,
		},
		{
			name:          "webrtc",
			requested:     driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
			wantTransport: driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC,
			wantCarried:   media.TransportWebRTC,
		},
		{
			name:          "tcp",
			requested:     driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP,
			wantTransport: driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP,
			wantCarried:   media.TransportTCP,
			wantURL:       true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mirrors := newFakeMirrors()
			handler := mirrorHandler(t, mirrors)
			response, err := handler.StartMirrorStream(context.Background(), startRequest(mirrorWorkspace, mirrorDevice, test.requested))
			if err != nil {
				t.Fatalf("start the stream: %v", err)
			}
			if len(mirrors.transports) != 1 || mirrors.transports[0] != string(test.wantCarried) {
				t.Fatalf("the transport asked for was carried as %v, want %q", mirrors.transports, test.wantCarried)
			}
			stream := response.Msg.GetStream()
			if stream.GetTransport() != test.wantTransport {
				t.Fatalf("transport = %v, want %v", stream.GetTransport(), test.wantTransport)
			}
			url := stream.GetStreamUrl()
			if test.wantURL {
				want := transportconnect.MirrorStreamPath + "?stream_id=" + stream.GetStreamId()
				if url != want {
					t.Fatalf("stream url = %q, want the stream's own endpoint %q", url, want)
				}
			} else if url != "" {
				t.Fatalf("a peer-connection stream states the endpoint %q, which it does not serve", url)
			}
			// Acceptance criterion 4, on the TCP path: what a browser is given to
			// reach the frames is this service's own path, and nothing about the
			// device it came from.
			if rendered := fmt.Sprintf("%v", stream); strings.Contains(rendered, mirrorSerial) {
				t.Fatalf("the stream descriptor carries the device's transport serial: %s", rendered)
			}
		})
	}
}

// TestStartMirrorStreamReportsARefusedDeviceAsARefusal: a device with no current
// transport endpoint has no stream to open, and that is the resolver's own
// classified refusal rather than an internal failure.
func TestStartMirrorStreamReportsARefusedDeviceAsARefusal(t *testing.T) {
	mirrors := newFakeMirrors()
	handler := transportconnect.NewDeviceMirrorHandler(mirrors, &fixedSerials{err: platformerrors.New(platformerrors.CodeUnavailable, "the device has no current transport")}, nil, nil)
	if handler == nil {
		t.Fatal("the live mirror handler was not constructed")
	}
	_, err := handler.StartMirrorStream(context.Background(), startRequest(mirrorWorkspace, mirrorDevice, driftv1.MirrorTransport_MIRROR_TRANSPORT_UNSPECIFIED))
	if err == nil {
		t.Fatal("a device with no current transport was streamed")
	}
	if code := connectrpc.CodeOf(err); code != connectrpc.CodeUnavailable {
		t.Fatalf("refusal code = %v, want unavailable", code)
	}
	if len(mirrors.opened) != 0 {
		t.Fatalf("a refused device still opened %v", mirrors.opened)
	}
}

// TestNegotiateMirrorStreamAnswersOnlyAKnownStream: negotiation never opens a
// stream, and a stream this service is not carrying answers not-found.
func TestNegotiateMirrorStreamAnswersOnlyAKnownStream(t *testing.T) {
	stream := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, answer: "v=0\r\na=answer\r\n"}
	mirrors := newFakeMirrors(stream)
	handler := mirrorHandler(t, mirrors)

	_, err := handler.NegotiateMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.NegotiateMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: "drift-nobody-00000000",
		OfferSdp: "v=0\r\na=offer\r\n",
	}))
	if err == nil {
		t.Fatal("an unknown stream was negotiated")
	}
	if code := connectrpc.CodeOf(err); code != connectrpc.CodeNotFound {
		t.Fatalf("refusal code = %v, want not_found", code)
	}
	if len(stream.offers) != 0 {
		t.Fatal("negotiating an unknown stream reached the stream transport")
	}
	if len(mirrors.opened) != 0 {
		t.Fatal("negotiation opened a stream")
	}

	response, err := handler.NegotiateMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.NegotiateMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: mirrorStreamID,
		OfferSdp: "v=0\r\na=offer\r\n",
	}))
	if err != nil {
		t.Fatalf("negotiate the stream: %v", err)
	}
	if response.Msg.GetAnswerSdp() != stream.answer {
		t.Fatalf("answer = %q, want the transport's own answer", response.Msg.GetAnswerSdp())
	}
	if len(stream.offers) != 1 || stream.offers[0] != "v=0\r\na=offer\r\n" {
		t.Fatalf("the stream received %v, want the caller's offer unchanged", stream.offers)
	}
}

// TestGetMirrorStreamStatesLiveOnlyOnPictures: this fleet's encoder produces one
// IDR per session, so a connected stream that has carried nothing is not live and
// must not be reported as one. A stream that failed reports its failure, and a
// stream that is carrying pictures reports them.
func TestGetMirrorStreamStatesLiveOnlyOnPictures(t *testing.T) {
	cases := []struct {
		name      string
		stream    *fakeMirrorStream
		wantState driftv1.MirrorStreamState
	}{
		{
			name:      "connected with no picture",
			stream:    &fakeMirrorStream{key: mirrorStreamID},
			wantState: driftv1.MirrorStreamState_MIRROR_STREAM_STATE_STARTING,
		},
		{
			name:      "carrying pictures",
			stream:    &fakeMirrorStream{key: mirrorStreamID, frames: 12, keyFrames: 2},
			wantState: driftv1.MirrorStreamState_MIRROR_STREAM_STATE_LIVE,
		},
		{
			name:      "failed",
			stream:    &fakeMirrorStream{key: mirrorStreamID, frames: 4, failure: "media: the stream is connected and has produced no picture"},
			wantState: driftv1.MirrorStreamState_MIRROR_STREAM_STATE_FAILED,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			handler := mirrorHandler(t, newFakeMirrors(test.stream))
			response, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID}))
			if err != nil {
				t.Fatalf("poll the stream: %v", err)
			}
			if state := response.Msg.GetStream().GetState(); state != test.wantState {
				t.Fatalf("state = %v, want %v", state, test.wantState)
			}
			if test.stream.failure != "" && response.Msg.GetStream().GetFailure() != test.stream.failure {
				t.Fatalf("failure = %q, want the stream's own reason %q", response.Msg.GetStream().GetFailure(), test.stream.failure)
			}
		})
	}
}

// TestGetMirrorStreamRefusesAnUnknownIdentity: a console whose stream ended learns
// that, rather than being told a stream it is not carrying is fine.
func TestGetMirrorStreamRefusesAnUnknownIdentity(t *testing.T) {
	handler := mirrorHandler(t, newFakeMirrors())
	if _, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: mirrorStreamID})); err == nil {
		t.Fatal("an unknown stream was reported as live")
	} else if code := connectrpc.CodeOf(err); code != connectrpc.CodeNotFound {
		t.Fatalf("refusal code = %v, want not_found", code)
	}
	if _, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: "  "})); err == nil {
		t.Fatal("a blank stream identity was accepted")
	} else if code := connectrpc.CodeOf(err); code != connectrpc.CodeInvalidArgument {
		t.Fatalf("refusal code = %v, want invalid_argument", code)
	}
}

// TestAnUnknownIdentityIsRefusedWithWhatIsCarried is the diagnosability the
// refusal owes an operator: the same sentence covers an identity that was never a
// stream, one whose session has ended, and one that is a stream under another
// name, and a frame that shows nothing cannot tell them apart. So the refusal
// names the identity it was given and the identities this service IS carrying, and
// says plainly that it carries none when that is the fact.
//
// What it names is a stream identity and never the serial a stream came from: the
// identities are the handles a browser is already given for the streams it opens.
func TestAnUnknownIdentityIsRefusedWithWhatIsCarried(t *testing.T) {
	live := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, serial: mirrorSerial}
	handler := mirrorHandler(t, newFakeMirrors(live))

	const asked = "drift-elsewhere-00000000"
	_, err := handler.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: asked}))
	if err == nil {
		t.Fatal("an unknown stream was reported as live")
	}
	message := err.Error()
	if !strings.Contains(message, `asked for "`+asked+`"`) {
		t.Fatalf("the refusal does not name the identity it was given: %q", message)
	}
	if !strings.Contains(message, mirrorStreamID) {
		t.Fatalf("the refusal does not name the stream that IS being carried (%q): %q", mirrorStreamID, message)
	}
	if strings.Contains(message, mirrorSerial) {
		t.Fatalf("the refusal names the serial a stream came from: %q", message)
	}

	// A service carrying nothing says so, rather than listing nothing.
	empty := mirrorHandler(t, newFakeMirrors())
	_, emptyErr := empty.GetMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: asked}))
	if emptyErr == nil {
		t.Fatal("an unknown stream was reported as live by a transport carrying nothing")
	}
	if !strings.Contains(emptyErr.Error(), "carrying nothing right now") {
		t.Fatalf("a transport carrying no streams did not say so: %q", emptyErr.Error())
	}
}

// TestStopMirrorStreamEndsItOnceAndReportsWhatItCarried: ending releases the
// browser's subscription, which is what stops a device nobody is watching from
// being captured, and the answer states the end over what the stream carried.
func TestStopMirrorStreamEndsItOnceAndReportsWhatItCarried(t *testing.T) {
	stream := &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice, frames: 30, keyFrames: 3}
	handler := mirrorHandler(t, newFakeMirrors(stream))

	response, err := handler.StopMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: mirrorStreamID,
	}))
	if err != nil {
		t.Fatalf("stop the stream: %v", err)
	}
	if stream.closes != 1 {
		t.Fatalf("the stream was released %d times, want exactly 1", stream.closes)
	}
	ended := response.Msg.GetStream()
	if ended.GetState() != driftv1.MirrorStreamState_MIRROR_STREAM_STATE_ENDED {
		t.Fatalf("state = %v, want ENDED", ended.GetState())
	}
	if ended.GetFrames() != 30 || ended.GetKeyFrames() != 3 {
		t.Fatalf("the ended stream reports %d frames and %d key frames, want what it carried", ended.GetFrames(), ended.GetKeyFrames())
	}
}

// TestStopMirrorStreamRefusesAStreamItDoesNotCarry: a stop for an identity this
// service never opened is a refusal, and no stream is released on the way.
func TestStopMirrorStreamRefusesAStreamItDoesNotCarry(t *testing.T) {
	stream := &fakeMirrorStream{key: mirrorStreamID}
	handler := mirrorHandler(t, newFakeMirrors(stream))
	_, err := handler.StopMirrorStream(context.Background(), connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{
		Context:  mirrorRequestContext(),
		StreamId: "drift-elsewhere-00000000",
	}))
	if err == nil {
		t.Fatal("a stop for an unknown stream was accepted")
	}
	if code := connectrpc.CodeOf(err); code != connectrpc.CodeNotFound {
		t.Fatalf("refusal code = %v, want not_found", code)
	}
	if stream.closes != 0 {
		t.Fatalf("an unknown stream released %d streams", stream.closes)
	}
}

// TestStartMirrorStreamReportsAnOpenFailureAsARefusal: a device whose capture
// cannot be opened is refused with the transport's own classification, and no
// stream is handed to the caller.
func TestStartMirrorStreamReportsAnOpenFailureAsARefusal(t *testing.T) {
	mirrors := newFakeMirrors()
	mirrors.openErr = platformerrors.Wrap(platformerrors.CodeUnavailable, "the device's stream could not be opened", errors.New("the scrcpy server refused"))
	handler := mirrorHandler(t, mirrors)
	_, err := handler.StartMirrorStream(context.Background(), startRequest(mirrorWorkspace, mirrorDevice, driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC))
	if err == nil {
		t.Fatal("a stream that could not be opened was reported as open")
	}
	if code := connectrpc.CodeOf(err); code != connectrpc.CodeUnavailable {
		t.Fatalf("refusal code = %v, want unavailable", code)
	}
}
