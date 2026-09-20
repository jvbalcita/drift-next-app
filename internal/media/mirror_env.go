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
)

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
// The reserve is checked against the capacity as well: a reserve larger than the
// capacity is read as a capacity no tile may spend, which is a legitimate
// deployment ("this host is for working devices, not for a wall of pictures") and
// is therefore reported rather than refused.
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
	// A reserve larger than the capacity is returned as it was configured: the
	// engine reads it as a capacity no tile may spend (see AmbientCapacity), which
	// is a legitimate deployment - a host for working devices rather than for a wall
	// of pictures - and not a value to refuse or to silently correct.
	return sessionCapacity, operatorReserve, nil
}
