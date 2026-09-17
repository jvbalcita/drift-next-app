// Package scrcpy speaks the part of the scrcpy 4.1 client protocol this spike
// needs: it pushes and starts the server on the device, owns the video and
// control sockets, and encodes the one control message the measurement uses.
//
// It exists so the spike measures the hop ARC-143 will actually ship -- a raw
// H.264 stream off scrcpy's video socket and input injected through scrcpy's
// own control socket -- rather than a stand-in. It deliberately does NOT use
// `adb shell input`: that spawns a process on the device and costs 100-300 ms,
// which is exactly the effect the measurement has to distinguish itself from.
//
// Protocol references (scrcpy 4.1, read-only):
//   - doc/develop.md, "Protocol"
//   - server/.../device/DesktopConnection.java (socket order, dummy byte)
//   - app/tests/test_control_msg_serialize.c (control message layout)
package scrcpy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VideoCodecH264 is the codec id the server sends first on the video socket.
const VideoCodecH264 uint32 = 0x68323634 // "h264"

// PacketHeaderSize is the 12-byte header prefixed to every packet when
// send_frame_meta=true.
const PacketHeaderSize = 12

// SessionPacketSize is the 12-byte session header sent once per capture session
// when send_stream_meta=true.
const SessionPacketSize = 12

// Options configure one scrcpy session against one device.
type Options struct {
	ADB        string // adb binary
	Serial     string // device serial (host:port for network adb)
	ServerPath string // local path to the scrcpy-server jar
	MaxSize    int    // 0 keeps the device's own capture size
	MaxFPS     int    // 0 keeps the device's own capture cadence
	BitRate    int    // video bit rate in bits/s; 0 keeps the server's own default
	LogLevel   string // scrcpy server log level
	AcceptWait time.Duration
	// CodecOptions are passed to the device's MediaCodec video encoder as
	// key=value pairs. MediaCodec's KEY_I_FRAME_INTERVAL is what makes the
	// device emit periodic IDRs: measured on this fleet, the device's screen
	// encoder produces a single IDR at session start and never another, so a
	// receiver that is not decoding from the first frame stays black for the
	// whole session. That is a property of the device half, not of the browser,
	// and it has to be fixed at the encoder.
	CodecOptions []string
}

// Session is one live scrcpy session: a video socket carrying raw H.264 access
// units and a control socket for injected input.
type Session struct {
	opts Options

	video   net.Conn
	control net.Conn
	shell   *exec.Cmd
	scid    uint32
	port    int
	scidHex string

	// Width and Height are the encoded frame size the server reported in its
	// session packet. They are the coordinate frame injected input must use.
	Width  int
	Height int

	packets  int
	bytes    int64
	writeMu  sync.Mutex
	stopOnce sync.Once
	stderr   *lineBuffer
}

// Start pushes the server, opens the reverse tunnel, launches the server and
// returns the session once the device has told us what it is encoding.
func Start(ctx context.Context, opts Options) (*Session, error) {
	if opts.ADB == "" {
		opts.ADB = "adb"
	}
	if opts.Serial == "" {
		return nil, errors.New("scrcpy: no device serial")
	}
	if opts.ServerPath == "" {
		return nil, errors.New("scrcpy: no server path")
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "info"
	}
	if opts.AcceptWait == 0 {
		opts.AcceptWait = 15 * time.Second
	}

	s := &Session{opts: opts, stderr: &lineBuffer{}}
	// A 31-bit random id, so several sessions on one device cannot collide.
	s.scid = uint32(time.Now().UnixNano()) & 0x7fffffff
	s.scidHex = fmt.Sprintf("%08x", s.scid)
	abstract := "scrcpy_" + s.scidHex

	if out, err := runADB(ctx, opts.ADB, opts.Serial, "push", opts.ServerPath, "/data/local/tmp/scrcpy-server.jar"); err != nil {
		return nil, fmt.Errorf("scrcpy: pushing server: %v: %s", err, out)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("scrcpy: listening: %w", err)
	}
	s.port = listener.Addr().(*net.TCPAddr).Port
	defer listener.Close()

	if out, err := runADB(ctx, opts.ADB, opts.Serial, "reverse", "localabstract:"+abstract, "tcp:"+strconv.Itoa(s.port)); err != nil {
		return nil, fmt.Errorf("scrcpy: adb reverse: %v: %s", err, out)
	}

	args := []string{"-s", opts.Serial, "shell",
		"CLASSPATH=/data/local/tmp/scrcpy-server.jar", "app_process", "/",
		"com.genymobile.scrcpy.Server", "4.1",
		"scid=" + s.scidHex,
		"log_level=" + opts.LogLevel,
		"audio=false",
		"control=true",
		"tunnel_forward=false",
		// The device name is not needed and would have to be parsed off the
		// first socket; the stream and frame metadata ARE needed: the session
		// packet carries the encoded size (the input coordinate frame), and the
		// frame header gives one packet per encoded frame with its own
		// boundaries, so a live access unit can be forwarded the moment it
		// arrives instead of being held until the next one starts.
		"send_device_meta=false",
		"send_stream_meta=true",
		"send_frame_meta=true",
		"send_dummy_byte=false",
	}
	if opts.MaxSize > 0 {
		args = append(args, "max_size="+strconv.Itoa(opts.MaxSize))
	}
	if opts.MaxFPS > 0 {
		args = append(args, "max_fps="+strconv.Itoa(opts.MaxFPS))
	}
	if opts.BitRate > 0 {
		args = append(args, "video_bit_rate="+strconv.Itoa(opts.BitRate))
	}
	if len(opts.CodecOptions) > 0 {
		args = append(args, "video_codec_options="+strings.Join(opts.CodecOptions, ","))
	}
	cmd := exec.CommandContext(ctx, opts.ADB, args...)
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		s.cleanup(ctx)
		return nil, fmt.Errorf("scrcpy: starting server: %w", err)
	}
	s.shell = cmd

	// Socket order in reverse mode is video, then audio (disabled), then
	// control: the server connects in that order, so the accept order is the
	// assignment (DesktopConnection.open, tunnelForward=false).
	video, err := acceptWithTimeout(listener, opts.AcceptWait)
	if err != nil {
		s.cleanup(ctx)
		return nil, fmt.Errorf("scrcpy: video socket: %w (%s)", err, s.stderr.String())
	}
	control, err := acceptWithTimeout(listener, opts.AcceptWait)
	if err != nil {
		video.Close()
		s.cleanup(ctx)
		return nil, fmt.Errorf("scrcpy: control socket: %w (%s)", err, s.stderr.String())
	}
	s.video, s.control = video, control

	if err := s.readStreamMeta(); err != nil {
		s.Stop(context.Background())
		return nil, err
	}
	go s.drainControl()
	go func() { _ = cmd.Wait() }()
	return s, nil
}

// readStreamMeta consumes the codec id and the session packet.
func (s *Session) readStreamMeta() error {
	_ = s.video.SetReadDeadline(time.Now().Add(s.opts.AcceptWait))
	hdr := make([]byte, 4+SessionPacketSize)
	if _, err := io.ReadFull(s.video, hdr); err != nil {
		return fmt.Errorf("scrcpy: reading stream metadata: %w (%s)", err, s.stderr.String())
	}
	codec := binary.BigEndian.Uint32(hdr[0:4])
	if codec != VideoCodecH264 {
		return fmt.Errorf("scrcpy: device is streaming codec %#08x, not h264", codec)
	}
	// Session packet: byte 0 bit 7 is the session flag; bytes 4..11 the size.
	if hdr[4]&0x80 == 0 {
		return fmt.Errorf("scrcpy: expected a session packet, got %#02x", hdr[4])
	}
	s.Width = int(binary.BigEndian.Uint32(hdr[8:12]))
	s.Height = int(binary.BigEndian.Uint32(hdr[12:16]))
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("scrcpy: device reported a %dx%d stream", s.Width, s.Height)
	}
	_ = s.video.SetReadDeadline(time.Time{})
	return nil
}

// Packet is one encoded frame as the device produced it.
type Packet struct {
	Seq         int    `json:"seq"`
	PTSUS       uint64 `json:"pts_us"`
	Key         bool   `json:"key"`
	Config      bool   `json:"config"`
	Bytes       int    `json:"bytes"`
	ConfigBytes int    `json:"config_bytes"`
	RecvNS      int64  `json:"recv_ns"`
	SentNS      int64  `json:"sent_ns"`
	WriteNS     int64  `json:"write_ns"`
	RTPPackets  int    `json:"rtp_packets"`
	RTPBytes    int    `json:"rtp_bytes"`
	Duration    time.Duration
	Data        []byte
}

// ReadPacket reads the next packet. Config packets (SPS/PPS) are prepended to
// the next media packet rather than forwarded as their own access unit, which
// is what a demuxer does and what keeps every sample the payloader sees a
// self-contained picture.
func (s *Session) ReadPacket() (Packet, error) {
	var pending []byte
	var pendingConfigBytes int
	for {
		hdr := make([]byte, PacketHeaderSize)
		if _, err := io.ReadFull(s.video, hdr); err != nil {
			return Packet{}, err
		}
		key, config, pts, size, err := ParsePacketHeader(hdr)
		if err != nil {
			return Packet{}, err
		}
		pkt := Packet{
			Seq:    s.packets + 1,
			Key:    key,
			Config: config,
			PTSUS:  pts,
			RecvNS: time.Now().UnixNano(),
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(s.video, data); err != nil {
			return Packet{}, err
		}
		if pkt.Config {
			pending = append(pending, data...)
			pendingConfigBytes += size
			continue
		}
		if len(pending) > 0 {
			data = append(pending, data...)
			pending = nil
		}
		pkt.Data = data
		pkt.Bytes = len(data)
		pkt.ConfigBytes = pendingConfigBytes
		pendingConfigBytes = 0
		s.packets++
		s.bytes += int64(size)
		return pkt, nil
	}
}

// ParsePacketHeader decodes the 12-byte frame header the server prefixes to
// every packet when send_frame_meta=true.
//
// The header is a big-endian u64 followed by a u32 size, and it is the server's
// own bit assignment that defines it (device/Streamer.java, v4.1):
//
//	bit 63 = session packet,  bit 62 = config packet,  bit 61 = key frame,
//	bits 0..60 (the remaining 61 bits) = PTS in microseconds
//
// A media packet therefore has the top bit *clear* -- the flag is the session
// flag, not a "media" flag -- which is the opposite of what the prose in
// doc/develop.md ("0CK.....") reads like.
func ParsePacketHeader(hdr []byte) (key, config bool, pts uint64, size int, err error) {
	if len(hdr) < PacketHeaderSize {
		return false, false, 0, 0, fmt.Errorf("scrcpy: short packet header (%d bytes)", len(hdr))
	}
	flag := hdr[0]
	if flag&0x80 != 0 {
		return false, false, 0, 0, errors.New("scrcpy: expected a media packet, got a session packet")
	}
	config = flag&0x40 != 0
	key = flag&0x20 != 0
	// PTS is 61 bits: the low 5 bits of byte 0, then bytes 1..7.
	pts = uint64(flag&0x1f) << 56
	for i := 1; i < 8; i++ {
		pts |= uint64(hdr[i]) << uint(8*(7-i))
	}
	size = int(binary.BigEndian.Uint32(hdr[8:12]))
	if size > 64<<20 {
		return false, false, 0, 0, fmt.Errorf("scrcpy: implausible packet size %d", size)
	}
	return key, config, pts, size, nil
}

// Packets and Bytes report how much of the stream has been read.
func (s *Session) Packets() (int, int64) { return s.packets, s.bytes }

// Frames returns the encoded size the device is streaming.
func (s *Session) Size() (int, int) { return s.Width, s.Height }

// Touch writes one tap as a DOWN/UP pair on the control socket and reports the
// host-clock instants the two messages were written.
//
// The write instant is the measurement's left edge: everything after it --
// tunnel, server controller thread, Android input dispatch, the app's handler
// -- is what the input round-trip number contains.
func (s *Session) Touch(x, y int) (downNS, upNS int64, err error) {
	down := EncodeTouch(ActionDown, x, y, s.Width, s.Height)
	up := EncodeTouch(ActionUp, x, y, s.Width, s.Height)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.control == nil {
		return 0, 0, errors.New("scrcpy: control socket is closed")
	}
	downNS = time.Now().UnixNano()
	if _, err := s.control.Write(down); err != nil {
		return downNS, 0, fmt.Errorf("scrcpy: writing touch down: %w", err)
	}
	upNS = time.Now().UnixNano()
	if _, err := s.control.Write(up); err != nil {
		return downNS, upNS, fmt.Errorf("scrcpy: writing touch up: %w", err)
	}
	return downNS, upNS, nil
}

// Control message types and actions (control_msg.h).
const (
	ControlInjectKeycode   = 0
	ControlInjectText      = 1
	ControlInjectTouch     = 2
	ControlSetClipboard    = 9
	ActionDown             = 0
	ActionUp               = 1
	ActionMove             = 2
	GenericFingerPointerID = 0xfffffffffffffffe
	ButtonPrimary          = 1
)

// EncodeTouch builds the 32-byte INJECT_TOUCH_EVENT message.
//
// Layout (app/tests/test_control_msg_serialize.c): type u8, action u8,
// pointer id u64, x i32, y i32, width u16, height u16, pressure u16,
// action button u32, buttons u32. width/height are the encoded frame size the
// device is streaming, which is the frame those coordinates are measured in.
func EncodeTouch(action, x, y, width, height int) []byte {
	b := make([]byte, 32)
	b[0] = ControlInjectTouch
	b[1] = byte(action)
	binary.BigEndian.PutUint64(b[2:10], GenericFingerPointerID)
	binary.BigEndian.PutUint32(b[10:14], uint32(int32(x)))
	binary.BigEndian.PutUint32(b[14:18], uint32(int32(y)))
	binary.BigEndian.PutUint16(b[18:20], uint16(width))
	binary.BigEndian.PutUint16(b[20:22], uint16(height))
	binary.BigEndian.PutUint16(b[22:24], 0xffff) // pressure 1.0
	binary.BigEndian.PutUint32(b[24:28], ButtonPrimary)
	binary.BigEndian.PutUint32(b[28:32], ButtonPrimary)
	return b
}

// Stop closes the sockets, stops the server process and removes the tunnel.
func (s *Session) Stop(ctx context.Context) {
	s.stopOnce.Do(func() { s.cleanup(ctx) })
}

func (s *Session) cleanup(ctx context.Context) {
	if s.control != nil {
		_ = s.control.Close()
	}
	if s.video != nil {
		_ = s.video.Close()
	}
	if s.shell != nil && s.shell.Process != nil {
		_ = s.shell.Process.Kill()
	}
	if s.scidHex != "" {
		abstract := "scrcpy_" + s.scidHex
		if out, err := runADB(ctx, s.opts.ADB, s.opts.Serial, "reverse", "--remove", "localabstract:"+abstract); err != nil {
			log.Printf("scrcpy: removing reverse tunnel %s: %v: %s", abstract, err, out)
		}
	}
}

// drainControl reads whatever the server sends back (device messages such as
// clipboard changes). Leaving it unread would block the server's writer thread.
func (s *Session) drainControl() {
	buf := make([]byte, 4096)
	for {
		if _, err := s.control.Read(buf); err != nil {
			return
		}
	}
}

// ServerStderr returns the scrcpy server's log output so a run can report it.
func (s *Session) ServerStderr() string { return s.stderr.String() }

func acceptWithTimeout(l net.Listener, d time.Duration) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := l.Accept()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("no connection within %s", d)
	}
}

func runADB(ctx context.Context, adb, serial string, args ...string) (string, error) {
	full := append([]string{"-s", serial}, args...)
	cmd := exec.CommandContext(ctx, adb, full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
