package transportconnect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
	transportconnect "drift.local/drift-next/internal/transport/connect"
	"github.com/coder/websocket"
)

const testConsoleOrigin = "http://localhost:5173"

func TestMirrorH264WebSocketUsesOneUseOriginBoundTicket(t *testing.T) {
	packet, err := media.EncodeH264FramePacket(media.H264FramePacket{
		Sequence: 1,
		Key:      true,
		Width:    720,
		Height:   1280,
		Data:     []byte{0, 0, 1, 0x65, 0x88},
	})
	if err != nil {
		t.Fatalf("encode expected packet: %v", err)
	}
	stream := &fakeH264EndpointStream{fakeMirrorStream: &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice}, packet: packet}
	handlers := transportconnect.NewMirrorH264Handlers(newFakeMirrors(stream))
	if handlers == nil {
		t.Fatal("H.264 handlers were not constructed over the live stream")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case transportconnect.MirrorH264TicketPath:
			handlers.TicketHandler().ServeHTTP(writer, request)
		case transportconnect.MirrorH264SocketPath:
			handlers.WebSocketHandler().ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	ticket := requestH264Ticket(t, server.URL+transportconnect.MirrorH264TicketPath, mirrorStreamID, testConsoleOrigin)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+transportconnect.MirrorH264SocketPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://attacker.example"}},
		Subprotocols: []string{
			transportconnect.MirrorH264Protocol,
			transportconnect.MirrorH264TicketSubprotocolPrefix + ticket,
		},
	})
	if err == nil {
		t.Fatal("a stream ticket was accepted from a different origin")
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+transportconnect.MirrorH264SocketPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{testConsoleOrigin}},
		Subprotocols: []string{
			transportconnect.MirrorH264Protocol,
			transportconnect.MirrorH264TicketSubprotocolPrefix + ticket,
		},
	})
	if err != nil {
		t.Fatalf("dial authenticated H.264 stream: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test complete") })
	if got := conn.Subprotocol(); got != transportconnect.MirrorH264Protocol {
		t.Fatalf("negotiated subprotocol = %q, want the fixed frame protocol", got)
	}
	_, received, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read H.264 frame: %v", err)
	}
	if string(received) != string(packet) {
		t.Fatalf("WebSocket frame = %d bytes, want the encoded H.264 packet (%d bytes)", len(received), len(packet))
	}
	if got := stream.servedCount(); got != 1 {
		t.Fatalf("stream carried %d WebSocket viewers, want one", got)
	}

	_, _, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+transportconnect.MirrorH264SocketPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{testConsoleOrigin}},
		Subprotocols: []string{
			transportconnect.MirrorH264Protocol,
			transportconnect.MirrorH264TicketSubprotocolPrefix + ticket,
		},
	})
	if err == nil {
		t.Fatal("a consumed stream ticket authorized a second WebSocket")
	}
}

func TestMirrorH264TicketRequiresAnAllowedOriginAndH264Stream(t *testing.T) {
	h264 := &fakeH264EndpointStream{fakeMirrorStream: &fakeMirrorStream{key: mirrorStreamID, deviceID: mirrorDevice}}
	peer := &fakeMirrorStream{key: "drift-device-beta-2abc1234", deviceID: "device-beta"}
	handlers := transportconnect.NewMirrorH264Handlers(newFakeMirrors(h264, peer))
	if handlers == nil {
		t.Fatal("H.264 handlers were not constructed")
	}
	server := httptest.NewServer(handlers.TicketHandler())
	t.Cleanup(server.Close)

	for _, test := range []struct {
		name      string
		streamKey string
		origin    string
	}{
		{name: "peer connection has no H.264 byte stream", streamKey: peer.key, origin: testConsoleOrigin},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"stream_id":"`+test.streamKey+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode < 400 {
				t.Fatalf("ticket request status = %d, want refusal", response.StatusCode)
			}
		})
	}
}

func requestH264Ticket(t *testing.T, endpoint, streamKey, origin string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"stream_id":"`+streamKey+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d, want 201", response.StatusCode)
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode ticket response: %v", err)
	}
	if body.Ticket == "" {
		t.Fatal("ticket response is empty")
	}
	return body.Ticket
}

type fakeH264EndpointStream struct {
	*fakeMirrorStream
	mu     sync.Mutex
	packet []byte
	served int
}

func (s *fakeH264EndpointStream) ServeH264(ctx context.Context, write func([]byte) error) error {
	s.mu.Lock()
	s.served++
	s.mu.Unlock()
	if len(s.packet) == 0 {
		return nil
	}
	return write(s.packet)
}

func (s *fakeH264EndpointStream) servedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served
}
