package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/scrcpy"
)

const (
	// EnvServerPath is the absolute host path of the scrcpy server binary that is
	// pushed to each device. It is a deployment input rather than a discovered
	// one: this file comes from the operator's scrcpy installation, its version
	// is pinned by the allow-list (only a 4.1 server's launch is admitted), and a
	// deployment without it must fail where it is configured rather than after
	// pushing an empty file onto a device.
	EnvServerPath = "DRIFT_MIRROR_SCRCPY_SERVER"

	// EnvKeepAwake turns the device-screen hold on or off. It defaults to on
	// because a sleeping device produces no frames at all: a mirror of one is a
	// black rectangle with nothing to report. A deployment that wants it off -
	// because holding an operator's screen on is a side effect it will not take -
	// sets this to 0, false or no and says so, which is what the dialer's own
	// contract requires: the choice is explicit, never inherited.
	EnvKeepAwake = "DRIFT_MIRROR_KEEP_AWAKE"
)

// NewDialerFromEnv builds the live mirror's dialer from the deployment's own
// configuration, or reports which input is missing.
//
// It fails closed and it fails EARLY. Every input it needs is checked here, at
// the point an operator configured it, rather than at the moment a viewer opens
// a mirror: an engine that exists, is armed, and cannot dial is worse than an
// engine that was never armed, because the first looks like a working product
// showing nothing.
//
// The runner it is given is the device's own allow-listed runner - the same
// adapter the lab service reaches devices through - and the starter it builds is
// the adapter's own long-lived-process entry point over the same adb executable.
// Both go through the same admission, so no command the mirror issues reaches a
// device without being on the allow-list.
func NewDialerFromEnv(lookup lab.EnvLookup, runner scrcpy.Runner) (*Dialer, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if runner == nil {
		return nil, errors.New(
			"the live mirror needs the device's own allow-listed runner, and this deployment bound no device transport")
	}
	adbPath := strings.TrimSpace(lab.ConfiguredADBPath(lookup))
	if adbPath == "" {
		return nil, fmt.Errorf(
			"the live mirror needs the absolute path of adb (%s or %s), and this deployment configured neither",
			lab.EnvRuntimeADB, lab.EnvADBPath)
	}
	serverPath := strings.TrimSpace(envValue(lookup, EnvServerPath))
	if serverPath == "" {
		return nil, fmt.Errorf(
			"the live mirror needs the host path of the scrcpy server (%s): the server is pushed to each device from this file",
			EnvServerPath)
	}
	if err := validateServerPath(serverPath); err != nil {
		return nil, err
	}
	starter, err := adb.NewProcessStarter(adbPath)
	if err != nil {
		return nil, fmt.Errorf("the live mirror's device-side server could not be started: %w", err)
	}
	keepAwake, err := keepAwakeFromEnv(lookup)
	if err != nil {
		return nil, err
	}
	return NewDialer(DialerConfig{
		ADB:        adbPath,
		ServerPath: serverPath,
		Runner:     runner,
		Starter:    starter,
		KeepAwake:  keepAwake,
	})
}

// validateServerPath refuses a server path the push could not carry, and refuses
// it here rather than at the first session.
//
// The name bound is the allow-list's own (adb.IsMirrorHostServerPath) rather than
// a second copy of it: the adapter is what re-derives the bound before a device
// is touched, and this asks the same question one step earlier so a deployment
// pointing at the wrong file is a startup diagnosis instead of a mirror that
// opens and shows nothing.
func validateServerPath(serverPath string) error {
	if !filepath.IsAbs(serverPath) {
		return fmt.Errorf("%s must be an absolute path; this deployment configured %q", EnvServerPath, serverPath)
	}
	if !adb.IsMirrorHostServerPath(filepath.ToSlash(serverPath)) {
		return fmt.Errorf(
			"%s must name the scrcpy server itself - an absolute path without a traversal whose base name is scrcpy-server or scrcpy-server-v<version> - and this deployment configured %q",
			EnvServerPath, serverPath)
	}
	info, err := os.Stat(serverPath)
	if err != nil {
		return fmt.Errorf("%s names a file that cannot be read: %w", EnvServerPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s names a directory, and the server is a file", EnvServerPath)
	}
	return nil
}

// keepAwakeFromEnv reads the explicit screen-hold decision. An unreadable value
// is refused rather than guessed: "yes, probably" is not a decision an operator
// can rely on, and the two values mean different things on a device.
func keepAwakeFromEnv(lookup lab.EnvLookup) (bool, error) {
	raw, ok := lookup(EnvKeepAwake)
	if !ok || strings.TrimSpace(raw) == "" {
		return true, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be one of 1/0, true/false, yes/no or on/off; this deployment set %q", EnvKeepAwake, raw)
	}
}

// envValue reads one variable, reporting "" for an unset one.
func envValue(lookup lab.EnvLookup, key string) string {
	value, _ := lookup(key)
	return value
}
