// Command rtpreplay feeds a captured Annex-B stream through the same pion hop
// and the same browser probe as the live rig, with no device involved.
//
// It exists because "the browser decoded nothing" has two very different
// causes: the bytes on the wire, or the way they were delivered. Replaying the
// exact bytes a run captured, at a fixed frame rate, separates the two -- and
// since the capture is a recording of the device's screen, the probe still has a
// real clock strip to read, so the whole instrument can be validated without a
// device.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spikelocal/arc144/internal/annexb"
	"spikelocal/arc144/internal/livepeer"
	"spikelocal/arc144/internal/webui"
)

type config struct {
	h264     string
	addr     string
	fps      int
	stripX   int
	stripY   int
	cell     int
	videoW   int
	videoH   int
	outDir   string
	label    string
	tail     time.Duration
	barePeer bool
}

type server struct {
	cfg  config
	peer *livepeer.Peer
	aus  []annexb.AccessUnit
	plid string

	mu           sync.Mutex
	probeReady   bool
	probeResult  json.RawMessage
	probeGeom    json.RawMessage
	pumpStarted  bool
	pumpFinished bool
	startNS      int64
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.h264, "h264", "", "captured Annex-B stream (required)")
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:8793", "listen address")
	flag.IntVar(&cfg.fps, "fps", 60, "frame rate to replay at")
	flag.IntVar(&cfg.stripX, "strip-x", 0, "clock strip x in the video frame")
	flag.IntVar(&cfg.stripY, "strip-y", 569, "clock strip y in the video frame")
	flag.IntVar(&cfg.cell, "cell", 32, "clock strip cell size in device pixels")
	flag.IntVar(&cfg.videoW, "video-w", 1080, "encoded width")
	flag.IntVar(&cfg.videoH, "video-h", 2280, "encoded height")
	flag.StringVar(&cfg.outDir, "out", "results", "output directory")
	flag.StringVar(&cfg.label, "label", "replay", "label for output files")
	flag.DurationVar(&cfg.tail, "tail", 4*time.Second, "collection time after the last frame")
	flag.BoolVar(&cfg.barePeer, "bare-peer", false, "build the peer the way Part A did (default API interceptors only)")
	flag.Parse()

	if cfg.h264 == "" {
		log.Fatal("rtpreplay: -h264 is required")
	}
	raw, err := os.ReadFile(cfg.h264)
	if err != nil {
		log.Fatal(err)
	}
	aus, err := annexb.Split(raw)
	if err != nil {
		log.Fatal(err)
	}
	plid, err := livepeer.ProfileLevelID(aus[0].Data)
	if err != nil {
		log.Printf("rtpreplay: reading the profile-level-id: %v", err)
	}
	log.Printf("rtpreplay: %d access units, profile-level-id %s, %dx%d, replaying at %d fps",
		len(aus), plid, cfg.videoW, cfg.videoH, cfg.fps)

	s := &server{cfg: cfg, aus: aus, plid: plid}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(webui.FS())))
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/offer", s.handleOffer)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/now", s.handleNow)
	mux.HandleFunc("/geometry", s.handleGeometry)
	mux.HandleFunc("/result", s.handleResult)
	go func() { _ = http.ListenAndServe(cfg.addr, mux) }()

	if err := s.awaitProbe(60 * time.Second); err != nil {
		log.Fatalf("rtpreplay: %v", err)
	}
	s.pace()
	log.Printf("rtpreplay: replay complete")
	os.Exit(0)
}

func (s *server) awaitProbe(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ok := s.probeReady
		s.mu.Unlock()
		if ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("no probe reached /offer within %s", timeout)
}

func (s *server) pace() {
	period := time.Second / time.Duration(s.cfg.fps)
	s.mu.Lock()
	s.startNS = time.Now().UnixNano()
	s.pumpStarted = true
	peer := s.peer
	s.mu.Unlock()

	start := time.Now()
	for i, au := range s.aus {
		if d := time.Until(start.Add(time.Duration(i) * period)); d > 0 {
			time.Sleep(d)
		}
		if peer != nil {
			sample := peer.Write(au.Data, period)
			if sample.Err != "" {
				log.Printf("rtpreplay: frame %d: %s", i, sample.Err)
			}
		}
	}
	s.mu.Lock()
	s.pumpFinished = true
	peerNow := s.peer
	s.mu.Unlock()
	// The sender's own count is the other half of the comparison with the
	// browser's packetsReceived: without it, a missing packet cannot be
	// attributed to the hop or to the receiver.
	if peerNow != nil {
		counts, err := json.MarshalIndent(peerNow.Counts(), "", "  ")
		if err == nil {
			out := filepath.Join(s.cfg.outDir, "replay-sender-"+s.cfg.label+".json")
			if werr := os.WriteFile(out, counts, 0o644); werr != nil {
				log.Printf("rtpreplay: writing %s: %v", out, werr)
			}
			log.Printf("rtpreplay: sender counts: %s", string(counts))
		}
	}
	// Wait for the probe to post its samples before exiting.
	deadline := time.Now().Add(s.cfg.tail + 30*time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := s.probeResult != nil
		s.mu.Unlock()
		if got {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("rtpreplay: no probe results before the deadline")
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"label": s.cfg.label, "readbackEvery": 1, "transport": "rtp",
		"strip": map[string]any{
			"videoX": s.cfg.stripX, "videoY": s.cfg.stripY,
			"cell": s.cfg.cell, "found": true, "error": "",
		},
		"video":            map[string]any{"width": s.cfg.videoW, "height": s.cfg.videoH},
		"screen":           map[string]any{"width": s.cfg.videoW, "height": s.cfg.videoH},
		"clockToleranceMS": 600000, "tapCount": 0,
	})
}

func (s *server) handleOffer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SDP string `json:"sdp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SDP == "" {
		http.Error(w, "bad offer", http.StatusBadRequest)
		return
	}
	var peer *livepeer.Peer
	var err error
	if s.cfg.barePeer {
		peer, err = livepeer.NewBare(s.plid)
	} else {
		peer, err = livepeer.New(s.plid)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	answer, err := peer.Answer(body.SDP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.peer = peer
	s.probeReady = true
	s.mu.Unlock()
	log.Printf("rtpreplay: answered the probe's offer, waiting %s for it to settle", 2*time.Second)
	time.Sleep(2 * time.Second)
	writeJSON(w, map[string]any{"sdp": answer})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"pumpStarted": s.pumpStarted, "pumpFinished": s.pumpFinished,
		"startNS": s.startNS, "probeReady": s.probeReady,
		"pageAlive": true, "taps": []any{}, "connected": s.peer != nil && s.peer.Connected(),
	})
}

func (s *server) handleNow(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ns": time.Now().UnixNano()})
}

func (s *server) handleGeometry(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	s.mu.Lock()
	s.probeGeom = json.RawMessage(body)
	s.mu.Unlock()
	log.Printf("rtpreplay: probe geometry: %s", string(body))
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleResult(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	path := filepath.Join(s.cfg.outDir, fmt.Sprintf("replay-client-%s.json", s.cfg.label))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.probeResult = json.RawMessage(body)
	s.mu.Unlock()
	log.Printf("rtpreplay: probe results (%d bytes) -> %s", len(body), path)
	writeJSON(w, map[string]any{"ok": true, "bytes": len(body)})
}

func readBody(r *http.Request) []byte {
	body := make([]byte, 0, 1<<16)
	buf := make([]byte, 64<<10)
	total := 0
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			total += n
			if total > 128<<20 {
				break
			}
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return body
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
