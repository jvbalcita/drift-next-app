package media_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/media"
)

const (
	gridSweepProbeEnv            = "DRIFT_GRID_SWEEP_PROBE"
	gridSweepProbeOutEnv         = "DRIFT_GRID_SWEEP_PROBE_OUT"
	gridSweepProbeSweepsEnv      = "DRIFT_GRID_SWEEP_PROBE_SWEEPS"
	gridSweepProbeDurationEnv    = "DRIFT_GRID_SWEEP_PROBE_DURATION"
	gridSweepProbeDeviceLimitEnv = "DRIFT_GRID_SWEEP_PROBE_DEVICE_LIMIT"
	gridSweepProbeTimeout        = 5 * time.Minute
	gridSweepProbeMaxDuration    = 8 * time.Hour
	gridSweepProbeMaxAttempts    = 10
	gridSweepProbeSampleEvery    = 50 * time.Millisecond
)

func TestGridSweepProbeDurationParsingIsBounded(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset keeps sweep mode"},
		{name: "bounded soak", input: "8h", want: 8 * time.Hour},
		{name: "trimmed duration", input: " 30s ", want: 30 * time.Second},
		{name: "zero is refused", input: "0s", wantErr: true},
		{name: "negative is refused", input: "-1m", wantErr: true},
		{name: "malformed duration is refused", input: "overnight", wantErr: true},
		{name: "duration beyond eight hours is refused", input: "8h1m", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGridSweepProbeDuration(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseGridSweepProbeDuration(%q) error = %v, wantErr %t", test.input, err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("parseGridSweepProbeDuration(%q) = %s, want %s", test.input, got, test.want)
			}
		})
	}
}

func TestGridSweepProbeDeviceLimitIsBounded(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		available int
		maximum   int
		want      int
		wantErr   bool
	}{
		{name: "unset keeps all attached devices", available: 18, maximum: 64, want: 18},
		{name: "bounded sample", input: "1", available: 18, maximum: 64, want: 1},
		{name: "zero is refused", input: "0", available: 18, maximum: 64, wantErr: true},
		{name: "malformed limit is refused", input: "many", available: 18, maximum: 64, wantErr: true},
		{name: "limit cannot exceed attached devices", input: "19", available: 18, maximum: 64, wantErr: true},
		{name: "limit cannot exceed deployment bound", input: "13", available: 18, maximum: 12, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGridSweepProbeDeviceLimit(test.input, test.available, test.maximum)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseGridSweepProbeDeviceLimit(%q) error = %v, wantErr %t", test.input, err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("parseGridSweepProbeDeviceLimit(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

// TestGridStillSweepProbe measures bounded fleet sweeps through the same public
// subscription and frame interfaces the grid uses. Every device is captured only
// after it is subscribed, through the engine's bounded worker set; this probe
// opens no live mirror, sends no input, and does not issue standalone screenshots.
//
// It is opt-in because it captures every online device attached to this host:
//
//	DRIFT_GRID_SWEEP_PROBE=1
//	DRIFT_GRID_PROBE_ADB=/abs/adb
//	DRIFT_GRID_SWEEP_PROBE_OUT=/abs/report.json
//	DRIFT_GRID_CONCURRENT_CAPTURES=12 (optional, the deployment's grid setting)
//	DRIFT_GRID_SWEEP_PROBE_SWEEPS=3 (optional, capture each device this many times)
//	DRIFT_GRID_SWEEP_PROBE_DURATION=8h (optional, bounded continuous soak instead of sweeps)
//	DRIFT_GRID_SWEEP_PROBE_DEVICE_LIMIT=1 (optional, first N serials after sort; default all)
func TestGridStillSweepProbe(t *testing.T) {
	if strings.TrimSpace(os.Getenv(gridSweepProbeEnv)) != "1" {
		t.Skipf("skipping still-grid sweep probe: %s is not 1", gridSweepProbeEnv)
	}

	adbPath := strings.TrimSpace(os.Getenv(gridProbeADBEnv))
	if adbPath == "" || !filepath.IsAbs(adbPath) {
		t.Fatalf("%s must be an absolute adb path", gridProbeADBEnv)
	}
	outPath := strings.TrimSpace(os.Getenv(gridSweepProbeOutEnv))
	if outPath == "" || !filepath.IsAbs(outPath) {
		t.Fatalf("%s must be an absolute report path", gridSweepProbeOutEnv)
	}

	settings, err := media.GridSettingsFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("resolving the deployment grid settings: %v", err)
	}
	if settings.StreamPreviews {
		t.Fatal("the still-sweep probe refuses a deployment configured for persistent stream previews")
	}
	soakDuration, err := parseGridSweepProbeDuration(os.Getenv(gridSweepProbeDurationEnv))
	if err != nil {
		t.Fatal(err)
	}
	attemptsPerDevice := 1
	if raw := strings.TrimSpace(os.Getenv(gridSweepProbeSweepsEnv)); raw != "" {
		if soakDuration > 0 {
			t.Fatalf("%s and %s are mutually exclusive", gridSweepProbeDurationEnv, gridSweepProbeSweepsEnv)
		}
		attempts, parseErr := strconv.Atoi(raw)
		if parseErr != nil || attempts < 1 || attempts > gridSweepProbeMaxAttempts {
			t.Fatalf("%s must be an integer between 1 and %d", gridSweepProbeSweepsEnv, gridSweepProbeMaxAttempts)
		}
		attemptsPerDevice = attempts
	}
	if soakDuration > 0 {
		attemptsPerDevice = 0
	}
	engineInterval := gridSweepProbeTimeout
	if attemptsPerDevice > 1 || soakDuration > 0 {
		engineInterval = settings.Cadence
	}

	adapter, err := gridProbeAdapter(adbPath)
	if err != nil {
		t.Fatalf("building the device capture path: %v", err)
	}
	timedCapturer := &gridSweepTimedCapturer{inner: adapter, durations: make([]int64, 0, 512)}

	enumerationCtx, cancelEnumeration := context.WithTimeout(context.Background(), gridSweepProbeTimeout)
	devices, err := adapter.Enumerate(enumerationCtx)
	cancelEnumeration()
	if err != nil {
		t.Fatalf("enumerating online devices: %v", err)
	}
	serials := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.State == adb.StateDevice {
			serials = append(serials, device.Serial)
		}
	}
	sort.Strings(serials)
	if len(serials) == 0 {
		t.Skip("skipping still-grid sweep probe: adb reported no usable device")
	}
	deviceLimit, err := parseGridSweepProbeDeviceLimit(os.Getenv(gridSweepProbeDeviceLimitEnv), len(serials), settings.MaxDevices)
	if err != nil {
		t.Fatal(err)
	}
	serials = serials[:deviceLimit]
	probeTimeout := gridSweepProbeTimeout
	if soakDuration > 0 {
		captureWaves := (len(serials) + settings.ConcurrentCaptures - 1) / settings.ConcurrentCaptures
		warmupAndDrain := time.Duration(captureWaves)*media.DefaultFrameCaptureTimeout + settings.FreshnessCeiling
		probeTimeout = soakDuration + 2*warmupAndDrain
	}

	engine, err := media.NewFrameEngine(media.FrameEngineConfig{
		Capturer: timedCapturer,
		// A single-attempt probe suppresses later ticks so it measures a cold sweep.
		// A multi-attempt probe uses the deployment cadence to measure sustained
		// scheduling and the per-device cadence the engine actually achieves.
		Interval:           engineInterval,
		CaptureTimeout:     media.DefaultFrameCaptureTimeout,
		MaxSubscribers:     settings.MaxDevices,
		ConcurrentCaptures: settings.ConcurrentCaptures,
		ActiveInterval:     settings.ActiveCadence,
		IdleInterval:       settings.IdleCadence,
		FreshnessCeiling:   settings.FreshnessCeiling,
		Profile:            settings.Profile,
		Logf:               func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("building the subscribed still-frame engine: %v", err)
	}
	subscription := engine.SyncSubscriptions(serials)
	if len(subscription.Admitted) != len(serials) {
		t.Fatalf("the still engine admitted %d of %d online devices: refused=%v invalid=%v", len(subscription.Admitted), len(serials), subscription.Refused, subscription.Invalid)
	}
	cost := engine.GridCost()
	if cost.ConcurrentCaptures != settings.ConcurrentCaptures || cost.FreshnessCeiling != settings.FreshnessCeiling {
		t.Fatalf("engine cost = %+v, want deployment concurrency %d and freshness ceiling %s", cost, settings.ConcurrentCaptures, settings.FreshnessCeiling)
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	engineDone := make(chan media.FrameEngineOutcome, 1)
	go func() { engineDone <- engine.Run(ctx) }()

	// Complete at the public frame boundary when every device has the requested
	// number of attempts or, in soak mode, after its full duration and one final
	// successful capture per device. In single-attempt mode the long engine
	// interval prevents another tick; multi-attempt mode measures actual cadence.
	ticker := time.NewTicker(gridSweepProbeSampleEvery)
	defer ticker.Stop()
	completed := false
	waiting := make([]string, 0, len(serials))
	peakAgeBySerial := make(map[string]int64, len(serials))
	soakEndFrames := make(map[string]int, len(serials))
	soakDeadline := time.Time{}
	soakStarted := false
	soakReached := false
	var heapInuseMaxBytes, heapSysMaxBytes, processRSSMaxBytes uint64
	var lastRSSSample time.Time
	rssSampleInterval := 250 * time.Millisecond
	if soakDuration > 0 {
		// Keep the soak's process sampler bounded without spawning four ps
		// processes per second for the entire run.
		rssSampleInterval = time.Second
	}
	rssMeasurementNote := "RSS is sampled every 250ms for the opt-in Go test process plus local adb CLI/server processes; it excludes the control plane, console/browser, OS and unrelated processes, so it is not whole-host RSS"
	if soakDuration > 0 {
		rssMeasurementNote = "RSS is sampled every 1s for the opt-in Go test process plus local adb CLI/server processes; it excludes the control plane, console/browser, OS and unrelated processes, so it is not whole-host RSS"
	}
	for !completed {
		if soakDuration > 0 && !soakStarted {
			soakStarted = true
			for _, serial := range serials {
				frame, ok := engine.Frame(serial)
				if !ok || frame.CapturedAt.IsZero() || !frame.Current() {
					soakStarted = false
					break
				}
			}
			if soakStarted {
				soakDeadline = time.Now().Add(soakDuration)
			}
		}
		if soakDuration > 0 && soakStarted && !soakReached && !time.Now().Before(soakDeadline) {
			soakReached = true
			for _, serial := range serials {
				frame, ok := engine.Frame(serial)
				if ok {
					soakEndFrames[serial] = frame.Frames
				}
			}
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		if memory.HeapInuse > heapInuseMaxBytes {
			heapInuseMaxBytes = memory.HeapInuse
		}
		if memory.HeapSys > heapSysMaxBytes {
			heapSysMaxBytes = memory.HeapSys
		}
		if time.Since(lastRSSSample) >= rssSampleInterval {
			lastRSSSample = time.Now()
			if rssBytes, rssErr := gridSweepProcessAndADBBytes(os.Getpid()); rssErr != nil {
				rssMeasurementNote = "Go test and local adb process RSS could not be sampled with ps; Go heap metrics only"
			} else if rssBytes > processRSSMaxBytes {
				processRSSMaxBytes = rssBytes
			}
		}
		waiting = waiting[:0]
		if soakDuration > 0 {
			completed = soakStarted && soakReached
		} else {
			completed = true
		}
		for _, serial := range serials {
			frame, ok := engine.Frame(serial)
			if ok && !frame.CapturedAt.IsZero() && (soakDuration == 0 || soakStarted) {
				age := time.Since(frame.CapturedAt).Milliseconds()
				if age > peakAgeBySerial[serial] {
					peakAgeBySerial[serial] = age
				}
			}
			if soakDuration > 0 {
				if !soakStarted || !soakReached || !ok || frame.Frames <= soakEndFrames[serial] {
					completed = false
					waiting = append(waiting, serial)
				}
			} else if !ok || frame.Frames+frame.Failures < attemptsPerDevice {
				completed = false
				waiting = append(waiting, serial)
			}
		}
		if completed {
			cancel()
			break
		}
		select {
		case <-ctx.Done():
			cancel()
			completed = false
			break
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	outcome := <-engineDone
	if !completed {
		if soakDuration > 0 {
			t.Errorf("the %s still-grid soak and post-soak refresh did not complete within %s for %d device(s): %v", soakDuration, probeTimeout, len(waiting), ctx.Err())
		} else {
			t.Errorf("%d subscribed still attempt(s) did not finish for %d device(s) within %s: %v", attemptsPerDevice, len(waiting), gridSweepProbeTimeout, ctx.Err())
		}
	}

	now := time.Now()
	report := gridSweepProbeReport{
		MeasuredAt:              now.UTC().Format(time.RFC3339),
		Host:                    gridProbeHostName(),
		HostLoad:                gridProbeHostLoad(),
		DeviceCount:             len(serials),
		SoakStarted:             soakStarted,
		SoakReached:             soakReached,
		ProbeCompleted:          completed,
		CadenceMillis:           settings.Cadence.Milliseconds(),
		ProbeIntervalMillis:     engineInterval.Milliseconds(),
		ProbeTimeoutMillis:      probeTimeout.Milliseconds(),
		SoakDurationMillis:      soakDuration.Milliseconds(),
		RequestedAttempts:       attemptsPerDevice,
		ActiveCadenceMillis:     settings.ActiveCadence.Milliseconds(),
		IdleCadenceMillis:       settings.IdleCadence.Milliseconds(),
		ConcurrentCaptures:      settings.ConcurrentCaptures,
		FreshnessCeilingMillis:  settings.FreshnessCeiling.Milliseconds(),
		MaxDevices:              settings.MaxDevices,
		CaptureTimeoutMillis:    media.DefaultFrameCaptureTimeout.Milliseconds(),
		LongestSweepMillis:      outcome.LongestSweep.Milliseconds(),
		Ticks:                   outcome.Ticks,
		Captures:                outcome.Captures,
		Frames:                  outcome.Frames,
		Failures:                outcome.Failures,
		Cancelled:               outcome.Cancelled,
		GoHeapPeakInuseBytes:    heapInuseMaxBytes,
		GoHeapPeakSysBytes:      heapSysMaxBytes,
		ProcessPeakRSSBytes:     processRSSMaxBytes,
		RSSMeasurementNote:      rssMeasurementNote,
		Truncated:               outcome.Truncated,
		Tiles:                   make([]gridSweepProbeTile, 0, len(serials)),
		AgeSampleIntervalMillis: gridSweepProbeSampleEvery.Milliseconds(),
	}
	captureTimings := timedCapturer.snapshot()
	report.CaptureP50Millis = gridSweepP95Millis(captureTimings, 50)
	report.CaptureP95Millis = gridSweepP95Millis(captureTimings, 95)
	for _, duration := range captureTimings {
		if duration > report.CaptureMaxMillis {
			report.CaptureMaxMillis = duration
		}
	}
	ages := make([]int64, 0, len(serials))
	peakAges := make([]int64, 0, len(serials))
	for _, serial := range serials {
		frame, ok := engine.Frame(serial)
		tile := gridSweepProbeTile{
			SerialMask:    gridProbeMaskSerial(serial),
			PeakAgeMillis: peakAgeBySerial[serial],
		}
		peakAges = append(peakAges, tile.PeakAgeMillis)
		if ok {
			tile.State = string(frame.State())
			tile.Freshness = frame.Freshness
			tile.Frames = frame.Frames
			tile.Failures = frame.Failures
			tile.RestartCount = frame.RestartCount
			tile.WorkerState = frame.WorkerState
			tile.FailureClass = string(frame.FailureClass)
			tile.ObservedCadenceMillis = frame.ObservedCadence.Milliseconds()
			tile.EffectiveFPS = frame.EffectiveFPS
			if !frame.CapturedAt.IsZero() {
				tile.AgeMillis = now.Sub(frame.CapturedAt).Milliseconds()
				ages = append(ages, tile.AgeMillis)
				if frame.Current() && time.Duration(tile.AgeMillis)*time.Millisecond <= settings.FreshnessCeiling {
					report.FreshTiles++
				}
			}
			if frame.Current() {
				report.CurrentTiles++
			}
		}
		report.Tiles = append(report.Tiles, tile)
	}
	report.P95TileAgeMillis = gridSweepP95Millis(ages, 95)
	report.PeakP95TileAgeMillis = gridSweepP95Millis(peakAges, 95)
	if len(peakAges) > 0 {
		report.PeakMaxTileAgeMillis = peakAges[0]
		for _, age := range peakAges[1:] {
			if age > report.PeakMaxTileAgeMillis {
				report.PeakMaxTileAgeMillis = age
			}
		}
	}
	if len(ages) > 0 {
		report.MaxTileAgeMillis = ages[0]
		for _, age := range ages[1:] {
			if age > report.MaxTileAgeMillis {
				report.MaxTileAgeMillis = age
			}
		}
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encoding sweep report: %v", err)
	}
	if err := os.WriteFile(outPath, encoded, 0o600); err != nil {
		t.Fatalf("writing sweep report to %s: %v", outPath, err)
	}
	if soakDuration > 0 {
		t.Logf("still-grid soak: duration=%s devices=%d concurrency=%d final p95 tile age=%dms, per-tile peak p95/max=%d/%dms fresh=%d captures=%d failures=%d recoveries=%d cancelled=%d report=%s",
			soakDuration, report.DeviceCount, report.ConcurrentCaptures, report.P95TileAgeMillis, report.PeakP95TileAgeMillis,
			report.PeakMaxTileAgeMillis, report.FreshTiles, report.Captures, report.Failures, gridSweepRecoveryCount(report.Tiles), report.Cancelled, outPath)
	} else {
		t.Logf("still sweep: devices=%d concurrency=%d attempts/device=%d interval=%dms sweep=%dms final p95 tile age=%dms, per-tile peak p95/max=%d/%dms fresh=%d captures=%d cancelled=%d report=%s",
			report.DeviceCount, report.ConcurrentCaptures, attemptsPerDevice, report.ProbeIntervalMillis, report.LongestSweepMillis,
			report.P95TileAgeMillis, report.PeakP95TileAgeMillis, report.PeakMaxTileAgeMillis, report.FreshTiles, report.Captures, report.Cancelled, outPath)
	}

	if !completed {
		return
	}
	if report.CurrentTiles != report.DeviceCount {
		t.Errorf("the still engine did not leave every tile current: current=%d/%d failures=%d", report.CurrentTiles, report.DeviceCount, report.Failures)
	}
	for _, tile := range report.Tiles {
		if tile.Failures > 0 && tile.RestartCount == 0 {
			t.Errorf("device %s had %d capture failure(s) but no later successful recovery", tile.SerialMask, tile.Failures)
		}
	}
	if report.FreshTiles != report.DeviceCount {
		t.Errorf("only %d of %d subscribed tiles are within the %s freshness ceiling", report.FreshTiles, report.DeviceCount, settings.FreshnessCeiling)
	}
	if report.P95TileAgeMillis > settings.FreshnessCeiling.Milliseconds() {
		t.Errorf("p95 tile age is %dms, over the %dms deployment freshness ceiling", report.P95TileAgeMillis, settings.FreshnessCeiling.Milliseconds())
	}
	if report.PeakP95TileAgeMillis > settings.FreshnessCeiling.Milliseconds() {
		t.Errorf("p95 per-tile peak age is %dms, over the %dms deployment freshness ceiling", report.PeakP95TileAgeMillis, settings.FreshnessCeiling.Milliseconds())
	}
	if report.PeakMaxTileAgeMillis > settings.FreshnessCeiling.Milliseconds() {
		t.Errorf("peak tile age observed during the run is %dms, over the %dms deployment freshness ceiling", report.PeakMaxTileAgeMillis, settings.FreshnessCeiling.Milliseconds())
	}
	if soakDuration > 0 && report.Captures == 0 {
		t.Error("the still-grid soak completed without capturing any frames")
	}
	minimumCaptures := len(serials) * attemptsPerDevice
	if outcome.Captures < minimumCaptures {
		t.Errorf("engine completed %d captures, want at least %d (%d per %d subscribed device(s))", outcome.Captures, minimumCaptures, attemptsPerDevice, len(serials))
	}
	if attemptsPerDevice == 1 && outcome.Captures > len(serials) {
		t.Errorf("one-sweep probe performed %d captures for %d subscribed devices; it should not start a second sweep", outcome.Captures, len(serials))
	}
}

type gridSweepProbeReport struct {
	MeasuredAt              string               `json:"MeasuredAt"`
	Host                    string               `json:"Host"`
	HostLoad                string               `json:"HostLoad"`
	DeviceCount             int                  `json:"DeviceCount"`
	SoakStarted             bool                 `json:"SoakStarted"`
	SoakReached             bool                 `json:"SoakReached"`
	ProbeCompleted          bool                 `json:"ProbeCompleted"`
	CadenceMillis           int64                `json:"CadenceMillis"`
	ProbeIntervalMillis     int64                `json:"ProbeIntervalMillis"`
	SoakDurationMillis      int64                `json:"SoakDurationMillis,omitempty"`
	ActiveCadenceMillis     int64                `json:"ActiveCadenceMillis"`
	IdleCadenceMillis       int64                `json:"IdleCadenceMillis"`
	ConcurrentCaptures      int                  `json:"ConcurrentCaptures"`
	RequestedAttempts       int                  `json:"RequestedAttemptsPerDevice"`
	ProbeTimeoutMillis      int64                `json:"ProbeTimeoutMillis"`
	FreshnessCeilingMillis  int64                `json:"FreshnessCeilingMillis"`
	MaxDevices              int                  `json:"MaxDevices"`
	CaptureTimeoutMillis    int64                `json:"CaptureTimeoutMillis"`
	CaptureP50Millis        int64                `json:"CaptureP50Millis"`
	CaptureP95Millis        int64                `json:"CaptureP95Millis"`
	CaptureMaxMillis        int64                `json:"CaptureMaxMillis"`
	LongestSweepMillis      int64                `json:"LongestSweepMillis"`
	Ticks                   int                  `json:"Ticks"`
	Captures                int                  `json:"Captures"`
	Frames                  int                  `json:"Frames"`
	Failures                int                  `json:"Failures"`
	Cancelled               int                  `json:"Cancelled"`
	GoHeapPeakInuseBytes    uint64               `json:"GoHeapPeakInuseBytes"`
	GoHeapPeakSysBytes      uint64               `json:"GoHeapPeakSysBytes"`
	ProcessPeakRSSBytes     uint64               `json:"ProcessPeakRSSBytes"`
	RSSMeasurementNote      string               `json:"RSSMeasurementNote"`
	Truncated               int                  `json:"Truncated"`
	CurrentTiles            int                  `json:"CurrentTiles"`
	FreshTiles              int                  `json:"FreshTiles"`
	P95TileAgeMillis        int64                `json:"P95TileAgeMillis"`
	MaxTileAgeMillis        int64                `json:"MaxTileAgeMillis"`
	AgeSampleIntervalMillis int64                `json:"AgeSampleIntervalMillis"`
	PeakP95TileAgeMillis    int64                `json:"PeakP95TileAgeMillis"`
	PeakMaxTileAgeMillis    int64                `json:"PeakMaxTileAgeMillis"`
	Tiles                   []gridSweepProbeTile `json:"Tiles"`
}

func parseGridSweepProbeDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 || duration > gridSweepProbeMaxDuration {
		return 0, fmt.Errorf("%s must be a positive duration no longer than %s", gridSweepProbeDurationEnv, gridSweepProbeMaxDuration)
	}
	return duration, nil
}

func parseGridSweepProbeDeviceLimit(raw string, available, maximum int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return available, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > available || limit > maximum {
		return 0, fmt.Errorf("%s must be a positive device count no greater than the %d attached devices or the deployment bound %d", gridSweepProbeDeviceLimitEnv, available, maximum)
	}
	return limit, nil
}

func gridSweepRecoveryCount(tiles []gridSweepProbeTile) int {
	recoveries := 0
	for _, tile := range tiles {
		recoveries += tile.RestartCount
	}
	return recoveries
}

// gridSweepTimedCapturer measures the actual adapter call the still engine
// depends on. Keeping the sample list fixed-capacity makes this opt-in diagnostic
// bounded even if the probe runs for its full timeout.
type gridSweepTimedCapturer struct {
	inner     media.FrameCapturer
	mu        sync.Mutex
	durations []int64
}

func (c *gridSweepTimedCapturer) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	started := time.Now()
	result, err := c.inner.Screenshot(ctx, serial)
	duration := time.Since(started).Milliseconds()
	c.mu.Lock()
	if len(c.durations) < cap(c.durations) {
		c.durations = append(c.durations, duration)
	}
	c.mu.Unlock()
	return result, err
}

func (c *gridSweepTimedCapturer) snapshot() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.durations...)
}

func gridSweepProcessAndADBBytes(pid int) (uint64, error) {
	psPath, err := exec.LookPath("ps")
	if err != nil {
		return 0, err
	}
	output, err := exec.Command(psPath, "-axo", "pid=,rss=,comm=").Output()
	if err != nil {
		return 0, err
	}
	var totalKB uint64
	var sawTestProcess bool
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		processID, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil {
			continue
		}
		kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		isProbe := processID == pid
		isADB := filepath.Base(fields[2]) == "adb"
		if isProbe || isADB {
			totalKB += kilobytes
		}
		if isProbe {
			sawTestProcess = true
		}
	}
	if !sawTestProcess {
		return 0, fmt.Errorf("process %d was not visible in ps output", pid)
	}
	return totalKB * 1024, nil
}

type gridSweepProbeTile struct {
	SerialMask            string  `json:"SerialMask"`
	PeakAgeMillis         int64   `json:"PeakAgeMillis"`
	State                 string  `json:"State,omitempty"`
	Freshness             string  `json:"Freshness,omitempty"`
	AgeMillis             int64   `json:"AgeMillis,omitempty"`
	Frames                int     `json:"Frames"`
	Failures              int     `json:"Failures"`
	RestartCount          int     `json:"RestartCount"`
	WorkerState           string  `json:"WorkerState,omitempty"`
	FailureClass          string  `json:"FailureClass,omitempty"`
	ObservedCadenceMillis int64   `json:"ObservedCadenceMillis,omitempty"`
	EffectiveFPS          float64 `json:"EffectiveFPS,omitempty"`
}

func gridSweepP95Millis(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if percentile <= 0 || percentile > 100 {
		percentile = 95
	}
	rank := (len(sorted)*percentile + 99) / 100
	return sorted[rank-1]
}
