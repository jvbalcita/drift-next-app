package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// skewSample is one adb round trip used to estimate the difference between the
// device's clock and this host's.
type skewSample struct {
	RTTRoundMS   float64 `json:"rtt_ms"`
	OffsetMS     float64 `json:"device_minus_host_ms"`
	DeviceMS     int64   `json:"device_ms"`
	HostBeforeMS int64   `json:"host_before_ms"`
	HostAfterMS  int64   `json:"host_after_ms"`
	Error        string  `json:"error,omitempty"`
}

// skewReport is the clock-skew estimate, with the spread kept in the number.
//
// The estimator is the classic one: the sample with the smallest round trip has
// the least queuing, so its midpoint offset is the best available estimate, and
// the spread over all samples is the honest error bar. A skew correction that
// does not state its uncertainty would move straight into the latency number.
type skewReport struct {
	Samples           int          `json:"samples"`
	OK                int          `json:"ok"`
	DeviceMinusHostMS float64      `json:"device_minus_host_ms_min_rtt"`
	SpreadMS          float64      `json:"spread_ms"`
	MinOffsetMS       float64      `json:"min_offset_ms"`
	MaxOffsetMS       float64      `json:"max_offset_ms"`
	AdbRTTMinMS       float64      `json:"adb_rtt_min_ms"`
	AdbRTTP50MS       float64      `json:"adb_rtt_p50_ms"`
	Command           string       `json:"command"`
	Raw               []skewSample `json:"raw"`
}

// measureSkew samples `adb shell date +%s%N` and pairs it with the host clock
// read immediately before and after the command.
//
// The device is reached over Wi-Fi (its adb endpoint is a TCP address), so a
// round trip costs tens of milliseconds and the midpoint estimate carries half
// of that as error. That error bar is reported, and the caller prefers the
// page's own HTTP-delivered clock reading when it has one, which is far tighter.
func measureSkew(ctx context.Context, adb, serial string, samples int) skewReport {
	cmd := "date +%s%N"
	rep := skewReport{Samples: samples, Command: "adb shell " + cmd}
	var rtts, offsets []float64
	for i := 0; i < samples; i++ {
		before := time.Now()
		out, err := exec.CommandContext(ctx, adb, "-s", serial, "shell", cmd).Output()
		after := time.Now()
		s := skewSample{
			HostBeforeMS: before.UnixMilli(),
			HostAfterMS:  after.UnixMilli(),
			RTTRoundMS:   float64(after.Sub(before).Microseconds()) / 1000,
		}
		if err != nil {
			s.Error = err.Error()
			rep.Raw = append(rep.Raw, s)
			continue
		}
		text := strings.TrimSpace(string(out))
		var ns int64
		if _, err := fmt.Sscanf(text, "%d", &ns); err != nil {
			// Some builds do not support %N; fall back to seconds.
			s.Error = "unparsable date output: " + text
			rep.Raw = append(rep.Raw, s)
			continue
		}
		s.DeviceMS = ns / 1e6
		midpoint := float64(before.UnixNano()+after.UnixNano()) / 2 / 1e6
		s.OffsetMS = float64(s.DeviceMS) - midpoint
		rep.OK++
		rtts = append(rtts, s.RTTRoundMS)
		offsets = append(offsets, s.OffsetMS)
		rep.Raw = append(rep.Raw, s)
	}
	if len(rtts) == 0 {
		return rep
	}
	sortFloats(rtts)
	sortFloats(offsets)
	best := 0
	for i, s := range rep.Raw {
		if s.Error != "" {
			continue
		}
		if s.RTTRoundMS == rtts[0] {
			rep.DeviceMinusHostMS = rep.Raw[i].OffsetMS
			_ = best
			break
		}
	}
	rep.AdbRTTMinMS = rtts[0]
	rep.AdbRTTP50MS = rtts[len(rtts)/2]
	rep.MinOffsetMS = offsets[0]
	rep.MaxOffsetMS = offsets[len(offsets)-1]
	rep.SpreadMS = rep.MaxOffsetMS - rep.MinOffsetMS
	return rep
}

func sortFloats(v []float64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// signalSkew estimates the skew from the device page's own HTTP posts.
//
// Each post carries the device's Date.now() as it was read on the device; the
// host records when the request arrived. host_ms - dev_ms = skew + one-way
// delay, so the minimum over many samples is the skew plus the smallest delay
// the network ever added -- sub-millisecond on a LAN, and far tighter than an
// adb round trip.
func signalSkew(signals []deviceSignal) (float64, int, float64) {
	var vals []float64
	for _, s := range signals {
		if s.DevMS == 0 || s.HostRecvNS == 0 {
			continue
		}
		vals = append(vals, float64(s.HostRecvNS/1e6-s.DevMS))
	}
	if len(vals) == 0 {
		return 0, 0, 0
	}
	sortFloats(vals)
	return vals[0], len(vals), vals[len(vals)-1] - vals[0]
}
