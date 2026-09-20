package media

import (
	"strings"
	"testing"
)

// The plane's device-session capacity is a deployment input, so these cases are
// about where it is read and what a deployment that configured something
// unreadable is told. They are not about the engine's arithmetic: that the bound
// keeps the operator's place is proven against the engine in
// mirror_capacity_test.go, and this file only has to prove that the number the
// engine is built with is the number the deployment stated.

// envLookup answers a fixed environment.
func envLookup(values map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// TestSessionCapacityFallsBackToThePlanesOwnDefault: a deployment that states
// nothing gets the plane's documented default rather than a number that depends on
// what a console's grid happened to allocate, and the composition root states that
// default explicitly when it builds the engine.
func TestSessionCapacityFallsBackToThePlanesOwnDefault(t *testing.T) {
	capacity, reserve, err := SessionCapacityFromEnv(envLookup(nil))
	if err != nil {
		t.Fatalf("an unconfigured deployment was refused: %v", err)
	}
	if capacity != DefaultMirrorSessionCapacity || reserve != DefaultOperatorReserve {
		t.Fatalf("an unconfigured deployment reads capacity %d reserve %d, want the plane's own %d and %d",
			capacity, reserve, DefaultMirrorSessionCapacity, DefaultOperatorReserve)
	}
}

// TestSessionCapacityIsReadFromTheDeployment: the bound is stated at composition,
// so what the deployment configured is what the engine carries - and the console's
// share follows from it rather than from a second constant.
func TestSessionCapacityIsReadFromTheDeployment(t *testing.T) {
	capacity, reserve, err := SessionCapacityFromEnv(envLookup(map[string]string{
		EnvSessionCapacity: "9",
		EnvOperatorReserve: "2",
	}))
	if err != nil {
		t.Fatalf("a configured deployment was refused: %v", err)
	}
	if capacity != 9 || reserve != 2 {
		t.Fatalf("the deployment reads capacity %d reserve %d, want what it configured (9 and 2)", capacity, reserve)
	}
	engine, buildErr := NewMirrorEngine(MirrorEngineConfig{
		Dialer:          newFakeDialer(),
		MaxSessions:     capacity,
		OperatorReserve: reserve,
	})
	if buildErr != nil {
		t.Fatalf("NewMirrorEngine: %v", buildErr)
	}
	defer func() { _ = engine.Stop(nil) }()
	if engine.Capacity() != 9 || engine.OperatorReserve() != 2 || engine.AmbientCapacity() != 7 {
		t.Fatalf("the engine carries capacity %d reserve %d and offers the grid %d, want 9, 2 and 7",
			engine.Capacity(), engine.OperatorReserve(), engine.AmbientCapacity())
	}
}

// TestSessionCapacityRefusesAValueItCannotRead is the fail-where-it-is-configured
// rule: a bound that is not a positive whole number is a bound nobody stated, and
// carrying on with the default would hide the mistake behind a plane whose
// behaviour is inexplicable from its own configuration.
func TestSessionCapacityRefusesAValueItCannotRead(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "a capacity that is not a number", key: EnvSessionCapacity, value: "four"},
		{name: "a capacity of zero", key: EnvSessionCapacity, value: "0"},
		{name: "a negative capacity", key: EnvSessionCapacity, value: "-2"},
		{name: "a fractional capacity", key: EnvSessionCapacity, value: "4.5"},
		{name: "a reserve that is not a number", key: EnvOperatorReserve, value: "one"},
		{name: "a reserve of zero", key: EnvOperatorReserve, value: "0"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := SessionCapacityFromEnv(envLookup(map[string]string{test.key: test.value}))
			if err == nil {
				t.Fatalf("%s=%q was accepted: the deployment's bound is not a bound at all", test.key, test.value)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("the refusal does not name the input the deployment got wrong: %v", err)
			}
			if !strings.Contains(err.Error(), test.value) {
				t.Fatalf("the refusal does not name the value it read: %v", err)
			}
		})
	}
}

// TestSessionCapacityReadsAReserveThatIsTheWholeCapacity: a deployment may keep
// every session for the operator's own frames - a host for working devices rather
// than for a wall of pictures - and that is a configuration rather than a mistake,
// so it is read as configured and the engine offers the grid nothing.
func TestSessionCapacityReadsAReserveThatIsTheWholeCapacity(t *testing.T) {
	capacity, reserve, err := SessionCapacityFromEnv(envLookup(map[string]string{
		EnvSessionCapacity: "2",
		EnvOperatorReserve: "3",
	}))
	if err != nil {
		t.Fatalf("a deployment keeping every session for the operator was refused: %v", err)
	}
	if capacity != 2 || reserve != 3 {
		t.Fatalf("the deployment reads capacity %d reserve %d, want what it configured (2 and 3)", capacity, reserve)
	}
	engine, buildErr := NewMirrorEngine(MirrorEngineConfig{Dialer: newFakeDialer(), MaxSessions: capacity, OperatorReserve: reserve})
	if buildErr != nil {
		t.Fatalf("NewMirrorEngine: %v", buildErr)
	}
	defer func() { _ = engine.Stop(nil) }()
	if engine.AmbientCapacity() != 0 {
		t.Fatalf("a plane keeping every session for the operator offers the grid %d", engine.AmbientCapacity())
	}
}

// TestSessionCapacityIgnoresAnEmptyValue: an input that is present and empty is the
// same fact as one that was never set - the deployment stated nothing - and it is
// not worth refusing over, because the plane's own default is what it means.
func TestSessionCapacityIgnoresAnEmptyValue(t *testing.T) {
	capacity, reserve, err := SessionCapacityFromEnv(envLookup(map[string]string{
		EnvSessionCapacity: "  ",
		EnvOperatorReserve: "",
	}))
	if err != nil {
		t.Fatalf("an empty value was refused rather than read as unstated: %v", err)
	}
	if capacity != DefaultMirrorSessionCapacity || reserve != DefaultOperatorReserve {
		t.Fatalf("empty values read capacity %d reserve %d, want the plane's own defaults", capacity, reserve)
	}
}

// TestSessionCapacityDefaultsWhenNoLookupIsGiven: the engine's seam is usable from a
// caller that has no environment at all, and it answers with the plane's own default
// rather than panicking on a nil read.
func TestSessionCapacityDefaultsWhenNoLookupIsGiven(t *testing.T) {
	capacity, reserve, err := SessionCapacityFromEnv(nil)
	if err != nil {
		t.Fatalf("no lookup was refused: %v", err)
	}
	if capacity != DefaultMirrorSessionCapacity || reserve != DefaultOperatorReserve {
		t.Fatalf("no lookup reads capacity %d reserve %d, want the plane's own defaults", capacity, reserve)
	}
}
