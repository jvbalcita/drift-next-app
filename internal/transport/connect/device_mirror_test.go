package transportconnect_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	width     int
	height    int

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
	}
}

func (s *fakeMirrorStream) Close() error {
	s.closes++
	return s.closeErr
}

// fakeMirrors is the stream transport the surface calls.
type fakeMirrors struct {
	streams  map[string]*fakeMirrorStream
	openErr  error
	opened   []string
	openFunc func(deviceID, serial string) *fakeMirrorStream
}

func newFakeMirrors(streams ...*fakeMirrorStream) *fakeMirrors {
	byKey := make(map[string]*fakeMirrorStream, len(streams))
	for _, stream := range streams {
		byKey[stream.key] = stream
	}
	return &fakeMirrors{streams: byKey}
}

func (m *fakeMirrors) Open(_ context.Context, deviceID, serial string) (transportconnect.DeviceMirrorStream, error) {
	m.opened = append(m.opened, deviceID+"/"+serial)
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

func mirrorRequestContext() *driftv1.RequestContext {
	return &driftv1.RequestContext{
		RequestId:      "mirror-request-1",
		ActorId:        "operator-1",
		IdempotencyKey: "mirror-key-1",
	}
}

func mirrorHandler(t *testing.T, mirrors transportconnect.DeviceMirrors) *transportconnect.DeviceMirrorHandler {
	t.Helper()
	handler := transportconnect.NewDeviceMirrorHandler(mirrors, &fixedSerials{serial: mirrorSerial})
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
	if handler := transportconnect.NewDeviceMirrorHandler(nil, serials); handler != nil {
		t.Fatal("a handler with no stream transport was constructed")
	}
	var typedNil *fakeMirrors
	if handler := transportconnect.NewDeviceMirrorHandler(typedNil, serials); handler != nil {
		t.Fatal("a handler over a typed-nil stream transport was constructed")
	}
	if handler := transportconnect.NewDeviceMirrorHandler(newFakeMirrors(), nil); handler != nil {
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
			name: "a transport that is not implemented", workspace: mirrorWorkspace, device: mirrorDevice,
			transport: driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP,
			wantCode:  connectrpc.CodeUnavailable,
		},
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

// TestStartMirrorStreamReportsARefusedDeviceAsARefusal: a device with no current
// transport endpoint has no stream to open, and that is the resolver's own
// classified refusal rather than an internal failure.
func TestStartMirrorStreamReportsARefusedDeviceAsARefusal(t *testing.T) {
	mirrors := newFakeMirrors()
	handler := transportconnect.NewDeviceMirrorHandler(mirrors, &fixedSerials{err: platformerrors.New(platformerrors.CodeUnavailable, "the device has no current transport")})
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
