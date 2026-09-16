package lab_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
)

// EnvLabSerial is the explicit serial an operator names for the Phase 13
// one-device slice. It is deliberately separate from the two lab-mode gates so
// that enabling lab mode can never, by itself, select a target.
//
// See tests/compatibility/adb/README.md for the full procedure and the safety
// rules this test enforces in code.
const EnvLabSerial = "DRIFT_P13_LAB_SERIAL"

const realDeviceOperator = "operator-p13-lab"

// TestRealDeviceReadOnlyObservation exercises the read-only vertical slice
// against one explicitly confirmed lab device.
//
// It is opt-in and skips unless every gate is satisfied: DRIFT_P13_LAB_MODE=1,
// DRIFT_P13_ADB_PATH pointing at a real absolute adb executable, and
// DRIFT_P13_LAB_SERIAL naming the target. CI sets none of these.
//
// Everything it does is observation: enumerate, validate, health, screenshot,
// and hierarchy dump. It issues no input, changes no settings, installs
// nothing, and establishes no transport. The only device-side write is the
// adapter-owned temporary hierarchy file, which the adapter always removes.
func TestRealDeviceReadOnlyObservation(t *testing.T) {
	serial := requireRealDeviceGates(t)

	service, err := lab.NewServiceFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("NewServiceFromEnv() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if mode := service.Status(ctx).Mode; mode != lab.ModeLab {
		t.Fatalf("service mode = %q, want %q: the lab gates were set but the service did not bind a real adapter", mode, lab.ModeLab)
	}

	// The capture names its target explicitly. The service enumerates, matches
	// the named serial exactly, and fails closed when it is absent, ambiguous,
	// or unusable, so this test never has to fall back to a heuristic: with
	// several transports attached, naming the serial is the whole safeguard.
	bundle, err := service.CaptureObservation(ctx, lab.CaptureRequest{
		Serial:         serial,
		IdempotencyKey: "p13-compat-" + time.Now().UTC().Format("20060102T150405Z"),
		Timeout:        60 * time.Second,
		OperatorID:     realDeviceOperator,
	})
	if err != nil {
		// A classified failure is a legitimate observation outcome, not a test
		// bug. Report it with its class so the compatibility matrix can record
		// what actually happened instead of an invented result.
		t.Fatalf("CaptureObservation() error = %v (failure class %q, indeterminate=%t)", err, bundle.FailureClass, bundle.Indeterminate)
	}
	t.Logf("captured %s read-only; no device was registered and no state was changed", maskSerial(bundle.Serial))

	assertObservationBundle(t, bundle, serial)
	logSanitizedBundle(t, bundle)
}

// requireRealDeviceGates skips unless the operator explicitly opted in, pointed
// at a real adb executable, and named a target. Any partial configuration skips
// rather than guessing.
func requireRealDeviceGates(t *testing.T) string {
	t.Helper()

	if mode := strings.TrimSpace(os.Getenv(lab.EnvLabMode)); mode != "1" {
		t.Skipf("skipping real-device test: %s must equal 1 (opt-in)", lab.EnvLabMode)
	}

	executable := strings.TrimSpace(os.Getenv(lab.EnvADBPath))
	if executable == "" {
		t.Skipf("skipping real-device test: %s is not set", lab.EnvADBPath)
	}
	if !filepath.IsAbs(executable) {
		t.Skipf("skipping real-device test: %s must be an absolute path", lab.EnvADBPath)
	}
	info, err := os.Stat(executable)
	if err != nil {
		t.Skipf("skipping real-device test: %s is not reachable", lab.EnvADBPath)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skipf("skipping real-device test: %s does not point at an executable file", lab.EnvADBPath)
	}

	serial := strings.TrimSpace(os.Getenv(EnvLabSerial))
	if serial == "" {
		t.Skipf("skipping real-device test: %s must name the target explicitly; a target is never inferred", EnvLabSerial)
	}
	if err := adb.ValidateSerial(serial); err != nil {
		t.Fatalf("%s is not a valid device serial: %v", EnvLabSerial, err)
	}
	return serial
}

// assertObservationBundle checks the read-only postconditions the slice
// promises. It never asserts a latency figure: real-device timing is measured
// and recorded in tests/compatibility/adb/matrix.md, not hard-coded here.
func assertObservationBundle(t *testing.T, bundle lab.ObservationBundle, serial string) {
	t.Helper()

	if bundle.Serial != serial {
		t.Fatal("observation bundle does not describe the explicitly named target")
	}
	if bundle.Indeterminate {
		t.Fatal("observation bundle is indeterminate; it must not be replayed automatically")
	}
	if !bundle.PostconditionVerified {
		t.Fatalf("observation postcondition was not verified (failure class %q)", bundle.FailureClass)
	}
	if bundle.FailureClass != "" {
		t.Fatalf("verified bundle still carries failure class %q", bundle.FailureClass)
	}
	if bundle.HealthState != string(adb.StateDevice) {
		t.Fatalf("health state = %q, want %q", bundle.HealthState, adb.StateDevice)
	}
	if bundle.ScreenshotHash == "" || bundle.ScreenshotBytes == 0 {
		t.Fatal("screenshot evidence is missing its content hash or byte count")
	}
	if bundle.ScreenshotMediaType != lab.ScreenshotMediaType {
		t.Fatalf("screenshot media type = %q, want %q", bundle.ScreenshotMediaType, lab.ScreenshotMediaType)
	}
	if !bundle.HierarchyComplete || bundle.NodeCount == 0 {
		t.Fatalf("hierarchy is not complete: %s", bundle.HierarchySummary)
	}
	if bundle.HierarchyFreshnessToken == "" {
		t.Fatal("hierarchy capture is missing its freshness token")
	}
	if bundle.StableIdentity == "" || !strings.HasPrefix(bundle.StableIdentity, lab.StableIdentityPrefix) {
		t.Fatalf("bundle stable identity %q does not carry the lab prefix", bundle.StableIdentity)
	}
	if bundle.AdapterVersion == "" || bundle.PlatformToolsVersion == "" {
		t.Fatal("bundle must attribute the observation to an adapter and platform-tools version")
	}
}

// logSanitizedBundle records what the matrix needs without leaking the target's
// identity, its address, its screen contents, or any observed text. Serials are
// masked, screen bytes are referenced by hash only, and the hierarchy appears
// as bounded counts.
func logSanitizedBundle(t *testing.T, bundle lab.ObservationBundle) {
	t.Helper()

	t.Logf("serial=%s identity=%s adapter=%s platform-tools=%s",
		maskSerial(bundle.Serial),
		maskSerial(bundle.StableIdentity),
		bundle.AdapterVersion,
		bundle.PlatformToolsVersion,
	)
	t.Logf("health=%s screenshot=%s bytes=%d preview_truncated=%t",
		bundle.HealthState,
		bundle.ScreenshotHash,
		bundle.ScreenshotBytes,
		bundle.PreviewTruncated,
	)
	t.Logf("hierarchy: %s (nodes=%d depth=%d complete=%t)",
		bundle.HierarchySummary,
		bundle.NodeCount,
		bundle.MaxDepth,
		bundle.HierarchyComplete,
	)
	t.Logf("latency_ms=%d postcondition_verified=%t", bundle.LatencyMs, bundle.PostconditionVerified)

	for _, event := range bundle.Events {
		t.Logf("event name=%s class=%s serial=%s", event.Name, event.FailureClass, maskSerial(event.Serial))
	}
}

// maskSerial replaces a real serial or wireless address with a stable
// pseudonym. The digest is truncated so the value is correlatable within one
// run without being reversible into an address or a device identity.
func maskSerial(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "serial-" + hex.EncodeToString(sum[:4])
}
