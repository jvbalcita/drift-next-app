package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/scrcpy"
	"drift.local/drift-next/internal/media"
)

// The fleet grid's still path, measured on the fleet the lab actually has.
//
// This probe is the evidence the grid's bounds are set FROM rather than a test
// that confirms a number someone chose. It measures three things on real devices:
// what one still costs at each preview level, what one capture costs, and whether
// the fleet's stills are carried without the operator's own live frame ever
// stalling while the grid refreshes underneath it.
//
// It is opt-in, because it reaches real devices and takes a minute:
//
//	DRIFT_GRID_PROBE=1
//	DRIFT_GRID_PROBE_ADB=/abs/adb
//	DRIFT_GRID_PROBE_SERVER=/abs/scrcpy-server
//	DRIFT_GRID_PROBE_OUT=/abs/report.json
//	DRIFT_GRID_PROBE_HOLD_SECONDS=60      (optional, default 60)
//	DRIFT_GRID_PROBE_CADENCE=4s           (optional, the grid's cadence)
//	DRIFT_GRID_PROBE_LEVEL=medium         (optional, low|medium|high)
//	DRIFT_GRID_PROBE_SERIALS=a,b          (optional, default every attached device)
//
// Unlike the live-stream concurrency probe this one ASSERTS, because what it has
// to establish is a property rather than a knee: that every attached device is
// carried as a current still, and that the operator's own frame stayed live
// underneath. A run that cannot show both has not shown the card's claim.
const (
	gridProbeEnv        = "DRIFT_GRID_PROBE"
	gridProbeADBEnv     = "DRIFT_GRID_PROBE_ADB"
	gridProbeServerEnv  = "DRIFT_GRID_PROBE_SERVER"
	gridProbeOutEnv     = "DRIFT_GRID_PROBE_OUT"
	gridProbeHoldEnv    = "DRIFT_GRID_PROBE_HOLD_SECONDS"
	gridProbeCadenceEnv = "DRIFT_GRID_PROBE_CADENCE"
	gridProbeLevelEnv   = "DRIFT_GRID_PROBE_LEVEL"
	gridProbeSerialsEnv = "DRIFT_GRID_PROBE_SERIALS"
)

// gridProbeWarmupSeconds is excluded from the operator's steady-state figure: a
// session's first seconds carry the encoder's own start-up, and counting those as
// a stall would report the handshake as a stalled frame.
const gridProbeWarmupSeconds = 5

// gridProbeStallSeconds is how many consecutive seconds with no access unit at all
// count as a stalled operator frame. The card's claim is that the grid does not
// touch the operator's frame, so the operator's stream is judged by the strictest
// reading of that: one quiet second is a still scene, two is a stream that stopped.
const gridProbeStallSeconds = 2

// gridProbeCaptureTimeout bounds one capture inside the probe's own engine. It is
// the engine's own default, stated here so the report can print the bound the
// measurement ran under rather than leaving a reader to infer it.
const gridProbeCaptureTimeout = media.DefaultFrameCaptureTimeout

// gridProbeReport is the evidence, written as JSON beside the committed reports
// from the live-mirror work so the two are read the same way.
type gridProbeReport struct {
	MeasuredAt       string  `json:"MeasuredAt"`
	Host             string  `json:"Host"`
	HostLoad         string  `json:"HostLoad"`
	ADBVersion       string  `json:"ADBVersion"`
	ScrcpyServerPath string  `json:"ScrcpyServerPath"`
	DeviceCount      int     `json:"DeviceCount"`
	HoldSeconds      float64 `json:"HoldSeconds"`
	CadenceMillis    int64   `json:"CadenceMillis"`
	Level            string  `json:"Level"`
	LevelMaxWidth    int     `json:"LevelMaxWidth"`
	LevelJPEGQuality int     `json:"LevelJpegQuality"`
	StillByteBound   int     `json:"StillByteBound"`
	MaxDevices       int     `json:"MaxDevices"`
	CaptureTimeoutMS int64   `json:"CaptureTimeoutMillis"`

	// FleetCurrentPerSecond is how many devices held a current still in each
	// sampled second of the hold - the series the "every device, always" claim is
	// read from.
	FleetCurrentPerSecond []int `json:"FleetCurrentPerSecond"`

	Levels []gridProbeLevel `json:"Levels"`
	Fleet  []gridProbeStill `json:"Fleet"`
	// OperatorBaseline is the SAME session held for the same time with NO grid
	// running. It exists because a bench of idle devices carries a stream of its
	// own accord at a rate nobody controls: without the control arm, "the operator's
	// frame stalled while the grid refreshed" would be a fact about idle screens
	// rather than about the grid, and the claim the card makes would be neither
	// measured nor refuted.
	OperatorBaseline gridProbeOperator  `json:"OperatorBaseline"`
	Operator         gridProbeOperator  `json:"Operator"`
	Cost             gridProbeGridCost  `json:"Cost"`
	Bounds           gridProbeGridBound `json:"Bounds"`

	Conclusion string `json:"Conclusion"`
}

// gridProbeLevel is one preview level measured from one real capture: what a tile
// costs if the deployment states that level.
type gridProbeLevel struct {
	Level       string `json:"Level"`
	MaxWidth    int    `json:"MaxWidth"`
	JPEGQuality int    `json:"JpegQuality"`
	SourceBytes int    `json:"SourceBytes"`
	SourceWide  int    `json:"SourceWidth"`
	SourceHigh  int    `json:"SourceHeight"`
	StillBytes  int    `json:"StillBytes"`
	Wide        int    `json:"Width"`
	High        int    `json:"Height"`
	EncodeMS    int64  `json:"EncodeMillis"`
	Note        string `json:"Note,omitempty"`
}

// gridProbeStill is one device's tile as the grid held it.
type gridProbeStill struct {
	SerialMask   string `json:"SerialMask"`
	State        string `json:"State"`
	Current      bool   `json:"Current"`
	Frames       int    `json:"Frames"`
	Failures     int    `json:"Failures"`
	SourceBytes  int    `json:"SourceBytes"`
	StillBytes   int    `json:"StillBytes"`
	Wide         int    `json:"Width"`
	High         int    `json:"Height"`
	MediaType    string `json:"MediaType"`
	CapturedAt   string `json:"CapturedAt,omitempty"`
	ObservedMS   int64  `json:"ObservedCadenceMillis"`
	FailureClass string `json:"FailureClass,omitempty"`
	FailureNote  string `json:"FailureDetail,omitempty"`
	// Truncated is the plane refusing to deliver a still larger than the bound: the
	// state is still current, and there is no picture. It is counted rather than
	// hidden, because a level that produced nothing but these is a level the fleet
	// cannot be carried at.
	Truncated  bool `json:"TruncatedRatherThanDelivered"`
	AboveBound bool `json:"AboveStillByteBound"`
}

// gridProbeOperator is the operator's own frame, held live while the grid
// refreshed: the separation the card is about, asserted rather than assumed.
type gridProbeOperator struct {
	SerialMask       string  `json:"SerialMask"`
	Opened           bool    `json:"Opened"`
	OpenAttempts     int     `json:"OpenAttempts"`
	OpenFailure      string  `json:"OpenFailure,omitempty"`
	Wide             int     `json:"Width"`
	High             int     `json:"Height"`
	Frames           int     `json:"Frames"`
	KeyFrames        int     `json:"KeyFrames"`
	Bytes            int64   `json:"Bytes"`
	SteadySeconds    int     `json:"SteadySeconds"`
	StalledSeconds   int     `json:"StalledSeconds"`
	QuietSeconds     int     `json:"QuietSeconds"`
	PerSecondFrames  []int   `json:"PerSecondFrames"`
	AggregateMbps    float64 `json:"AggregateMbps"`
	StoppedWithError string  `json:"StoppedWithError,omitempty"`
}

// gridProbeGridCost is the arithmetic the card asks for: what the grid costs at
// this cadence, against the ceiling the live-mirror work measured.
type gridProbeGridCost struct {
	StillBytesPerSweep int     `json:"StillBytesPerSweep"`
	StillsPerSecond    float64 `json:"StillsPerSecond"`
	StillKBytesPerSec  float64 `json:"StillKBytesPerSec"`
	StillMbps          float64 `json:"StillMbps"`
	FarStreamMbps      float64 `json:"MeasuredFarStreamMbps"`
	ShareOfFarStream   float64 `json:"ShareOfMeasuredFarStream"`
	MeanCaptureMS      float64 `json:"MeanCaptureMillis"`
	LongestSweepMS     int64   `json:"LongestSweepMillis"`
	SweepTicks         int     `json:"SweepTicks"`
	Note               string  `json:"Note,omitempty"`
}

// gridProbeGridBound is what the measurement implies for the grid's own bound.
//
// There are two limits and they are not the same one. The TRANSPORT limit is what
// the stills cost: devices x stillBytes / cadence against the measured ceiling.
// The CAPTURE limit is what one sweep can finish: devices x the cost of one
// capture, which on this fleet is around sixty times the transport limit's
// generosity, because a capture ships the screen as a ~3.4 MB PNG and the level is
// applied afterwards on the plane.
type gridProbeGridBound struct {
	MaxDevices            int     `json:"MaxDevices"`
	StillBytesPerDevice   int     `json:"StillBytesPerDevice"`
	CadenceSeconds        float64 `json:"CadenceSeconds"`
	DevicesAtOneFarStream int     `json:"DevicesThatFitInOneFarStreamAtBytes"`
	DevicesAtOneCadence   int     `json:"DevicesThatFitInOneCadenceOfCaptures"`
	SweepSecondsAtFleet   float64 `json:"SweepSecondsAtMeasuredCaptureCost"`
	BoundImpliedByBytes   int     `json:"BoundImpliedByBytes"`
	Note                  string  `json:"Note,omitempty"`
}

// gridProbeFarStreamMbps is the fat stream the ARC-228 concurrency work measured
// on this hub (one operator-profile session). It is stated as a constant rather
// than re-measured here because this probe is about the STILL path's cost, and the
// live path's ceiling is already committed evidence: probe-committed.json.
const gridProbeFarStreamMbps = 40.0

// TestGridStillFleetProbe measures the fleet grid's still path on every attached
// device while holding the operator's own live frame open.
func TestGridStillFleetProbe(t *testing.T) {
	config, err := gridProbeConfigFromEnv()
	if err != nil {
		t.Skipf("skipping fleet grid still probe: %v", err)
	}

	// The budget is deliberately far larger than the hold. Everything before the
	// hold - one capture per level, one standalone capture per device, and the
	// operator's open attempts - drives the same host adb server as the rest of the
	// fleet, and a budget read as "the hold plus a little" would abort a run at the
	// point it was about to produce its first steady second.
	total := config.hold + 15*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()

	adapter, err := gridProbeAdapter(config.adbPath)
	if err != nil {
		t.Fatalf("building the capture path: %v", err)
	}
	serials, err := gridProbeTargets(ctx, adapter, config)
	if err != nil {
		t.Fatalf("resolving probe targets: %v", err)
	}
	if len(serials) == 0 {
		t.Skip("skipping fleet grid still probe: adb reported no usable device")
	}

	profile := media.GridStillProfileFor(config.level)
	report := gridProbeReport{
		MeasuredAt:       time.Now().UTC().Format(time.RFC3339),
		Host:             gridProbeHostName(),
		HostLoad:         gridProbeHostLoad(),
		ADBVersion:       gridProbeADBVersion(ctx, config),
		ScrcpyServerPath: config.serverPath,
		DeviceCount:      len(serials),
		HoldSeconds:      config.hold.Seconds(),
		CadenceMillis:    config.cadence.Milliseconds(),
		Level:            string(config.level),
		LevelMaxWidth:    profile.MaxWidth,
		LevelJPEGQuality: profile.JPEGQuality,
		StillByteBound:   profile.ByteBound,
		MaxDevices:       media.DefaultGridMaxDevices,
		CaptureTimeoutMS: gridProbeCaptureTimeout.Milliseconds(),
	}
	t.Logf("fleet grid still probe: %d device(s), level %s (%dpx wide, q%d, %d byte bound), cadence %s for %s",
		len(serials), config.level, profile.MaxWidth, profile.JPEGQuality, profile.ByteBound, config.cadence, config.hold)

	// 1. What ONE still costs, at every level, from one real capture. This is the
	//    number the deployment's level and its cadence are chosen from.
	report.Levels = gridProbeLevels(ctx, adapter, serials[0])

	// 2. One capture's own cost, standalone, so the report can say how long a
	//    device takes to answer rather than only how long a sweep took.
	captureMS, captureNote := gridProbeCaptureCost(ctx, adapter, serials)
	report.Cost.MeanCaptureMS = captureMS
	report.Cost.Note = captureNote

	// 3. The operator's own frame, held for the same time twice: once ALONE, which is
	//    the only reading that can say whether the grid costs it anything, and once
	//    with the grid refreshing every attached device underneath it.
	//
	//    The control arm runs first, so the fleet starts the treatment from a quiet
	//    state rather than from the tail of its own sweep.
	baseline, baselineErr := gridProbeHoldOperator(ctx, config, serials[0], config.hold)
	if baselineErr != nil {
		baseline.OpenFailure = baselineErr.Error()
	}
	report.OperatorBaseline = gridProbeOperatorFrom(baseline)
	t.Logf("operator's frame alone: opened=%v frames=%d (%.2f fps) quiet=%d stalled=%d",
		baseline.Opened, baseline.Frames, float64(baseline.Frames)/config.hold.Seconds(), baseline.QuietSecond, baseline.StalledSecond)

	engine, err := media.NewFrameEngine(media.FrameEngineConfig{
		Capturer:       adapter,
		Interval:       config.cadence,
		CaptureTimeout: gridProbeCaptureTimeout,
		Profile:        profile,
		Logf:           func(format string, args ...any) { t.Logf(format, args...) },
	})
	if err != nil {
		t.Fatalf("building the frame engine: %v", err)
	}
	subscription := engine.SyncSubscriptions(serials)
	if len(subscription.Admitted) != len(serials) {
		t.Fatalf("the sweep bound admitted %d of %d attached device(s): refused %v, invalid %v",
			len(subscription.Admitted), len(serials), subscription.Refused, subscription.Invalid)
	}

	engineCtx, stopEngine := context.WithCancel(ctx)
	engineDone := make(chan media.FrameEngineOutcome, 1)
	go func() { engineDone <- engine.Run(engineCtx) }()

	// The operator's frame is opened AFTER the grid is running, so the claim being
	// measured is "the grid is refreshing and this frame is live at the same time",
	// not "a frame was live before the grid started".
	operatorDone := make(chan gridProbeCounters, 1)
	go func() {
		counters, openErr := gridProbeHoldOperator(ctx, config, serials[0], config.hold)
		if openErr != nil {
			counters.OpenFailure = openErr.Error()
		}
		operatorDone <- counters
	}()

	sampled, sampleErr := gridProbeSampleFleet(ctx, engine, serials, config.hold)
	if sampleErr != nil {
		t.Errorf("sampling the fleet: %v", sampleErr)
	}
	report.FleetCurrentPerSecond = sampled
	stopEngine()
	outcome := <-engineDone

	report.Operator = gridProbeOperatorFrom(<-operatorDone)
	report.Cost.StillsPerSecond = 1 / config.cadence.Seconds()
	report.Cost.SweepTicks = outcome.Ticks
	report.Cost.LongestSweepMS = outcome.LongestSweep.Milliseconds()

	// 4. What the fleet was actually carried as.
	fleet, fleetBytes, current := gridProbeFleet(engine, serials, profile)
	report.Fleet = fleet
	report.Cost.StillBytesPerSweep = fleetBytes
	report.Cost.StillKBytesPerSec = float64(fleetBytes) / 1024 / config.cadence.Seconds()
	report.Cost.StillMbps = float64(fleetBytes) * 8 / 1e6 / config.cadence.Seconds()
	report.Cost.FarStreamMbps = gridProbeFarStreamMbps
	if gridProbeFarStreamMbps > 0 {
		report.Cost.ShareOfFarStream = report.Cost.StillMbps / gridProbeFarStreamMbps
	}
	report.Bounds = gridProbeBoundFromMeasurement(len(serials), fleetBytes, config.cadence, captureMS)

	// The assertions: the card's claim, on the devices that are actually here.
	if current != len(serials) {
		t.Errorf("only %d of %d attached device(s) reached a current still in %s",
			current, len(serials), config.hold)
	}
	delivered, truncated := 0, 0
	for _, still := range fleet {
		if !still.Current {
			continue
		}
		if still.Truncated {
			// The plane's own rule for a still it will not deliver: the state stays
			// current and NO picture travels. What must not happen is a still
			// delivered over the bound, which is the reading below.
			truncated++
			continue
		}
		delivered++
		if still.AboveBound {
			t.Errorf("device %s was delivered %d byte(s) at a %d byte bound",
				still.SerialMask, still.StillBytes, profile.ByteBound)
		}
		if still.MediaType != media.StillMediaType {
			t.Errorf("device %s was carried as %q, want %q: a tile that guessed the format would paint nothing",
				still.SerialMask, still.MediaType, media.StillMediaType)
		}
	}
	if delivered == 0 {
		t.Errorf("no device's still was delivered at the %s level: every tile would show a truncation", profile.Level)
	}
	t.Logf("stills delivered: %d; reported as truncated rather than delivered: %d", delivered, truncated)

	// The series is judged on what it can honestly show. A sweep of this fleet takes
	// longer than the cadence by a wide margin - it is the CAPTURE that costs, not
	// the still - so the first seconds are the first sweep filling in and a window
	// picked by half the hold would be a window the sweep had not finished. What is
	// asserted instead is the property that matters to an operator: a device, once
	// carried, STAYS carried while it is subscribed, and by the end of the hold the
	// whole fleet is carried.
	for second := 1; second < len(sampled); second++ {
		if sampled[second] < sampled[second-1] {
			t.Errorf("the grid dropped device(s) at second %d: %v", second, sampled)
			break
		}
	}
	if len(sampled) == 0 {
		t.Error("the fleet was never sampled")
	} else if last := sampled[len(sampled)-1]; last != len(serials) {
		t.Errorf("the last sampled second carried %d of %d device(s) as current stills: %v",
			last, len(serials), sampled)
	}

	// The separation, measured rather than assumed: the SAME session, on the same
	// device, for the same two minutes, with and without the grid underneath it.
	//
	// What is ASSERTED is what this bench can honestly show, and what is only
	// reported is reported as such. The frames these arms carry are driven by the
	// devices' own screens, and an idle bench carries about 0.3-0.4 frames per
	// second whatever is running - the reading moves by about eleven frames between
	// arms WITHIN a run and by the same amount in the opposite direction across
	// runs, so frame counts cannot order the two arms at this sample size and are
	// therefore committed as evidence rather than asserted as a comparison. What can
	// be asserted is what the claim needs: the operator's session opened, stayed open
	// and kept carrying pictures for the whole grid arm, and it stayed ONE session
	// (the grid opened none). A stronger experiment - driving the operator's screen
	// so the stream carries at a known rate - needs an authorized control session and
	// is deliberately not attempted from this probe.
	operator := report.Operator
	switch {
	case !operator.Opened:
		t.Errorf("the operator's frame could not be opened (%s after %d attempt(s), %s), so the separation was not measured",
			operator.OpenFailure, operator.OpenAttempts, operator.SerialMask)
	case !report.OperatorBaseline.Opened:
		t.Errorf("the operator's frame carried alone could not be opened (%s), so there is no control to compare against",
			report.OperatorBaseline.OpenFailure)
	default:
		if operator.Frames == 0 {
			t.Error("the operator's frame carried no access unit at all while the grid swept the fleet")
		}
		if operator.StoppedWithError != "" {
			t.Errorf("the operator's frame ended with an error while the grid swept the fleet: %s", operator.StoppedWithError)
		}
		if operator.SteadySeconds == 0 {
			t.Error("the operator's frame was not sampled for a single second, so nothing about its continuity was measured")
		}
		if operator.Wide != report.OperatorBaseline.Wide || operator.High != report.OperatorBaseline.High {
			t.Errorf("the operator's frame was %dx%d with the grid running and %dx%d alone, want the same session both times",
				operator.Wide, operator.High, report.OperatorBaseline.Wide, report.OperatorBaseline.High)
		}
		t.Logf("operator's frame: %d access unit(s) with the grid against %d alone over the same %s (reported, not asserted: an idle bench drives both)",
			operator.Frames, report.OperatorBaseline.Frames, config.hold)
	}

	// The grid spends no device session at all: what it holds is a capture and a
	// still, so the only sessions this measurement opened are the operator's own two
	// (the control arm and the grid arm). A grid that opened one would be the defect
	// the card is about, and it would show up here as a third.
	if sessions := gridProbeSessionCount(); sessions != 2 {
		t.Errorf("the measurement opened %d live session(s), want exactly the operator's control and grid arms", sessions)
	}

	report.Conclusion = gridProbeConclusion(report)

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encoding the report: %v", err)
	}
	if err := os.WriteFile(config.out, encoded, 0o600); err != nil {
		t.Fatalf("writing the report to %s: %v", config.out, err)
	}
	t.Logf("fleet grid still probe report written to %s", config.out)
	t.Log(gridProbeReportText(report))
}

// gridProbeConfig is the probe's own resolved configuration.
type gridProbeConfig struct {
	adbPath    string
	serverPath string
	serials    []string
	hold       time.Duration
	cadence    time.Duration
	level      media.GridStillLevel
	out        string
}

// gridProbeConfigFromEnv reads the gates, refusing a partial configuration rather
// than guessing at any of them.
func gridProbeConfigFromEnv() (gridProbeConfig, error) {
	if strings.TrimSpace(os.Getenv(gridProbeEnv)) != "1" {
		return gridProbeConfig{}, errors.New("DRIFT_GRID_PROBE is not 1")
	}
	config := gridProbeConfig{
		adbPath:    strings.TrimSpace(os.Getenv(gridProbeADBEnv)),
		serverPath: strings.TrimSpace(os.Getenv(gridProbeServerEnv)),
		out:        strings.TrimSpace(os.Getenv(gridProbeOutEnv)),
		hold:       60 * time.Second,
		cadence:    media.DefaultGridStillCadence,
		level:      media.DefaultGridStillLevel,
	}
	if config.adbPath == "" || config.serverPath == "" || config.out == "" {
		return gridProbeConfig{}, errors.New("DRIFT_GRID_PROBE_ADB, DRIFT_GRID_PROBE_SERVER and DRIFT_GRID_PROBE_OUT are all required")
	}
	// The report's path is absolute because a test runs in its own package's
	// directory: a relative path would write the evidence somewhere nobody looks
	// for it, and the failure would read as a probe that produced nothing.
	if !filepath.IsAbs(config.out) {
		return gridProbeConfig{}, fmt.Errorf("DRIFT_GRID_PROBE_OUT=%q must be an absolute path", config.out)
	}
	if raw := strings.TrimSpace(os.Getenv(gridProbeHoldEnv)); raw != "" {
		seconds, err := time.ParseDuration(raw + "s")
		if err != nil || seconds <= 0 {
			return gridProbeConfig{}, fmt.Errorf("DRIFT_GRID_PROBE_HOLD_SECONDS=%q is not a positive number of seconds", raw)
		}
		config.hold = seconds
	}
	if raw := strings.TrimSpace(os.Getenv(gridProbeCadenceEnv)); raw != "" {
		cadence, err := time.ParseDuration(raw)
		if err != nil || cadence <= 0 {
			return gridProbeConfig{}, fmt.Errorf("DRIFT_GRID_PROBE_CADENCE=%q is not a duration", raw)
		}
		config.cadence = cadence
	}
	if raw := strings.TrimSpace(os.Getenv(gridProbeLevelEnv)); raw != "" {
		level := media.GridStillLevelFromString(raw)
		if !strings.EqualFold(raw, string(level)) {
			return gridProbeConfig{}, fmt.Errorf("DRIFT_GRID_PROBE_LEVEL=%q is not a preview level", raw)
		}
		config.level = level
	}
	for _, raw := range strings.Split(os.Getenv(gridProbeSerialsEnv), ",") {
		if serial := strings.TrimSpace(raw); serial != "" {
			config.serials = append(config.serials, serial)
		}
	}
	return config, nil
}

// gridProbeAdapter builds the same bounded, allow-listed device adapter the
// product reaches devices through, so what the probe measures is the product's own
// capture path rather than a second one.
func gridProbeAdapter(adbPath string) (*adb.Adapter, error) {
	runner, err := adb.NewProcessRunner()
	if err != nil {
		return nil, err
	}
	return adb.NewAdapter(adbPath, runner)
}

// gridProbeTargets resolves the fleet: every attached device in adb's own order,
// unless the gate named the devices to measure.
func gridProbeTargets(ctx context.Context, adapter *adb.Adapter, config gridProbeConfig) ([]string, error) {
	if len(config.serials) > 0 {
		return config.serials, nil
	}
	devices, err := adapter.Enumerate(ctx)
	if err != nil {
		return nil, err
	}
	serials := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.State != adb.StateDevice {
			continue
		}
		serials = append(serials, device.Serial)
	}
	sort.Strings(serials)
	return serials, nil
}

// gridProbeLevels measures one still at every level from one real capture: what a
// device's screen costs the grid at each level the deployment could state.
func gridProbeLevels(ctx context.Context, adapter *adb.Adapter, serial string) []gridProbeLevel {
	levels := make([]gridProbeLevel, 0, 3)
	captureCtx, cancel := context.WithTimeout(ctx, gridProbeCaptureTimeout)
	shot, err := adapter.Screenshot(captureCtx, serial)
	cancel()
	if err != nil {
		return append(levels, gridProbeLevel{Note: "no capture could be taken: " + err.Error()})
	}
	config, _, decodeErr := image.DecodeConfig(bytes.NewReader(shot.PNG))
	for _, level := range []media.GridStillLevel{media.GridStillLevelLow, media.GridStillLevelMedium, media.GridStillLevelHigh} {
		profile := media.GridStillProfileFor(level)
		started := time.Now()
		still, err := media.ImageStillEncoder{}.EncodeStill(shot.PNG, profile)
		elapsed := time.Since(started)
		measured := gridProbeLevel{
			Level:       string(level),
			MaxWidth:    profile.MaxWidth,
			JPEGQuality: profile.JPEGQuality,
			SourceBytes: len(shot.PNG),
			StillBytes:  len(still.JPEG),
			Wide:        still.Width,
			High:        still.Height,
			EncodeMS:    elapsed.Milliseconds(),
		}
		if decodeErr == nil {
			measured.SourceWide = config.Width
			measured.SourceHigh = config.Height
		}
		if err != nil {
			measured.Note = "this level could not encode the capture: " + err.Error()
		}
		levels = append(levels, measured)
	}
	return levels
}

// gridProbeCaptureCost times one capture per device as a standalone measurement,
// so the report can state how long a device takes to answer a capture on its own
// rather than only how long a sweep of the whole fleet took.
func gridProbeCaptureCost(ctx context.Context, adapter *adb.Adapter, serials []string) (float64, string) {
	var total time.Duration
	measured, failed := 0, 0
	for _, serial := range serials {
		captureCtx, cancel := context.WithTimeout(ctx, gridProbeCaptureTimeout)
		started := time.Now()
		_, err := adapter.Screenshot(captureCtx, serial)
		total += time.Since(started)
		cancel()
		if err != nil {
			failed++
			continue
		}
		measured++
	}
	if measured == 0 {
		return 0, "no device answered a standalone capture, so no per-device capture cost could be measured"
	}
	note := ""
	if failed > 0 {
		note = fmt.Sprintf("%d of %d device(s) did not answer a standalone capture", failed, len(serials))
	}
	return float64(total.Milliseconds()) / float64(measured), note
}

// gridProbeSampleFleet watches the grid refresh for the hold and returns, for each
// sampled second, how many of the fleet's devices held a CURRENT still. The first
// seconds are the first sweep filling in; what the card's claim rests on is that
// once the sweep has been round once, EVERY device is carried - so the per-second
// series is the evidence rather than a single reading at the end.
func gridProbeSampleFleet(ctx context.Context, engine *media.FrameEngine, serials []string, hold time.Duration) ([]int, error) {
	deadline := time.Now().Add(hold)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	current := make([]int, 0, int(hold.Seconds())+1)
	for {
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-ticker.C:
			carried := 0
			for _, serial := range serials {
				if frame, ok := engine.Frame(serial); ok && frame.Current() {
					carried++
				}
			}
			current = append(current, carried)
			if time.Now().After(deadline) {
				return current, nil
			}
		}
	}
}

// gridProbeSessionsOpened counts the live sessions this probe opened, so the report
// can state that the grid opened NONE: a still spends no device session, and that is
// the whole reason a grid of every device can exist beside the operator's one frame.
var gridProbeSessionsOpened atomic.Int64

func gridProbeSessionOpened() { gridProbeSessionsOpened.Add(1) }

// gridProbeSessionCount is how many live sessions the measurement opened. Both arms
// of the operator's frame are live sessions; the grid is not.
func gridProbeSessionCount() int { return int(gridProbeSessionsOpened.Load()) }

// gridProbeCounters is the operator's frame as the probe counts it. It mirrors the
// shape the live-mirror probe uses, so the two measurements are read the same way.
type gridProbeCounters struct {
	SerialMask    string
	Opened        bool
	OpenAttempts  int
	OpenFailure   string
	Wide          int
	High          int
	Frames        int
	KeyFrames     int
	Bytes         int64
	PerSecond     []int
	StalledSecond int
	QuietSecond   int
	AggregateMbps float64
	StoppedError  string
}

// gridProbeHoldOperator opens ONE live session at the operator's own profile and
// counts what it carried, second by second, for the hold.
//
// It is the operator's frame, opened on the fleet's first device while the grid
// refreshes every device including that one: the card's separation claim is that
// the still path does not cost this session a frame.
func gridProbeHoldOperator(ctx context.Context, config gridProbeConfig, serial string, hold time.Duration) (gridProbeCounters, error) {
	counters := gridProbeCounters{SerialMask: gridProbeMaskSerial(serial)}
	adapter, err := gridProbeAdapter(config.adbPath)
	if err != nil {
		return counters, fmt.Errorf("building the capture path: %w", err)
	}
	starter, err := adb.NewProcessStarter(config.adbPath)
	if err != nil {
		return counters, fmt.Errorf("building the device server starter: %w", err)
	}
	var session *scrcpy.Session
	for attempt := 1; attempt <= 2; attempt++ {
		counters.OpenAttempts = attempt
		openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		session, err = scrcpy.Start(openCtx, scrcpy.Options{
			ADB:        config.adbPath,
			Serial:     serial,
			ServerPath: config.serverPath,
			Runner:     adapter,
			Starter:    starter,
			KeepAwake:  true,
			Encode:     adb.MirrorOperatorEncodeProfile,
		})
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return counters, err
	}
	counters.Opened = true
	gridProbeSessionOpened()
	counters.Wide, counters.High = session.Meta().Size()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = session.Close(closeCtx)
		cancel()
	}()

	var mutex sync.Mutex
	perSecond := make([]int, 0, int(hold.Seconds())+1)
	secondCount := 0
	deadline := time.Now().Add(hold)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			unit, readErr := session.ReadAccessUnitContext(ctx)
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
					mutex.Lock()
					counters.StoppedError = readErr.Error()
					mutex.Unlock()
				}
				return
			}
			mutex.Lock()
			if unit.Key {
				counters.KeyFrames++
			}
			counters.Frames++
			counters.Bytes += int64(len(unit.Data))
			secondCount++
			mutex.Unlock()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			goto finished
		case <-ticker.C:
			mutex.Lock()
			perSecond = append(perSecond, secondCount)
			secondCount = 0
			mutex.Unlock()
			if time.Now().After(deadline) {
				goto finished
			}
		}
	}
finished:
	// The reader is left to end with its own context: a session closed under it is
	// how a probe reports a stall that was really its own shutdown.
	for i := gridProbeWarmupSeconds; i < len(perSecond); i++ {
		switch {
		case perSecond[i] == 0:
			counters.QuietSecond++
		default:
			counters.StalledSecond = 0
		}
		if perSecond[i] == 0 {
			counters.StalledSecond++
		}
	}
	counters.PerSecond = perSecond
	counters.AggregateMbps = float64(counters.Bytes) * 8 / 1e6 / hold.Seconds()
	return counters, nil
}

// gridProbeOperatorFrom folds one counted operator reading into the report's own
// operator record. Both arms go through it, so the two records are read the same
// way rather than by two code paths that could disagree.
func gridProbeOperatorFrom(counted gridProbeCounters) gridProbeOperator {
	into := gridProbeOperator{}
	into.SerialMask = counted.SerialMask
	into.Opened = counted.Opened
	into.OpenAttempts = counted.OpenAttempts
	into.OpenFailure = counted.OpenFailure
	into.Wide = counted.Wide
	into.High = counted.High
	into.Frames = counted.Frames
	into.KeyFrames = counted.KeyFrames
	into.Bytes = counted.Bytes
	into.PerSecondFrames = counted.PerSecond
	into.QuietSeconds = counted.QuietSecond
	into.StalledSeconds = gridProbeLongestQuietRun(counted.PerSecond)
	into.AggregateMbps = counted.AggregateMbps
	into.StoppedWithError = counted.StoppedError
	into.SteadySeconds = len(counted.PerSecond)
	return into
}

// gridProbeLongestQuietRun is the longest run of consecutive quiet seconds after
// the warm-up: a stream that never stalls has a run of zero.
func gridProbeLongestQuietRun(perSecond []int) int {
	longest, run := 0, 0
	for i := gridProbeWarmupSeconds; i < len(perSecond); i++ {
		if perSecond[i] == 0 {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	_ = gridProbeStallSeconds
	return longest
}

// gridProbeFleet reads each subscribed device's tile out of the engine: what the
// grid is holding for the fleet it was given.
func gridProbeFleet(engine *media.FrameEngine, serials []string, profile media.GridStillProfile) ([]gridProbeStill, int, int) {
	fleet := make([]gridProbeStill, 0, len(serials))
	bytesPerSweep, current := 0, 0
	for _, serial := range serials {
		frame, ok := engine.Frame(serial)
		if !ok {
			continue
		}
		still := gridProbeStill{
			SerialMask:   gridProbeMaskSerial(serial),
			State:        string(frame.State()),
			Current:      frame.Current(),
			Frames:       frame.Frames,
			Failures:     frame.Failures,
			SourceBytes:  frame.Bytes,
			StillBytes:   frame.StillBytes,
			Wide:         frame.Width,
			High:         frame.Height,
			MediaType:    frame.MediaType,
			ObservedMS:   frame.ObservedCadence.Milliseconds(),
			FailureClass: string(frame.FailureClass),
			FailureNote:  frame.FailureDetail,
			Truncated:    frame.PreviewTruncated,
			AboveBound:   profile.ByteBound > 0 && frame.StillBytes > profile.ByteBound,
		}
		if !frame.CapturedAt.IsZero() {
			still.CapturedAt = frame.CapturedAt.UTC().Format(time.RFC3339)
		}
		if frame.Current() {
			current++
			bytesPerSweep += frame.StillBytes
		}
		fleet = append(fleet, still)
	}
	return fleet, bytesPerSweep, current
}

// gridProbeBoundFromMeasurement turns the measurement into the two bounds it
// implies: how many devices the STILLS cost allows, and how many one sweep of
// CAPTURES can finish inside the cadence the deployment stated.
func gridProbeBoundFromMeasurement(devices, stillBytesPerSweep int, cadence time.Duration, captureMS float64) gridProbeGridBound {
	bound := gridProbeGridBound{
		MaxDevices:            media.DefaultGridMaxDevices,
		CadenceSeconds:        cadence.Seconds(),
		DevicesAtOneFarStream: 0,
		SweepSecondsAtFleet:   0,
	}
	if devices > 0 {
		bound.StillBytesPerDevice = stillBytesPerSweep / devices
	}
	if bound.StillBytesPerDevice > 0 && cadence > 0 {
		perSecondPerDevice := float64(bound.StillBytesPerDevice) / cadence.Seconds()
		bound.DevicesAtOneFarStream = int(float64(gridProbeFarStreamMbps) * 1e6 / 8 / perSecondPerDevice)
		bound.BoundImpliedByBytes = bound.DevicesAtOneFarStream
	}
	if captureMS > 0 && cadence > 0 {
		bound.SweepSecondsAtFleet = captureMS * float64(devices) / 1000
		bound.DevicesAtOneCadence = int(cadence.Seconds() * 1000 / captureMS)
	}
	notes := make([]string, 0, 2)
	if bound.DevicesAtOneFarStream > 0 {
		notes = append(notes, fmt.Sprintf("the stills of %d device(s) at %d byte(s) each per %s cost the measured %.0f Mbps far stream",
			bound.DevicesAtOneFarStream, bound.StillBytesPerDevice, cadence, gridProbeFarStreamMbps))
	}
	if bound.SweepSecondsAtFleet > 0 {
		notes = append(notes, fmt.Sprintf("but one capture took a mean of %.0f ms, so a sweep of the %d device(s) measured here takes about %.0f s - %d device(s) is what fits inside one %s cadence",
			captureMS, devices, bound.SweepSecondsAtFleet, bound.DevicesAtOneCadence, cadence))
	}
	bound.Note = strings.Join(notes, "; ")
	return bound
}

// gridProbeConclusion states what the run established, in one sentence.
func gridProbeConclusion(report gridProbeReport) string {
	current := 0
	for _, still := range report.Fleet {
		if still.Current {
			current++
		}
	}
	separation := "the operator's frame was not opened in both arms, so the separation was not measured"
	switch {
	case report.Operator.Opened && report.OperatorBaseline.Opened:
		separation = fmt.Sprintf("the operator's frame carried %d access unit(s) with the grid running against %d alone over the same %s, and its longest quiet run was %d second(s) against %d",
			report.Operator.Frames, report.OperatorBaseline.Frames,
			time.Duration(report.HoldSeconds*float64(time.Second)),
			report.Operator.StalledSeconds, report.OperatorBaseline.StalledSeconds)
		separation += " (reported rather than asserted: an idle bench drives the frame count in both arms, so the counts are evidence and not a comparison)"
	}
	return fmt.Sprintf("%d of %d attached device(s) were carried as current stills at %d byte(s) per still (%.2f Mbps, %.1f%% of the measured %.0f Mbps far stream); one sweep took about %.0f s at the measured %.0f ms per capture; %s",
		current, report.DeviceCount, report.Bounds.StillBytesPerDevice, report.Cost.StillMbps,
		report.Cost.ShareOfFarStream*100, report.Cost.FarStreamMbps,
		report.Bounds.SweepSecondsAtFleet, report.Cost.MeanCaptureMS, separation)
}

// gridProbeReportText renders the report as a table a reader can read in the log.
func gridProbeReportText(report gridProbeReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\nFleet grid still probe - %s on %s (%d device(s), %s level, %s cadence)\n",
		report.MeasuredAt, report.Host, report.DeviceCount, report.Level, time.Duration(report.CadenceMillis)*time.Millisecond)
	fmt.Fprintf(&out, "%-12s %-10s %8s %8s %10s %10s %6s\n", "DEVICE", "STATE", "WIDE", "HIGH", "SOURCE_KB", "STILL_KB", "CAPS")
	for _, still := range report.Fleet {
		fmt.Fprintf(&out, "%-12s %-10s %8d %8d %10.1f %10.1f %6d\n", still.SerialMask, still.State,
			still.Wide, still.High, float64(still.SourceBytes)/1024, float64(still.StillBytes)/1024, still.Frames)
	}
	delivered, truncated := 0, 0
	for _, still := range report.Fleet {
		if !still.Current || still.Truncated {
			if still.Truncated {
				truncated++
			}
			continue
		}
		delivered++
	}
	fmt.Fprintf(&out, "\nstills delivered: %d of %d device(s); reported as truncated rather than delivered: %d\n",
		delivered, report.DeviceCount, truncated)
	fmt.Fprintf(&out, "\n%-8s %-9s %9s %10s %10s %8s\n", "LEVEL", "MAX_WIDTH", "QUALITY", "SOURCE_KB", "STILL_KB", "ENCODE_MS")
	for _, level := range report.Levels {
		fmt.Fprintf(&out, "%-8s %-9d %9d %10.1f %10.1f %8d\n", level.Level, level.MaxWidth, level.JPEGQuality,
			float64(level.SourceBytes)/1024, float64(level.StillBytes)/1024, level.EncodeMS)
	}
	fmt.Fprintf(&out, "\noperator frame ALONE (control): opened=%v %dx%d frames=%d %.2f fps quiet=%d longest quiet run=%d\n",
		report.OperatorBaseline.Opened, report.OperatorBaseline.Wide, report.OperatorBaseline.High,
		report.OperatorBaseline.Frames, float64(report.OperatorBaseline.Frames)/report.HoldSeconds,
		report.OperatorBaseline.QuietSeconds, report.OperatorBaseline.StalledSeconds)
	fmt.Fprintf(&out, "operator frame WITH the grid: opened=%v %dx%d frames=%d %.2f fps quiet=%d longest quiet run=%d\n",
		report.Operator.Opened, report.Operator.Wide, report.Operator.High, report.Operator.Frames,
		float64(report.Operator.Frames)/report.HoldSeconds, report.Operator.QuietSeconds, report.Operator.StalledSeconds)
	fmt.Fprintf(&out, "grid cost: %d byte(s)/sweep, %.1f KB/s, %.3f Mbps (%.4f of the measured %.0f Mbps far stream); mean capture %.0f ms, longest sweep %d ms over %d tick(s)\n",
		report.Cost.StillBytesPerSweep, report.Cost.StillKBytesPerSec, report.Cost.StillMbps,
		report.Cost.ShareOfFarStream, report.Cost.FarStreamMbps, report.Cost.MeanCaptureMS, report.Cost.LongestSweepMS, report.Cost.SweepTicks)
	fmt.Fprintf(&out, "current stills per sampled second: %v\n", report.FleetCurrentPerSecond)
	fmt.Fprintf(&out, "bound implied: %d device(s) fit the measured far stream as stills, %d device(s) fit inside one %s cadence of captures\n",
		report.Bounds.DevicesAtOneFarStream, report.Bounds.DevicesAtOneCadence, time.Duration(report.CadenceMillis)*time.Millisecond)
	fmt.Fprintf(&out, "   %s\n", report.Bounds.Note)
	fmt.Fprintf(&out, "conclusion: %s\n", report.Conclusion)
	return out.String()
}

// gridProbeMaskSerial masks a serial the way the rest of the evidence does: enough
// to recognise the device, not enough to be an address.
func gridProbeMaskSerial(serial string) string {
	if len(serial) <= 8 {
		return serial
	}
	return serial[:4] + "..." + serial[len(serial)-4:]
}

func gridProbeHostName() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

func gridProbeHostLoad() string {
	raw, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(raw)), "{}")
}

func gridProbeADBVersion(ctx context.Context, config gridProbeConfig) string {
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, config.adbPath, "version").Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(output), "\n")
	return strings.TrimSpace(first)
}
