// Command live is the Part B rig: a live device screen measured end to end.
//
// It is the same module, the same pion hop and the same browser-probe style as
// Part A, extended in three ways Part B needs:
//
//   - the source is the device itself: a scrcpy 4.1 session's H.264 video socket
//     is forwarded packet by packet into the pion track, and the device's own
//     encoder PTS supplies the sample duration;
//   - the clock is the device's: a page it displays paints the millisecond wall
//     clock into its own pixels as a cell strip, which the probe reads back out
//     of the decoded video, so glass-to-glass is measured against the device's
//     own glass rather than against a host-side timestamp;
//   - input goes through scrcpy's control socket, never `adb shell input`, so
//     the input round-trip is the hop ARC-143 will actually use.
//
// Nothing here is wired into the product: it is a measurement rig in the
// throwaway spike module.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"spikelocal/arc144/internal/clockcode"
	"spikelocal/arc144/internal/livepeer"
	"spikelocal/arc144/internal/scrcpy"
)

type deviceSignal struct {
	Type       string          `json:"type"`
	DevMS      int64           `json:"devMs"`
	TapCount   int             `json:"tapCount"`
	Source     string          `json:"source"`
	HostRecvNS int64           `json:"host_recv_ns"`
	Drawn      int             `json:"drawn"`
	FirstDrawn int64           `json:"firstDrawnMs"`
	LastDrawn  int64           `json:"lastDrawnMs"`
	MaxGapMS   int64           `json:"maxGapMs"`
	MissedRaf  int             `json:"missedRaf"`
	Raw        json.RawMessage `json:"raw"`
}

type tapRecord struct {
	Seq        int    `json:"seq"`
	Kind       string `json:"kind"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	HostDownNS int64  `json:"host_down_ns"`
	HostUpNS   int64  `json:"host_up_ns"`
	Error      string `json:"error,omitempty"`
	// Device-side reaction, joined from the page's own signal.
	DeviceMS    int64 `json:"device_ms"`
	DeviceTapNo int   `json:"device_tap_count"`
	HostRecvNS  int64 `json:"device_signal_recv_ns"`
}

type server struct {
	cfg     config
	peer    *livepeer.Peer
	httpSrv *http.Server

	mu               sync.Mutex
	session          *scrcpy.Session
	pageAlive        bool
	pageInfo         json.RawMessage
	fullscreenSignal json.RawMessage
	ntp              json.RawMessage
	heartbeats       []deviceSignal
	deviceTaps       []deviceSignal
	fullscreen       bool
	probeReady       bool
	probeGeom        json.RawMessage
	probeResult      json.RawMessage
	probeSamples     int
	probeDecoded     int
	probePaused      bool
	probeReadyState  int
	probeVideoWidth  int
	taps             []tapRecord
	packets          []scrcpy.Packet
	pumpStarted      bool
	pumpFinished     bool
	startNS          int64
	endNS            int64
	skew             skewReport
	strip            stripGeometry
	notes            []string
	launchedAt       int64
	dump             *os.File
}

type config struct {
	serial           string
	adb              string
	serverJar        string
	addr             string
	label            string
	outDir           string
	duration         time.Duration
	settle           time.Duration
	tail             time.Duration
	tapCount         int
	tapInterval      time.Duration
	maxSize          int
	setupTap         bool
	skewSamples      int
	clockTolMS       int64
	readbackEvery    int
	waitPage         time.Duration
	waitProbe        time.Duration
	deviceURL        string
	chromePkg        string
	resetBrowser     bool
	runTag           string
	dumpH264         string
	allowNoProbe     bool
	keyframeInterval int
}

// stripGeometry is where the clock strip is, in both coordinate frames: the
// device's screen (where a screenshot finds it) and the encoded video (where the
// browser reads it). Touch injection uses the video frame, because that is the
// frame the control message declares.
type stripGeometry struct {
	ScreenW      int     `json:"screen_width"`
	ScreenH      int     `json:"screen_height"`
	VideoW       int     `json:"video_width"`
	VideoH       int     `json:"video_height"`
	ScreenX      int     `json:"screen_x"`
	ScreenY      int     `json:"screen_y"`
	VideoX       int     `json:"video_x"`
	VideoY       int     `json:"video_y"`
	ScaleX       float64 `json:"scale_x"`
	ScaleY       float64 `json:"scale_y"`
	Cell         int     `json:"cell"`
	ScreenMS     int64   `json:"screen_ms"`
	ScreenHostMS int64   `json:"screen_host_ms"`
	Method       string  `json:"method"`
	Found        bool    `json:"found"`
	Error        string  `json:"error,omitempty"`
}

type livepeerHolder struct {
	p interface{}
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.serial, "serial", "", "device serial (required)")
	flag.StringVar(&cfg.adb, "adb", "adb", "adb binary")
	flag.StringVar(&cfg.serverJar, "server-jar", defaultServerJar(), "scrcpy-server jar")
	flag.StringVar(&cfg.addr, "addr", "0.0.0.0:8792", "listen address (must be reachable by the device)")
	flag.StringVar(&cfg.label, "label", "live", "label for this run's output files")
	flag.StringVar(&cfg.outDir, "out", "results", "output directory")
	flag.DurationVar(&cfg.duration, "duration", 25*time.Second, "how long to stream and record")
	flag.DurationVar(&cfg.settle, "settle", 3*time.Second, "delay after the probe is ready before the first tap")
	flag.DurationVar(&cfg.tail, "tail", 4*time.Second, "collection time after the last frame")
	flag.IntVar(&cfg.tapCount, "taps", 8, "how many taps to inject during the run")
	flag.DurationVar(&cfg.tapInterval, "tap-interval", 2500*time.Millisecond, "time between taps")
	flag.IntVar(&cfg.maxSize, "max-size", 0, "cap the encoded size (0 keeps the device's own)")
	flag.BoolVar(&cfg.setupTap, "setup-tap", true, "inject one tap first to put the page fullscreen")
	flag.IntVar(&cfg.skewSamples, "skew-samples", 25, "adb round trips used for the clock skew estimate")
	flag.Int64Var(&cfg.clockTolMS, "clock-tolerance-ms", 5000, "how far the strip's clock may differ from the host clock")
	flag.IntVar(&cfg.readbackEvery, "readback-every", 1, "pixel readback every Nth presented frame (0 disables the probe)")
	flag.DurationVar(&cfg.waitPage, "wait-page", 45*time.Second, "how long to wait for the device page")
	flag.DurationVar(&cfg.waitProbe, "wait-probe", 60*time.Second, "how long to wait for the browser probe")
	flag.StringVar(&cfg.chromePkg, "chrome", "com.android.chrome", "device browser package")
	flag.BoolVar(&cfg.resetBrowser, "reset-browser", true, "stop the device browser before launching the page (one tab, one clock)")
	flag.StringVar(&cfg.dumpH264, "dump-h264", "", "write every forwarded access unit to this Annex-B file (debugging)")
	flag.IntVar(&cfg.keyframeInterval, "keyframe-interval", 2, "seconds between IDRs the device encoder is asked for (0 leaves the encoder default)")
	flag.BoolVar(&cfg.allowNoProbe, "allow-no-probe", false, "run the pump even if no browser probe connects (device-side debugging only; the run then has no latency samples)")
	flag.Parse()

	if cfg.serial == "" {
		log.Fatal("live: -serial is required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.outDir, "logs"), 0o755); err != nil {
		log.Fatal(err)
	}
	lan := lanIP(cfg.addr)
	// The cache-busting query matters: the page is served from this rig, and
	// Chrome will happily reuse a cached copy across runs, which would measure
	// yesterday's page. The same tag is what the page echoes back on every post,
	// so a signal from another run's page can be recognised and dropped.
	cfg.runTag = strconv.FormatInt(time.Now().UnixNano(), 36)
	cfg.deviceURL = fmt.Sprintf("http://%s/clock.html?run=%s", net.JoinHostPort(lan, portOf(cfg.addr)), cfg.runTag)

	s := &server{cfg: cfg}
	if cfg.dumpH264 != "" {
		f, err := os.Create(cfg.dumpH264)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		s.dump = f
	}
	if err := s.run(context.Background()); err != nil {
		log.Printf("live: %v", err)
		s.writeReport(err)
		os.Exit(1)
	}
	s.writeReport(nil)
}

func (s *server) run(ctx context.Context) error {
	// 1. HTTP surface first: both the device page and the browser probe need it.
	sub, err := webSub()
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/signal", s.handleSignal)
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/offer", s.handleOffer)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/now", s.handleNow)
	mux.HandleFunc("/geometry", s.handleGeometry)
	mux.HandleFunc("/progress", s.handleProgress)
	mux.HandleFunc("/tap", s.handleTap)
	mux.HandleFunc("/result", s.handleResult)
	mux.HandleFunc("/device-signals", s.handleDeviceSignals)
	s.httpSrv = &http.Server{Addr: s.cfg.addr, Handler: mux}
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("live: http server: %v", err)
		}
	}()
	log.Printf("live: serving %s (device page %s)", s.cfg.addr, s.cfg.deviceURL)

	// 2. Clock skew between the device and this host, before anything is timed.
	skew := measureSkew(ctx, s.cfg.adb, s.cfg.serial, s.cfg.skewSamples)
	s.mu.Lock()
	s.skew = skew
	s.mu.Unlock()
	log.Printf("live: device clock skew %.3f ms (min-RTT %.3f ms, spread %.3f ms, adb round trip p50 %.1f ms)",
		skew.DeviceMinusHostMS, skew.AdbRTTMinMS, skew.SpreadMS, skew.AdbRTTP50MS)

	// 3. The scrcpy session: video and control sockets.
	stopwatch := time.Now()
	codecOpts := []string{}
	if s.cfg.keyframeInterval > 0 {
		// scrcpy's syntax is key[:type]=value: the server rejects a bare
		// key:value pair outright.
		codecOpts = append(codecOpts, fmt.Sprintf("i-frame-interval:int=%d", s.cfg.keyframeInterval))
	}
	sess, err := scrcpy.Start(ctx, scrcpy.Options{
		ADB: s.cfg.adb, Serial: s.cfg.serial, ServerPath: s.cfg.serverJar,
		MaxSize: s.cfg.maxSize, LogLevel: "info", CodecOptions: codecOpts,
	})
	if err != nil {
		return fmt.Errorf("scrcpy session: %w", err)
	}
	s.mu.Lock()
	s.session = sess
	s.mu.Unlock()
	defer sess.Stop(context.Background())
	w, h := sess.Size()
	log.Printf("live: scrcpy session up in %s, streaming %dx%d", time.Since(stopwatch).Round(time.Millisecond), w, h)

	// 4. Open the clock page on the device.
	if err := s.openDevicePage(ctx); err != nil {
		return err
	}
	if err := s.waitForPage(s.cfg.waitPage); err != nil {
		return err
	}
	log.Printf("live: device page is alive")

	// 5. One setup tap puts the page fullscreen, which removes the browser's own
	// chrome (and its tooltips) from the pixels the strip is read out of.
	if s.cfg.setupTap {
		if err := s.injectTap("setup", s.videoW()/2, s.videoH()/2); err != nil {
			s.note("setup tap failed: " + err.Error())
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			fs := s.fullscreen
			s.mu.Unlock()
			if fs {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Chrome shows a transient "swipe down to exit full screen" toast that
		// sits exactly where the strip is; let it leave before locating it.
		time.Sleep(4 * time.Second)
		s.mu.Lock()
		log.Printf("live: fullscreen=%v after the setup tap", s.fullscreen)
		s.mu.Unlock()
	}

	// 6. Find the strip in a screenshot, in device pixels, then restate it in
	// the video's frame.
	if err := s.locateStrip(ctx); err != nil {
		s.note("strip not located: " + err.Error())
		log.Printf("live: %v", err)
	}

	// 7. Wait for the probe, then start the pump and the tap plan.
	if err := s.waitForProbe(s.cfg.waitProbe); err != nil {
		if !s.cfg.allowNoProbe {
			return err
		}
		s.note("no browser probe: " + err.Error())
	}
	log.Printf("live: browser probe ready")

	pumpErr := make(chan error, 1)
	go func() { pumpErr <- s.pump() }()

	select {
	case err := <-pumpErr:
		if err != nil {
			return fmt.Errorf("pump: %w", err)
		}
	case <-time.After(s.cfg.duration + 10*time.Second):
		s.note(fmt.Sprintf("pump did not finish within %s", s.cfg.duration+10*time.Second))
	}

	// The probe is still collecting its tail and posting its samples when the
	// pump ends; exiting now would lose the measurement the run exists for.
	if err := s.awaitProbeResults(30 * time.Second); err != nil {
		s.note(err.Error())
	} else {
		log.Printf("live: probe results received")
	}

	s.finish()
	return nil
}

// awaitDecoding blocks until the browser has actually decoded and read a clock
// out of the video.
//
// This gate exists because the device's own stream makes the difference
// material: its encoder emits a single IDR at session start, so a browser that
// did not decode from the first frame never decodes at all, and taps injected
// into that state produce a round-trip with no observable reaction. Waiting for
// real decoded samples, or timing out and saying so, is the honest version.
func (s *server) awaitDecoding(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		decoded, samples, paused, rs := s.probeDecoded, s.probeSamples, s.probePaused, s.probeReadyState
		s.mu.Unlock()
		if decoded >= 5 {
			log.Printf("live: browser is decoding (%d clock reads from %d presented frames, readyState %d)", decoded, samples, rs)
			return
		}
		if samples > 0 && paused {
			s.note(fmt.Sprintf("browser video element is paused (readyState %d) after %d frames", rs, samples))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.mu.Lock()
	decoded, samples, rs := s.probeDecoded, s.probeSamples, s.probeReadyState
	s.mu.Unlock()
	s.note(fmt.Sprintf("browser never decoded a clock: %d samples, %d clock reads, video readyState %d", samples, decoded, rs))
}

// awaitProbeResults waits for the browser probe to post its samples.
func (s *server) awaitProbeResults(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := s.probeResult != nil
		s.mu.Unlock()
		if got {
			// Give the probe a moment to finish its own logging before the
			// server stops listening.
			time.Sleep(500 * time.Millisecond)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("no probe results within %s", timeout)
}

// pump forwards every encoded frame the device produces to the RTP track the
// moment it arrives, recording the instants on both sides of the Go hop.
func (s *server) pump() error {
	s.mu.Lock()
	s.startNS = time.Now().UnixNano()
	s.pumpStarted = true
	sess := s.session
	s.mu.Unlock()

	tapDone := make(chan struct{})
	go func() {
		defer close(tapDone)
		time.Sleep(s.cfg.settle)
		s.awaitDecoding(15 * time.Second)
		for i := 0; i < s.cfg.tapCount; i++ {
			if err := s.injectTap("measure", s.videoW()/2, s.videoH()/2); err != nil {
				log.Printf("live: tap %d: %v", i+1, err)
			}
			if i < s.cfg.tapCount-1 {
				time.Sleep(s.cfg.tapInterval)
			}
		}
	}()

	deadline := time.Now().Add(s.cfg.duration)
	first := true
	for {
		if time.Now().After(deadline) {
			break
		}
		pkt, err := sess.ReadPacket()
		if err != nil {
			s.note("video stream ended: " + err.Error())
			break
		}
		if pkt.Duration <= 0 {
			pkt.Duration = time.Second / 60
		}
		if first {
			// The device's own SPS decides the profile-level-id the SDP must
			// advertise; a real hardware encoder does not send ours.
			if plid, err := livepeer.ProfileLevelID(pkt.Data); err == nil {
				s.mu.Lock()
				s.notes = append(s.notes, "device SPS profile-level-id "+plid)
				s.mu.Unlock()
			}
			first = false
		}
		s.send(pkt)
	}
	<-tapDone
	s.mu.Lock()
	s.pumpFinished = true
	s.endNS = time.Now().UnixNano()
	s.mu.Unlock()
	// Let the tail arrive (last frames still in the browser) before the run ends.
	time.Sleep(s.cfg.tail)
	return nil
}

func (s *server) finish() {
	s.mu.Lock()
	packets := len(s.packets)
	taps := len(s.taps)
	s.mu.Unlock()
	log.Printf("live: run complete: %d frames forwarded, %d taps injected", packets, taps)
}

func (s *server) note(msg string) {
	s.mu.Lock()
	s.notes = append(s.notes, msg)
	s.mu.Unlock()
	log.Printf("live: %s", msg)
}

func (s *server) videoW() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return 0
	}
	w, _ := s.session.Size()
	return w
}

func (s *server) videoH() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return 0
	}
	_, h := s.session.Size()
	return h
}

func lanIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" && host != "0.0.0.0" && host != "::" {
		return host
	}
	// The device reaches this host over the LAN, so report the address the
	// route to the device actually uses.
	if c, err := net.Dial("udp", "192.168.1.1:53"); err == nil {
		defer c.Close()
		if u, ok := c.LocalAddr().(*net.UDPAddr); ok {
			return u.IP.String()
		}
	}
	return "127.0.0.1"
}

func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return "8792"
}

func defaultServerJar() string {
	candidates := []string{
		"/opt/homebrew/Cellar/scrcpy/4.1_1/share/scrcpy/scrcpy-server",
		"/opt/homebrew/share/scrcpy/scrcpy-server",
		"/usr/local/share/scrcpy/scrcpy-server",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// stripScale converts a screenshot-coordinate geometry into the encoded video's
// frame. The video is the same picture as the screen, so the mapping is a scale
// of both axes (and identity when scrcpy is not downscaling).
func stripScale(screenW, screenH, videoW, videoH, x, y int) (int, int, float64, float64) {
	sx, sy := 1.0, 1.0
	if screenW > 0 && videoW > 0 {
		sx = float64(videoW) / float64(screenW)
	}
	if screenH > 0 && videoH > 0 {
		sy = float64(videoH) / float64(screenH)
	}
	return int(float64(x)*sx + 0.5), int(float64(y)*sy + 0.5), sx, sy
}

var _ = clockcode.Cell
