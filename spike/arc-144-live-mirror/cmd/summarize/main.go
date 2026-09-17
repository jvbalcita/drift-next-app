// Command summarize turns the per-run reports into the one table the ARC-144
// finding is stated from, so the numbers in the write-up are generated rather
// than transcribed.
//
//	go run ./cmd/summarize results/report-on.json results/report-on2.json ...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

type report map[string]any

func main() {
	var markdown = flag.Bool("markdown", true, "emit markdown")
	flag.Parse()
	paths := flag.Args()
	if len(paths) == 0 {
		log.Fatal("summarize: pass one or more report-*.json")
	}

	type row struct {
		label, transport        string
		p50, p90, p95, max, min float64
		observed, sent, drops   int
		fragCadence             float64
		jitterBuffer, decodeMS  float64
		fragArrivalP50          float64
		dupes                   int
	}
	var rows []row
	capLines := []string{}

	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		var r report
		if err := json.Unmarshal(b, &r); err != nil {
			log.Fatal(err)
		}
		src := r["source"].(map[string]any)
		label, _ := src["label"].(string)
		if len(capLines) == 0 {
			if rtp, ok := r["rtp"].(map[string]any); ok {
				capLines = append(capLines, describeCaps(rtp)...)
			}
		}
		for _, t := range []string{"rtp", "mse"} {
			m, ok := r[t].(map[string]any)
			if !ok || m == nil {
				continue
			}
			lat, _ := m["latencyMS"].(map[string]any)
			if lat == nil {
				continue
			}
			rw := row{
				label:     label,
				transport: t,
				p50:       num(lat["p50"]),
				p90:       num(lat["p90"]),
				p95:       num(lat["p95"]),
				max:       num(lat["max"]),
				min:       num(lat["min"]),
				observed:  int(num(m["observed"])),
				sent:      int(num(m["sentFrames"])),
				drops:     int(num(m["droppedFrames"])),
				dupes:     int(num(m["duplicates"])),
			}
			if v, ok := m["jitterBufferMS"]; ok {
				rw.jitterBuffer = num(v)
			}
			if v, ok := m["decodeMS"]; ok {
				rw.decodeMS = num(v)
			}
			if v := m["fragmentCadenceMS"]; v != nil {
				if mm, ok := v.(map[string]any); ok {
					rw.fragCadence = num(mm["p50"])
				}
			}
			if v := m["fragmentToArrivalMS"]; v != nil {
				if mm, ok := v.(map[string]any); ok {
					rw.fragArrivalP50 = num(mm["p50"])
				}
			}
			rows = append(rows, rw)
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].label != rows[b].label {
			return rows[a].label < rows[b].label
		}
		return rows[a].transport < rows[b].transport
	})

	if *markdown {
		fmt.Println("| run | transport | p50 ms | p90 ms | p95 ms | max ms | min ms | frames observed/sent | dropped | note |")
		fmt.Println("|---|---|---|---|---|---|---|---|---|---|")
		for _, r := range rows {
			note := ""
			if r.transport == "mse" && r.fragCadence > 0 {
				note = fmt.Sprintf("fragment cadence %.0f ms, push→arrival %.2f ms, %d re-presented",
					r.fragCadence, r.fragArrivalP50, r.dupes)
			} else if r.decodeMS > 0 {
				note = fmt.Sprintf("jitter buffer %.2f ms, decode %.2f ms", r.jitterBuffer, r.decodeMS)
			}
			fmt.Printf("| %s | %s | %.1f | %.1f | %.1f | %.1f | %.1f | %d/%d | %d | %s |\n",
				r.label, r.transport, r.p50, r.p90, r.p95, r.max, r.min, r.observed, r.sent, r.drops, note)
		}
	}

	// The headline: what MSE costs over the RTP path measured in the same run.
	fmt.Println()
	byLabel := map[string]map[string]row{}
	for _, r := range rows {
		if byLabel[r.label] == nil {
			byLabel[r.label] = map[string]row{}
		}
		byLabel[r.label][r.transport] = r
	}
	var labels []string
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	fmt.Println("MSE minus RTP, same run (p50, ms):")
	for _, l := range labels {
		g := byLabel[l]
		rtp, ok1 := g["rtp"]
		mse, ok2 := g["mse"]
		if !ok1 || !ok2 {
			continue
		}
		fmt.Printf("  %-6s rtp %.1f  mse %.1f  delta %.1f\n", l, rtp.p50, mse.p50, mse.p50-rtp.p50)
	}

	if len(capLines) > 0 {
		fmt.Println()
		fmt.Println("browser decode facts (" + filepath.Base(paths[0]) + "):")
		for _, l := range capLines {
			fmt.Println("  " + l)
		}
	}
}

func describeCaps(m map[string]any) []string {
	var out []string
	caps, _ := m["browserCapabilities"].(map[string]any)
	if caps != nil {
		if ua, ok := caps["userAgent"].(string); ok {
			out = append(out, "user agent: "+ua)
		}
		out = append(out, fmt.Sprintf("MediaSource.isTypeSupported(avc): %v", caps["mseIsTypeSupported"]))
		for _, k := range []string{"decodingInfoWebRTC", "decodingInfoMediaSource"} {
			if v, ok := caps[k].(map[string]any); ok {
				out = append(out, fmt.Sprintf("%s: supported=%v smooth=%v powerEfficient=%v",
					k, v["supported"], v["smooth"], v["powerEfficient"]))
			}
		}
		for _, k := range []string{"videoDecoder", "videoDecoderPreferHardware"} {
			if v, ok := caps[k].(map[string]any); ok {
				out = append(out, fmt.Sprintf("VideoDecoder.isConfigSupported %s: %v", k, v["supported"]))
			}
		}
	}
	if rec, ok := m["receiver"].(map[string]any); ok {
		if cs, ok := rec["codecs"].([]any); ok && len(cs) > 0 {
			if c0, ok := cs[0].(map[string]any); ok {
				out = append(out, fmt.Sprintf("negotiated RTP codec: %v clockRate %v fmtp %q",
					c0["mimeType"], c0["clockRate"], c0["sdpFmtpLine"]))
			}
		}
	}
	if pq, ok := m["playbackQuality"].(map[string]any); ok {
		out = append(out, fmt.Sprintf("RTP video element: %vx%v, totalVideoFrames %v, dropped %v, corrupted %v",
			pq["videoWidth"], pq["videoHeight"], pq["totalVideoFrames"], pq["droppedVideoFrames"],
			pq["corruptedVideoFrames"]))
	}
	if st, ok := m["rtpStats"].(map[string]any); ok && st != nil {
		out = append(out, fmt.Sprintf("inbound-rtp: framesDecoded %v, framesDropped %v, packetsLost %v, jitter %v, bytes %v",
			st["framesDecoded"], st["framesDropped"], st["packetsLost"], st["jitter"], st["bytesReceived"]))
	}
	return out
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	}
	return 0
}
