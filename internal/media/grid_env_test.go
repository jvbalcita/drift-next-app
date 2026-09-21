package media_test

import (
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
)

// lookupOf builds the deployment's own configuration for a test: exactly the keys
// this test states are set, and nothing else.
func lookupOf(values map[string]string) media.EnvLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// TestGridSettingsFromEnvDefaultsEveryInput pins that an unstated deployment is a
// documented configuration rather than an unbounded one: the grid the composition
// root builds always carries a cadence, a level and a sweep bound.
func TestGridSettingsFromEnvDefaultsEveryInput(t *testing.T) {
	t.Parallel()
	settings, err := media.GridSettingsFromEnv(lookupOf(nil))
	if err != nil {
		t.Fatalf("GridSettingsFromEnv() = %v", err)
	}
	if settings.Cadence != media.DefaultGridStillCadence {
		t.Fatalf("cadence = %s, want the default %s", settings.Cadence, media.DefaultGridStillCadence)
	}
	if settings.Profile.Level != media.DefaultGridStillLevel {
		t.Fatalf("level = %q, want the default %q", settings.Profile.Level, media.DefaultGridStillLevel)
	}
	if settings.MaxDevices != media.DefaultGridMaxDevices {
		t.Fatalf("max devices = %d, want the default %d", settings.MaxDevices, media.DefaultGridMaxDevices)
	}
	if settings.Profile.ByteBound != media.DefaultPreviewLimit {
		t.Fatalf("the default level's byte bound = %d, want the one-shot preview bound %d",
			settings.Profile.ByteBound, media.DefaultPreviewLimit)
	}
	report := settings.Report()
	for _, want := range []string{"grid stills", "every 4s", "medium", "device(s) per sweep"} {
		if !strings.Contains(report, want) {
			t.Fatalf("startup report %q does not state %q", report, want)
		}
	}
}

// TestGridSettingsFromEnvReadsADeploymentThatStatesItsNumbers pins the reading of
// each input, including a cadence written as a duration and as a bare number of
// seconds.
func TestGridSettingsFromEnvReadsADeploymentThatStatesItsNumbers(t *testing.T) {
	t.Parallel()
	settings, err := media.GridSettingsFromEnv(lookupOf(map[string]string{
		media.EnvGridStillCadence: "1500ms",
		media.EnvGridStillLevel:   "high",
		media.EnvGridMaxDevices:   "200",
	}))
	if err != nil {
		t.Fatalf("GridSettingsFromEnv() = %v", err)
	}
	if settings.Cadence != 1500*time.Millisecond {
		t.Fatalf("cadence = %s, want 1.5s", settings.Cadence)
	}
	if settings.Profile.Level != media.GridStillLevelHigh {
		t.Fatalf("level = %q, want high", settings.Profile.Level)
	}
	if settings.MaxDevices != 200 {
		t.Fatalf("max devices = %d, want 200", settings.MaxDevices)
	}

	bare, err := media.GridSettingsFromEnv(lookupOf(map[string]string{media.EnvGridStillCadence: "10"}))
	if err != nil {
		t.Fatalf("GridSettingsFromEnv() with a bare cadence = %v", err)
	}
	if bare.Cadence != 10*time.Second {
		t.Fatalf("a bare cadence read as %s, want 10s", bare.Cadence)
	}
}

// TestGridSettingsFromEnvRefusesTheNumbersNobodyMeant pins the difference between
// a NAME and a NUMBER in this configuration: a level this plane does not know
// resolves to the bounded default, while a cadence or a device bound that is not a
// number the product asks for stops the plane where it is configured.
//
// A cadence of zero is not a cadence with a default - it is a plane that would
// capture continuously, which is the stream the stills exist not to be - and a
// sweep bound of zero is a grid that can show nothing.
func TestGridSettingsFromEnvRefusesTheNumbersNobodyMeant(t *testing.T) {
	t.Parallel()
	refused := map[string]map[string]string{
		"a cadence of zero":                   {media.EnvGridStillCadence: "0"},
		"a cadence of zero seconds":           {media.EnvGridStillCadence: "0s"},
		"a negative cadence":                  {media.EnvGridStillCadence: "-4s"},
		"a cadence below the floor":           {media.EnvGridStillCadence: "999ms"},
		"a cadence above the ceiling":         {media.EnvGridStillCadence: "61s"},
		"a cadence that is not a duration":    {media.EnvGridStillCadence: "often"},
		"a device bound of zero":              {media.EnvGridMaxDevices: "0"},
		"a negative device bound":             {media.EnvGridMaxDevices: "-3"},
		"a device bound that is not a number": {media.EnvGridMaxDevices: "many"},
	}
	for name, values := range refused {
		settings, err := media.GridSettingsFromEnv(lookupOf(values))
		if err == nil {
			t.Fatalf("%s: GridSettingsFromEnv() = %+v, want a refusal", name, settings)
		}
		if !strings.Contains(err.Error(), media.EnvGridStillCadence) && !strings.Contains(err.Error(), media.EnvGridMaxDevices) {
			t.Fatalf("%s: refusal %q does not name the input that is wrong", name, err)
		}
	}

	// A level this plane does not know is the one input that resolves rather than
	// refuses, and it resolves to the bounded default.
	settings, err := media.GridSettingsFromEnv(lookupOf(map[string]string{media.EnvGridStillLevel: "ultra"}))
	if err != nil {
		t.Fatalf("an unknown level was refused: %v", err)
	}
	if settings.Profile.Level != media.DefaultGridStillLevel {
		t.Fatalf("an unknown level resolved to %q, want the bounded default %q", settings.Profile.Level, media.DefaultGridStillLevel)
	}
}

// TestGridSettingsFromEnvAcceptsItsOwnBounds pins the two ends of the accepted
// cadence range: a floor and a ceiling that were refused would be bounds the
// product states and does not honor.
func TestGridSettingsFromEnvAcceptsItsOwnBounds(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{media.MinGridStillCadence.String(), media.MaxGridStillCadence.String()} {
		settings, err := media.GridSettingsFromEnv(lookupOf(map[string]string{media.EnvGridStillCadence: raw}))
		if err != nil {
			t.Fatalf("a cadence of exactly %s was refused: %v", raw, err)
		}
		if settings.Cadence.String() != raw {
			t.Fatalf("cadence = %s, want %s", settings.Cadence, raw)
		}
	}
}
