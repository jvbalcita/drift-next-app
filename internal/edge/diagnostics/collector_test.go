package diagnostics

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

type scriptedRunner struct {
	outputs map[string]string
	fail    map[string]bool
	calls   [][]string
}

func (r *scriptedRunner) RunAllowlisted(_ context.Context, _ string, argv []string) (adb.Result, error) {
	r.calls = append(r.calls, append([]string(nil), argv...))
	key := argvKey(argv)
	if r.fail[key] {
		return adb.Result{}, errors.New("unavailable")
	}
	return adb.Result{Stdout: []byte(r.outputs[key])}, nil
}

func argvKey(argv []string) string { return fmtArgs(argv) }
func fmtArgs(argv []string) string {
	result := ""
	for _, arg := range argv {
		result += "\x00" + arg
	}
	return result
}

func TestCollectorProjectsEveryDiagnosticFactAndUsesFixedArgv(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{
		argvKey(adb.DiagnosticsPropertiesArgv()): "[ro.product.brand]: [samsung]\n[ro.product.device]: [beyond2q]\n[ro.hardware]: [qcom]\n[ro.build.version.release]: [16]\n[ro.build.version.sdk]: [36]\n",
		argvKey([]string{"shell", "wm", "size"}): "Physical size: 1440x3040\nOverride size: 1080x2280\n",
		argvKey(adb.DiagnosticsDensityArgv()):    "Physical density: 560\nOverride density: 420\n",
		argvKey(adb.DiagnosticsBatteryArgv()):    "level: 87\ntemperature: 321\nstatus: 2\n",
		argvKey(adb.DiagnosticsStorageArgv()):    "Filesystem 1K-blocks Used Available Use% Mounted on\n/data 1000 250 750 25% /data\n",
		argvKey(adb.DiagnosticsMemoryArgv()):     "MemTotal: 8000 kB\nMemFree: 1000 kB\nMemAvailable: 3000 kB\n",
		argvKey(adb.DiagnosticsUptimeArgv()):     "12345.67 100.00\n",
		argvKey(adb.DiagnosticsForegroundArgv()): "mResumedActivity: ActivityRecord{x u0 com.example/.MainActivity t1}\n",
	}}
	collector := NewCollector(runner)
	observed := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	collector.now = func() time.Time { return observed }

	got, err := collector.Collect(context.Background(), "SERIAL")
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservedAt != observed || got.Brand == nil || *got.Brand != "samsung" || got.DeviceCodename == nil || *got.DeviceCodename != "beyond2q" || got.Hardware == nil || *got.Hardware != "qcom" || got.AndroidVersion == nil || *got.AndroidVersion != "16" {
		t.Fatalf("identity projection = %#v", got)
	}
	if got.SDKLevel == nil || *got.SDKLevel != 36 || got.ScreenWidthPx == nil || *got.ScreenWidthPx != 1080 || got.ScreenHeightPx == nil || *got.ScreenHeightPx != 2280 || got.DensityDPI == nil || *got.DensityDPI != 420 {
		t.Fatalf("platform projection = %#v", got)
	}
	if got.BatteryLevelPercent == nil || *got.BatteryLevelPercent != 87 || got.BatteryTemperatureCelsius == nil || *got.BatteryTemperatureCelsius != 32.1 || got.BatteryStatus == nil || *got.BatteryStatus != "charging" {
		t.Fatalf("battery projection = %#v", got)
	}
	if got.StorageTotalBytes == nil || *got.StorageTotalBytes != 1024000 || got.StorageFreeBytes == nil || *got.StorageFreeBytes != 768000 || got.RAMTotalBytes == nil || *got.RAMTotalBytes != 8192000 || got.RAMFreeBytes == nil || *got.RAMFreeBytes != 1024000 || got.RAMAvailableBytes == nil || *got.RAMAvailableBytes != 3072000 {
		t.Fatalf("capacity projection = %#v", got)
	}
	if got.UptimeSeconds == nil || *got.UptimeSeconds != 12345 || got.ForegroundPackage == nil || *got.ForegroundPackage != "com.example" || got.ForegroundActivity == nil || *got.ForegroundActivity != ".MainActivity" {
		t.Fatalf("runtime projection = %#v", got)
	}
	wantCalls := [][]string{adb.DiagnosticsPropertiesArgv(), {"shell", "wm", "size"}, adb.DiagnosticsDensityArgv(), adb.DiagnosticsBatteryArgv(), adb.DiagnosticsStorageArgv(), adb.DiagnosticsMemoryArgv(), adb.DiagnosticsUptimeArgv(), adb.DiagnosticsForegroundArgv()}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("argv = %#v", runner.calls)
	}
}

func TestCollectorRetainsPartialFactsAndBoundsUntrustedValues(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{
		argvKey(adb.DiagnosticsPropertiesArgv()): "[ro.product.brand]: [samsung]\n[ro.build.version.sdk]: [999]\n",
		argvKey(adb.DiagnosticsBatteryArgv()):    "level: 101\ntemperature: invalid\nstatus: 99\n",
		argvKey([]string{"shell", "wm", "size"}): "Physical size: 99999x3040\n",
	}, fail: map[string]bool{argvKey(adb.DiagnosticsMemoryArgv()): true}}
	got, err := NewCollector(runner).Collect(context.Background(), "SERIAL")
	if err != nil {
		t.Fatal(err)
	}
	if got.Brand == nil || *got.Brand != "samsung" {
		t.Fatalf("brand = %#v", got.Brand)
	}
	if got.SDKLevel != nil || got.BatteryLevelPercent != nil || got.BatteryTemperatureCelsius != nil || got.BatteryStatus != nil || got.ScreenWidthPx != nil {
		t.Fatalf("unbounded value accepted: %#v", got)
	}
}

func TestCollectorTreatsBatteryTemperatureSentinelAsUnreported(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{
		argvKey(adb.DiagnosticsBatteryArgv()): "level: 83\ntemperature: -200\nstatus: 3\n",
	}}
	got, err := NewCollector(runner).Collect(context.Background(), "SERIAL")
	if err != nil {
		t.Fatal(err)
	}
	if got.BatteryLevelPercent == nil || *got.BatteryLevelPercent != 83 || got.BatteryStatus == nil || *got.BatteryStatus != "discharging" {
		t.Fatalf("battery facts = %#v", got)
	}
	if got.BatteryTemperatureCelsius != nil {
		t.Fatalf("sentinel battery temperature was reported: %#v", got.BatteryTemperatureCelsius)
	}
}

func TestCollectorFailsOnlyWhenEveryCommandFails(t *testing.T) {
	runner := &scriptedRunner{fail: map[string]bool{}}
	for _, argv := range [][]string{adb.DiagnosticsPropertiesArgv(), {"shell", "wm", "size"}, adb.DiagnosticsDensityArgv(), adb.DiagnosticsBatteryArgv(), adb.DiagnosticsStorageArgv(), adb.DiagnosticsMemoryArgv(), adb.DiagnosticsUptimeArgv(), adb.DiagnosticsForegroundArgv()} {
		runner.fail[argvKey(argv)] = true
	}
	if _, err := NewCollector(runner).Collect(context.Background(), "SERIAL"); err == nil {
		t.Fatal("expected collection failure")
	}
}
