package lab

import (
	"os"
	"strings"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
)

// EnvLookup reads one configuration value. It exists so the composition seam
// below stays testable and so no domain code reads the process environment.
type EnvLookup func(key string) (string, bool)

// LabModeRequested reports whether an operator explicitly opted into
// real-device lab mode and configured an adb executable path.
func LabModeRequested(lookup EnvLookup) bool {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	optIn := firstEnv(lookup, EnvRuntimeMode, EnvLabMode)
	executable := firstEnv(lookup, EnvRuntimeADB, EnvADBPath)
	return (strings.TrimSpace(optIn) == "connected" || strings.TrimSpace(optIn) == "1") && strings.TrimSpace(executable) != ""
}

func firstEnv(lookup EnvLookup, keys ...string) string {
	for _, key := range keys {
		if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// NewServiceFromEnv is the composition seam that chooses between deterministic
// mock mode and real-device lab mode.
//
// Lab mode requires both an explicit opt-in (DRIFT_P13_LAB_MODE=1) and an
// absolute adb executable path (DRIFT_P13_ADB_PATH). Anything else yields a
// mock-mode service, so a misconfigured host never silently reaches a device.
func NewServiceFromEnv(lookup EnvLookup, opts ...Option) (*Service, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if !LabModeRequested(lookup) {
		return NewService(opts...)
	}

	executable := firstEnv(lookup, EnvRuntimeADB, EnvADBPath)
	runner, err := adb.NewProcessRunner()
	if err != nil {
		return nil, err
	}
	devices, err := adb.NewAdapter(strings.TrimSpace(executable), runner)
	if err != nil {
		return nil, err
	}
	hierarchy, err := uiautomator.New(devices)
	if err != nil {
		return nil, err
	}
	return NewService(append([]Option{WithLabAdapters(devices, hierarchy)}, opts...)...)
}
