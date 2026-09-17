package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"spikelocal/arc144/internal/clockcode"
	"spikelocal/arc144/internal/scrcpy"
	"spikelocal/arc144/internal/screencap"
	"spikelocal/arc144/internal/webui"
)

func webSub() (fs.FS, error) { return webui.FS(), nil }

// openDevicePage asks the device's browser to display the clock page. The page
// is served by this rig over the LAN: it has to be reachable from the device,
// which is why the rig binds the LAN address rather than loopback.
//
// The browser is stopped first. Measured the hard way: a page left open by an
// earlier run stays in its own tab, keeps posting heartbeats to this rig's
// (unchanged) port, and keeps its own clock running -- so a run would collect
// two pages' signals and could not tell which one it measured. Stopping the
// browser is not a device mutation beyond ending a process, and it gives the run
// one tab, one page and one clock.
func (s *server) openDevicePage(ctx context.Context) error {
	if s.cfg.resetBrowser && s.cfg.chromePkg != "" {
		if out, err := exec.CommandContext(ctx, s.cfg.adb, "-s", s.cfg.serial, "shell", "am", "force-stop", s.cfg.chromePkg).CombinedOutput(); err != nil {
			s.note(fmt.Sprintf("force-stop %s: %v: %s", s.cfg.chromePkg, err, out))
		} else {
			log.Printf("live: stopped %s to start from one tab", s.cfg.chromePkg)
			time.Sleep(1500 * time.Millisecond)
		}
	}
	args := []string{"-s", s.cfg.serial, "shell", "am", "start",
		"-a", "android.intent.action.VIEW", "-d", s.cfg.deviceURL,
	}
	if s.cfg.chromePkg != "" {
		args = append(args, s.cfg.chromePkg)
	}
	out, err := exec.CommandContext(ctx, s.cfg.adb, args...).CombinedOutput()
	s.mu.Lock()
	s.launchedAt = time.Now().UnixNano()
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("launching %s at %s: %v: %s", s.cfg.chromePkg, s.cfg.deviceURL, err, out)
	}
	log.Printf("live: launched %s at %s", s.cfg.chromePkg, s.cfg.deviceURL)
	return nil
}

func (s *server) waitForPage(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		alive := s.pageAlive
		s.mu.Unlock()
		if alive {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("device page did not signal alive within %s", timeout)
}

func (s *server) waitForProbe(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ready := s.probeReady
		s.mu.Unlock()
		if ready {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("browser probe did not reach /offer within %s", timeout)
}

// locateStrip finds the clock strip in a screenshot of the device.
//
// The screenshot is the only place the exact origin can be established, because
// it is a lossless capture of the device's own framebuffer: the strip's top edge
// is the one boundary where a dark pixel sits directly above a full white cell.
// The result is then restated in the video's coordinate frame, which is the
// frame the probe reads and the frame touch coordinates are declared in.
func (s *server) locateStrip(ctx context.Context) error {
	pngPath := filepath.Join(s.cfg.outDir, "diag", fmt.Sprintf("live-%s-screen.png", s.cfg.label))
	if err := os.MkdirAll(filepath.Dir(pngPath), 0o755); err != nil {
		return err
	}
	hostBefore := time.Now().UnixNano()
	path, err := screencap.Take(s.cfg.adb, s.cfg.serial, pngPath)
	if err != nil {
		return err
	}
	hostAfter := time.Now().UnixNano()
	g, err := screencap.LoadGray(path)
	if err != nil {
		return err
	}
	geom := stripGeometry{ScreenW: g.Width, ScreenH: g.Height, Cell: clockcode.Cell, ScreenHostMS: hostAfter / 1e6}
	loc, err := clockcode.Locate(g, hostAfter/1e6, s.cfg.clockTolMS)
	if err != nil {
		geom.Error = err.Error()
		if approx, aerr := clockcode.LocateTolerant(g, hostAfter/1e6, s.cfg.clockTolMS, 64, 900); aerr == nil {
			geom.ScreenX, geom.ScreenY, geom.ScreenMS = approx.X, approx.Y, approx.MS
			geom.Method = approx.Method + " (tolerant: not usable for the measurement)"
			s.mu.Lock()
			s.strip = geom
			s.mu.Unlock()
		}
		s.mu.Lock()
		vw := 0
		vh := 0
		if s.session != nil {
			vw, vh = s.session.Size()
		}
		s.mu.Unlock()
		geom.VideoW, geom.VideoH = vw, vh
		return fmt.Errorf("locating the clock strip: %w (screenshot %dx%d, host clock %d)", err, g.Width, g.Height, geom.ScreenHostMS)
	}
	geom.ScreenX, geom.ScreenY, geom.ScreenMS, geom.Method = loc.X, loc.Y, loc.MS, loc.Method
	geom.ScreenHostMS = hostAfter / 1e6
	s.mu.Lock()
	s.sessionSize(&geom)
	s.mu.Unlock()
	geom.VideoX, geom.VideoY, geom.ScaleX, geom.ScaleY =
		stripScale(geom.ScreenW, geom.ScreenH, geom.VideoW, geom.VideoH, geom.ScreenX, geom.ScreenY)
	// The cell is measured from the SCREEN's geometry and restated in the video
	// frame, like the origin: a downscaled stream has a fractional video cell
	// (scrcpy's 720-class size makes this device's 32 px cell 10.07 px wide), and
	// a probe told 32 would misread every column.
	geom.VideoCell = float64(geom.Cell) * geom.ScaleX
	geom.Found = true
	s.mu.Lock()
	s.strip = geom
	s.mu.Unlock()
	log.Printf("live: strip found by %s at screen (%d,%d) -> video (%d,%d), cell %d, screenshot->locate %s, screen clock %d vs host %d",
		loc.Method, geom.ScreenX, geom.ScreenY, geom.VideoX, geom.VideoY, geom.Cell,
		time.Duration(hostAfter-hostBefore).Round(time.Millisecond), geom.ScreenMS, geom.ScreenHostMS)
	return nil
}

func (s *server) sessionSize(geom *stripGeometry) {
	if s.session != nil {
		geom.VideoW, geom.VideoH = s.session.Size()
	}
}

// injectTap writes one touch down/up pair over the control socket.
//
// This is the whole point of using scrcpy's control socket: `adb shell input`
// would spawn a process on the device and cost 100-300 ms, which is larger than
// the input budget being measured.
func (s *server) injectTap(kind string, x, y int) error {
	s.mu.Lock()
	sess := s.session
	seq := len(s.taps) + 1
	s.taps = append(s.taps, tapRecord{Seq: seq, Kind: kind, X: x, Y: y})
	s.mu.Unlock()
	if sess == nil {
		err := fmt.Errorf("no scrcpy session")
		s.setTapError(seq, err)
		return err
	}
	down, up, err := sess.Touch(x, y)
	s.mu.Lock()
	for i := range s.taps {
		if s.taps[i].Seq == seq {
			s.taps[i].HostDownNS, s.taps[i].HostUpNS = down, up
			if err != nil {
				s.taps[i].Error = err.Error()
			}
		}
	}
	s.mu.Unlock()
	return err
}

func (s *server) setTapError(seq int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.taps {
		if s.taps[i].Seq == seq {
			s.taps[i].Error = err.Error()
		}
	}
}

// send forwards one access unit to the RTP track, recording the instants on
// both sides of the hop.
func (s *server) send(pkt scrcpy.Packet) {
	s.mu.Lock()
	peer := s.peer
	if len(s.packets) > 0 {
		last := s.packets[len(s.packets)-1]
		if pkt.PTSUS > last.PTSUS {
			d := time.Duration(pkt.PTSUS-last.PTSUS) * time.Microsecond
			if d >= time.Millisecond && d <= 200*time.Millisecond {
				pkt.Duration = d
			}
		}
	}
	s.mu.Unlock()

	if s.dump != nil {
		if _, err := s.dump.Write(pkt.Data); err != nil {
			s.note("dumping access unit: " + err.Error())
		}
	}
	if peer == nil {
		pkt.SentNS = 0
		s.mu.Lock()
		s.packets = append(s.packets, pkt)
		s.mu.Unlock()
		return
	}
	sample := peer.Write(pkt.Data, pkt.Duration)
	pkt.SentNS = sample.StartNS
	pkt.WriteNS = sample.EndNS
	pkt.RTPPackets = sample.RTPPackets
	pkt.RTPBytes = sample.RTPBytes
	if sample.Err != "" {
		s.note("WriteSample: " + sample.Err)
	}
	s.mu.Lock()
	s.packets = append(s.packets, pkt)
	s.mu.Unlock()
}

// runReport is everything one run's raw log carries.
type runReport struct {
	Label        string           `json:"label"`
	Serial       string           `json:"serial"`
	LabelTime    string           `json:"started_at"`
	DurationS    float64          `json:"duration_s"`
	VideoW       int              `json:"video_width"`
	VideoH       int              `json:"video_height"`
	MaxSize      int              `json:"max_size"`
	MaxFPS       int              `json:"max_fps"`
	BitRate      int              `json:"bit_rate"`
	KeyframeS    int              `json:"idr_interval_s"`
	CodecOptions []string         `json:"codec_options"`
	SPSProfileID string           `json:"sps_profile_level_id,omitempty"`
	TapsPlanned  int              `json:"taps_planned"`
	TapInterval  string           `json:"tap_interval"`
	Skew         skewReport       `json:"skew_adb"`
	SignalSkew   signalSkewReport `json:"skew_from_page"`
	Strip        stripGeometry    `json:"strip"`
	Notes        []string         `json:"notes"`
	PageInfo     json.RawMessage  `json:"page_info"`
	Heartbeat    clockcode.Stats  `json:"device_clock_stats"`
	Heartbeats   []deviceSignal   `json:"device_heartbeats"`
	DeviceTaps   []deviceSignal   `json:"device_taps"`
	Taps         []tapRecord      `json:"taps"`
	Packets      []scrcpy.Packet  `json:"packets"`
	ProbeGeom    json.RawMessage  `json:"probe_geometry"`
	Fullscreen   json.RawMessage  `json:"fullscreen_signal"`
	DeviceNTP    json.RawMessage  `json:"device_ntp"`
	Sender       json.RawMessage  `json:"sender_counts"`
	ServerStderr string           `json:"scrcpy_server_stderr"`
	Env          envReport        `json:"environment"`
	Error        string           `json:"error,omitempty"`
}

// signalSkewReport carries the min-one-way-delay estimate of the clock skew.
// The field name states the direction (host minus device) because that is the
// sign the latency correction needs, and a reader must not have to guess it: the
// first version of this struct named the fields the other way round while
// storing host-minus-device values, and the analyser -- reading a tag that did
// not exist -- silently corrected every latency by zero.
type signalSkewReport struct {
	HostMinusDeviceMS float64 `json:"host_minus_device_ms_min_delay"`
	Samples           int     `json:"samples"`
	SpreadMS          float64 `json:"spread_ms"`
}

type envReport struct {
	HostLoad1   string `json:"host_loadavg"`
	DeviceLoad  string `json:"device_loadavg"`
	ScreencapCM string `json:"screencap_command"`
}

func (s *server) writeReport(runErr error) {
	s.mu.Lock()
	var devMs []int64
	for _, h := range s.heartbeats {
		if h.DevMS > 0 {
			devMs = append(devMs, h.DevMS)
		}
	}
	allSignals := append(append([]deviceSignal{}, s.heartbeats...), s.deviceTaps...)
	best, n, spread := signalSkew(allSignals)
	rep := runReport{
		Label:        s.cfg.label,
		Serial:       s.cfg.serial,
		LabelTime:    time.Now().Format(time.RFC3339Nano),
		DurationS:    s.cfg.duration.Seconds(),
		MaxSize:      s.cfg.maxSize,
		MaxFPS:       s.cfg.maxFPS,
		BitRate:      s.cfg.bitrate,
		KeyframeS:    s.cfg.keyframeInterval,
		CodecOptions: codecOptsOf(s.cfg),
		SPSProfileID: s.devicePLID,
		TapsPlanned:  s.cfg.tapCount,
		TapInterval:  s.cfg.tapInterval.String(),
		Skew:         s.skew,
		SignalSkew:   signalSkewReport{HostMinusDeviceMS: best, Samples: n, SpreadMS: spread},
		Strip:        s.strip,
		Notes:        s.notes,
		PageInfo:     s.pageInfo,
		Heartbeat:    clockcode.Summarise(devMs),
		Heartbeats:   s.heartbeats,
		DeviceTaps:   s.deviceTaps,
		Taps:         s.taps,
		Packets:      s.packets,
		ProbeGeom:    s.probeGeom,
		Fullscreen:   s.fullscreenSignal,
		DeviceNTP:    s.ntp,
	}
	if s.peer != nil {
		if counts, err := json.Marshal(s.peer.Counts()); err == nil {
			rep.Sender = counts
		}
	}
	rep.VideoW, rep.VideoH = s.sessionSizeValues()
	if s.session != nil {
		rep.ServerStderr = s.session.ServerStderr()
		rep.Env.ScreencapCM = "adb exec-out screencap -p"
	}
	s.mu.Unlock()
	rep.Env.HostLoad1 = loadAvg()
	rep.Env.DeviceLoad = deviceLoad(s.cfg.adb, s.cfg.serial)
	if runErr != nil {
		rep.Error = runErr.Error()
	}

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		log.Printf("live: marshalling report: %v", err)
		return
	}
	out := filepath.Join(s.cfg.outDir, "live-"+s.cfg.label+".json")
	if err := writeFileAtomic(out, body); err != nil {
		log.Printf("live: writing %s: %v", out, err)
		return
	}
	log.Printf("live: wrote %s (%d frames, %d taps)", out, len(rep.Packets), len(rep.Taps))
}

func (s *server) sessionSizeValues() (int, int) {
	if s.session == nil {
		return 0, 0
	}
	return s.session.Size()
}

func loadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		return string(b)
	}
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func deviceLoad(adb, serial string) string {
	out, err := exec.Command(adb, "-s", serial, "shell", "cat", "/proc/loadavg").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("live: encoding response: %v", err)
	}
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
