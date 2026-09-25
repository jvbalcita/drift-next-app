package media

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The fleet grid's deployment inputs.
//
// The grid's cost is arithmetic rather than a preference: `devices x still bytes
// / cadence` against the path the plane is on. The still bytes are the level's
// business (see still.go) and the device count is the fleet's, so what a
// deployment chooses is how OFTEN each device is captured and how many devices one
// sweep may carry - and both are stated here, where the engine that spends them
// reads them, rather than being baked into the console or into a constant nobody
// can tune without a rebuild.
const (
	// EnvGridStillCadence is how often the plane aims to capture each subscribed
	// device. It is a deployment input because the same fleet on a slower hub
	// wants a longer cadence, and an operator tuning a grid should not need a
	// rebuild to do it.
	EnvGridStillCadence = "DRIFT_GRID_STILL_CADENCE"

	// EnvGridStillLevel is the level every tile's still is carried at: the width
	// cap and the JPEG quality (see GridStillLevel).
	EnvGridStillLevel = "DRIFT_GRID_STILL_LEVEL"

	// EnvGridMaxDevices is how many devices one sweep of the grid may carry.
	//
	// It is a bound on the WORK, not on the fleet: a still spends no device
	// session, so this is not a session capacity and it is deliberately far larger
	// than one. What it bounds is one sweep's worst case -
	// `ceil(max_devices / concurrent_captures) x the capture timeout` - so a plane
	// cannot be asked for unbounded work by a console that names a fleet it does not
	// have. A device the bound does not reach is reported as not shown, with the
	// bound named, rather than silently dropped.
	EnvGridMaxDevices         = "DRIFT_GRID_MAX_DEVICES"
	EnvGridConcurrentCaptures = "DRIFT_GRID_CONCURRENT_CAPTURES"
	EnvGridActiveCadence      = "DRIFT_GRID_ACTIVE_CADENCE"
	EnvGridIdleCadence        = "DRIFT_GRID_IDLE_CADENCE"
	EnvGridFreshnessCeiling   = "DRIFT_GRID_FRESHNESS_CEILING"
	// EnvGridStreamPreviews opts into persistent scrcpy workers per tile. It
	// defaults off: the fleet grid is JPEG stills (medium / concurrency 12 /
	// adaptive cadence), and turning this on multiplies encoders and memory.
	// Leave it off unless a deployment deliberately wants live tile previews.
	EnvGridStreamPreviews = "DRIFT_GRID_STREAM_PREVIEWS"
	EnvGridFFmpegPath     = "DRIFT_GRID_FFMPEG"
)

const (
	// DefaultGridStillCadence is the cadence a deployment that states none gets:
	// four seconds, which is the freshness a fleet view is scanned at rather than
	// the continuity a frame is worked at.
	//
	// It is an AIM, and the engine states each device's own MEASURED cadence on its
	// frame, which is what a tile shows. An earlier sequential capture path took a
	// mean of 3.1-4.2 s per device over TCP (a 1080x2280 screen arrives as a ~3.4 MB
	// PNG, and the level is applied on the plane after it arrives), so a sweep of
	// 19 devices took 59-71 s. Bounded concurrent scheduling materially changes
	// that cost; do not infer freshness from this configured cadence alone
	// (evidence:
	// tests/compatibility/grid/evidence-2026-09-21-fleet-still-grid.md).
	DefaultGridStillCadence = 4 * time.Second

	// MinGridStillCadence is the shortest cadence this plane will accept. Below
	// it a "still" is a stream in everything but name: the work per device
	// approaches continuous capture and the point of a still - one bounded
	// transaction that spends no session and holds no encoder - is lost. A
	// deployment that wants that is asking for the live mirror.
	MinGridStillCadence = 1 * time.Second

	// MaxGridStillCadence is the longest cadence this plane will accept. A tile
	// refreshed less often than a minute is not a view of a fleet: an operator
	// reads it as a broken grid rather than as a slow one, and the product offers
	// the one-shot capture for a picture taken on purpose.
	MaxGridStillCadence = 60 * time.Second

	// DefaultGridMaxDevices is the sweep bound a deployment that states none gets.
	// It is set where a lab fleet and a real one both fit under it while the work
	// it bounds stays finite, and a deployment with a larger fleet states its own
	// number and reads it back.
	//
	// A sweep's capture-time bound is the ceiling of this device bound divided by
	// the concurrent capture bound, times the capture path's own timeout. With the
	// measured default of 12 workers and the 15 s capture timeout, 64 devices are
	// at most six timeout waves; actual duration is measured rather than assumed.
	// The stills' delivered bandwidth is separate: at the medium level the earlier
	// 19-device sample cost 0.77 Mbps, under 2% of the 40 Mbps one live operator
	// profile stream measured at. The engine reports its actual sweep duration and
	// per-device cadence so a deployment can see which bound is binding (evidence:
	// tests/compatibility/grid/evidence-2026-09-21-fleet-still-grid.md).
	DefaultGridMaxDevices = 64
	// DefaultGridConcurrentCaptures bounds simultaneous ADB still captures. Twelve
	// workers are the measured balance for 18 authorized lab devices under the
	// still-only scheduler: two independent four-attempt runs stayed below the
	// 10 s peak tile-age ceiling with no capture failures. The Go probe plus local
	// ADB processes peaked near 354 MB; this excludes the control plane and
	// console/browser, so it is tuning evidence rather than proof of the whole-host
	// memory cap. Reproduction reports are listed in the fleet-grid evidence file.
	DefaultGridConcurrentCaptures = 12
	// DefaultStreamPreviewMaxWorkers is the hard persistent-session bound. It is
	// lower than the legacy screenshot sweep bound because every admitted device
	// owns an encoder and socket until the grid releases it.
	DefaultStreamPreviewMaxWorkers = 25
)

// GridSettings is the grid's resolved configuration: what one sweep costs and how
// often it runs.
type GridSettings struct {
	// Cadence is how often each subscribed device is captured.
	Cadence time.Duration
	// Profile is the level every tile's still is carried at.
	Profile GridStillProfile
	// MaxDevices is how many devices one sweep may carry.
	MaxDevices         int
	ConcurrentCaptures int
	ActiveCadence      time.Duration
	IdleCadence        time.Duration
	FreshnessCeiling   time.Duration
	StreamPreviews     bool
	FFmpegPath         string
}

// Report renders the settings as the one line a deployment reads back at startup:
// what the grid resolved to, in the numbers it resolved to.
func (s GridSettings) Report() string {
	mode := "bounded screenshots"
	if s.StreamPreviews {
		mode = "persistent scrcpy workers"
	}
	return fmt.Sprintf("grid stills: %s, every %s, at %s, up to %d device(s) per sweep, %d concurrent still capture(s), adaptive %s-%s, freshness ceiling %s",
		mode, s.Cadence, s.Profile.Report(), s.MaxDevices, s.ConcurrentCaptures, s.ActiveCadence, s.IdleCadence, s.FreshnessCeiling)
}

// GridSettingsFromEnv reads the grid's configuration from the deployment's own
// configuration.
//
// An unset input is not an error: it answers with the documented default, which
// the composition root then states explicitly when it builds the engine, so the
// ONE place the grid's cost is decided is still the composition root.
//
// The three inputs are read the way their kinds require rather than by one rule.
// A LEVEL is a name, and a name this plane does not know resolves to the
// documented default: the default is itself a level with a cap, so an
// unrecognised name is bounded, where refusing it would leave the grid
// unbounded. A CADENCE and a DEVICE BOUND are numbers, and a number that is not
// one, or is outside the range this product asks for, is refused where the
// deployment is configured: a cadence of zero is not a cadence with a default, it
// is a plane that would capture continuously, and carrying on would put a bound
// on the fleet that nobody chose.
func GridSettingsFromEnv(lookup EnvLookup) (GridSettings, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	settings := GridSettings{
		Cadence:            DefaultGridStillCadence,
		Profile:            DefaultGridStillProfile(),
		MaxDevices:         DefaultGridMaxDevices,
		ConcurrentCaptures: DefaultGridConcurrentCaptures,
		ActiveCadence:      time.Second,
		IdleCadence:        5 * time.Second,
		FreshnessCeiling:   10 * time.Second,
	}
	if raw, ok := lookup(EnvGridStillLevel); ok && strings.TrimSpace(raw) != "" {
		settings.Profile = GridStillProfileFor(GridStillLevelFromString(raw))
	}
	if raw, ok := lookup(EnvGridStillCadence); ok && strings.TrimSpace(raw) != "" {
		cadence, err := gridCadence(strings.TrimSpace(raw))
		if err != nil {
			return GridSettings{}, err
		}
		settings.Cadence = cadence
	}
	maxDevicesConfigured := false
	if raw, ok := lookup(EnvGridMaxDevices); ok && strings.TrimSpace(raw) != "" {
		maxDevicesConfigured = true
		devices, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || devices <= 0 {
			return GridSettings{}, fmt.Errorf(
				"%s must be a positive whole number of devices one sweep may carry, and this deployment configured %q",
				EnvGridMaxDevices, strings.TrimSpace(raw))
		}
		settings.MaxDevices = devices
	}
	if raw, ok := lookup(EnvGridConcurrentCaptures); ok && strings.TrimSpace(raw) != "" {
		workers, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || workers < 1 || workers > 25 {
			return GridSettings{}, fmt.Errorf("%s must be between 1 and 25, and this deployment configured %q", EnvGridConcurrentCaptures, strings.TrimSpace(raw))
		}
		settings.ConcurrentCaptures = workers
	}
	if raw, ok := lookup(EnvGridStreamPreviews); ok && strings.TrimSpace(raw) != "" {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on":
			settings.StreamPreviews = true
		case "0", "false", "no", "off":
		case "":
		default:
			return GridSettings{}, fmt.Errorf("%s must be one of 1/0, true/false, yes/no or on/off; this deployment set %q", EnvGridStreamPreviews, raw)
		}
	}
	if raw, ok := lookup(EnvGridFFmpegPath); ok {
		settings.FFmpegPath = strings.TrimSpace(raw)
	}
	if settings.StreamPreviews && settings.FFmpegPath == "" {
		return GridSettings{}, fmt.Errorf("%s is required when %s is enabled", EnvGridFFmpegPath, EnvGridStreamPreviews)
	}
	if settings.StreamPreviews && settings.MaxDevices > DefaultStreamPreviewMaxWorkers {
		if maxDevicesConfigured {
			return GridSettings{}, fmt.Errorf("%s may not exceed %d when %s is enabled", EnvGridMaxDevices, DefaultStreamPreviewMaxWorkers, EnvGridStreamPreviews)
		}
		settings.MaxDevices = DefaultStreamPreviewMaxWorkers
	}
	var err error
	if raw, ok := lookup(EnvGridActiveCadence); ok && strings.TrimSpace(raw) != "" {
		settings.ActiveCadence, err = gridCadence(strings.TrimSpace(raw))
		if err != nil {
			return GridSettings{}, err
		}
	}
	if raw, ok := lookup(EnvGridIdleCadence); ok && strings.TrimSpace(raw) != "" {
		settings.IdleCadence, err = gridCadence(strings.TrimSpace(raw))
		if err != nil {
			return GridSettings{}, err
		}
	}
	if settings.IdleCadence < settings.ActiveCadence {
		return GridSettings{}, fmt.Errorf("%s may not be shorter than %s", EnvGridIdleCadence, EnvGridActiveCadence)
	}
	if raw, ok := lookup(EnvGridFreshnessCeiling); ok && strings.TrimSpace(raw) != "" {
		settings.FreshnessCeiling, err = gridCadence(strings.TrimSpace(raw))
		if err != nil {
			return GridSettings{}, err
		}
	}
	return settings, nil
}

// gridCadence reads a cadence that may be written either as a duration ("4s",
// "1500ms") or as a bare number of seconds ("4"), because both are things an
// operator writes and neither is ambiguous.
func gridCadence(raw string) (time.Duration, error) {
	value := raw
	if _, err := strconv.Atoi(raw); err == nil {
		value = raw + "s"
	}
	cadence, err := time.ParseDuration(value)
	if err != nil || cadence < MinGridStillCadence || cadence > MaxGridStillCadence {
		return 0, fmt.Errorf(
			"%s must be a duration between %s and %s (a bare number is read as seconds), and this deployment configured %q",
			EnvGridStillCadence, MinGridStillCadence, MaxGridStillCadence, raw)
	}
	return cadence, nil
}
