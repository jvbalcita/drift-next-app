package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"spikelocal/arc144/internal/livepeer"
)

func (s *server) handleSignal(w http.ResponseWriter, r *http.Request) {
	recv := time.Now().UnixNano()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 8192)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	var sig deviceSignal
	if err := json.Unmarshal(body, &sig); err != nil {
		http.Error(w, "bad signal", http.StatusBadRequest)
		return
	}
	sig.HostRecvNS = recv
	sig.Raw = json.RawMessage(body)
	// A page from an earlier run can still be open on the device and posting to
	// this port. Its clock is not this run's clock, so its signals are dropped.
	if tag := signalRunTag(body); tag != "" && tag != s.cfg.runTag {
		log.Printf("live: dropping a signal from another run's page (tag %s)", tag)
		writeJSON(w, map[string]any{"ok": false, "reason": "stale run tag"})
		return
	}

	s.mu.Lock()
	switch sig.Type {
	case "alive":
		s.pageAlive = true
		s.pageInfo = json.RawMessage(body)
		log.Printf("live: device page alive (devMs %d)", sig.DevMS)
	case "ntp":
		s.ntp = json.RawMessage(body)
		var n struct {
			BestRTTMS         float64 `json:"bestRTTMS"`
			HostMinusDeviceMS float64 `json:"hostMinusDeviceMS"`
			Samples           int     `json:"samples"`
		}
		_ = json.Unmarshal(body, &n)
		log.Printf("live: device-side NTP estimate: host-device %.3f ms at %.3f ms RTT over %d samples", n.HostMinusDeviceMS, n.BestRTTMS, n.Samples)
	case "fullscreen":
		// Records what the page reported, not what was hoped for: the request
		// can be refused, and a run must not claim a fullscreen layout it did
		// not get.
		s.fullscreenSignal = json.RawMessage(body)
		var fs struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &fs)
		s.fullscreen = fs.OK
		log.Printf("live: fullscreen request: ok=%v err=%q", fs.OK, fs.Error)
	case "tap":
		s.deviceTaps = append(s.deviceTaps, sig)
		// Join the device's own reaction time onto the injected tap with the
		// matching sequence number, so the input path can be reported without
		// waiting for the frame to come back.
		for i := range s.taps {
			if s.taps[i].DeviceTapNo == 0 && s.taps[i].Seq == sig.TapCount {
				s.taps[i].DeviceMS = sig.DevMS
				s.taps[i].DeviceTapNo = sig.TapCount
				s.taps[i].HostRecvNS = recv
			}
		}
		log.Printf("live: device reports tap %d at device ms %d", sig.TapCount, sig.DevMS)
	default:
		s.heartbeats = append(s.heartbeats, sig)
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	strip := s.strip
	cfg := s.cfg
	notes := append([]string(nil), s.notes...)
	skew := s.skew
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"label":         cfg.label,
		"readbackEvery": cfg.readbackEvery,
		"strip": map[string]any{
			"videoX": strip.VideoX, "videoY": strip.VideoY,
			"cell": strip.Cell, "found": strip.Found, "error": strip.Error,
		},
		"video":            map[string]any{"width": s.videoW(), "height": s.videoH()},
		"screen":           map[string]any{"width": strip.ScreenW, "height": strip.ScreenH},
		"clockToleranceMS": cfg.clockTolMS,
		"skew":             skew,
		"notes":            notes,
		"tapCount":         cfg.tapCount,
		"transport":        "rtp",
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
	s.mu.Lock()
	plid := ""
	for _, n := range s.notes {
		if len(n) > 26 && n[:26] == "device SPS profile-level-id " {
			plid = n[26:]
		}
	}
	existing := s.peer
	s.mu.Unlock()

	if existing == nil {
		peer, err := livepeer.New(plid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.peer = peer
		s.probeReady = true
		s.mu.Unlock()
		existing = peer
	}
	answer, err := existing.Answer(body.SDP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("live: answered the probe's offer (profile-level-id %q)", plid)
	writeJSON(w, map[string]any{"sdp": answer})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"pumpStarted":      s.pumpStarted,
		"pumpFinished":     s.pumpFinished,
		"startNS":          s.startNS,
		"endNS":            s.endNS,
		"packets":          len(s.packets),
		"probeReady":       s.probeReady,
		"pageAlive":        s.pageAlive,
		"fullscreen":       s.fullscreen,
		"taps":             s.taps,
		"strip":            s.strip,
		"fullscreenSignal": s.fullscreenSignal,
		"connected":        s.peerConnected(),
	})
}

func (s *server) handleNow(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ns": time.Now().UnixNano()})
}

func (s *server) handleProgress(w http.ResponseWriter, r *http.Request) {
	body := readRequestBody(r)
	var p struct {
		Samples    int  `json:"samples"`
		Decoded    int  `json:"decoded"`
		Paused     bool `json:"paused"`
		ReadyState int  `json:"readyState"`
		VideoWidth int  `json:"videoWidth"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad progress", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.probeSamples = p.Samples
	s.probeDecoded = p.Decoded
	s.probePaused = p.Paused
	s.probeReadyState = p.ReadyState
	s.probeVideoWidth = p.VideoWidth
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func readRequestBody(r *http.Request) []byte {
	body := make([]byte, 0, 1<<16)
	buf := make([]byte, 32<<10)
	total := 0
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			total += n
			if total > 32<<20 {
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

func (s *server) handleGeometry(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0, 2048)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	s.mu.Lock()
	s.probeGeom = json.RawMessage(body)
	s.mu.Unlock()
	log.Printf("live: probe geometry: %s", string(body))
	writeJSON(w, map[string]any{"ok": true})
}

func (s *server) handleTap(w http.ResponseWriter, r *http.Request) {
	x, y := 0, 0
	if v := r.URL.Query().Get("x"); v != "" {
		fmt.Sscanf(v, "%d", &x)
	}
	if v := r.URL.Query().Get("y"); v != "" {
		fmt.Sscanf(v, "%d", &y)
	}
	if x == 0 && y == 0 {
		x, y = s.videoW()/2, s.videoH()/2
	}
	if err := s.injectTap("manual", x, y); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "x": x, "y": y})
}

func (s *server) handleResult(w http.ResponseWriter, r *http.Request) {
	transport := r.URL.Query().Get("transport")
	if transport == "" {
		transport = "rtp"
	}
	body := make([]byte, 0, 1<<20)
	buf := make([]byte, 64<<10)
	total := 0
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			total += n
			if total > 128<<20 {
				http.Error(w, "too large", http.StatusBadRequest)
				return
			}
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	path := filepath.Join(s.cfg.outDir, fmt.Sprintf("client-%s-%s.json", s.cfg.label, transport))
	if err := writeFileAtomic(path, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.probeResult = json.RawMessage(body)
	s.mu.Unlock()
	log.Printf("live: probe results (%d bytes) -> %s", len(body), path)
	writeJSON(w, map[string]any{"ok": true, "bytes": len(body)})
}

func (s *server) handleDeviceSignals(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"pageInfo":   s.pageInfo,
		"heartbeats": s.heartbeats,
		"taps":       s.deviceTaps,
	})
}

func (s *server) peerConnected() bool {
	if s.peer == nil {
		return false
	}
	return s.peer.Connected()
}

// signalRunTag extracts the run tag a page stamps on its posts.
func signalRunTag(body []byte) string {
	var probe struct {
		Run string `json:"run"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Run
}
