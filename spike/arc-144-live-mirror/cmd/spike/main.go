// Command spike is the throwaway measurement harness for ARC-144.
//
// It serves one page that renders the same H.264 source twice: once as an RTP
// H.264 track over pion/webrtc (the ARC-143 plan), once as fragmented MP4 into
// Media Source Extensions (the control transport). Both are fed by one pacer
// from the same generated elementary stream, so the two transports are
// compared on identical bytes at identical times.
//
// It is a measurement rig, not a feature: it is not wired into cmd/control-plane
// or the console, and it lives in its own Go module so the shipped module graph
// is untouched.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spikelocal/arc144/internal/annexb"
	"spikelocal/arc144/internal/fmp4"
)

//go:embed web
var webFS embed.FS

type config struct {
	h264Path      string
	mp4Path       string
	fps           int
	addr          string
	outDir        string
	label         string
	wait          time.Duration
	tail          time.Duration
	settle        time.Duration
	readbackEvery int
}

type server struct {
	cfg    config
	aus    []annexb.AccessUnit
	frags  []fmp4.Fragment
	init   []byte
	plid   string // SPS profile-level-id, e.g. 42e01f
	source int64  // total access-unit bytes

	mu       sync.Mutex
	rtpReady bool
	mseReady bool
	rtp      *rtpClient
	mse      *mseSink
	startNS  int64
	started  bool
	finished bool
	results  map[string]json.RawMessage

	logMu sync.Mutex
	log   runLog
}

func main() {
	cfg := config{}
	var wait, tail, settle time.Duration
	var readbackEvery int
	flag.StringVar(&cfg.h264Path, "h264", "", "Annex-B elementary stream (required)")
	flag.StringVar(&cfg.mp4Path, "mp4", "", "fragmented MP4 muxed from the same stream (required)")
	flag.IntVar(&cfg.fps, "fps", 30, "source frame rate")
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:8791", "listen address")
	flag.StringVar(&cfg.outDir, "out", "results", "output directory")
	flag.StringVar(&cfg.label, "label", "run", "label for this run's output files")
	flag.DurationVar(&wait, "wait", 30*time.Second, "how long to wait for both consumers to be ready")
	flag.DurationVar(&settle, "settle", 2*time.Second, "pause between both consumers reporting ready and the first frame")
	flag.DurationVar(&tail, "tail", 3*time.Second, "collect for this long after the last frame")
	flag.IntVar(&readbackEvery, "readback-every", 1, "read the barcode every Nth presented frame (0 disables the pixel probe, as an instrument-off control)")
	flag.Parse()
	cfg.wait, cfg.tail, cfg.settle, cfg.readbackEvery = wait, tail, settle, readbackEvery

	if cfg.h264Path == "" || cfg.mp4Path == "" {
		log.Fatal("spike: -h264 and -mp4 are required")
	}
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	raw, err := os.ReadFile(cfg.h264Path)
	if err != nil {
		log.Fatal(err)
	}
	aus, err := annexb.Split(raw)
	if err != nil {
		log.Fatal(err)
	}
	mp4raw, err := os.ReadFile(cfg.mp4Path)
	if err != nil {
		log.Fatal(err)
	}
	mp4, err := fmp4.Parse(mp4raw, cfg.fps)
	if err != nil {
		log.Fatal(err)
	}
	if last := mp4.Fragments[len(mp4.Fragments)-1].EndFrame; last != len(aus) {
		log.Fatalf("spike: mp4 covers %d frames but the elementary stream has %d access units", last, len(aus))
	}
	var total int64
	for _, au := range aus {
		total += int64(len(au.Data))
	}
	plid, err := spsProfileLevel(aus)
	if err != nil {
		log.Fatal(err)
	}

	s := &server{
		cfg: cfg, aus: aus, frags: mp4.Fragments, init: mp4.Init,
		plid: plid, source: total,
		results: map[string]json.RawMessage{},
		log: runLog{
			Label:      cfg.label,
			FPS:        cfg.fps,
			FrameCount: len(aus),
			Source:     cfg.h264Path,
			MP4:        cfg.mp4Path,
			Fragments:  len(mp4.Fragments),
			Timescale:  mp4.Timescale,
			ProfileLID: plid,
		},
	}
	s.log.Notes = append(s.log.Notes,
		fmt.Sprintf("access units %d, idr every %d frames, one slice per picture", len(aus), cfg.fps))

	mux := http.NewServeMux()
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/offer", s.handleOffer)
	mux.HandleFunc("/mse/init", s.handleMSEInit)
	mux.HandleFunc("/mse/stream", s.handleMSEStream)
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/now", s.handleNow)
	mux.HandleFunc("/result", s.handleResult)

	srv := &http.Server{Addr: cfg.addr, Handler: mux}
	log.Printf("spike: serving %s (%d frames @ %d fps = %.1fs, %d fragments, profile-level-id %s)",
		cfg.addr, len(aus), cfg.fps, float64(len(aus))/float64(cfg.fps), len(mp4.Fragments), plid)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	// A run needs at least the MSE consumer; without it there is nothing to
	// compare the RTP path against.
	if err := s.awaitReady(cfg.wait); err != nil {
		log.Fatalf("spike: %v", err)
	}
	if cfg.settle > 0 {
		// Let both elements finish starting playback before the first frame, so
		// the beginning of the stream is observed rather than spent waiting for
		// the element to become ready.
		log.Printf("spike: settling for %s before the first frame", cfg.settle)
		time.Sleep(cfg.settle)
	}
	s.pace()
	s.awaitResults(cfg.tail, 20*time.Second)
	if err := s.writeRunLog(); err != nil {
		log.Fatalf("spike: writing run log: %v", err)
	}
	s.summary()
	_ = srv.Close()
	log.Printf("spike: run complete")
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"fps":            s.cfg.fps,
		"frameCount":     len(s.aus),
		"runSeconds":     float64(len(s.aus)) / float64(s.cfg.fps),
		"profileLevelID": s.plid,
		"mseContentType": fmt.Sprintf(`video/mp4; codecs="avc1.%s"`, s.plid),
		"rtpCodec":       "video/H264",
		"fragments":      len(s.frags),
		"sourceBytes":    s.source,
		"label":          s.cfg.label,
		"readbackEvery":  s.cfg.readbackEvery,
	})
}

func (s *server) handleNow(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ns": time.Now().UnixNano()})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"started":  s.started,
		"finished": s.finished,
		"startNS":  s.startNS,
		"rtpReady": s.rtpReady,
		"mseReady": s.mseReady,
	})
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Transport string          `json:"transport"`
		Info      json.RawMessage `json:"info"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	switch body.Transport {
	case "rtp":
		s.rtpReady = true
	case "mse":
		s.mseReady = true
	}
	s.mu.Unlock()
	log.Printf("spike: %s consumer ready", body.Transport)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleResult(w http.ResponseWriter, r *http.Request) {
	transport := r.URL.Query().Get("transport")
	if transport == "" {
		http.Error(w, "missing transport", http.StatusBadRequest)
		return
	}
	raw, err := readLimited(r, 64<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.cfg.outDir, fmt.Sprintf("client-%s-%s.json", s.cfg.label, transport))
	if err := writeFileAtomic(path, raw); err != nil {
		log.Printf("spike: writing %s: %v", path, err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.results[transport] = json.RawMessage(raw)
	s.mu.Unlock()
	log.Printf("spike: received %s results (%d bytes) -> %s", transport, len(raw), path)
	writeJSON(w, map[string]any{"ok": true, "bytes": len(raw)})
}

// awaitReady blocks until both consumers have signalled readiness.
func (s *server) awaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		rtp, mse := s.rtpReady, s.mseReady
		s.mu.Unlock()
		if rtp && mse {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for consumers (rtp=%v mse=%v)", timeout, rtp, mse)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *server) awaitResults(tail, timeout time.Duration) {
	deadline := time.Now().Add(tail + timeout)
	for {
		s.mu.Lock()
		n := len(s.results)
		s.mu.Unlock()
		if n >= 2 {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("spike: collected %d/2 result sets before the deadline", n)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *server) writeRunLog() error {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	body, err := json.MarshalIndent(s.log, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.cfg.outDir, "send-"+s.cfg.label+".json"), body)
}

func (s *server) summary() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	var late int64
	var bytes int64
	var pkts int
	for _, f := range s.log.Frames {
		if f.LateNS > late {
			late = f.LateNS
		}
		bytes += int64(f.Bytes)
		pkts += f.RTPPackets
	}
	log.Printf("spike: sent %d frames, %d RTP packets, %d bytes; worst scheduling lateness %s",
		len(s.log.Frames), pkts, bytes, time.Duration(late))
	dropped := 0
	for _, f := range s.log.FragmentSends {
		if f.Dropped {
			dropped++
		}
	}
	log.Printf("spike: pushed %d fragments, %d dropped", len(s.log.FragmentSends), dropped)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("spike: encoding response: %v", err)
	}
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readLimited(r *http.Request, limit int64) ([]byte, error) {
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 64<<10)
	total := int64(0)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return nil, fmt.Errorf("body exceeds %d bytes", limit)
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			if err == http.ErrBodyReadAfterClose {
				return buf, nil
			}
			if total > 0 && n == 0 {
				return buf, nil
			}
			return buf, nil
		}
	}
}
