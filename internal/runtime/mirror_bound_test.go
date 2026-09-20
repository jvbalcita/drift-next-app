package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drift.local/drift-next/internal/media"
)

// The live mirror's bound is a DEPLOYMENT input, and these tests are that claim
// from the deployment's side: the numbers live in runtime.json, they reach the
// control plane on every start path, they are reported in a startup line, and an
// inconsistent pair is refused where it is stated rather than where a viewer
// arrives.

// configuredBound is the bound the cases below state, and it is deliberately NOT
// the plane's own default: a case that configured the default could not tell
// "the deployment's input arrived" from "the plane defaulted".
func configuredBound() Config {
	return Config{
		MirrorSessionCapacity:     8,
		MirrorOperatorReserve:     2,
		MirrorTransportBudgetKbps: 24000,
	}
}

func applyBound(config *Config, bound Config) {
	config.MirrorSessionCapacity = bound.MirrorSessionCapacity
	config.MirrorOperatorReserve = bound.MirrorOperatorReserve
	config.MirrorTransportBudgetKbps = bound.MirrorTransportBudgetKbps
}

// TestEveryControlPlaneStartPathHandsTheMirrorItsBound: the TUI's launch and the
// console's troubleshooting path both build the control plane, and a deployment
// input that arrives on one and not the other is the same defect on whichever path
// was forgotten - the bound would be in force only until someone restarted a
// component.
func TestEveryControlPlaneStartPathHandsTheMirrorItsBound(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		start func(*Supervisor) error
	}{
		{
			name:  "Start All",
			start: func(supervisor *Supervisor) error { return supervisor.StartAll(context.Background(), false) },
		},
		{
			name: "Start Component",
			start: func(supervisor *Supervisor) error {
				return supervisor.StartComponent(context.Background(), "Control Plane")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			supervisor, children := newMirrorTestSupervisor(t, "/opt/homebrew/share/scrcpy/scrcpy-server")
			bound := configuredBound()
			applyBound(&supervisor.config, bound)
			if err := testCase.start(supervisor); err != nil {
				t.Fatalf("%s failed: %v", testCase.name, err)
			}
			child := controlPlaneChild(t, *children)
			for _, expected := range []struct{ key, value string }{
				{media.EnvSessionCapacity, "8"},
				{media.EnvOperatorReserve, "2"},
				{media.EnvTransportBudgetKbps, "24000"},
			} {
				value, ok := childEnvValue(child.env, expected.key)
				if !ok {
					t.Fatalf("%s did not hand the control plane %s: %v", testCase.name, expected.key, child.env)
				}
				if value != expected.value {
					t.Fatalf("%s handed %s=%q, want the configured %q", testCase.name, expected.key, value, expected.value)
				}
			}
		})
	}
}

// TestAnUnstatedBoundIsPassedAsNothingAndNamedInTheStartupLine: a deployment that
// states no bound is running on the plane's measured default, and that is only
// acceptable because it is READABLE - the input is passed as nothing rather than as
// a value this runtime invented, and the startup line names every input the
// deployment left unstated so an operator can see which default is in force.
func TestAnUnstatedBoundIsPassedAsNothingAndNamedInTheStartupLine(t *testing.T) {
	supervisor, children := newMirrorTestSupervisor(t, "/opt/homebrew/share/scrcpy/scrcpy-server")
	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	child := controlPlaneChild(t, *children)
	for _, key := range []string{
		media.EnvSessionCapacity, media.EnvOperatorReserve, media.EnvTransportBudgetKbps,
	} {
		if value, ok := childEnvValue(child.env, key); ok {
			t.Fatalf("the runtime invented a value for %s (%q); an unstated bound is passed as nothing", key, value)
		}
	}

	line := startupLineContaining(t, supervisor, "Live mirror bound")
	if !strings.Contains(line, "states none of it") {
		t.Fatalf("the startup line does not say the deployment stated no bound: %q", line)
	}
	for _, key := range []string{
		media.EnvSessionCapacity, media.EnvOperatorReserve, media.EnvTransportBudgetKbps,
	} {
		if !strings.Contains(line, key) {
			t.Fatalf("the startup line does not name the unstated input %s, so the default in force cannot be read back: %q", key, line)
		}
	}
	if !strings.Contains(line, "runtime.json") {
		t.Fatalf("the startup line does not say where the bound is stated: %q", line)
	}
}

// TestSetupStatesTheBoundThisDeploymentConfigured: the other half of readability -
// what the deployment DID state is on the frame, with the values, so a deployment
// can be read back without opening its file.
func TestSetupStatesTheBoundThisDeploymentConfigured(t *testing.T) {
	supervisor, _ := newMirrorTestSupervisor(t, "/opt/homebrew/share/scrcpy/scrcpy-server")
	bound := configuredBound()
	applyBound(&supervisor.config, bound)
	if err := supervisor.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	line := startupLineContaining(t, supervisor, "Live mirror bound")
	for _, want := range []string{
		media.EnvSessionCapacity + "=8",
		media.EnvOperatorReserve + "=2",
		media.EnvTransportBudgetKbps + "=24000",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the startup line does not state %q, so the bound in force cannot be read back: %q", want, line)
		}
	}
	if strings.Contains(line, "states none of it") {
		t.Fatalf("a deployment that stated every input is reported as stating none: %q", line)
	}
}

// startupLineContaining returns the one startup line that carries the marker, and
// fails when there is not exactly one - a bound reported twice is a bound an
// operator has to reconcile.
func startupLineContaining(t *testing.T, supervisor *Supervisor, marker string) string {
	t.Helper()
	var found []string
	for _, line := range supervisor.Logs() {
		if strings.Contains(line, marker) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d startup line(s) carrying %q, want exactly one: %v", len(found), marker, supervisor.Logs())
	}
	return found[0]
}

// validTestConfig is a configuration that validates on its own, so a case that
// fails does so for the bound it stated and nothing else.
func validTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ControlPlaneAddress: "127.0.0.1:18080",
		EdgeAgentAddress:    "127.0.0.1:18081",
		DatabasePath:        filepath.Join(t.TempDir(), "control-plane.db"),
		ArtifactRoot:        t.TempDir(),
		OperatorID:          "operator-local",
		ServiceToken:        "secret-token",
	}
}

// TestAnInconsistentBoundIsRefusedWhereTheDeploymentStatesIt is the card's own
// acceptance case: the pair is refused at composition, with a named reason, rather
// than carried into a plane that can never show a tile picture.
func TestAnInconsistentBoundIsRefusedWhereTheDeploymentStatesIt(t *testing.T) {
	cases := []struct {
		name  string
		bound Config
		wants []string
	}{
		{
			name:  "a reserve that is the whole capacity",
			bound: Config{MirrorSessionCapacity: 4, MirrorOperatorReserve: 4},
			wants: []string{"mirror_operator_reserve", "4", "mirror_session_capacity"},
		},
		{
			name:  "a reserve above the capacity",
			bound: Config{MirrorSessionCapacity: 2, MirrorOperatorReserve: 5},
			wants: []string{"mirror_operator_reserve", "5", "mirror_session_capacity", "2"},
		},
		{
			// A deployment that states ONLY a capacity of one is refused as the pair
			// the plane will actually carry: the reserve it will resolve to is the
			// documented one, so this is a reserve that is the whole capacity.
			name:  "a capacity of one with no reserve stated",
			bound: Config{MirrorSessionCapacity: 1},
			wants: []string{"mirror_operator_reserve", "1", "mirror_session_capacity"},
		},
		{
			name:  "a capacity below one",
			bound: Config{MirrorSessionCapacity: -1, MirrorOperatorReserve: 1},
			wants: []string{"mirror_session_capacity", "-1"},
		},
		{
			name:  "a budget below one",
			bound: Config{MirrorSessionCapacity: 4, MirrorOperatorReserve: 1, MirrorTransportBudgetKbps: -1},
			wants: []string{"mirror_transport_budget_kbps", "-1"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := validTestConfig(t)
			applyBound(&config, testCase.bound)
			err := config.validate()
			if err == nil {
				t.Fatalf("%s was accepted: this deployment would carry a bound nobody can mean", testCase.name)
			}
			for _, want := range testCase.wants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal does not name %q, so an operator cannot see which number is the mistake: %v", want, err)
				}
			}
		})
	}
}

// TestAFreshDeploymentStatesTheMeasuredBoundInItsOwnFile: a deployment created by
// this runtime states the measured bound EXPLICITLY in runtime.json rather than
// inheriting it invisibly - the inputs live where the deployment's other inputs
// live, so the file an operator opens is the file that says what the grid carries.
func TestAFreshDeploymentStatesTheMeasuredBoundInItsOwnFile(t *testing.T) {
	dataDir := t.TempDir()
	config, err := LoadOrCreate(dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if config.MirrorSessionCapacity != media.DefaultMirrorSessionCapacity ||
		config.MirrorOperatorReserve != media.DefaultOperatorReserve ||
		config.MirrorTransportBudgetKbps != media.DefaultTransportBudgetKbps {
		t.Fatalf("a fresh deployment states capacity %d reserve %d budget %d, want the measured bound (%d, %d, %d)",
			config.MirrorSessionCapacity, config.MirrorOperatorReserve, config.MirrorTransportBudgetKbps,
			media.DefaultMirrorSessionCapacity, media.DefaultOperatorReserve, media.DefaultTransportBudgetKbps)
	}

	raw, err := os.ReadFile(filepath.Join(dataDir, "runtime.json"))
	if err != nil {
		t.Fatalf("reading the deployment's own file: %v", err)
	}
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the deployment's file is not JSON: %v", err)
	}
	for _, key := range []string{
		"mirror_session_capacity", "mirror_operator_reserve", "mirror_transport_budget_kbps",
	} {
		if _, ok := written[key]; !ok {
			t.Fatalf("the deployment's file does not state %q, so the bound in force is inherited rather than written down: %s", key, raw)
		}
	}
}

// TestANonNumericBoundInTheDeploymentsFileIsRefused: the inputs are numbers, and a
// file that states one as text is a bound nobody meant rather than a bound the
// runtime silently drops - the parse fails where the deployment is read, naming the
// file it came from.
func TestANonNumericBoundInTheDeploymentsFileIsRefused(t *testing.T) {
	dataDir := t.TempDir()
	config, err := LoadOrCreate(dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	config.MirrorSessionCapacity = 0
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encoding the deployment's file: %v", err)
	}
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("the deployment's file is not JSON: %v", err)
	}
	// "four" is what an operator typing the number into the wrong field writes.
	written["mirror_session_capacity"] = "four"
	edited, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("encoding the edited file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "runtime.json"), edited, 0o600); err != nil {
		t.Fatalf("writing the edited file: %v", err)
	}
	if _, err := LoadOrCreate(dataDir); err == nil {
		t.Fatal("a deployment whose capacity is not a number was accepted: the plane would carry a bound nobody stated")
	} else if !strings.Contains(err.Error(), "mirror_session_capacity") {
		t.Fatalf("the refusal does not name the input that is wrong, so an operator cannot fix it: %v", err)
	}
}
