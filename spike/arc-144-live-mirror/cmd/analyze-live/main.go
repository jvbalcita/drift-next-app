// Command analyze-live joins one Part B run into the numbers that matter.
//
// Three logs meet here:
//
//   - the server's run log: every encoded frame with the instants either side of
//     the Go hop, every injected tap with the host-clock instant it was written,
//     and the device's own clock signals;
//   - the device page's signals: the device clock at the moment it handled each
//     tap, and the heartbeats used to estimate the device/host skew;
//   - the browser probe's samples: for every frame the compositor presented, the
//     device-clock value read out of that frame's own pixels and the browser's
//     estimate of when it displayed it.
//
// Glass-to-glass is then: (host clock when the browser displayed the frame) -
// (device clock visible in that frame's pixels), skew-corrected. Nothing is
// asserted that is not in those logs; a missing log is a reported gap.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"spikelocal/arc144/internal/clockcode"
)

type packet struct {
	Seq         int    `json:"seq"`
	PTSUS       uint64 `json:"pts_us"`
	Key         bool   `json:"key"`
	Bytes       int    `json:"bytes"`
	ConfigBytes int    `json:"config_bytes"`
	RecvNS      int64  `json:"recv_ns"`
	SentNS      int64  `json:"sent_ns"`
	WriteNS     int64  `json:"write_ns"`
	RTPPackets  int    `json:"rtp_packets"`
	RTPBytes    int    `json:"rtp_bytes"`
}

type tapRecord struct {
	Seq         int    `json:"seq"`
	Kind        string `json:"kind"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	HostDownNS  int64  `json:"host_down_ns"`
	HostUpNS    int64  `json:"host_up_ns"`
	Error       string `json:"error,omitempty"`
	DeviceMS    int64  `json:"device_ms"`
	DeviceTapNo int    `json:"device_tap_count"`
	HostRecvNS  int64  `json:"device_signal_recv_ns"`
}

type deviceSignal struct {
	Type       string `json:"type"`
	DevMS      int64  `json:"devMs"`
	TapCount   int    `json:"tapCount"`
	Drawn      int    `json:"drawn"`
	FirstDrawn int64  `json:"firstDrawnMs"`
	LastDrawn  int64  `json:"lastDrawnMs"`
	MaxGapMS   int64  `json:"maxGapMs"`
	MissedRaf  int    `json:"missedRaf"`
	HostRecvNS int64  `json:"host_recv_ns"`
}

type runLog struct {
	Label       string          `json:"label"`
	Serial      string          `json:"serial"`
	VideoW      int             `json:"video_width"`
	VideoH      int             `json:"video_height"`
	DurationS   float64         `json:"duration_s"`
	TapsPlanned int             `json:"taps_planned"`
	TapInterval string          `json:"tap_interval"`
	Skew        skewReport      `json:"skew_adb"`
	SignalSkew  signalSkew      `json:"skew_from_page"`
	Strip       json.RawMessage `json:"strip"`
	Notes       []string        `json:"notes"`
	PageInfo    json.RawMessage `json:"page_info"`
	Heartbeat   clockcode.Stats `json:"device_clock_stats"`
	Heartbeats  []deviceSignal  `json:"device_heartbeats"`
	DeviceTaps  []deviceSignal  `json:"device_taps"`
	Taps        []tapRecord     `json:"taps"`
	Packets     []packet        `json:"packets"`
	ProbeGeom   json.RawMessage `json:"probe_geometry"`
	DeviceNTP   json.RawMessage `json:"device_ntp"`
	Sender      json.RawMessage `json:"sender_counts"`
	Env         struct {
		HostLoad1  string `json:"host_loadavg"`
		DeviceLoad string `json:"device_loadavg"`
	} `json:"environment"`
	ServerStderr string `json:"scrcpy_server_stderr"`
	Error        string `json:"error,omitempty"`
}

type skewReport struct {
	Samples           int     `json:"samples"`
	OK                int     `json:"ok"`
	DeviceMinusHostMS float64 `json:"device_minus_host_ms_min_rtt"`
	SpreadMS          float64 `json:"spread_ms"`
	AdbRTTMinMS       float64 `json:"adb_rtt_min_ms"`
	AdbRTTP50MS       float64 `json:"adb_rtt_p50_ms"`
}

type signalSkew struct {
	HostMinusDeviceMS float64 `json:"host_minus_device_ms_min_delay"`
	Samples           int     `json:"samples"`
	SpreadMS          float64 `json:"spread_ms"`
}

type sample struct {
	cb       float64
	edt      float64
	pf       int
	proc     float64
	readMS   float64
	ms       float64
	hasMS    bool
	reacting bool
	taps     int
	hasTaps  bool
	err      string
}

type probeResult struct {
	Transport string `json:"transport"`
	Samples   []struct {
		Cb     float64  `json:"cb"`
		Edt    float64  `json:"edt"`
		Pf     int      `json:"pf"`
		Proc   float64  `json:"proc"`
		ReadMS float64  `json:"readMS"`
		MS     *float64 `json:"ms"`
		React  bool     `json:"react"`
		Taps   *int     `json:"taps"`
		Err    string   `json:"err"`
	} `json:"samples"`
	Decode struct {
		Frames     int            `json:"frames"`
		Decoded    int            `json:"decoded"`
		Failed     int            `json:"failed"`
		ByErr      map[string]int `json:"byErr"`
		Rate       float64        `json:"rate"`
		ReadMSMean float64        `json:"readMSMean"`
		ReadMSMax  float64        `json:"readMSMax"`
	} `json:"decode"`
	Geometry   json.RawMessage `json:"geometry"`
	Offsets    json.RawMessage `json:"offsets"`
	FinalStats struct {
		Inbound struct {
			Codec                string  `json:"codec"`
			FramesReceived       int     `json:"framesReceived"`
			FramesDecoded        int     `json:"framesDecoded"`
			FramesDropped        int     `json:"framesDropped"`
			KeyFramesDecoded     int     `json:"keyFramesDecoded"`
			FreezeCount          int     `json:"freezeCount"`
			PacketsReceived      int     `json:"packetsReceived"`
			PacketsLost          int     `json:"packetsLost"`
			BytesReceived        int64   `json:"bytesReceived"`
			Jitter               float64 `json:"jitter"`
			JitterBufferDelay    float64 `json:"jitterBufferDelay"`
			JitterBufferEmitted  int     `json:"jitterBufferEmittedCount"`
			TotalDecodeTime      float64 `json:"totalDecodeTime"`
			TotalProcessingDelay float64 `json:"totalProcessingDelay"`
			TotalAssemblyTime    float64 `json:"totalAssemblyTime"`
		} `json:"inbound"`
		Codec *struct {
			MimeType    string `json:"mimeType"`
			ClockRate   int    `json:"clockRate"`
			PayloadType int    `json:"payloadType"`
		} `json:"codec"`
	} `json:"finalStats"`
	PlaybackQuality struct {
		TotalVideoFrames     int `json:"totalVideoFrames"`
		DroppedVideoFrames   int `json:"droppedVideoFrames"`
		CorruptedVideoFrames int `json:"corruptedVideoFrames"`
		VideoWidth           int `json:"videoWidth"`
		VideoHeight          int `json:"videoHeight"`
	} `json:"playbackQuality"`
	UserAgent string          `json:"userAgent"`
	Cfg       json.RawMessage `json:"cfg"`
}

func main() {
	var runPath, probePath, outPath, summaryPath, label string
	flag.StringVar(&runPath, "run", "", "server run log (required)")
	flag.StringVar(&probePath, "probe", "", "browser probe results (required)")
	flag.StringVar(&outPath, "out", "", "report JSON path")
	flag.StringVar(&summaryPath, "summary", "", "summary markdown path")
	flag.StringVar(&label, "label", "", "run label")
	flag.Parse()
	if runPath == "" || probePath == "" {
		log.Fatal("analyze-live: -run and -probe are required")
	}

	var run runLog
	mustRead(runPath, &run)
	var probe probeResult
	mustRead(probePath, &probe)
	if label == "" {
		label = run.Label
	}

	skew, skewSource, skewDetail := chooseSkew(run)

	// Per-frame glass-to-glass: the browser's expected display time for the
	// frame, minus the device clock carried in that frame's pixels, skew-corrected.
	var latencies []float64
	var rawLatencies []float64
	var devClocks []int64
	var presented, decoded, failed int
	skewMS := skew
	for _, s := range probe.Samples {
		presented++
		if s.MS == nil {
			failed++
			continue
		}
		decoded++
		devClocks = append(devClocks, int64(*s.MS))
		raw := s.Edt - *s.MS
		rawLatencies = append(rawLatencies, raw)
		latencies = append(latencies, raw-skewMS)
	}
	clockStats := clockcode.Summarise(devClocks)

	// The device's own view of the frames it painted: the page counts its rAF
	// callbacks and their worst gap, which is how a stalled page is told apart
	// from a slow transport.
	pageFrames := 0
	var pageMaxGap int64
	var pageMissed int
	for _, h := range run.Heartbeats {
		if h.Drawn > pageFrames {
			pageFrames = h.Drawn
		}
		if h.MaxGapMS > pageMaxGap {
			pageMaxGap = h.MaxGapMS
		}
		if h.MissedRaf > pageMissed {
			pageMissed = h.MissedRaf
		}
	}

	// Go hop cost, per frame, and the encoder's own cadence from its PTS.
	var hopCosts []float64
	var writeCosts []float64
	var ptsDeltas []float64
	keyFrames := 0
	var videoBytes int64
	for i, p := range run.Packets {
		if p.Key {
			keyFrames++
		}
		videoBytes += int64(p.Bytes)
		if p.RecvNS > 0 && p.WriteNS >= p.RecvNS {
			hopCosts = append(hopCosts, float64(p.WriteNS-p.RecvNS)/1e6)
		}
		if p.SentNS > 0 && p.WriteNS >= p.SentNS {
			writeCosts = append(writeCosts, float64(p.WriteNS-p.SentNS)/1e6)
		}
		if i > 0 && p.PTSUS > run.Packets[i-1].PTSUS {
			ptsDeltas = append(ptsDeltas, float64(p.PTSUS-run.Packets[i-1].PTSUS)/1000)
		}
	}

	// Input round-trip, two ways, both stated:
	//  (a) device-side handler time: when the page's own clock read the touch,
	//      skew-corrected, minus the instant the control message was written.
	//      This excludes the return video path entirely.
	//  (b) video-observed: the first presented frame carrying the tap's counter,
	//      minus the same instant. This includes the return video path, so it is
	//      the number to compare against "operator clicks -> device reacts as
	//      seen in the console".
	type tapResult struct {
		Seq              int      `json:"seq"`
		Kind             string   `json:"kind"`
		HostDownMS       float64  `json:"host_down_ms"`
		WritePairMS      float64  `json:"write_down_up_ms"`
		HandlerMS        *float64 `json:"handler_ms"`
		VideoSeenMS      *float64 `json:"video_seen_ms"`
		MatchedDeviceTap int      `json:"matched_device_tap"`
		Error            string   `json:"error,omitempty"`
	}
	var tapResults []tapResult
	tapCounters := map[int]float64{}
	for _, s := range probe.Samples {
		if s.Taps == nil || *s.Taps == 0 {
			continue
		}
		// The first frame showing counter n is the reaction to tap n.
		if _, seen := tapCounters[*s.Taps]; !seen {
			tapCounters[*s.Taps] = s.Edt
		}
	}
	// The device's own taps are joined to the injected ones by *time*, not by
	// counter. Measured: the browser's first-touch tooltip can swallow one
	// injected tap, so the page's counter and the rig's sequence are not
	// necessarily the same numbers -- a counter-based join would then report
	// every remaining tap as one interval too late, which it did (2.5 s, the tap
	// spacing) before this was fixed. Matching each injected tap to the nearest
	// device reaction, with a bound and with each device reaction used once, is
	// robust to both a swallowed tap and a dropped one.
	type deviceTap struct {
		Count int
		DevMS int64
	}
	var devTaps []deviceTap
	for _, dt := range run.DeviceTaps {
		if dt.DevMS > 0 {
			devTaps = append(devTaps, deviceTap{Count: dt.TapCount, DevMS: dt.DevMS})
		}
	}
	const joinWindowMS = 1500.0
	used := make([]bool, len(devTaps))
	for _, t := range run.Taps {
		tr := tapResult{
			Seq: t.Seq, Kind: t.Kind,
			HostDownMS:  float64(t.HostDownNS) / 1e6,
			WritePairMS: float64(t.HostUpNS-t.HostDownNS) / 1e6,
			Error:       t.Error,
		}
		target := tr.HostDownMS
		best, bestDiff := -1, math.MaxFloat64
		for i, dt := range devTaps {
			if used[i] {
				continue
			}
			d := math.Abs(float64(dt.DevMS) + skewMS - target)
			if d < bestDiff && d <= joinWindowMS {
				best, bestDiff = i, d
			}
		}
		if best >= 0 {
			used[best] = true
			v := float64(devTaps[best].DevMS) + skewMS - target
			tr.HandlerMS = &v
			tr.MatchedDeviceTap = devTaps[best].Count
			if paint, ok := tapCounters[devTaps[best].Count]; ok {
				w := paint - target
				tr.VideoSeenMS = &w
			}
		}
		tapResults = append(tapResults, tr)
	}
	var handlerLat, videoLat []float64
	measureTaps := 0
	for _, t := range tapResults {
		if t.Kind == "setup" {
			// The setup tap is not a measurement: it is consumed dismissing the
			// browser's own tooltip as often as not.
			continue
		}
		measureTaps++
		if t.HandlerMS != nil {
			handlerLat = append(handlerLat, *t.HandlerMS)
		}
		if t.VideoSeenMS != nil {
			videoLat = append(videoLat, *t.VideoSeenMS)
		}
	}

	in := probe.FinalStats.Inbound
	jitterBufMS, decodeMS, procMS := math.NaN(), math.NaN(), math.NaN()
	if in.JitterBufferEmitted > 0 {
		jitterBufMS = in.JitterBufferDelay / float64(in.JitterBufferEmitted) * 1000
	}
	if in.FramesDecoded > 0 {
		decodeMS = in.TotalDecodeTime / float64(in.FramesDecoded) * 1000
		procMS = in.TotalProcessingDelay / float64(in.FramesDecoded) * 1000
	}

	report := map[string]any{
		"label":        label,
		"generated_at": time.Now().Format(time.RFC3339Nano),
		"serial":       run.Serial,
		"video":        map[string]any{"width": run.VideoW, "height": run.VideoH},
		"explain": map[string]any{
			"glass_to_glass": "browser expectedDisplayTime for frame N minus the device clock read out of frame N's own pixels, skew-corrected",
			"input_handler":  "device clock when the page handled the touch, skew-corrected, minus the host instant the control message was written (excludes the return video path)",
			"input_video":    "first presented frame carrying that tap's counter, minus the same host instant (includes the return video path)",
			"skew_source":    skewSource,
			"skew_detail":    skewDetail,
			"skew_ms":        skewMS,
		},
		"glass_to_glass": map[string]any{
			"frames_presented":       presented,
			"frames_with_clock":      decoded,
			"frames_without_clock":   failed,
			"p50_ms":                 round(clockcode.Percentile(toInts(latencies), 50)),
			"p95_ms":                 round(clockcode.Percentile(toInts(latencies), 95)),
			"p99_ms":                 round(clockcode.Percentile(toInts(latencies), 99)),
			"min_ms":                 minFloat(latencies),
			"max_ms":                 maxFloat(latencies),
			"mean_ms":                mean(latencies),
			"negative_samples":       countNegative(latencies),
			"below_zero_note":        "samples below zero bound the join's own noise: the pixel readback can catch the frame after the one the callback reported, which makes a latency read low by one frame interval (~18 ms). Reported, not hidden.",
			"p50_raw_uncorrected_ms": round(clockcode.Percentile(toInts(rawLatencies), 50)),
			"device_clock_stats":     clockStats,
		},
		"probe_cost": map[string]any{
			"read_ms_mean":              probe.Decode.ReadMSMean,
			"read_ms_max":               probe.Decode.ReadMSMax,
			"decode_rate":               probe.Decode.Rate,
			"decode_failures_by_reason": probe.Decode.ByErr,
			"geometry":                  probe.Geometry,
			"calibration":               probe.Offsets,
		},
		"page": map[string]any{
			"raf_frames": pageFrames,
			"max_gap_ms": pageMaxGap,
			"missed_raf": pageMissed,
			"info":       run.PageInfo,
		},
		"go_hop": map[string]any{
			"frames":                   len(run.Packets),
			"key_frames":               keyFrames,
			"video_bytes":              videoBytes,
			"hop_ms_p50":               round(clockcode.Percentile(toInts(hopCosts), 50)),
			"hop_ms_p95":               round(clockcode.Percentile(toInts(hopCosts), 95)),
			"hop_ms_max":               round(maxFloat(hopCosts)),
			"write_sample_ms_p50":      round(clockcode.Percentile(toInts(writeCosts), 50)),
			"encoder_pts_delta_ms_p50": round(clockcode.Percentile(toInts(ptsDeltas), 50)),
			"encoder_pts_delta_ms_p95": round(clockcode.Percentile(toInts(ptsDeltas), 95)),
		},
		"browser_decode": map[string]any{
			"codec":                        in.Codec,
			"frames_received":              in.FramesReceived,
			"frames_decoded":               in.FramesDecoded,
			"frames_dropped":               in.FramesDropped,
			"key_frames_decoded":           in.KeyFramesDecoded,
			"freeze_count":                 in.FreezeCount,
			"packets_received":             in.PacketsReceived,
			"packets_lost":                 in.PacketsLost,
			"jitter_ms":                    in.Jitter,
			"jitter_buffer_ms":             round(jitterBufMS),
			"decode_ms":                    round(decodeMS),
			"processing_delay_ms":          round(procMS),
			"presented_total_video_frames": probe.PlaybackQuality.TotalVideoFrames,
			"presented_dropped_frames":     probe.PlaybackQuality.DroppedVideoFrames,
			"video_size":                   []int{probe.PlaybackQuality.VideoWidth, probe.PlaybackQuality.VideoHeight},
		},
		"input_round_trip": map[string]any{
			"taps":                 tapResults,
			"taps_injected":        measureTaps,
			"device_taps_reported": len(devTaps),
			"device_tap_replies":   len(handlerLat),
			"handler_p50_ms":       round(clockcode.Percentile(toInts(handlerLat), 50)),
			"handler_p95_ms":       round(clockcode.Percentile(toInts(handlerLat), 95)),
			"handler_n":            len(handlerLat),
			"video_seen_p50_ms":    round(clockcode.Percentile(toInts(videoLat), 50)),
			"video_seen_p95_ms":    round(clockcode.Percentile(toInts(videoLat), 95)),
			"video_seen_n":         len(videoLat),
		},
		"skew": map[string]any{
			"adb":  run.Skew,
			"page": run.SignalSkew,
		},
		"sender_counts":        run.Sender,
		"environment":          run.Env,
		"notes":                run.Notes,
		"run_error":            run.Error,
		"scrcpy_server_stderr": run.ServerStderr,
		"probe_user_agent":     probe.UserAgent,
		"strip":                run.Strip,
	}

	body, _ := json.MarshalIndent(report, "", "  ")
	if outPath == "" {
		outPath = fmt.Sprintf("results/report-live-%s.json", label)
	}
	if err := os.WriteFile(outPath, body, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(body))

	if summaryPath == "" {
		summaryPath = fmt.Sprintf("results/summary-part-b-%s.md", label)
	}
	if err := os.WriteFile(summaryPath, []byte(markdown(label, report)), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("analyze-live: wrote %s and %s", outPath, summaryPath)
}

// chooseSkew picks the clock-skew estimate the latency numbers are corrected
// with, from three independent ones, and states which it used.
//
// The preference order is by how much the estimator can be biased:
//
//   - the device page's NTP-style round trip (host_ms minus device_ms over 25
//     exchanges, best RTT kept): standard, and its bias is half the path
//     asymmetry, which on this fleet's wired link is sub-millisecond;
//   - the min-one-way-delay estimator over the page's posts: needs the smallest
//     observed delay to be ~0, which is safe only while something fast crosses
//     the link (the posts themselves);
//   - the adb round trip: the weakest, because the device's shell-spawn time sits
//     inside the round trip and the midpoint correction lands late. It is
//     reported for contrast, not used unless the others are missing.
func chooseSkew(run runLog) (float64, string, string) {
	page, pageOK := run.SignalSkew.HostMinusDeviceMS, run.SignalSkew.Samples
	adbSkew := -run.Skew.DeviceMinusHostMS
	ntpSkew, ntpRTT, ntpOK := math.NaN(), math.NaN(), 0
	if len(run.DeviceNTP) > 0 {
		var n struct {
			HostMinusDeviceMS float64 `json:"hostMinusDeviceMS"`
			BestRTTMS         float64 `json:"bestRTTMS"`
			Samples           int     `json:"samples"`
			SpreadMS          float64 `json:"spreadMS"`
		}
		if err := json.Unmarshal(run.DeviceNTP, &n); err == nil {
			ntpSkew, ntpRTT, ntpOK = n.HostMinusDeviceMS, n.BestRTTMS, n.Samples
		}
	}
	detail := fmt.Sprintf("ntp round trip (device side) %+.3f ms at %.3f ms best RTT over %d exchanges; min-delay estimator %+.3f ms over %d posts (spread %.3f ms); adb midpoint %+.3f ms (min RTT %.1f ms, spread %.3f ms)",
		ntpSkew, ntpRTT, ntpOK, page, pageOK, run.SignalSkew.SpreadMS, adbSkew, run.Skew.AdbRTTMinMS, run.Skew.SpreadMS)
	switch {
	case ntpOK >= 5:
		return ntpSkew, "device_ntp_round_trip", detail
	case pageOK >= 5:
		return page, "page_min_delay", detail + "; no device-side NTP report, fell back to min-delay"
	default:
		return adbSkew, "adb_midpoint", detail + "; fell back to adb"
	}
}

func markdown(label string, rep map[string]any) string {
	g := rep["glass_to_glass"].(map[string]any)
	hop := rep["go_hop"].(map[string]any)
	dec := rep["browser_decode"].(map[string]any)
	inp := rep["input_round_trip"].(map[string]any)
	sk := rep["skew"].(map[string]any)
	var b strings.Builder
	fmt.Fprintf(&b, "# ARC-144 Part B — %s\n\n", label)
	fmt.Fprintf(&b, "| measurement | value | n |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| glass-to-glass p50 | %v ms | %v frames with a clock read |\n", g["p50_ms"], g["frames_with_clock"])
	fmt.Fprintf(&b, "| glass-to-glass p95 | %v ms | |\n", g["p95_ms"])
	fmt.Fprintf(&b, "| glass-to-glass mean | %.1f ms | %v samples below zero |\n", g["mean_ms"], g["negative_samples"])
	fmt.Fprintf(&b, "| glass-to-glass + page render bound | %v ms | |\n", g["p50_ms"])
	fmt.Fprintf(&b, "| input, device handler (excludes return video) p50 | %v ms | %v taps |\n", inp["handler_p50_ms"], inp["handler_n"])
	fmt.Fprintf(&b, "| input, video-observed p50 | %v ms | %v taps |\n", inp["video_seen_p50_ms"], inp["video_seen_n"])
	fmt.Fprintf(&b, "| Go hop forward p50 | %v ms | %v frames |\n", hop["hop_ms_p50"], hop["frames"])
	fmt.Fprintf(&b, "| browser jitter buffer | %v ms | |\n", dec["jitter_buffer_ms"])
	fmt.Fprintf(&b, "| browser decode | %v ms | |\n", dec["decode_ms"])
	fmt.Fprintf(&b, "| browser processing delay | %v ms | |\n", dec["processing_delay_ms"])
	fmt.Fprintf(&b, "\n## Clock\n\n- skew used: %v (%v)\n- %v\n- device clock in pixels: %v\n",
		rep["explain"].(map[string]any)["skew_ms"], rep["explain"].(map[string]any)["skew_source"],
		rep["explain"].(map[string]any)["skew_detail"], g["device_clock_stats"])
	fmt.Fprintf(&b, "\n## Decode\n\n- %v frames presented, %v carried a decodable clock, %v did not\n- browser: received %v, decoded %v, dropped %v, lost %v\n- probe cost: mean %v ms, max %v ms per frame\n",
		g["frames_presented"], g["frames_with_clock"], g["frames_without_clock"],
		dec["frames_received"], dec["frames_decoded"], dec["frames_dropped"], dec["packets_lost"],
		rep["probe_cost"].(map[string]any)["read_ms_mean"], rep["probe_cost"].(map[string]any)["read_ms_max"])
	fmt.Fprintf(&b, "\n## Skew reports\n\n```json\n%s\n```\n", pretty(sk))
	fmt.Fprintf(&b, "\n## Taps\n\n```json\n%s\n```\n", pretty(inp["taps"]))
	return b.String()
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func mustRead(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("analyze-live: %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("analyze-live: %s: %v", path, err)
	}
}

func countNegative(v []float64) int {
	n := 0
	for _, x := range v {
		if x < 0 {
			n++
		}
	}
	return n
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func minFloat(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	m := v[0]
	for _, x := range v {
		if x < m {
			m = x
		}
	}
	return m
}

func maxFloat(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	m := v[0]
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

// toInts converts to the tenth-of-a-millisecond resolution the percentile
// helper works in, which is finer than any number this rig can honestly claim.
func toInts(v []float64) []int64 {
	out := make([]int64, 0, len(v))
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		out = append(out, int64(math.Round(x*10)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// round puts a value from clockcode.Percentile (tenths) back into milliseconds.
func round(v float64) float64 {
	if math.IsNaN(v) {
		return math.NaN()
	}
	return v / 10
}
