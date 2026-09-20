package media

import (
	"fmt"
	"strconv"
	"strings"
)

// EnvLookup reads one deployment input, in the shape os.LookupEnv has.
//
// It is declared here rather than imported from the device adapter because this
// package is the one that SPENDS the bound these inputs state: the engine owns the
// capacity, so the engine's own package is where reading it, defaulting it and
// refusing a value it cannot read belong.
type EnvLookup func(key string) (string, bool)

const (
	// EnvSessionCapacity is how many devices this plane will mirror at once. It is
	// a deployment input, not a discovered fact: the bound is the host's own
	// appetite for captures and encoders, and a deployment that does not state one
	// gets DefaultMirrorSessionCapacity rather than whatever a console's grid
	// happened to allocate.
	EnvSessionCapacity = "DRIFT_MIRROR_SESSION_CAPACITY"

	// EnvOperatorReserve is how many of the capacity are kept for the operator's
	// own big frame: the console's ambient tiles may hold at most
	// `capacity - reserve` sessions between them. It must be at least one, because
	// a reserve of none is a grid that may spend the whole plane and leave the
	// operator's own frame refused - the defect this bound exists to close - and a
	// deployment is not allowed to configure that by accident or on purpose
	// without saying it somewhere else.
	EnvOperatorReserve = "DRIFT_MIRROR_OPERATOR_RESERVE"

	// EnvPreviewQuality is the workspace's preview level: what an ambient viewer
	// - one of the console's grid tiles - is carried at. It is a deployment
	// input rather than a discovered fact, and it is a BOUND rather than a
	// preference: the level it names is a hard cap on the size and the bit rate
	// every ambient stream may use.
	EnvPreviewQuality = "DRIFT_MIRROR_PREVIEW_QUALITY"

	// EnvPreviewFrameRate is the capture rate ambient viewers are carried at. It
	// is the workspace's own frame rate setting, and it is bounded to the range
	// the console's control offers.
	EnvPreviewFrameRate = "DRIFT_MIRROR_PREVIEW_FRAME_RATE"
)

// PreviewFromEnv reads the workspace's preview setting from the deployment's own
// configuration.
//
// An unset input is not an error: it answers with the documented default, which
// the composition root then states explicitly when it builds the engine, so the
// ONE place the setting is decided is still the composition root.
//
// The two inputs are read differently, and the difference is real rather than an
// oversight. A QUALITY is a name, and a name this plane does not know resolves to
// the documented default: the default is itself a level with a cap, so an
// unrecognised name is bounded, where refusing it would leave the plane with no
// setting at all - which is the unbounded state this input exists to remove. A
// FRAME RATE is a number, and a number that is not one, or is outside the range
// this product asks for, is refused where the deployment is configured: a rate of
// 300 is not a rate with a default, it is a rate nobody meant, and carrying on
// would put a bound on the wire that no operator chose.
func PreviewFromEnv(lookup EnvLookup) (MirrorPreview, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	preview := DefaultPreview()
	if raw, ok := lookup(EnvPreviewQuality); ok && strings.TrimSpace(raw) != "" {
		preview.Quality = PreviewQualityFromString(raw)
	}
	if raw, ok := lookup(EnvPreviewFrameRate); ok && strings.TrimSpace(raw) != "" {
		rate, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || !previewFrameRateInRange(rate) {
			return MirrorPreview{}, fmt.Errorf(
				"%s must be a whole number of frames per second in %d..%d, and this deployment configured %q",
				EnvPreviewFrameRate, MinPreviewFrameRate, MaxPreviewFrameRate, strings.TrimSpace(raw))
		}
		preview.FrameRate = rate
	}
	return preview, nil
}

// SessionCapacityFromEnv reads the plane's device-session bound from the
// deployment's own configuration.
//
// It fails where the deployment is configured, not where a viewer arrives: a
// capacity that is not a positive whole number, or a reserve that is not a
// positive whole number, is refused here so a mistyped bound is a startup line
// rather than a fleet of tiles that behave inexplicably. An unset input is not an
// error - it answers with the plane's documented default, which the composition
// root then states explicitly when it builds the engine, so the ONE place the
// bound is decided is still the composition root.
//
// The pair is also checked against itself, because a capacity and a reserve are
// two numbers that only mean something together. A reserve that is the whole of
// the capacity - or more than it - is REFUSED rather than read as a capacity no
// tile may spend: the plane would carry a bound whose stated grid share is zero
// and whose every ambient request is refused, which is a deployment that has
// disabled its own fleet view, and a bound that says "no pictures" has to be
// stated as such rather than arrived at by arithmetic. A deployment that means it
// states the smaller capacity it actually has.
func SessionCapacityFromEnv(lookup EnvLookup) (sessionCapacity int, operatorReserve int, err error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	sessionCapacity = DefaultMirrorSessionCapacity
	if raw, ok := lookup(EnvSessionCapacity); ok && strings.TrimSpace(raw) != "" {
		// Read as a number rather than as free text: a value that is not one is a
		// bound nobody stated, and carrying on with the default would hide it.
		capacity, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || capacity <= 0 {
			return 0, 0, fmt.Errorf("%s must be a positive whole number of device sessions, and this deployment configured %q",
				EnvSessionCapacity, strings.TrimSpace(raw))
		}
		sessionCapacity = capacity
	}
	operatorReserve = DefaultOperatorReserve
	if raw, ok := lookup(EnvOperatorReserve); ok && strings.TrimSpace(raw) != "" {
		reserve, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || reserve <= 0 {
			return 0, 0, fmt.Errorf("%s must be a positive whole number of the plane's sessions kept for the operator's own frame, and this deployment configured %q",
				EnvOperatorReserve, strings.TrimSpace(raw))
		}
		operatorReserve = reserve
	}
	// The pair has to leave the plane somewhere to be spent. A reserve that is the
	// whole capacity is a plane whose own grid may hold nothing, and it is refused
	// here with both numbers named so the operator can see which of the two is the
	// mistake.
	if operatorReserve >= sessionCapacity {
		return 0, 0, fmt.Errorf(
			"%s (%d) must be smaller than %s (%d): a reserve that is the whole capacity leaves the console's grid no place at all, so this deployment would carry a plane that can never show a tile picture",
			EnvOperatorReserve, operatorReserve, EnvSessionCapacity, sessionCapacity)
	}
	return sessionCapacity, operatorReserve, nil
}
