// Command analyze joins the sender's per-frame log with the browser's
// per-frame observations and reports the latency both transports actually
// achieved.
//
// The join is per frame, on the frame index the browser read out of the
// decoded pixels, so every number here is "this source frame was released by
// the Go hop at T1 and the browser expected to display that exact frame at T2".
//
//	go run ./cmd/analyze -send results/send-<label>.json \
//	    -rtp results/client-<label>-rtp.json -mse results/client-<label>-mse.json
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
)

type frameSend struct {
	Frame        int   `json:"frame"`
	SchedNS      int64 `json:"sched_ns"`
	SendStartNS  int64 `json:"send_start_ns"`
	SendEndNS    int64 `json:"send_end_ns"`
	LateNS       int64 `json:"late_ns"`
	Bytes        int   `json:"bytes"`
	RTPPackets   int   `json:"rtp_packets"`
	RTPBytes     int   `json:"rtp_bytes"`
	NoSubscriber bool  `json:"no_subscriber"`
}

type fragSend struct {
	Index      int   `json:"index"`
	StartFrame int   `json:"start_frame"`
	EndFrame   int   `json:"end_frame"`
	SchedNS    int64 `json:"sched_ns"`
	PushNS     int64 `json:"push_ns"`
	FlushNS    int64 `json:"flush_ns"`
	Bytes      int   `json:"bytes"`
	Dropped    bool  `json:"dropped"`
}

type runLog struct {
	Label         string      `json:"label"`
	FPS           int         `json:"fps"`
	FrameCount    int         `json:"frame_count"`
	Fragments     int         `json:"fragments"`
	Timescale     uint32      `json:"timescale"`
	ProfileLID    string      `json:"profile_level_id"`
	StartNS       int64       `json:"start_ns"`
	EndNS         int64       `json:"end_ns"`
	Notes         []string    `json:"notes"`
	Frames        []frameSend `json:"frames"`
	FragmentSends []fragSend  `json:"fragment_sends"`
}

type sample struct {
	Index  *int    `json:"i"`
	EDT    float64 `json:"edt"` // epoch ms, when the browser expects the frame on screen
	CB     float64 `json:"cb"`  // epoch ms, when the callback ran
	PF     int64   `json:"pf"`
	MT     float64 `json:"mt"` // media time in seconds
	Proc   float64 `json:"proc"`
	W      int     `json:"w"`
	H      int     `json:"h"`
	ReadMS float64 `json:"readMS"`
}

type clientResult struct {
	Transport     string                 `json:"transport"`
	Errors        []string               `json:"errors"`
	Samples       []sample               `json:"samples"`
	Stats         []map[string]any       `json:"stats"`
	FinalStats    map[string]any         `json:"finalStats"`
	Playback      map[string]any         `json:"playbackQuality"`
	Receiver      map[string]any         `json:"receiverParameters"`
	FragmentArr   []map[string]any       `json:"fragmentArrivals"`
	ReadMSMax     float64                `json:"readMSMax"`
	RawSourceInfo map[string]any         `json:"sourceBuffer"`
	Caps          map[string]any         `json:"caps"`
	Extra         map[string]interface{} `json:"-"`
}

func main() {
	var (
		sendPath     = flag.String("send", "", "sender run log")
		rtpPath      = flag.String("rtp", "", "browser RTP results")
		msePath      = flag.String("mse", "", "browser MSE results")
		outPath      = flag.String("out", "", "optional JSON report path")
		warmup       = flag.Int("warmup", 30, "frames excluded from the steady-state figures")
		keepPerFrame = flag.Bool("per-frame", true, "include the per-frame series (large); off keeps the aggregate report small")
	)
	flag.Parse()
	if *sendPath == "" {
		log.Fatal("analyze: -send is required")
	}
	var run runLog
	load(*sendPath, &run)

	report := map[string]any{
		"source": map[string]any{
			"label":          run.Label,
			"fps":            run.FPS,
			"frames":         run.FrameCount,
			"fragments":      run.Fragments,
			"timescale":      run.Timescale,
			"profileLevelID": run.ProfileLID,
			"runSeconds":     float64(run.FrameCount) / float64(run.FPS),
			"notes":          run.Notes,
			"send":           summarizeSend(run),
		},
	}

	fmt.Printf("source: %d frames @ %d fps (%.1fs), %d fragments, timescale %d, profile-level-id %s\n",
		run.FrameCount, run.FPS, float64(run.FrameCount)/float64(run.FPS), run.Fragments, run.Timescale, run.ProfileLID)
	s := report["source"].(map[string]any)["send"].(map[string]any)
	fmt.Printf("send:   worst scheduling lateness %.2f ms, %v access units, %v packets, %.0f kbit/s\n",
		s["worstLateMS"], s["frames"], s["rtpPackets"], s["kbps"])

	if *rtpPath != "" {
		var r clientResult
		load(*rtpPath, &r)
		lat := latencyFor(r, run, "rtp", *warmup, keepPerFrame)
		report["rtp"] = lat
		printTransport("rtp", lat)
	}
	if *msePath != "" {
		var r clientResult
		load(*msePath, &r)
		lat := latencyFor(r, run, "mse", *warmup, keepPerFrame)
		report["mse"] = lat
		printTransport("mse", lat)
	}

	if *outPath != "" {
		// A NaN anywhere (an empty distribution) would make json.Marshal fail,
		// and silently writing nothing looks like a successful run. Walk the
		// report, name every non-finite statistic, and write nulls for them.
		if bad := sanitize(report, "root"); len(bad) > 0 {
			log.Printf("analyze: non-finite statistics replaced by null: %s", strings.Join(bad, ", "))
		}
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatalf("analyze: marshalling report: %v", err)
		}
		if err := os.WriteFile(*outPath, b, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nwrote %s\n", *outPath)
	}
}

func load(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("analyze: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("analyze: parsing %s: %v", path, err)
	}
}

func summarizeSend(run runLog) map[string]any {
	var worstLate int64
	var totalBytes, totalPkts int64
	var noSub int
	for _, f := range run.Frames {
		if f.LateNS > worstLate {
			worstLate = f.LateNS
		}
		totalBytes += int64(f.Bytes)
		totalPkts += int64(f.RTPPackets)
		if f.NoSubscriber {
			noSub++
		}
	}
	secs := float64(run.EndNS-run.StartNS) / 1e9
	kbps := 0.0
	if secs > 0 {
		kbps = float64(totalBytes) * 8 / secs / 1000
	}
	dropped := 0
	var pushToFlush []float64
	for _, f := range run.FragmentSends {
		if f.Dropped {
			dropped++
		}
		if f.FlushNS > 0 && f.PushNS > 0 {
			pushToFlush = append(pushToFlush, float64(f.FlushNS-f.PushNS)/1e6)
		}
	}
	return map[string]any{
		"frames":                len(run.Frames),
		"worstLateMS":           float64(worstLate) / 1e6,
		"accessUnitBytes":       totalBytes,
		"rtpPackets":            totalPkts,
		"kbps":                  math.Round(kbps*10) / 10,
		"noSubscriber":          noSub,
		"fragmentsPushed":       len(run.FragmentSends),
		"fragmentsDropped":      dropped,
		"fragmentPushToFlushMS": percentiles(pushToFlush),
		"runSeconds":            secs,
	}
}

func printTransport(name string, lat map[string]any) {
	fmt.Printf("\n== %s ==\n", name)
	fmt.Printf("  presented frames observed: %v of %v sent\n", lat["observed"], lat["sentFrames"])
	fmt.Printf("  barcode reads: %v valid, %v unreadable, %v dropped by the browser, %v re-presented\n",
		lat["validReads"], lat["unreadable"], lat["droppedFrames"], lat["duplicates"])
	fmt.Printf("  frame latency send -> expected display (ms): %s\n", fmtPct(lat["latencyMS"]))
	fmt.Printf("  steady state (after %v warmup frames): %s\n", lat["warmup"], fmtPct(lat["steadyMS"]))
	if v, ok := lat["jitterBufferMS"]; ok && v != nil {
		fmt.Printf("  of which jitter buffer per frame: %.2f ms, decode per frame: %.2f ms\n",
			num(v), num(lat["decodeMS"]))
	}
	if m, ok := lat["readMSMean"].(float64); ok {
		fmt.Printf("  instrument cost per frame: %.2f ms (max %.2f)\n", m, num(lat["readMSMax"]))
	} else {
		fmt.Printf("  instrument cost per frame: (pixel probe disabled for this run)\n")
	}
	if v, ok := lat["missingFrames"]; ok {
		fmt.Printf("  frames never presented: %v\n", v)
	}
	if v, ok := lat["fragmentToArrivalMS"]; ok && v != nil {
		fmt.Printf("  fragment push -> browser arrival (ms): %s\n", fmtPct(v))
	}
	if v, ok := lat["fragmentCadenceMS"].(map[string]any); ok {
		fmt.Printf("  fragment cadence: p50 %.2f ms\n", num(v["p50"]))
	}
	if bp, ok := lat["latencyByFragmentPosition"].(map[string]any); ok {
		for _, pos := range []string{"0", "1", "2"} {
			if m, ok := bp[pos].(map[string]any); ok {
				fmt.Printf("  latency for frames %s after a fragment boundary: %s\n", pos, fmtPct(m))
			}
		}
	}
}

func fmtPct(v any) string {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return "(none)"
	}
	return fmt.Sprintf("p50 %.2f  p90 %.2f  p95 %.2f  max %.2f  min %.2f  n %v",
		num(m["p50"]), num(m["p90"]), num(m["p95"]), num(m["max"]), num(m["min"]), m["n"])
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return math.NaN()
}

// latencyFor joins every browser-observed frame back to the moment the Go hop
// released it, and reports the distribution.
func latencyFor(r clientResult, run runLog, transport string, warmup int, keepPerFrame *bool) map[string]any {
	sendEnd := map[int]int64{}
	sendStart := map[int]int64{}
	for _, f := range run.Frames {
		sendEnd[f.Frame] = f.SendEndNS
		sendStart[f.Frame] = f.SendStartNS
	}

	var lats, steady []float64
	var readMS []float64
	seen := map[int]int{}
	firstSeen := map[int]bool{}
	var nullReads, dropped, outOfOrder, duplicates int
	last := -1
	perFrame := []map[string]any{}
	for _, s := range r.Samples {
		if s.ReadMS > 0 {
			readMS = append(readMS, s.ReadMS)
		}
		if s.Index == nil {
			nullReads++
			// A frame the browser says it displayed but whose index could not be
			// read: keep it visible in the report rather than dropping it.
			continue
		}
		i := *s.Index
		seen[i]++
		if i < last {
			outOfOrder++
		}
		last = i
		if firstSeen[i] {
			// A frame presented more than once: the MSE consumer re-presents a
			// frame after seeking back to the live edge. Only the first
			// presentation is the frame's arrival; counting the repeats would
			// report the seek's cost as transport latency.
			duplicates++
			continue
		}
		firstSeen[i] = true
		sent, ok := sendEnd[i]
		if !ok {
			continue
		}
		lat := s.EDT*1e6 - float64(sent)
		lats = append(lats, lat/1e6)
		if i >= warmup {
			steady = append(steady, lat/1e6)
		}
		perFrame = append(perFrame, map[string]any{
			"frame":  i,
			"sentNS": sent,
			"edtMS":  s.EDT,
			"latMS":  lat / 1e6,
			"mt":     s.MT,
			"pf":     s.PF,
		})
	}
	if pf, ok := r.Playback["droppedVideoFrames"].(float64); ok {
		dropped = int(pf)
	}

	var missing []int
	for i := 0; i < run.FrameCount; i++ {
		if seen[i] == 0 {
			missing = append(missing, i)
		}
	}

	perFrameSeries := any(perFrame)
	perFrameNote := ""
	if keepPerFrame != nil && !*keepPerFrame {
		perFrameSeries = nil
		perFrameNote = "per-frame series omitted; re-run cmd/analyze with -per-frame for it"
	}
	out := map[string]any{
		"transport":     transport,
		"observed":      len(r.Samples),
		"sentFrames":    run.FrameCount,
		"validReads":    len(lats),
		"unreadable":    nullReads,
		"droppedFrames": dropped,
		"outOfOrder":    outOfOrder,
		"duplicates":    duplicates,
		"latencyMS":     percentiles(lats),
		"steadyMS":      percentiles(steady),
		"warmup":        warmup,
		"readMSMean":    meanOrNil(readMS),
		"readMSMax":     r.ReadMSMax,
		"missingFrames": len(missing),
		"missingFirst":  firstN(missing, 10),
		"errors":        r.Errors,
		"perFrame":      perFrameSeries,
		"perFrameNote":  perFrameNote,
	}
	if len(readMS) > 0 {
		pm := percentiles(readMS)
		out["readMS"] = pm
		out["readMSMean"] = pm["p50"]
	}

	// MSE: a frame is only deliverable once the fragment containing it is
	// pushed, so a frame's transport latency has a structural floor set by its
	// position inside the fragment and the fragment's cadence. Report latency
	// grouped by that position: it is the difference between "this transport is
	// slow" and "this transport is chunked".
	if len(run.FragmentSends) > 0 {
		fragOf := map[int]fragSend{}
		for _, f := range run.FragmentSends {
			for i := f.StartFrame; i < f.EndFrame; i++ {
				fragOf[i] = f
			}
		}
		byPos := map[int][]float64{}
		var pushCadence []float64
		sorted := append([]fragSend(nil), run.FragmentSends...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a].Index < sorted[b].Index })
		for i := 1; i < len(sorted); i++ {
			if sorted[i].PushNS > 0 && sorted[i-1].PushNS > 0 {
				pushCadence = append(pushCadence, float64(sorted[i].PushNS-sorted[i-1].PushNS)/1e6)
			}
		}
		for _, pf := range perFrame {
			i := pf["frame"].(int)
			f, ok := fragOf[i]
			if !ok {
				continue
			}
			pos := i - f.StartFrame
			byPos[pos] = append(byPos[pos], pf["latMS"].(float64))
		}
		posOut := map[string]any{}
		for pos, v := range byPos {
			posOut[fmt.Sprintf("%d", pos)] = percentiles(v)
		}
		out["latencyByFragmentPosition"] = posOut
		out["fragmentCadenceMS"] = percentiles(pushCadence)
	}

	// Everything the browser reported about the decode itself.
	if r.FinalStats != nil {
		if in, ok := r.FinalStats["inbound"].(map[string]any); ok {
			out["rtpStats"] = in
			if d, ok := in["jitterBufferDelay"].(float64); ok {
				if n, ok := in["jitterBufferEmittedCount"].(float64); ok && n > 0 {
					out["jitterBufferMS"] = d / n * 1000
				}
			}
			if d, ok := in["totalDecodeTime"].(float64); ok {
				if n, ok := in["framesDecoded"].(float64); ok && n > 0 {
					out["decodeMS"] = d / n * 1000
				}
			}
			if d, ok := in["totalProcessingDelay"].(float64); ok {
				if n, ok := in["framesDecoded"].(float64); ok && n > 0 {
					out["processingMS"] = d / n * 1000
				}
			}
		}
	}
	if r.Receiver != nil {
		out["receiver"] = r.Receiver
	}
	if r.Playback != nil {
		out["playbackQuality"] = r.Playback
	}
	if r.Caps != nil {
		out["browserCapabilities"] = r.Caps
	}
	if r.RawSourceInfo != nil {
		out["sourceBuffer"] = r.RawSourceInfo
	}

	// MSE: how long a fragment took to reach the browser after the Go hop
	// released it. This is the structural cost of a fragment-based transport.
	if len(r.FragmentArr) > 0 && len(run.FragmentSends) > 0 {
		var durs []float64
		byIndex := map[int]fragSend{}
		for _, f := range run.FragmentSends {
			byIndex[f.Index] = f
		}
		// Fragment i is the i'th arrival for this consumer.
		sorted := append([]fragSend(nil), run.FragmentSends...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a].Index < sorted[b].Index })
		for i, arr := range r.FragmentArr {
			if i >= len(sorted) {
				break
			}
			t, _ := arr["t"].(float64)
			push := sorted[i].PushNS
			if push > 0 {
				durs = append(durs, t*1e6-float64(push))
			}
		}
		ms := make([]float64, len(durs))
		for i, d := range durs {
			ms[i] = d / 1e6
		}
		out["fragmentToArrivalMS"] = percentiles(ms)
		out["fragmentArrivals"] = len(r.FragmentArr)
	}

	return out
}

func firstN(v []int, n int) []int {
	if len(v) < n {
		return v
	}
	return v[:n]
}

func percentiles(v []float64) map[string]any {
	if len(v) == 0 {
		return nil
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return map[string]any{
		"n":    len(s),
		"min":  round(s[0]),
		"p50":  round(pct(s, 50)),
		"p90":  round(pct(s, 90)),
		"p95":  round(pct(s, 95)),
		"p99":  round(pct(s, 99)),
		"max":  round(s[len(s)-1]),
		"mean": round(mean(s)),
	}
}

func pct(s []float64, p float64) float64 {
	if len(s) == 0 {
		return math.NaN()
	}
	i := int(math.Round(p / 100 * float64(len(s)-1)))
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// meanOrNil returns a mean, or nil when there is nothing to average, so an
// empty distribution never becomes NaN in the report.
func meanOrNil(s []float64) any {
	if len(s) == 0 {
		return nil
	}
	return mean(s)
}

func mean(s []float64) float64 {
	if len(s) == 0 {
		return math.NaN()
	}
	t := 0.0
	for _, v := range s {
		t += v
	}
	return t / float64(len(s))
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

// sanitize replaces non-finite float values in place with nil, returning the
// paths it changed. An empty distribution is not a measurement of zero.
func sanitize(v any, path string) []string {
	var bad []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			p := path + "." + k
			if f, ok := val.(float64); ok {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					t[k] = nil
					bad = append(bad, p)
				}
				continue
			}
			bad = append(bad, sanitize(val, p)...)
		}
	case []map[string]any:
		for i, val := range t {
			bad = append(bad, sanitize(val, fmt.Sprintf("%s[%d]", path, i))...)
		}
	case []any:
		for i, val := range t {
			if f, ok := val.(float64); ok {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					t[i] = nil
					bad = append(bad, fmt.Sprintf("%s[%d]", path, i))
				}
				continue
			}
			bad = append(bad, sanitize(val, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return bad
}
