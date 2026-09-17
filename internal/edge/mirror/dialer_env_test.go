package mirror

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/scrcpy"
)

// These tests cover the deployment seam: the point at which a host's own
// configuration either arms the live mirror or produces the reason it is not
// armed. Nothing here touches a device - the adb executable and the server are
// regular files in a temporary directory - because what is being tested is which
// inputs the seam requires and what it refuses, not what a session does with
// them.

// seamRunner is the device's own allow-listed runner as the seam sees it. It is
// never called: the seam builds a dialer, it does not dial.
type seamRunner struct{}

func (seamRunner) RunAllowlisted(context.Context, string, []string) (adb.Result, error) {
	return adb.Result{}, nil
}

// seamLookup is a deployment's environment as a map, so each case can state
// exactly what was configured.
func seamLookup(values map[string]string) lab.EnvLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// seamFile writes one file into the test's own directory and returns its path.
func seamFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("seam fixture"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// seamDir creates a directory named after the server and returns its path, which
// is the misconfiguration where the operator pointed at the directory the server
// lives in rather than at the server.
func seamDir(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	return path
}

// TestDialerFromEnvRefusesEveryMissingInput is the seam's whole job: each input
// the mirror needs is checked where the deployment configured it, with a reason
// that names what is missing. An engine armed without these is a mirror that
// looks configured and shows nothing.
func TestDialerFromEnvRefusesEveryMissingInput(t *testing.T) {
	dir := t.TempDir()
	adbPath := seamFile(t, dir, "adb")
	serverPath := seamFile(t, dir, "scrcpy-server")
	configured := func(overrides map[string]string) map[string]string {
		values := map[string]string{lab.EnvRuntimeADB: adbPath, EnvServerPath: serverPath}
		for key, value := range overrides {
			if value == "" {
				delete(values, key)
				continue
			}
			values[key] = value
		}
		return values
	}

	cases := []struct {
		name   string
		env    map[string]string
		runner scrcpy.Runner
		want   string
	}{
		{
			name:   "no adb path at all",
			env:    map[string]string{EnvServerPath: serverPath},
			runner: seamRunner{},
			want:   lab.EnvRuntimeADB,
		},
		{
			name:   "no scrcpy server path",
			env:    configured(map[string]string{EnvServerPath: ""}),
			runner: seamRunner{},
			want:   EnvServerPath,
		},
		{
			name:   "an adb path that is not a readable executable",
			env:    configured(map[string]string{lab.EnvRuntimeADB: filepath.Join(dir, "no-such-adb")}),
			runner: seamRunner{},
			want:   "executable",
		},
		{
			name:   "a server path that is not there",
			env:    configured(map[string]string{EnvServerPath: filepath.Join(dir, "gone", "scrcpy-server")}),
			runner: seamRunner{},
			want:   EnvServerPath,
		},
		{
			name:   "a relative server path",
			env:    configured(map[string]string{EnvServerPath: "scrcpy-server"}),
			runner: seamRunner{},
			want:   "absolute",
		},
		{
			name:   "a directory where the server should be",
			env:    configured(map[string]string{EnvServerPath: seamDir(t, filepath.Join(dir, "srv"), "scrcpy-server")}),
			runner: seamRunner{},
			want:   "directory",
		},
		{
			name:   "a file that is not named like the server",
			env:    configured(map[string]string{EnvServerPath: seamFile(t, dir, "payload")}),
			runner: seamRunner{},
			want:   "scrcpy-server",
		},
		{
			name:   "a server path that walks up out of its directory",
			env:    configured(map[string]string{EnvServerPath: dir + string(filepath.Separator) + ".." + string(filepath.Separator) + "scrcpy-server"}),
			runner: seamRunner{},
			want:   "traversal",
		},
		{
			name:   "no allow-listed runner",
			env:    configured(nil),
			runner: nil,
			want:   "allow-listed runner",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer, err := NewDialerFromEnv(seamLookup(tc.env), tc.runner)
			if err == nil {
				t.Fatalf("the seam armed a dialer with %s", tc.name)
			}
			if dialer != nil {
				t.Fatalf("the seam returned a dialer as well as an error: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not name %q: %v", tc.want, err)
			}
		})
	}
}

// TestDialerFromEnvBuildsTheDialerFromWhatTheDeploymentConfigured pins the other
// side of the seam: what was configured is what the dialer reaches a device with.
func TestDialerFromEnvBuildsTheDialerFromWhatTheDeploymentConfigured(t *testing.T) {
	dir := t.TempDir()
	adbPath := seamFile(t, dir, "adb")
	serverPath := seamFile(t, dir, "scrcpy-server-v4.1")

	dialer, err := NewDialerFromEnv(seamLookup(map[string]string{
		lab.EnvRuntimeADB: adbPath,
		EnvServerPath:     "  " + serverPath + "  ",
	}), seamRunner{})
	if err != nil {
		t.Fatalf("NewDialerFromEnv: %v", err)
	}
	if dialer.config.ADB != adbPath {
		t.Fatalf("the dialer would run %q, want the configured adb %q", dialer.config.ADB, adbPath)
	}
	if dialer.config.ServerPath != serverPath {
		t.Fatalf("the dialer would push %q, want the configured server %q (and without the whitespace around it)", dialer.config.ServerPath, serverPath)
	}
	if dialer.config.Runner == nil {
		t.Fatal("the dialer was built without the device's allow-listed runner")
	}
	if dialer.config.Starter == nil {
		t.Fatal("the dialer was built without a starter for the device-side server")
	}
	if dialer.config.Start == nil {
		t.Fatal("the dialer was built without a way to open a session")
	}
	// The screen hold defaults to ON, and that default is the honest one: a
	// sleeping device produces no frames at all, so a mirror that did not hold
	// the screen would be a black rectangle with nothing to report.
	if !dialer.config.KeepAwake {
		t.Fatal("the dialer does not hold the device's screen on, so a sleeping device would stream nothing")
	}

	// A deployment that configured the hold off gets it off, from any of the
	// spellings an environment can carry.
	for _, value := range []string{"0", "false", "no", "off"} {
		off, offErr := NewDialerFromEnv(seamLookup(map[string]string{
			lab.EnvRuntimeADB: adbPath,
			EnvServerPath:     serverPath,
			EnvKeepAwake:      value,
		}), seamRunner{})
		if offErr != nil {
			t.Fatalf("NewDialerFromEnv with %s=%s: %v", EnvKeepAwake, value, offErr)
		}
		if off.config.KeepAwake {
			t.Fatalf("%s=%s still held the device's screen on", EnvKeepAwake, value)
		}
	}
	for _, value := range []string{"1", "true", "yes", "on", "TRUE"} {
		on, onErr := NewDialerFromEnv(seamLookup(map[string]string{
			lab.EnvRuntimeADB: adbPath,
			EnvServerPath:     serverPath,
			EnvKeepAwake:      value,
		}), seamRunner{})
		if onErr != nil {
			t.Fatalf("NewDialerFromEnv with %s=%s: %v", EnvKeepAwake, value, onErr)
		}
		if !on.config.KeepAwake {
			t.Fatalf("%s=%s turned the device's screen hold off", EnvKeepAwake, value)
		}
	}
}

// TestDialerFromEnvRefusesAnUnreadableScreenHoldDecision: a value that is neither
// on nor off is refused rather than guessed, because the two values mean
// different things on the device and an operator cannot act on "probably".
func TestDialerFromEnvRefusesAnUnreadableScreenHoldDecision(t *testing.T) {
	dir := t.TempDir()
	dialer, err := NewDialerFromEnv(seamLookup(map[string]string{
		lab.EnvRuntimeADB: seamFile(t, dir, "adb"),
		EnvServerPath:     seamFile(t, dir, "scrcpy-server"),
		EnvKeepAwake:      "perhaps",
	}), seamRunner{})
	if err == nil {
		t.Fatal("an unreadable screen-hold value armed a dialer")
	}
	if dialer != nil {
		t.Fatalf("the seam returned a dialer as well as an error: %v", err)
	}
	if !strings.Contains(err.Error(), EnvKeepAwake) {
		t.Fatalf("the refusal does not name %s: %v", EnvKeepAwake, err)
	}
}
