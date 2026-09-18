package scrcpy

import (
	"bytes"
	"context"
	"errors"

	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// --- fakes -------------------------------------------------------------------

// fakeADBPath is an absolute path to a file that exists. The session validates
// its executable path before it builds any command, so a test that used a
// placeholder path would be asserting against the validation rather than against
// the behaviour under test.
func fakeADBPath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("resolving the test binary: %v", err)
	}
	return path
}

// allowlistedCall is one bounded command the session issued, as the allow-listed
// runner received it: the serial it was bound to and the serial-free array.
type allowlistedCall struct {
	serial string
	args   []string
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []allowlistedCall
	err   error
}

func (r *fakeRunner) RunAllowlisted(_ context.Context, serial string, args []string) (adb.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, allowlistedCall{serial: serial, args: append([]string(nil), args...)})
	return adb.Result{}, r.err
}

// argvMatching returns every recorded argument array containing the marker.
func (r *fakeRunner) argvMatching(marker string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matched [][]string
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call.args, " "), marker) {
			matched = append(matched, call.args)
		}
	}
	return matched
}

// recorded returns every recorded call in order.
func (r *fakeRunner) recorded() []allowlistedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]allowlistedCall(nil), r.calls...)
}

type fakeProcess struct {
	mu     sync.Mutex
	killed bool
	stderr string
	exited chan struct{}
}

func newFakeProcess() *fakeProcess { return &fakeProcess{exited: make(chan struct{})} }

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.killed {
		return nil
	}
	p.killed = true
	close(p.exited)
	return nil
}

func (p *fakeProcess) Wait() error {
	<-p.exited
	return nil
}

// Stderr reports what this fake server said. A fake is a server that said
// nothing unless a test makes it say something.
func (p *fakeProcess) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr
}

func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

type fakeStarter struct {
	mu     sync.Mutex
	serial string
	argv   []string
	proc   *fakeProcess
	err    error
	// stderr is what the process this starter hands back will report as its own
	// last words, for the tests that cover a server refusing to start.
	stderr string
}

func (s *fakeStarter) StartAllowlisted(_ context.Context, serial string, args []string) (adb.LongRunning, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serial = serial
	s.argv = append([]string(nil), args...)
	s.proc = newFakeProcess()
	s.proc.stderr = s.stderr
	if s.err != nil {
		return nil, s.err
	}
	return s.proc, nil
}

func (s *fakeStarter) launchArgv() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.argv...)
}

func (s *fakeStarter) launchSerial() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serial
}

// device is a stand-in for the scrcpy server: it connects to the handshake
// listener in the order the server does (video first, then control), writes a
// stream head and the packets it was given, and records everything it reads back
// on the control socket.
type device struct {
	listener net.Listener
	head     []byte
	packets  [][]byte

	mu      sync.Mutex
	control [][]byte
	done    chan struct{}
}

func (d *device) start(t *testing.T) {
	t.Helper()
	video, err := net.Dial("tcp", d.listener.Addr().String())
	if err != nil {
		t.Errorf("device: dialing the video socket: %v", err)
		return
	}
	if _, err := video.Write(d.head); err != nil {
		t.Errorf("device: writing the stream head: %v", err)
		return
	}
	for _, packet := range d.packets {
		if _, err := video.Write(packet); err != nil {
			t.Errorf("device: writing a packet: %v", err)
			return
		}
	}
	// The session accepts the two sockets in the order the server connects in:
	// video, then control. The head is already buffered, so a short pause here is
	// all that is needed for the session to be waiting in its second accept.
	time.Sleep(100 * time.Millisecond)
	control, err := net.Dial("tcp", d.listener.Addr().String())
	if err != nil {
		t.Errorf("device: dialing the control socket: %v", err)
		return
	}
	defer close(d.done)
	buf := make([]byte, 4096)
	for {
		count, err := control.Read(buf)
		if count > 0 {
			d.mu.Lock()
			d.control = append(d.control, append([]byte(nil), buf[:count]...))
			d.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// waitForControl waits until at least want bytes have arrived on the control
// socket and returns them. The device reads asynchronously, so a test that
// asserted on what it had read at the instant the caller returned would be
// racing its own fake.
// waitForGesture waits until the messages the device has read end with the
// gesture's release, and returns them.
func (h harness) waitForGesture(t *testing.T, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		message := h.device.controlBytes()
		if len(message) >= 2*32 && len(message)%32 == 0 && message[len(message)-31] == ActionUp {
			return message
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gesture never reached a release: % x", message)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h harness) waitForControl(t *testing.T, want int, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		message := h.device.controlBytes()
		if len(message) >= want {
			return message
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket received %d bytes, want at least %d", len(message), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (d *device) controlBytes() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	var all []byte
	for _, message := range d.control {
		all = append(all, message...)
	}
	return all
}

// frame builds one frame packet: the 12-byte header then its payload.
func frame(payload []byte, key bool, pts uint64) []byte {
	header := make([]byte, PacketHeaderSize)
	flags := pts
	if key {
		flags |= packetFlagKeyFrame
	}
	putUint64(header[0:8], flags)
	putUint32(header[8:12], uint32(len(payload)))
	return append(header, payload...)
}

func configFrame(payload []byte) []byte {
	header := make([]byte, PacketHeaderSize)
	putUint64(header[0:8], packetFlagConfig)
	putUint32(header[8:12], uint32(len(payload)))
	return append(header, payload...)
}

func putUint64(dst []byte, value uint64) {
	for index := 0; index < 8; index++ {
		dst[index] = byte(value >> (8 * (7 - index)))
	}
}

func putUint32(dst []byte, value uint32) {
	for index := 0; index < 4; index++ {
		dst[index] = byte(value >> (8 * (3 - index)))
	}
}

// harness is one session under test with the fakes it was built over.
type harness struct {
	session *Session
	runner  *fakeRunner
	starter *fakeStarter
	device  *device
}

func newHarness(t *testing.T, head []byte, packets [][]byte, mutate func(*Options)) harness {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	runner := &fakeRunner{}
	starter := &fakeStarter{}
	opts := Options{
		ADB:        fakeADBPath(t),
		Serial:     "192.168.1.104:5555",
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		Listen:     func() (net.Listener, error) { return listener, nil },
		IDSource:   func() (uint32, error) { return 0x2abc1234, nil },
		AcceptWait: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	dev := &device{listener: listener, head: head, packets: packets, done: make(chan struct{})}
	go dev.start(t)
	session, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close(context.Background())
		_ = listener.Close()
	})
	return harness{session: session, runner: runner, starter: starter, device: dev}
}

// --- tests -------------------------------------------------------------------

func TestStartHandshakesAndReadsTheDevicesFrames(t *testing.T) {
	idr := []byte{0, 0, 0, 1, 0x65, 0xb8, 0x48}
	p := []byte{0, 0, 0, 1, 0x21, 0xe2}
	h := newHarness(t, headFromDevice, [][]byte{configFrame([]byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x32}), frame(idr, true, 22265830649), frame(p, false, 22265930649)}, nil)

	width, height := h.session.Meta().Size()
	if width != 1080 || height != 2280 {
		t.Fatalf("session frame = %dx%d, want the device's own 1080x2280", width, height)
	}
	first, err := h.session.ReadAccessUnit()
	if err != nil {
		t.Fatalf("ReadAccessUnit: %v", err)
	}
	if !first.Config || first.Key {
		t.Fatalf("first unit = config=%v key=%v, want the codec configuration packet", first.Config, first.Key)
	}
	second, err := h.session.ReadAccessUnit()
	if err != nil {
		t.Fatalf("ReadAccessUnit: %v", err)
	}
	if !second.Key || second.PTSUS != 22265830649 || !bytes.Equal(second.Data, idr) {
		t.Fatalf("second unit = key=%v pts=%d data=% x", second.Key, second.PTSUS, second.Data)
	}
	third, err := h.session.ReadAccessUnit()
	if err != nil {
		t.Fatalf("ReadAccessUnit: %v", err)
	}
	if third.Key || third.PTSUS != 22265930649 {
		t.Fatalf("third unit = key=%v pts=%d, want the device's own media timestamp", third.Key, third.PTSUS)
	}
	if stats := h.session.Stats(); stats.Packets != 3 || stats.Configs != 1 || stats.KeyFrames != 1 {
		t.Fatalf("stats = %+v, want 3 packets, 1 config, 1 key frame", stats)
	}
}

// TestStartDerivesTheTunnelAndTheLaunchFromOneSessionID is the invariant that
// makes the handshake work at all: the device names its abstract socket after
// the session id it was given, so the tunnel the client registers and the id the
// server is launched with must be the same eight hex digits.
func TestStartDerivesTheTunnelAndTheLaunchFromOneSessionID(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	reverses := h.runner.argvMatching("reverse")
	if len(reverses) != 1 {
		t.Fatalf("recorded %d reverse calls, want 1: %v", len(reverses), reverses)
	}
	registered := ""
	for _, token := range reverses[0] {
		if strings.HasPrefix(token, "localabstract:") {
			registered = strings.TrimPrefix(token, "localabstract:")
		}
	}
	if registered != "scrcpy_2abc1234" {
		t.Fatalf("registered socket %q, want scrcpy_2abc1234", registered)
	}
	launch := h.starter.launchArgv()
	if len(launch) == 0 {
		t.Fatal("the device server was never started")
	}
	// The serial is bound by the entry point, never carried inside the shape:
	// the array is the shape the allow-list admits and nothing else.
	if serial := h.starter.launchSerial(); serial != "192.168.1.104:5555" {
		t.Fatalf("the launch was bound to serial %q, want the configured device", serial)
	}
	joined := strings.Join(launch, " ")
	for _, want := range []string{
		"com.genymobile.scrcpy.Server 4.1",
		"scid=2abc1234",
		"control=true",
		"tunnel_forward=false",
		"send_frame_meta=true",
		"send_stream_meta=true",
		"send_device_meta=false",
		"audio=false",
		"cleanup=true",
		"video_codec_options=i-frame-interval:int=2",
		"CLASSPATH=" + adb.MirrorServerDevicePath,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("launch %q does not carry %q", joined, want)
		}
	}
	// Every token is a token this client built: a printable run that no shell can
	// read as more than one word. The launch travels through `adb shell`, so a
	// token carrying a metacharacter would be command text.
	for _, token := range launch {
		if token == "" || strings.TrimSpace(token) != token {
			t.Fatalf("launch token %q is not a single bounded token", token)
		}
		if strings.ContainsAny(token, " 	\r\n;|&$`\\\"'<>(){}[]*?!~^") {
			t.Fatalf("launch token %q carries shell meaning", token)
		}
	}
}

// TestEveryCommandTheSessionIssuesIsAdmittedByTheRealAllowList closes the loop
// between this client and the transport it goes through: the arrays a session
// actually put on the wire are exactly the arrays the real ADB allow-list
// admits, and nothing the session issued is refused as unbuilt.
//
// It is the assertion that makes the admission worth having. A shape the client
// builds and the allow-list does not admit is a mirror that dials a device and
// shows nothing, and a shape the allow-list admits that the client no longer
// builds is a permission nobody needs.
func TestEveryCommandTheSessionIssuesIsAdmittedByTheRealAllowList(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	transport := adb.NewFakeRunner().RespondDefault(adb.FakeResponse{})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", transport, adb.WithOperationTimeout(time.Second))
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	const serial = "192.168.1.104:5555"
	arrays := make([][]string, 0, 8)
	for _, call := range h.runner.recorded() {
		if call.serial != serial {
			t.Fatalf("a command was bound to serial %q, want the device's own %q", call.serial, serial)
		}
		arrays = append(arrays, call.args)
	}
	arrays = append(arrays, h.starter.launchArgv())
	// The push and the tunnel are issued once, and the tunnel is removed when
	// the session closes: four arrays in total, and the sweep below is only
	// meaningful if the session actually issued them.
	if len(arrays) < 4 {
		t.Fatalf("the session issued %d argument arrays, want the push, the tunnel, the removal and the launch: %v", len(arrays), arrays)
	}
	for _, args := range arrays {
		if _, err := adapter.RunAllowlisted(context.Background(), serial, args); errors.Is(err, adb.ErrArgvNotAllowlisted) {
			t.Fatalf("the real allow-list refuses %q, which this client issued: the mirror would dial a device and show nothing", args)
		}
	}
	if invocations := transport.Invocations(); len(invocations) != len(arrays) {
		t.Fatalf("the adapter executed %d arrays, want the %d the session issued", len(invocations), len(arrays))
	}
}

// TestStartRefusesADeviceThatAnswersWithAnotherStream covers the refusal the
// session owns rather than the parser: a device that reports a codec this client
// cannot carry ends the session, and leaves nothing running.
func TestStartRefusesADeviceThatAnswersWithAnotherStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	disabled := append([]byte(nil), headFromDevice...)
	// The codec identifier is big-endian, so the device's "I will not stream"
	// signal is the first byte set to zero.
	disabled[0], disabled[1], disabled[2], disabled[3] = 0, 0, 0, 0
	runner := &fakeRunner{}
	starter := &fakeStarter{}
	go func() {
		conn, dialErr := net.Dial("tcp", listener.Addr().String())
		if dialErr != nil {
			return
		}
		_, _ = conn.Write(disabled)
	}()
	session, err := Start(context.Background(), Options{
		ADB:        fakeADBPath(t),
		Serial:     "192.168.1.104:5555",
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		Listen:     func() (net.Listener, error) { return listener, nil },
		IDSource:   func() (uint32, error) { return 1, nil },
		AcceptWait: 5 * time.Second,
	})
	if err == nil {
		_ = session.Close(context.Background())
		t.Fatal("a device that refused the capture produced a live session")
	}
	if !errors.Is(err, ErrStreamDisabled) {
		t.Fatalf("error = %v, want the device's own refusal", err)
	}
	if !starter.proc.wasKilled() {
		t.Fatal("a failed handshake left the device server running")
	}
	if len(runner.argvMatching("--remove")) == 0 {
		t.Fatal("a failed handshake left the reverse tunnel registered")
	}
}

func TestStartRefusesOptionsThatWouldTouchADeviceUnbuilt(t *testing.T) {
	good := Options{ADB: fakeADBPath(t), Serial: "192.168.1.104:5555", Runner: &fakeRunner{}, Starter: &fakeStarter{}}
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	cases := map[string]func(*Options){
		"a serial with a space": func(o *Options) { o.Serial = "192.168.1.104:5555 --serial" },
		"a relative adb path":   func(o *Options) { o.ADB = "adb" },
		"a missing server file": func(o *Options) { o.ServerPath = filepath.Join(t.TempDir(), "absent") },
		"no runner":             func(o *Options) { o.Runner = nil },
		"no starter":            func(o *Options) { o.Starter = nil },
		"an empty serial":       func(o *Options) { o.Serial = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := good
			opts.ServerPath = serverPath
			mutate(&opts)
			session, err := Start(context.Background(), opts)
			if err == nil {
				_ = session.Close(context.Background())
				t.Fatal("an option set that would reach a device was accepted")
			}
		})
	}
}

// TestStartReportsWhyTheDeviceServerRefused is the honest-failure half of a
// launch that goes wrong: a server that never opens its sockets says why on its
// own stderr and nowhere else, so a session that reported only its own timeout
// would name the caller's clock instead of the device's reason. The failure
// still leaves nothing behind: the process is killed and the tunnel removed.
func TestStartReportsWhyTheDeviceServerRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	serverPath := filepath.Join(t.TempDir(), "scrcpy-server")
	if err := os.WriteFile(serverPath, []byte("server"), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	runner := &fakeRunner{}
	starter := &fakeStarter{stderr: "ERROR: the server refused to start: stack corruption detected"}

	session, err := Start(context.Background(), Options{
		ADB:        fakeADBPath(t),
		Serial:     "192.168.1.104:5555",
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		Listen:     func() (net.Listener, error) { return listener, nil },
		IDSource:   func() (uint32, error) { return 0x2abc1234, nil },
		AcceptWait: 200 * time.Millisecond,
	})
	if err == nil {
		_ = session.Close(context.Background())
		t.Fatal("a device that never connected produced a live session")
	}
	if !strings.Contains(err.Error(), "stack corruption detected") {
		t.Fatalf("error = %v, want the device server's own reason", err)
	}
	if !starter.proc.wasKilled() {
		t.Fatal("a failed handshake left the device server running")
	}
	if len(runner.argvMatching("--remove")) == 0 {
		t.Fatal("a failed handshake left the reverse tunnel registered")
	}
}

func TestInputReachesTheControlSocketInTheStreamsFrame(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.session.Touch(ActionDown, 540, 1140); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if err := h.session.TypeText("hello world"); err != nil {
		t.Fatalf("TypeText: %v", err)
	}
	if err := h.session.KeyEvent(4, 1); err != nil {
		t.Fatalf("KeyEvent: %v", err)
	}
	if err := h.session.RequestKeyframe(); err != nil {
		t.Fatalf("RequestKeyframe: %v", err)
	}
	wantTouch, _ := EncodeTouch(ActionDown, 540, 1140, 1080, 2280)
	wantText, _ := EncodeText("hello world")
	wantKeyDown, _ := EncodeKeyEvent(ActionDown, 4, 0)
	wantKeyUp, _ := EncodeKeyEvent(ActionUp, 4, 0)
	want := bytes.Join([][]byte{wantTouch, wantText, wantKeyDown, wantKeyUp, EncodeResetVideo()}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Equal(h.device.controlBytes(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control socket received % x\nwant               % x", h.device.controlBytes(), want)
}

// TestTouchRefusesACoordinateOutsideTheStreamsFrame is the frame rule at the
// session boundary: a point is only ever sent in the frame the device reported,
// and a coordinate outside it is refused rather than scaled into it.
func TestTouchRefusesACoordinateOutsideTheStreamsFrame(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.session.Touch(ActionDown, 1200, 10); err == nil {
		t.Fatal("a coordinate the stream does not cover was sent")
	}
	if err := h.session.Touch(ActionDown, 10, 2400); err == nil {
		t.Fatal("a coordinate the stream does not cover was sent")
	}
	if got := h.device.controlBytes(); len(got) != 0 {
		t.Fatalf("a refused coordinate reached the device: % x", got)
	}
}

func TestSwipeIsBoundedAndReleasesThePointer(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.session.Swipe(context.Background(), 100, 200, 900, 2000, 400*time.Millisecond); err != nil {
		t.Fatalf("Swipe: %v", err)
	}
	message := h.waitForGesture(t, 2*time.Second)
	if len(message)%32 != 0 {
		t.Fatalf("swipe wrote %d bytes, which is not a whole number of touch messages", len(message))
	}
	count := len(message) / 32
	if count < 4 || count > maxSwipeSteps+2 {
		t.Fatalf("swipe sent %d touch messages, want a bounded gesture", count)
	}
	if message[1] != ActionDown {
		t.Fatalf("swipe did not start with a press: %d", message[1])
	}
	last := message[len(message)-32:]
	if last[1] != ActionUp {
		t.Fatalf("swipe did not end by releasing the pointer: %d", last[1])
	}
	if err := h.session.Swipe(context.Background(), 10, 10, 20, 20, 0); err == nil {
		t.Fatal("a swipe without a duration was sent")
	}
}

func TestCloseStopsTheServerAndRemovesTheTunnelOnce(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.session.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	select {
	case <-h.session.Done():
	default:
		t.Fatal("Close did not end the session")
	}
	if !h.starter.proc.wasKilled() {
		t.Fatal("Close left the device server running")
	}
	if removes := h.runner.argvMatching("--remove"); len(removes) != 1 {
		t.Fatalf("recorded %d tunnel removals, want exactly 1: %v", len(removes), removes)
	}
	if err := h.session.Touch(ActionDown, 10, 10); err == nil {
		t.Fatal("a closed session still accepted input")
	}
}

// TestAServerThatExitsEndsTheSession is the honest-failure path: the session
// does not keep answering as if the device were still there once the process
// that serves it is gone.
func TestAServerThatExitsEndsTheSession(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)
	if err := h.starter.proc.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case <-h.session.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the session did not end when the device server exited")
	}
	if err := h.session.Err(); err == nil {
		t.Fatal("the session ended without a reason")
	}
	if err := h.session.Touch(ActionDown, 10, 10); err == nil {
		t.Fatal("a session whose server exited accepted input")
	}
}

// TestReadAccessUnitContextStopsWhenTheCallerStops is the behaviour a mirror's
// worker depends on: a device with a static screen sends nothing at all, so a
// reader that waited on the socket alone would keep the session - and therefore
// the capture on the device - alive indefinitely. The context is what bounds it,
// and a quiet poll is not a failure.
func TestReadAccessUnitContextStopsWhenTheCallerStops(t *testing.T) {
	h := newHarness(t, headFromDevice, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		unit AccessUnit
		err  error
	}
	answers := make(chan result, 1)
	go func() {
		unit, err := h.session.ReadAccessUnitContext(ctx)
		answers <- result{unit: unit, err: err}
	}()

	// Nothing has been sent by the device, so the read is waiting.
	select {
	case answer := <-answers:
		t.Fatalf("a read with no data returned on its own: unit %+v, err %v", answer.unit, answer.err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case answer := <-answers:
		if !errors.Is(answer.err, context.Canceled) {
			t.Fatalf("the cancelled read returned %v, want the caller's own cancellation", answer.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read did not stop when its caller cancelled")
	}
	// The wait was cut short by the caller, not by the device: the session is
	// still live, and only the caller's own decision closes it.
	select {
	case <-h.session.Done():
		t.Fatalf("a cancelled read ended the session: %v", h.session.Err())
	default:
	}
}

// TestReadAccessUnitContextReturnsTheDevicesPackets is the other half: the
// bounded waiting above must not lose a packet that does arrive, or the mirror
// would be quiet rather than live.
func TestReadAccessUnitContextReturnsTheDevicesPackets(t *testing.T) {
	idr := []byte{0, 0, 0, 1, 0x65, 0xb8, 0x48}
	h := newHarness(t, headFromDevice, [][]byte{frame(idr, true, 22265830649)}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unit, err := h.session.ReadAccessUnitContext(ctx)
	if err != nil {
		t.Fatalf("ReadAccessUnitContext: %v", err)
	}
	if !unit.Key || unit.Config {
		t.Fatalf("the packet was read as key=%v config=%v, want the device's key frame", unit.Key, unit.Config)
	}
	if !bytes.Equal(unit.Data, idr) {
		t.Fatalf("the packet data is % x, want % x", unit.Data, idr)
	}
	if unit.PTSUS != 22265830649 {
		t.Fatalf("the packet's timestamp is %d, want the device's own %d", unit.PTSUS, 22265830649)
	}
}
