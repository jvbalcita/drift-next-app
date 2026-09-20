package scrcpy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/scrcpy"
)

// The live-stream concurrency probe (card ARC-228).
//
// The plane's device-session capacity is a number the deployment states, and the
// only honest source for it is a measurement of the thing it bounds: how many
// live mirror sessions this host and this fleet's transport actually carry at
// once. Bulk transfer is not that measurement - a hub path that moves 78 Mbps of
// four fat adb pulls can still collapse when eight ENCODERS run at once, and a
// bounded live tile costs a fraction of a bulk transfer's bandwidth - so this
// probe opens N real sessions over the product's own scrcpy client, holds them,
// and records what each stream carried.
//
// It is opt-in and it skips unless every gate is satisfied, because it opens real
// captures on real devices. CI sets none of these:
//
//	DRIFT_MIRROR_PROBE=1                       the opt-in
//	DRIFT_MIRROR_PROBE_ADB=/abs/path/adb       the adb executable
//	DRIFT_MIRROR_PROBE_SERVER=/abs/scrcpy-server  the pushed device-side server
//	DRIFT_MIRROR_PROBE_COUNTS=4,8,10,16        the concurrency points to measure
//	DRIFT_MIRROR_PROBE_HOLD_SECONDS=60         how long each point is held
//	DRIFT_MIRROR_PROBE_OUT=/abs/report.json    where the report is written
//
// Targets are taken from `adb devices` in the order adb lists them, so the point
// measuring four devices is the same four devices as the point measuring ten, and
// a run can never silently measure a different fleet than the run before it.
// DRIFT_MIRROR_PROBE_SERIALS may name them explicitly instead.
const (
	probeEnv        = "DRIFT_MIRROR_PROBE"
	probeADBEnv     = "DRIFT_MIRROR_PROBE_ADB"
	probeServerEnv  = "DRIFT_MIRROR_PROBE_SERVER"
	probeSerialsEnv = "DRIFT_MIRROR_PROBE_SERIALS"
	probeCountsEnv  = "DRIFT_MIRROR_PROBE_COUNTS"
	probeHoldEnv    = "DRIFT_MIRROR_PROBE_HOLD_SECONDS"
	probeOutEnv     = "DRIFT_MIRROR_PROBE_OUT"
)

// probeWarmupSeconds is excluded from every steady-state figure. A session's
// first seconds carry the encoder's own start-up - the first IDR, the parameter
// sets, the handshake's burst - and averaging those into a per-stream cost would
// report a rate no held session sustains.
const probeWarmupSeconds = 5

// probeStallSeconds is how many consecutive seconds with no access unit at all
// count as a stalled stream. One quiet second is a scene that did not change; two
// is a stream that stopped, which is the fact the capacity has to be sized under.
const probeStallSeconds = 2

// probeOpenTimeout bounds one attempt to open a live session - the push of the
// device-side server, the reverse tunnel, the launch and the handshake - and
// probeOpenAttempts is how many times one device is tried before its failure is
// reported as a fact about the fleet.
//
// The bound is generous because the invocations it covers share the host's adb
// server with whatever else is running here: measured on this host, a 733 KB push
// that takes 0.3s alone took longer than 45s while a plane's own preview captures
// were running, and a bound that read that contention as a mirror that cannot open
// a device would have reported a capacity ceiling that is not one.
const (
	probeOpenTimeout  = 60 * time.Second
	probeOpenAttempts = 2
)

// TestLiveStreamConcurrencyProbe measures how many concurrent live mirror
// sessions this fleet carries before a stream stalls.
//
// It asserts nothing about the outcome. What it produces is the evidence: the
// per-stream frame counts, the per-stream and aggregate throughput, and the
// honest first concurrency at which a stream stopped carrying pictures. A
// measurement that came back clean at every point is a result too, and a test
// that demanded a particular knee would be a test that could only confirm the
// number it was written with.
func TestLiveStreamConcurrencyProbe(t *testing.T) {
	config, err := probeConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live-stream concurrency probe: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTotalBudget(config))
	defer cancel()

	serials, err := probeTargets(ctx, config)
	if err != nil {
		t.Fatalf("resolving probe targets: %v", err)
	}
	if len(serials) == 0 {
		t.Skip("skipping live-stream concurrency probe: adb reported no usable device")
	}

	report := probeReport{
		MeasuredAt:       time.Now().UTC().Format(time.RFC3339),
		Host:             probeHostName(),
		HostLoad:         probeHostLoad(),
		ScrcpyServerPath: config.serverPath,
		ADBVersion:       probeADBVersion(ctx, config),
		DeviceCount:      len(serials),
		HoldSeconds:      config.hold.Seconds(),
		Counts:           config.counts,
	}
	t.Logf("probe fleet: %d device(s); measuring concurrency %v for %s each",
		len(serials), config.counts, config.hold)

	for _, concurrency := range config.counts {
		if concurrency > len(serials) {
			report.Runs = append(report.Runs, probeRun{
				Concurrency: concurrency,
				Note: fmt.Sprintf("not measurable on this fleet: %d device(s) are attached and one live session is one device",
					len(serials)),
			})
			t.Logf("concurrency %d: not measurable, only %d device(s) attached", concurrency, len(serials))
			continue
		}
		run := probeMeasure(ctx, t, config, serials[:concurrency])
		report.Runs = append(report.Runs, run)
		t.Logf("concurrency %d: opened %d, aggregate %.2f Mbps, stalled stream(s) %d",
			run.Concurrency, run.Opened, run.AggregateMbps, run.StalledStreams)
		// A held capture leaves the device's encoder and its transport settling;
		// the next point starts from a quiet fleet rather than from the tail of
		// the last one.
		time.Sleep(5 * time.Second)
	}
	report.Conclusion = probeConclusion(report)

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encoding the report: %v", err)
	}
	if err := os.WriteFile(config.out, encoded, 0o600); err != nil {
		t.Fatalf("writing the report to %s: %v", config.out, err)
	}
	t.Logf("probe report written to %s", config.out)
	t.Log(probeReportText(report))
}

// probeConfig is the probe's own resolved configuration.
type probeConfig struct {
	adbPath    string
	serverPath string
	serials    []string
	counts     []int
	hold       time.Duration
	out        string
}

// probeConfigFromEnv reads the gates, refusing a partial configuration rather
// than guessing at any of them.
func probeConfigFromEnv() (probeConfig, error) {
	if strings.TrimSpace(os.Getenv(probeEnv)) != "1" {
		return probeConfig{}, fmt.Errorf("%s must equal 1 (opt-in)", probeEnv)
	}
	config := probeConfig{
		adbPath:    strings.TrimSpace(os.Getenv(probeADBEnv)),
		serverPath: strings.TrimSpace(os.Getenv(probeServerEnv)),
		hold:       60 * time.Second,
		out:        strings.TrimSpace(os.Getenv(probeOutEnv)),
	}
	if config.adbPath == "" || !filepath.IsAbs(config.adbPath) {
		return probeConfig{}, fmt.Errorf("%s must name an absolute adb path", probeADBEnv)
	}
	if config.serverPath == "" || !filepath.IsAbs(config.serverPath) {
		return probeConfig{}, fmt.Errorf("%s must name an absolute scrcpy server path", probeServerEnv)
	}
	if config.out == "" || !filepath.IsAbs(config.out) {
		return probeConfig{}, fmt.Errorf("%s must name an absolute report path", probeOutEnv)
	}
	if raw := strings.TrimSpace(os.Getenv(probeHoldEnv)); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= probeWarmupSeconds {
			return probeConfig{}, fmt.Errorf("%s must be a whole number of seconds above %d, and this run set %q",
				probeHoldEnv, probeWarmupSeconds, raw)
		}
		config.hold = time.Duration(seconds) * time.Second
	}
	counts, err := probeCounts(os.Getenv(probeCountsEnv))
	if err != nil {
		return probeConfig{}, err
	}
	config.counts = counts
	for _, raw := range strings.Split(os.Getenv(probeSerialsEnv), ",") {
		if serial := strings.TrimSpace(raw); serial != "" {
			config.serials = append(config.serials, serial)
		}
	}
	return config, nil
}

// probeCounts reads the concurrency points, in the order they will be measured.
func probeCounts(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return []int{4, 8, 10, 16}, nil
	}
	counts := []int(nil)
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		value, err := strconv.Atoi(token)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("%s must be a comma-separated list of positive whole numbers, and this run set %q",
				probeCountsEnv, raw)
		}
		counts = append(counts, value)
	}
	if len(counts) == 0 {
		return nil, fmt.Errorf("%s named no concurrency point", probeCountsEnv)
	}
	return counts, nil
}

// probeTotalBudget bounds the whole probe so a hung device cannot hold the run
// open: every point is held for its own window plus the bounded attempts it may
// spend opening its sessions and a settle afterwards.
func probeTotalBudget(config probeConfig) time.Duration {
	largest := 1
	for _, count := range config.counts {
		if count > largest {
			largest = count
		}
	}
	perPoint := config.hold + time.Duration(largest*probeOpenAttempts)*probeOpenTimeout + 2*time.Minute
	return time.Duration(len(config.counts))*perPoint + 2*time.Minute
}

// probeTargets resolves the devices to measure, in a stable order.
func probeTargets(ctx context.Context, config probeConfig) ([]string, error) {
	if len(config.serials) > 0 {
		return config.serials, nil
	}
	adapter, err := probeAdapter(config.adbPath)
	if err != nil {
		return nil, err
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

// probeAdapter builds the same bounded, allow-listed device adapter the product
// reaches devices through: the probe runs the product's own path rather than a
// second one, so what it measures is what a mirror does.
func probeAdapter(adbPath string) (*adb.Adapter, error) {
	runner, err := adb.NewProcessRunner()
	if err != nil {
		return nil, err
	}
	return adb.NewAdapter(adbPath, runner)
}

// probeMeasure opens one concurrency point, holds it, and reports what every
// stream carried.
func probeMeasure(ctx context.Context, t *testing.T, config probeConfig, serials []string) probeRun {
	t.Helper()
	run := probeRun{Concurrency: len(serials), Requested: len(serials), SerialMask: make([]string, 0, len(serials))}

	adapter, err := probeAdapter(config.adbPath)
	if err != nil {
		run.Note = "no device adapter could be built: " + err.Error()
		return run
	}
	starter, err := adb.NewProcessStarter(config.adbPath)
	if err != nil {
		run.Note = "no device server could be started: " + err.Error()
		return run
	}

	sessions := make([]*scrcpy.Session, 0, len(serials))
	counters := make([]*probeCounters, 0, len(serials))
	defer func() {
		for _, session := range sessions {
			closeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_ = session.Close(closeCtx)
			cancel()
		}
	}()

	// Opening is sequential and it is what it is: a device that cannot be opened
	// is recorded with its own reason rather than substituted, so a point that
	// asked for ten sessions and carried eight says so.
	//
	// An open is retried once. The push and the reverse tunnel are bounded adb
	// invocations that share the host's adb server with whatever else is on this
	// host - a running plane's own preview captures, for one - and a bounded
	// invocation that timed out under that contention is not a fact about the
	// mirror's capacity. What a retry cannot fix is reported as the failure it is,
	// with the number of attempts made.
	for _, serial := range serials {
		masked := probeMaskSerial(serial)
		run.SerialMask = append(run.SerialMask, masked)
		var session *scrcpy.Session
		var openErr error
		// attempts counts the attempts MADE, not the loop's exit value: the report
		// states how many bounded tries a device was given, and a counter that ran
		// one past its own bound would overstate the effort on every failure.
		attempts := 0
		for attempts < probeOpenAttempts {
			attempts++
			openCtx, cancel := context.WithTimeout(ctx, probeOpenTimeout)
			session, openErr = scrcpy.Start(openCtx, scrcpy.Options{
				ADB:        config.adbPath,
				Serial:     serial,
				ServerPath: config.serverPath,
				Runner:     adapter,
				Starter:    starter,
				KeepAwake:  true,
			})
			cancel()
			if openErr == nil {
				break
			}
			if ctx.Err() != nil {
				break
			}
		}
		if openErr != nil {
			run.OpenFailures = append(run.OpenFailures, probeOpenFailure{
				Serial:   masked,
				Attempts: attempts,
				Reason:   openErr.Error(),
			})
			continue
		}
		width, height := session.Meta().Size()
		counter := &probeCounters{serial: masked, width: width, height: height}
		sessions = append(sessions, session)
		counters = append(counters, counter)
		go counter.consume(session)
	}
	run.Opened = len(sessions)
	if run.Opened == 0 {
		run.Note = "no live session could be opened"
		return run
	}

	// The hold window: every second, what each stream has carried since the last
	// look. A stream that carried nothing for probeStallSeconds consecutive
	// seconds is stalled, and the first second of it is reported.
	window := make([]probeSecond, 0, int(config.hold.Seconds()))
	previous := make([]probeTotals, run.Opened)
	for second := 1; second <= int(config.hold.Seconds()); second++ {
		select {
		case <-ctx.Done():
			run.Note = "the probe's own budget ended mid-window: " + ctx.Err().Error()
			second = int(config.hold.Seconds()) + 1
			continue
		case <-time.After(time.Second):
		}
		sample := probeSecond{Second: second, Streams: make([]probeStreamSecond, 0, run.Opened)}
		for index, counter := range counters {
			totals := counter.totals()
			delta := totals.subtract(previous[index])
			previous[index] = totals
			sample.Streams = append(sample.Streams, probeStreamSecond{
				Serial:    counter.serial,
				Frames:    delta.frames,
				KeyFrames: delta.keyFrames,
				Bytes:     delta.bytes,
				Ended:     totals.ended,
			})
		}
		window = append(window, sample)
	}

	run.HoldSeconds = config.hold.Seconds()
	run.Streams = make([]probeStream, 0, run.Opened)
	for _, counter := range counters {
		run.Streams = append(run.Streams, probeStreamOf(counter, window))
	}
	run.AggregateMbps = probeAggregateMbps(run.Streams)
	run.StalledStreams, run.FirstStallAt = probeStalls(run.Streams)
	run.Degraded = run.StalledStreams > 0 || len(run.OpenFailures) > 0
	return run
}

// probeCounters is what one stream carried, counted by the goroutine that reads
// it. It is the probe's own count rather than the session's, so a figure the
// report states is a figure the probe read from the wire.
type probeCounters struct {
	serial string
	width  int
	height int

	mu          sync.Mutex
	frames      int
	keyFrames   int
	configs     int
	bytes       int64
	lastFrameAt time.Time
	ptsDeltas   []uint64
	lastPTS     uint64
	hasPTS      bool
	ended       string
}

// consume reads one session's access units until the stream ends, which is also
// what makes the count real: an unread socket is a stream nothing is watching.
func (c *probeCounters) consume(session *scrcpy.Session) {
	for {
		unit, err := session.ReadAccessUnitContext(context.Background())
		if err != nil {
			c.mu.Lock()
			c.ended = err.Error()
			c.mu.Unlock()
			return
		}
		now := time.Now()
		c.mu.Lock()
		c.bytes += int64(len(unit.Data))
		if unit.Config {
			c.configs++
		} else {
			c.frames++
			c.lastFrameAt = now
			if unit.Key {
				c.keyFrames++
			}
			if c.hasPTS && unit.PTSUS >= c.lastPTS {
				c.ptsDeltas = append(c.ptsDeltas, unit.PTSUS-c.lastPTS)
			}
			c.lastPTS = unit.PTSUS
			c.hasPTS = true
		}
		c.mu.Unlock()
	}
}

// probeTotals is one stream's counters at one instant.
type probeTotals struct {
	frames    int
	keyFrames int
	bytes     int64
	ended     string
}

func (c *probeCounters) totals() probeTotals {
	c.mu.Lock()
	defer c.mu.Unlock()
	return probeTotals{frames: c.frames, keyFrames: c.keyFrames, bytes: c.bytes, ended: c.ended}
}

func (c *probeCounters) cadence() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uint64, len(c.ptsDeltas))
	copy(out, c.ptsDeltas)
	return out
}

func (t probeTotals) subtract(other probeTotals) probeTotals {
	return probeTotals{frames: t.frames - other.frames, keyFrames: t.keyFrames - other.keyFrames, bytes: t.bytes - other.bytes}
}

// probeStreamSecond is what one stream carried in one second of the window.
type probeStreamSecond struct {
	Serial    string
	Frames    int
	KeyFrames int
	Bytes     int64
	Ended     string
}

// probeSecond is one second of the hold window across every stream.
type probeSecond struct {
	Second  int
	Streams []probeStreamSecond
}

// probeStream is one stream's whole measurement.
type probeStream struct {
	Serial    string
	Width     int
	Height    int
	Frames    int
	KeyFrames int
	Configs   int
	Bytes     int64
	// SteadyFrames and SteadyBytes are the totals after the warm-up seconds,
	// which is what the per-stream rates are computed from.
	SteadyFrames int
	SteadyBytes  int64
	FPS          float64
	Mbps         float64
	// StalledSeconds counts the seconds in the steady window that carried no
	// picture at all, and FirstStallSecond is the first of them.
	StalledSeconds   int
	FirstStallSecond int
	// PTSDeltaMinUS, PTSDeltaMedianUS and PTSDeltaMaxUS are the encoder's own
	// frame cadence, as the device stamped it: a median far above the profile's
	// frame interval is an encoder that is not keeping up.
	PTSDeltaMinUS    uint64
	PTSDeltaMedianUS uint64
	PTSDeltaMaxUS    uint64
	Ended            string
}

// probeStreamOf reduces one stream's counters and the window to its report row.
func probeStreamOf(counter *probeCounters, window []probeSecond) probeStream {
	totals := counter.totals()
	cadence := counter.cadence()
	sort.Slice(cadence, func(i, j int) bool { return cadence[i] < cadence[j] })
	stream := probeStream{
		Serial:    counter.serial,
		Width:     counter.width,
		Height:    counter.height,
		Frames:    totals.frames,
		KeyFrames: totals.keyFrames,
		Bytes:     totals.bytes,
		Ended:     totals.ended,
	}
	if len(cadence) > 0 {
		stream.PTSDeltaMinUS = cadence[0]
		stream.PTSDeltaMedianUS = cadence[len(cadence)/2]
		stream.PTSDeltaMaxUS = cadence[len(cadence)-1]
	}
	steadySeconds := 0
	quietRun := 0
	for _, second := range window {
		if second.Second <= probeWarmupSeconds {
			continue
		}
		steadySeconds++
		for _, sample := range second.Streams {
			if sample.Serial != counter.serial {
				continue
			}
			stream.SteadyFrames += sample.Frames
			stream.SteadyBytes += sample.Bytes
			if sample.Frames == 0 {
				quietRun++
				stream.StalledSeconds++
				if quietRun >= probeStallSeconds && stream.FirstStallSecond == 0 {
					stream.FirstStallSecond = second.Second - probeStallSeconds + 1
				}
				continue
			}
			quietRun = 0
		}
	}
	if steadySeconds > 0 {
		seconds := float64(steadySeconds)
		stream.FPS = float64(stream.SteadyFrames) / seconds
		stream.Mbps = float64(stream.SteadyBytes) * 8 / seconds / 1e6
	}
	return stream
}

// probeAggregateMbps sums the steady-state per-stream rates: what the whole point
// put on the transport at once.
func probeAggregateMbps(streams []probeStream) float64 {
	total := 0.0
	for _, stream := range streams {
		total += stream.Mbps
	}
	return total
}

// probeStalls reports how many streams stalled and the earliest second any of
// them did.
func probeStalls(streams []probeStream) (int, int) {
	stalled := 0
	earliest := 0
	for _, stream := range streams {
		if stream.FirstStallSecond == 0 {
			continue
		}
		stalled++
		if earliest == 0 || stream.FirstStallSecond < earliest {
			earliest = stream.FirstStallSecond
		}
	}
	return stalled, earliest
}

// probeRun is one concurrency point.
type probeRun struct {
	Concurrency    int
	Requested      int
	Opened         int
	HoldSeconds    float64
	AggregateMbps  float64
	StalledStreams int
	FirstStallAt   int
	Degraded       bool
	SerialMask     []string
	OpenFailures   []probeOpenFailure
	Streams        []probeStream
	Note           string
}

// probeOpenFailure is a device that could not be opened at this point, and how
// many bounded attempts were made before it was reported.
type probeOpenFailure struct {
	Serial   string
	Attempts int
	Reason   string
}

// probeReport is the whole measurement.
type probeReport struct {
	MeasuredAt       string
	Host             string
	HostLoad         string
	ADBVersion       string
	ScrcpyServerPath string
	DeviceCount      int
	HoldSeconds      float64
	Counts           []int
	Runs             []probeRun
	Conclusion       string
}

// probeConclusion states the honest knee, and it states the TWO of them
// separately because they are different findings about different parts of the
// system:
//
//   - a stream that STALLS is the fleet's encode or its transport giving up under
//     concurrency, which is what a device-session capacity is meant to bound; and
//   - a device that cannot be OPENED is the path that starts a session - the push
//     of the device-side server and the reverse tunnel, bounded adb invocations
//     that share the host's adb server - running out of room, which bounds how
//     many sessions a console can START at once but says nothing about what a
//     running session costs.
//
// A single number that counted both would report a capacity ceiling that the
// streams themselves contradict: measured on this fleet, four sessions carried
// 32 Mbps at 30 fps with no stall at all while a fifth device's push timed out.
func probeConclusion(report probeReport) string {
	var measured []probeRun
	for _, run := range report.Runs {
		if run.Note != "" || run.Opened == 0 {
			continue
		}
		measured = append(measured, run)
	}
	if len(measured) == 0 {
		return "no concurrency point was measurable on this fleet"
	}
	largest := 0
	cleanAll := 0
	firstStall := 0
	firstOpenFailure := 0
	for _, run := range measured {
		if run.Concurrency > largest {
			largest = run.Concurrency
		}
		if run.StalledStreams > 0 {
			if firstStall == 0 || run.Concurrency < firstStall {
				firstStall = run.Concurrency
			}
		} else if len(run.OpenFailures) == 0 && run.Opened == run.Requested && run.Concurrency > cleanAll {
			// A point is carried CLEANLY only when every session it asked for was
			// live and every one of them carried its whole window: a point where a
			// device never opened did not carry that concurrency, whatever its
			// streams did.
			cleanAll = run.Concurrency
		}
		if len(run.OpenFailures) > 0 && (firstOpenFailure == 0 || run.Concurrency < firstOpenFailure) {
			firstOpenFailure = run.Concurrency
		}
	}

	parts := make([]string, 0, 4)
	if firstStall != 0 {
		parts = append(parts, fmt.Sprintf("the first point at which a STREAM stalled is %d concurrent live session(s)", firstStall))
	} else {
		parts = append(parts, "no stream stalled at any measured point")
	}
	if cleanAll > 0 {
		parts = append(parts, fmt.Sprintf("%d concurrent live session(s) were carried cleanly, with every device opening and every stream carrying its whole window", cleanAll))
	} else {
		parts = append(parts, "no measured point carried every session it asked for")
	}
	if firstOpenFailure != 0 {
		parts = append(parts, fmt.Sprintf("the first point at which a device could not be OPENED at all is %d concurrent live session(s), which bounds how many sessions a console can start at once rather than what a running session costs", firstOpenFailure))
	}
	parts = append(parts, fmt.Sprintf("largest measured point: %d", largest))
	return strings.Join(parts, "; ")
}

// TestProbeConclusionSeparatesAStalledStreamFromADeviceThatCouldNotBeOpened is the
// probe's own honesty check, and it runs without a device because it is about what
// the report SAYS rather than about what was measured.
//
// The two degradations are not interchangeable: a stalled stream is the fleet's
// encode or transport giving up, and a device that could not be opened is the
// session-START path running out of room. A conclusion that counted both as one
// ceiling would state a capacity the streams themselves contradict - which is
// exactly what this fleet produced: four sessions at 32 Mbps and 30 fps with no
// stall, beside a fifth device whose server push timed out.
func TestProbeConclusionSeparatesAStalledStreamFromADeviceThatCouldNotBeOpened(t *testing.T) {
	clean := func(concurrency int) probeRun {
		return probeRun{Concurrency: concurrency, Requested: concurrency, Opened: concurrency}
	}
	unopened := func(concurrency, opened int) probeRun {
		return probeRun{
			Concurrency: concurrency, Requested: concurrency, Opened: opened,
			OpenFailures: []probeOpenFailure{{Serial: "device-000000000000", Attempts: 2, Reason: "adb mirror-push-server failed (timeout)"}},
		}
	}
	stalled := func(concurrency, opened int) probeRun {
		return probeRun{Concurrency: concurrency, Requested: concurrency, Opened: opened, StalledStreams: opened, FirstStallAt: 6}
	}

	// The fleet's own result: every stream carried its window, and a device could not
	// be opened. The conclusion must NOT read as a capacity ceiling on the streams.
	got := probeConclusion(probeReport{Runs: []probeRun{clean(4), unopened(5, 4), unopened(8, 4)}})
	for _, want := range []string{
		"no stream stalled at any measured point",
		"4 concurrent live session(s) were carried cleanly",
		"could not be OPENED",
		"5 concurrent live session(s)",
		"rather than what a running session costs",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the conclusion does not state %q, so a start-path limit would be read as an encode limit:\n%s", want, got)
		}
	}
	if strings.Contains(got, "STREAM stalled") {
		t.Fatalf("a run with no stalled stream is reported as having stalled one: %s", got)
	}

	// A stream that stalled IS the fleet's own ceiling, and it is named as such.
	got = probeConclusion(probeReport{Runs: []probeRun{clean(2), stalled(3, 3)}})
	for _, want := range []string{"STREAM stalled is 3", "2 concurrent live session(s) were carried cleanly"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the conclusion does not state %q: %s", want, got)
		}
	}
	if strings.Contains(got, "could not be OPENED") {
		t.Fatalf("a run where every device opened is reported as having failed to open one: %s", got)
	}

	// A run where nothing could be measured at all says exactly that.
	got = probeConclusion(probeReport{Runs: []probeRun{{Concurrency: 16, Note: "not measurable on this fleet"}}})
	if !strings.Contains(got, "no concurrency point was measurable") {
		t.Fatalf("an unmeasurable run reported %q", got)
	}
}

// probeReportText renders the report as the lines a reviewer reads, so the
// committed artifact and the run's own output are the same measurement.
func probeReportText(report probeReport) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "live-stream concurrency, measured %s on %s (adb %s, %d device(s) attached, held %.0fs per point, host load %s)\n",
		report.MeasuredAt, report.Host, report.ADBVersion, report.DeviceCount, report.HoldSeconds, report.HostLoad)
	for _, run := range report.Runs {
		if run.Note != "" {
			fmt.Fprintf(&builder, "  N=%-3d not measurable: %s\n", run.Concurrency, run.Note)
			continue
		}
		fmt.Fprintf(&builder, "  N=%-3d opened %d/%d, aggregate %.2f Mbps, stalled %d stream(s), first stall at second %d\n",
			run.Concurrency, run.Opened, run.Requested, run.AggregateMbps, run.StalledStreams, run.FirstStallAt)
		for _, stream := range run.Streams {
			fmt.Fprintf(&builder, "      %s %dx%d: %.2f fps, %.2f Mbps, %d frame(s), %d key frame(s), pts median %dus, stalled %d s\n",
				stream.Serial, stream.Width, stream.Height, stream.FPS, stream.Mbps,
				stream.Frames, stream.KeyFrames, stream.PTSDeltaMedianUS, stream.StalledSeconds)
		}
		for _, failure := range run.OpenFailures {
			fmt.Fprintf(&builder, "      %s could not be opened after %d attempt(s): %s\n", failure.Serial, failure.Attempts, failure.Reason)
		}
	}
	fmt.Fprintf(&builder, "  conclusion: %s\n", report.Conclusion)
	return builder.String()
}

// probeADBVersion records the platform-tools version the measurement used, so
// the figure can be attributed to a transport implementation.
func probeADBVersion(ctx context.Context, config probeConfig) string {
	runner, err := adb.NewProcessRunner()
	if err != nil {
		return ""
	}
	result, err := runner.Run(ctx, config.adbPath, []string{"version"})
	if err != nil {
		return ""
	}
	line := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	if len(line) == 0 {
		return ""
	}
	return strings.TrimSpace(line[0])
}

// probeHostLoad records the load this host was under while the measurement was
// taken.
//
// It is evidence about the measurement rather than about the fleet: the sessions
// this probe holds share the host's adb server and its CPUs with whatever else is
// running here - a live control plane, its preview captures, another build - and a
// knee read off a host at load 60 is not the same finding as the same knee read
// off a quiet one. A run that cannot read the load says so with an empty value
// rather than guessing at one.
func probeHostLoad() string {
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		return strings.TrimSpace(string(raw))
	}
	// macOS has no /proc: ask sysctl, as a fixed argument array and never a shell.
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		return strings.Join(strings.Fields(string(out)), " ")
	}
	return ""
}

func probeHostName() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

// probeMaskSerial keeps a real transport address out of the committed artifact
// while leaving the same device correlatable within one report.
func probeMaskSerial(serial string) string {
	digest := adb.HashBytes([]byte(serial))
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return "device-" + digest
}
