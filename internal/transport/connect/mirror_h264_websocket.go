package transportconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// MirrorH264SocketPath carries one bounded binary Annex-B frame per message.
	MirrorH264SocketPath = "/drift/v1/mirror/h264"
	// MirrorH264TicketPath exchanges normal header authentication for a one-use
	// ticket suitable for the browser WebSocket handshake.
	MirrorH264TicketPath = "/drift/v1/mirror/h264-ticket"
	MirrorH264Protocol   = "drift-h264-v1"
	// MirrorH264TicketSubprotocolPrefix carries an ephemeral ticket in the
	// handshake header instead of a URL, where browser history and access logs
	// would retain it.
	MirrorH264TicketSubprotocolPrefix = "drift-ticket."
	mirrorH264TicketTTL               = 15 * time.Second
	maxMirrorH264Tickets              = 256
	maxMirrorH264TicketRequestBytes   = 1024
	maxMirrorH264ClientMessageBytes   = 64
)

// DeviceMirrorH264Stream is one per-viewing stream that can carry raw H.264
// access units. The same endpoint still provides fragmented MP4 through Serve;
// this capability adds a browser-native low-latency tier without starting a
// second device capture.
type DeviceMirrorH264Stream interface {
	DeviceMirrorStream
	ServeH264(ctx context.Context, write func([]byte) error) error
}

type mirrorH264Ticket struct {
	streamKey string
	origin    string
	expiresAt time.Time
}

// MirrorH264Handlers owns the bounded ephemeral authorization cache shared by
// the ticket-mint and WebSocket routes. Tickets authorize only one stream and
// one exact browser origin, and are consumed once before a connection upgrades.
type MirrorH264Handlers struct {
	streams DeviceMirrors
	tickets map[string]mirrorH264Ticket
	mu      sync.Mutex
	now     func() time.Time
}

// NewMirrorH264Handlers returns nil when no live transport was constructed, so
// neither raw H.264 surface is mounted over an absent engine.
func NewMirrorH264Handlers(streams DeviceMirrors) *MirrorH264Handlers {
	if isAbsentDeviceMirrors(streams) {
		return nil
	}
	return &MirrorH264Handlers{streams: streams, tickets: make(map[string]mirrorH264Ticket), now: time.Now}
}

// TicketHandler exchanges the authenticated lab-token request for a short-lived
// browser credential. The caller's stream identity is resolved before a ticket
// is issued, and neither the long-lived token nor a device identity enters the
// WebSocket URL or messages.
func (h *MirrorH264Handlers) TicketHandler() http.Handler {
	return http.HandlerFunc(h.issueTicket)
}

// WebSocketHandler carries one H.264 frame packet at a time. It accepts no
// client data messages; bounded reads exist only to process close and ping
// control frames so a disconnected browser promptly releases its subscription.
func (h *MirrorH264Handlers) WebSocketHandler() http.Handler {
	return http.HandlerFunc(h.serveWebSocket)
}

func (h *MirrorH264Handlers) issueTicket(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.streams == nil {
		http.Error(writer, "the live H.264 stream is not constructed", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "a live H.264 ticket is issued with POST", http.StatusMethodNotAllowed)
		return
	}
	origin := request.Header.Get("Origin")
	if !validMirrorOrigin(origin) {
		http.Error(writer, "the live H.264 ticket requires a valid browser origin", http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxMirrorH264TicketRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload struct {
		StreamID string `json:"stream_id"`
	}
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.StreamID) == "" {
		http.Error(writer, "a live H.264 ticket requires a stream identity", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(writer, "the live H.264 ticket request must contain one value", http.StatusBadRequest)
		return
	}
	stream, live := h.streams.Stream(payload.StreamID)
	if !live || stream.StreamKey() != payload.StreamID {
		http.Error(writer, noSuchStreamError(h.streams, payload.StreamID).Error(), http.StatusNotFound)
		return
	}
	if _, ok := stream.(DeviceMirrorH264Stream); !ok {
		http.Error(writer, "that stream does not carry raw H.264 frames", http.StatusNotFound)
		return
	}
	ticket, err := h.mint(payload.StreamID, origin)
	if err != nil {
		http.Error(writer, "the live H.264 ticket service is at capacity", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(struct {
		Ticket string `json:"ticket"`
	}{Ticket: ticket})
}

func (h *MirrorH264Handlers) serveWebSocket(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.streams == nil {
		http.Error(writer, "the live H.264 stream is not constructed", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "the live H.264 stream uses a WebSocket GET", http.StatusMethodNotAllowed)
		return
	}
	origin := request.Header.Get("Origin")
	ticket, appProtocol, valid := parseMirrorH264Protocols(request.Header.Values("Sec-WebSocket-Protocol"))
	if !valid || !appProtocol || !validMirrorOrigin(origin) {
		http.Error(writer, "the live H.264 WebSocket requires its protocol, ticket, and browser origin", http.StatusForbidden)
		return
	}
	streamKey, valid := h.consume(ticket, origin)
	if !valid {
		http.Error(writer, "the live H.264 WebSocket ticket is invalid, expired, or already used", http.StatusForbidden)
		return
	}
	stream, live := h.streams.Stream(streamKey)
	if !live || stream.StreamKey() != streamKey {
		http.Error(writer, "the live stream is no longer being carried", http.StatusNotFound)
		return
	}
	endpoint, ok := stream.(DeviceMirrorH264Stream)
	if !ok {
		http.Error(writer, "that stream does not carry raw H.264 frames", http.StatusNotFound)
		return
	}
	// The one-use ticket was minted with the lab token and bound to this exact
	// Origin. This manual check is stricter than the library's default same-host
	// rule, while allowing the supported local console origins across ports and
	// Tauri's custom scheme. Compression stays disabled for already-compressed
	// H.264 and to avoid per-connection memory overhead.
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols:       []string{MirrorH264Protocol},
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer func() { _ = connection.Close(websocket.StatusNormalClosure, "stream ended") }()
	if connection.Subprotocol() != MirrorH264Protocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "unsupported stream protocol")
		return
	}
	connection.SetReadLimit(maxMirrorH264ClientMessageBytes)
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, _, err := connection.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			// This is a server-to-browser video channel. The browser may send
			// WebSocket control frames, but no data messages are part of its API.
			cancel()
			return
		}
	}()
	write := func(packet []byte) error {
		return connection.Write(ctx, websocket.MessageBinary, packet)
	}
	_ = endpoint.ServeH264(ctx, write)
	cancel()
	_ = connection.Close(websocket.StatusNormalClosure, "stream ended")
	<-readerDone
}

func (h *MirrorH264Handlers) mint(streamKey, origin string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for existing, issued := range h.tickets {
		if !issued.expiresAt.After(now) {
			delete(h.tickets, existing)
		}
	}
	if len(h.tickets) >= maxMirrorH264Tickets {
		return "", errors.New("ticket capacity reached")
	}
	h.tickets[ticket] = mirrorH264Ticket{streamKey: streamKey, origin: origin, expiresAt: now.Add(mirrorH264TicketTTL)}
	return ticket, nil
}

func (h *MirrorH264Handlers) consume(ticket, origin string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	issued, ok := h.tickets[ticket]
	if !ok || !issued.expiresAt.After(h.now()) || issued.origin != origin {
		return "", false
	}
	delete(h.tickets, ticket)
	return issued.streamKey, true
}

func parseMirrorH264Protocols(values []string) (ticket string, appProtocol, valid bool) {
	ticketSeen := false
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == MirrorH264Protocol {
				appProtocol = true
				continue
			}
			if strings.HasPrefix(candidate, MirrorH264TicketSubprotocolPrefix) {
				if ticketSeen {
					return "", false, false
				}
				ticketSeen = true
				ticket = strings.TrimPrefix(candidate, MirrorH264TicketSubprotocolPrefix)
			}
		}
	}
	return ticket, appProtocol, ticketSeen && ticket != ""
}

func validMirrorOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "tauri":
		return true
	default:
		return false
	}
}
