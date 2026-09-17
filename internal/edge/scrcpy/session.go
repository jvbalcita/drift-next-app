package scrcpy

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

var (
	// ErrStreamDisabled reports that the device refused the screen capture it was
	// asked for. The device says so on the video socket; nothing else will be
	// sent, so a session that reads it has ended.
	ErrStreamDisabled = errors.New("scrcpy: the device refused the screen capture")

	// ErrStreamConfiguration reports the device's own configuration-error
	// signal: the requested capture cannot be configured at all.
	ErrStreamConfiguration = errors.New("scrcpy: the device reported a capture configuration error")

	// ErrServerMissing reports a scrcpy-server path that is not a readable file.
	// The server is a deployed artifact, so a deployment without it fails at the
	// boundary rather than after pushing an empty file.
	ErrServerMissing = errors.New("scrcpy: the server file is not a readable file")
)

// Runner executes one bounded adb command and returns its outcome. It is the
// same narrow port the rest of the edge adapters use, so the scrcpy client
// reaches adb exactly the way the allow-listed runner does: an executable and an
// argument array, never a shell.
type Runner interface {
	Run(ctx context.Context, executable string, args []string) (adb.Result, error)
}

// Process is one long-lived device-side process this session owns.
type Process interface {
	// Kill asks the process to stop. It is called once per session.
	Kill() error
	// Wait blocks until the process has exited and reports its outcome.
	Wait() error
}

// Starter starts the device-side server. It is separate from Runner because the
// server outlives any bounded command: it runs for the whole mirror session and
// exits when the session closes, so it cannot be started through a runner that
// waits for its exit.
type Starter interface {
	Start(executable string, args []string, stderr io.Writer) (Process, error)
}

// Options configure one live session against one device.
type Options struct {
	// ADB is the absolute path of the adb executable.
	ADB string
	// Serial names the device. It is validated before it reaches an argument
	// array, so a serial can never change what a command means.
	Serial string
	// ServerPath is the host path of the scrcpy server binary. It is a
	// deployment input rather than a constant because it is where the host's
	// scrcpy installation puts it.
	ServerPath string
	// Runner runs the bounded adb commands: the push and the reverse tunnel.
	Runner Runner
	// Starter starts the long-lived device-side server.
	Starter Starter
	// Listen opens the loopback listener the device connects back to. A test
	// supplies its own; the default binds 127.0.0.1 on an ephemeral port.
	Listen func() (net.Listener, error)
	// IDSource returns the session id the device names its socket after. A zero
	// IDSource draws one from the system's random source.
	IDSource func() (uint32, error)
	// AcceptWait bounds the wait for the device to connect and report what it is
	// streaming.
	AcceptWait time.Duration
	// LogLevel is the device-side server's own log level.
	LogLevel string
	// KeepAwake holds the device's screen on for the life of the session.
	//
	// It is on by default and it is a deliberate decision rather than a
	// convenience: a screen-off device produces no frames at all, so a mirror of
	// a sleeping device is a permanently black frame with nothing to report -
	// exactly the silent failure this feature must not have. Keeping the screen
	// on is what the mirror is for; it is not a device lifecycle change (the
	// device is not powered off, restarted, or reconfigured) and it ends with the
	// session.
	KeepAwake bool
}

// DefaultAcceptWait bounds the device handshake when a caller supplies no bound.
const DefaultAcceptWait = 15 * time.Second

// Session is one live scrcpy session: a video socket carrying encoded access
// units, a control socket carrying typed input, and the device-side process that
// serves them.
//
// A Session is single-reader: exactly one goroutine reads the video socket.
// Writes to the control socket are serialized internally, so several callers may
// send input.
type Session struct {
	opts  Options
	meta  StreamMeta
	scid  uint32
	port  int
	video net.Conn
	ctrl  net.Conn
	proc  Process

	// writeMu serializes control-socket writes: two half-written messages
	// interleaved would be one corrupt message, and the device would act on it.
	writeMu sync.Mutex
	stats   Stats
	statsMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}
	doneErr   error
	errMu     sync.Mutex
}

// Stats is what one session has carried. It is evidence about the session rather
// than about the device, so it is reported alongside the session's outcome.
type Stats struct {
	Packets    int
	Bytes      int64
	Configs    int
	KeyFrames  int
	Inputs     int
	ResetCalls int
	StartedAt  time.Time
}

// Start pushes the server, opens the loopback listener, registers the reverse
// tunnel, launches the device-side server, and returns the session once the
// device has reported what it is encoding.
//
// Every step that can fail leaves nothing behind: a launch that fails removes
// the tunnel and kills the process it started, so a session either exists and
// can be closed, or does not exist at all.
func Start(ctx context.Context, opts Options) (*Session, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.AcceptWait <= 0 {
		opts.AcceptWait = DefaultAcceptWait
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "info"
	}
	scid, err := sessionID(opts.IDSource)
	if err != nil {
		return nil, err
	}
	if opts.Listen == nil {
		opts.Listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}

	session := &Session{opts: opts, scid: scid, closed: make(chan struct{})}

	// The server is pushed on every session rather than reused from a previous
	// one. The device-side server removes the pushed file when it exits, and a
	// stale copy is exactly the hazard the push exists to remove: a file from an
	// unknown build left behind by an interrupted session.
	if _, err := opts.Runner.Run(ctx, opts.ADB, []string{"-s", opts.Serial, "push", opts.ServerPath, ServerDevicePath}); err != nil {
		return nil, fmt.Errorf("scrcpy: pushing the server to %s: %w", opts.Serial, err)
	}

	listener, err := opts.Listen()
	if err != nil {
		return nil, fmt.Errorf("scrcpy: opening the loopback listener: %w", err)
	}
	// The listener is closed as soon as the device has connected. It exists only
	// for the handshake, so the process holds no listening socket beyond it, and
	// the address it used is on the host's loopback interface and never on the
	// network.
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return nil, fmt.Errorf("scrcpy: the handshake listener must be bound to loopback, got %s", listener.Addr())
	}
	session.port = addr.Port

	if err := session.reverse(ctx, "add"); err != nil {
		return nil, err
	}
	cleanupTunnel := true
	defer func() {
		if cleanupTunnel {
			// A failed handshake still registered a tunnel, and a leaked tunnel
			// makes the device's abstract socket answer for a port nothing is
			// listening on.
			session.reverse(context.WithoutCancel(ctx), "remove")
		}
	}()

	argv := session.serverArgv()
	proc, err := opts.Starter.Start(opts.ADB, argv, &boundedWriter{limit: 8 << 10})
	if err != nil {
		return nil, fmt.Errorf("scrcpy: starting the device server: %w", err)
	}
	session.proc = proc

	if session.video, err = session.accept(listener); err != nil {
		_ = proc.Kill()
		return nil, err
	}
	deadline := time.Now().Add(opts.AcceptWait)
	if err := session.readStreamMeta(deadline); err != nil {
		session.video.Close()
		_ = proc.Kill()
		return nil, err
	}
	if session.ctrl, err = session.accept(listener); err != nil {
		session.video.Close()
		_ = proc.Kill()
		return nil, err
	}

	cleanupTunnel = false
	session.stats.StartedAt = time.Now()
	// The process is owned from here: an unexpected exit ends the session, so the
	// reader above fails with a reason rather than blocking on a socket whose
	// server is gone.
	go func() {
		_ = proc.Wait()
		session.end(errors.New("scrcpy: the device server exited"))
	}()
	// Whatever the device sends back on the control socket is read and dropped.
	// Leaving it unread blocks the server's own writer thread, which stops input
	// injection for every later message.
	go session.drainControl()
	return session, nil
}

// Meta reports what the device said it is streaming, including the encoded frame
// size that injected coordinates must be measured in.
func (s *Session) Meta() StreamMeta { return s.meta }

// Done is closed when the session has ended, whether by Close or by the device.
func (s *Session) Done() <-chan struct{} { return s.closed }

// Err reports why the session ended, or nil while it is live.
func (s *Session) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.doneErr
}

// Stats reports what the session has carried.
func (s *Session) Stats() Stats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.stats
}

// ReadAccessUnit reads the next packet from the video socket. It is called from
// one goroutine only.
func (s *Session) ReadAccessUnit() (AccessUnit, error) {
	header := make([]byte, PacketHeaderSize)
	if _, err := io.ReadFull(s.video, header); err != nil {
		return AccessUnit{}, s.end(fmt.Errorf("scrcpy: reading a frame header: %w", err))
	}
	config, key, pts, size, err := parseFrameHeader(header)
	if err != nil {
		return AccessUnit{}, s.end(err)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(s.video, data); err != nil {
		return AccessUnit{}, s.end(fmt.Errorf("scrcpy: reading a frame payload of %d bytes: %w", size, err))
	}
	s.statsMu.Lock()
	s.stats.Packets++
	s.stats.Bytes += int64(size)
	if config {
		s.stats.Configs++
	}
	if key {
		s.stats.KeyFrames++
	}
	s.statsMu.Unlock()
	return AccessUnit{Config: config, Key: key, PTSUS: pts, Data: data}, nil
}

// Touch sends one touch action at a point inside the stream's own frame.
//
// The frame is the session's, never the caller's: the coordinate is validated
// against the size the device reported on the video socket, so a point can only
// be sent in the frame it was measured in.
func (s *Session) Touch(action byte, x, y int) error {
	message, err := EncodeTouch(action, x, y, s.meta.Width, s.meta.Height)
	if err != nil {
		return err
	}
	return s.write(message)
}

// Swipe sends a swipe as one down, a bounded series of moves, and one up.
//
// The moves are bounded twice over: by a step count and by a cadence, so a long
// swipe is a bounded number of messages rather than a stream of them. The device
// interpolates between the points; it does not need every intermediate pixel,
// and a caller that sent them would be sending an unbounded message stream at
// the device for one gesture (AGENTS.md section 3).
func (s *Session) Swipe(ctx context.Context, fromX, fromY, toX, toY int, duration time.Duration) error {
	if duration <= 0 {
		return errors.New("scrcpy: a swipe needs a positive duration")
	}
	steps := swipeSteps(duration)
	if err := s.Touch(ActionDown, fromX, fromY); err != nil {
		return err
	}
	// A failure part-way through still releases the touch: a device left holding
	// a pointer is a device whose next gesture is not the operator's.
	sendErr := func() error {
		for step := 1; step <= steps; step++ {
			fraction := float64(step) / float64(steps)
			x := fromX + int(float64(toX-fromX)*fraction)
			y := fromY + int(float64(toY-fromY)*fraction)
			if err := s.Touch(ActionMove, x, y); err != nil {
				return err
			}
			if step < steps {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(duration / time.Duration(steps)):
				}
			}
		}
		return nil
	}()
	upErr := s.Touch(ActionUp, toX, toY)
	if sendErr != nil {
		return sendErr
	}
	return upErr
}

// bounds on one swipe: at most this many moves, and at most this many per second.
const (
	maxSwipeSteps    = 24
	maxSwipeStepsSec = 30
)

// swipeSteps is the bounded number of move messages one swipe sends.
func swipeSteps(duration time.Duration) int {
	steps := int(duration.Seconds() * maxSwipeStepsSec)
	if steps < 2 {
		return 2
	}
	if steps > maxSwipeSteps {
		return maxSwipeSteps
	}
	return steps
}

// TypeText sends text as typed content on the control socket.
func (s *Session) TypeText(text string) error {
	message, err := EncodeText(text)
	if err != nil {
		return err
	}
	return s.write(message)
}

// KeyEvent sends a key down and up pair for one key code, repeated as asked.
func (s *Session) KeyEvent(keyCode uint32, repeat uint32) error {
	if repeat == 0 {
		repeat = 1
	}
	for index := uint32(0); index < repeat; index++ {
		down, err := EncodeKeyEvent(ActionDown, keyCode, 0)
		if err != nil {
			return err
		}
		if err := s.write(down); err != nil {
			return err
		}
		up, err := EncodeKeyEvent(ActionUp, keyCode, 0)
		if err != nil {
			return err
		}
		if err := s.write(up); err != nil {
			return err
		}
	}
	return nil
}

// RequestKeyframe asks the device's capture to reset, which makes its encoder
// emit a fresh configuration packet and a key frame.
//
// It is the on-demand form of the periodic IDR the session also asks for, and
// the two are not redundant: a viewer that attaches while the device's screen is
// static would otherwise wait for a frame to change before anything was encoded
// at all.
func (s *Session) RequestKeyframe() error {
	if err := s.write(EncodeResetVideo()); err != nil {
		return err
	}
	s.statsMu.Lock()
	s.stats.ResetCalls++
	s.statsMu.Unlock()
	return nil
}

// Close ends the session: it closes both sockets, stops the device-side server,
// removes the reverse tunnel, and waits for the session's own goroutines to
// observe it. It is idempotent.
func (s *Session) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.errMu.Lock()
		if s.doneErr == nil {
			s.doneErr = errors.New("scrcpy: the session was closed")
		}
		s.errMu.Unlock()
		close(s.closed)
		if s.ctrl != nil {
			_ = s.ctrl.Close()
		}
		if s.video != nil {
			_ = s.video.Close()
		}
		if s.proc != nil {
			// The server exits on its own once both sockets are gone; the kill is
			// what makes that a bounded wait rather than a hope, and it is what
			// keeps a session that dies mid-handshake from leaving a capture
			// running on the device.
			if killErr := s.proc.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				err = fmt.Errorf("scrcpy: stopping the device server: %w", killErr)
			}
		}
		if removeErr := s.reverse(ctx, "remove"); removeErr != nil && err == nil {
			err = removeErr
		}
	})
	return err
}

// SessionID reports the id the device named its socket after. It is reported so
// a stray tunnel can be traced back to the session that opened it.
func (s *Session) SessionID() uint32 { return s.scid }

// serverArgv builds the device-side launch. It is a fixed builder shape: a fixed
// binary and fixed tokens, with exactly two derived values — the abstract socket
// name's session id (eight hex digits) and the bounded option values below.
func (s *Session) serverArgv() []string {
	return []string{
		"-s", s.opts.Serial, "shell",
		"CLASSPATH=" + ServerDevicePath, "app_process", "/",
		"com.genymobile.scrcpy.Server", Version,
		"scid=" + sessionIDHex(s.scid),
		"log_level=" + s.opts.LogLevel,
		"audio=false",
		"control=true",
		"tunnel_forward=false",
		"send_device_meta=false",
		"send_stream_meta=true",
		"send_frame_meta=true",
		// The server is removed from the device when it exits, so a device never
		// keeps a capture server of an unknown revision.
		"cleanup=true",
		"stay_awake=" + strconv.FormatBool(s.opts.KeepAwake),
		// The one encoder setting this client asks for: a periodic IDR. This
		// fleet's screen encoder emits exactly one IDR per session unasked, so a
		// receiver that missed the first frame decodes nothing for the rest of
		// the session - with no error anywhere. Asking for an IDR every two
		// seconds bounds how long that state can last.
		"video_codec_options=" + idrIntervalOption,
	}
}

// idrIntervalOption asks MediaCodec's encoder for an IDR every two seconds.
// scrcpy retires the server it is talking to, and the device refuses a client
// of a different version, so both ends of this string are pinned: the key is
// MediaCodec's own KEY_I_FRAME_INTERVAL spelled as scrcpy passes it through.
const idrIntervalOption = "i-frame-interval:int=2"

// reverse adds or removes the adb reverse tunnel that carries the device's two
// sockets back to this process's loopback listener.
//
// A reverse tunnel is used rather than a forward: the adb server's forwards are
// reachable off-host, while the address registered here is the process's own
// loopback listener, which is the only thing a reader could ever reach.
func (s *Session) reverse(ctx context.Context, action string) error {
	args := []string{"-s", s.opts.Serial, "reverse"}
	if action == "remove" {
		args = append(args, "--remove")
	}
	args = append(args, "localabstract:"+sessionSocketName(s.scid), "tcp:"+strconv.Itoa(s.port))
	if _, err := s.opts.Runner.Run(ctx, s.opts.ADB, args); err != nil {
		return fmt.Errorf("scrcpy: %s reverse tunnel %s: %w", action, sessionSocketName(s.scid), err)
	}
	return nil
}

// accept waits for the device to connect to the handshake listener, bounded by
// the caller's own wait.
func (s *Session) accept(listener net.Listener) (net.Conn, error) {
	if deadline, ok := listener.(interface{ SetDeadline(time.Time) error }); ok {
		_ = deadline.SetDeadline(time.Now().Add(s.opts.AcceptWait))
	} else if tcp, ok := listener.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(s.opts.AcceptWait))
	}
	conn, err := listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("scrcpy: waiting for the device to connect: %w", err)
	}
	return conn, nil
}

// readStreamMeta consumes the codec identifier and the session packet, and
// records the encoded frame size they carry.
func (s *Session) readStreamMeta(deadline time.Time) error {
	_ = s.video.SetReadDeadline(deadline)
	head := make([]byte, CodecHeaderSize+SessionPacketSize)
	if _, err := io.ReadFull(s.video, head); err != nil {
		return fmt.Errorf("scrcpy: reading the stream header: %w", err)
	}
	meta, err := ParseStreamMeta(head)
	if err != nil {
		return err
	}
	s.meta = meta
	_ = s.video.SetReadDeadline(time.Time{})
	return nil
}

// write serializes one control message onto the control socket, refusing a write
// on a session that has ended so a caller learns the input reached no device.
func (s *Session) write(message []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return fmt.Errorf("scrcpy: the session has ended: %w", s.Err())
	default:
	}
	if s.ctrl == nil {
		return errors.New("scrcpy: the control socket is not open")
	}
	if _, err := s.ctrl.Write(message); err != nil {
		return fmt.Errorf("scrcpy: writing to the control socket: %w", err)
	}
	s.statsMu.Lock()
	s.stats.Inputs++
	s.statsMu.Unlock()
	return nil
}

// drainControl reads and discards whatever the device sends back. The device
// uses this direction for its own messages, and an unread socket blocks the
// server's writer thread.
func (s *Session) drainControl() {
	buf := make([]byte, 4096)
	for {
		if _, err := s.ctrl.Read(buf); err != nil {
			return
		}
	}
}

// end records why the session stopped and closes its done channel exactly once.
func (s *Session) end(reason error) error {
	s.errMu.Lock()
	if s.doneErr == nil {
		s.doneErr = reason
	}
	err := s.doneErr
	s.errMu.Unlock()
	s.closeOnce.Do(func() { close(s.closed) })
	return err
}

// sessionID returns the session id, drawing one from the system's random source
// when no source is configured.
//
// The id is masked to 31 bits: the device parses it as a signed 32-bit integer,
// so the top bit must be clear for the value to survive the trip, and it names
// the abstract socket the device connects back to.
func sessionID(source func() (uint32, error)) (uint32, error) {
	if source == nil {
		var buf [4]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, fmt.Errorf("scrcpy: generating a session id: %w", err)
		}
		return binary.BigEndian.Uint32(buf[:]) & 0x7fffffff, nil
	}
	id, err := source()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, errors.New("scrcpy: a session id of zero would name the default device socket")
	}
	return id & 0x7fffffff, nil
}

// sessionIDHex renders a session id the way the device expects and the way it
// names the socket: the device reads `scid` as a hexadecimal integer, so the
// option and the abstract socket name must be the same eight hex digits.
func sessionIDHex(scid uint32) string { return fmt.Sprintf("%08x", scid) }

// sessionSocketName is the device-side abstract socket name for a session id.
func sessionSocketName(scid uint32) string { return abstractSocketPrefix + sessionIDHex(scid) }

// abstractSocketPrefix is scrcpy's own socket name prefix.
const abstractSocketPrefix = "scrcpy_"

// validateOptions fails closed on anything that would produce a command this
// client did not build, before a device is touched at all.
func validateOptions(opts Options) error {
	if err := validateExecutable(opts.ADB); err != nil {
		return err
	}
	if err := adb.ValidateSerial(opts.Serial); err != nil {
		return fmt.Errorf("scrcpy: device serial: %w", err)
	}
	if err := validateServerPath(opts.ServerPath); err != nil {
		return err
	}
	if opts.Runner == nil {
		return errors.New("scrcpy: a session requires the bounded adb runner")
	}
	if opts.Starter == nil {
		return errors.New("scrcpy: a session requires a process starter")
	}
	if opts.Listen == nil {
		opts.Listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}
	return nil
}

// validateExecutable requires an absolute path to a real file, so the executable
// a session runs is configuration rather than something PATH decides.
func validateExecutable(path string) error {
	if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) {
		return errors.New("scrcpy: the adb path must be an absolute path")
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return errors.New("scrcpy: the adb path contains a control character")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("scrcpy: the adb path %s is not readable: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return errors.New("scrcpy: the adb path is a directory")
	}
	return nil
}

// validateServerPath requires an absolute path to a readable server file. The
// file's digest is recorded when a session starts, so the server a session
// pushed is identifiable afterwards.
func validateServerPath(path string) error {
	if strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) || !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrServerMissing, path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%w: %q", ErrServerMissing, path)
	}
	return nil
}

// boundedWriter keeps a bounded tail of a long-lived process's output for a
// diagnostic, and never grows without limit while doing it.
type boundedWriter struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = w.buf[len(w.buf)-w.limit:]
	}
	return len(p), nil
}

// String reports the retained output.
func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// CommandStarter starts the device-side server as a child process.
type CommandStarter struct{}

// Start implements Starter with os/exec. The child's stderr is retained by the
// caller's writer, bounded, so a server that refuses to start can be explained
// without keeping its whole output.
func (CommandStarter) Start(executable string, args []string, stderr io.Writer) (Process, error) {
	cmd := exec.Command(executable, args...)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &commandProcess{cmd: cmd}, nil
}

type commandProcess struct{ cmd *exec.Cmd }

func (p *commandProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *commandProcess) Wait() error { return p.cmd.Wait() }
