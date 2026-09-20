package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/adb"
)

type Runner interface {
	RunAllowlisted(context.Context, string, []string) (adb.Result, error)
}

type Collector struct {
	runner Runner
	now    func() time.Time
}

func NewCollector(runner Runner) *Collector { return &Collector{runner: runner, now: time.Now} }

func (c *Collector) Collect(ctx context.Context, serial string) (devices.Diagnostics, error) {
	if c == nil || c.runner == nil {
		return devices.Diagnostics{}, errors.New("diagnostics runner is required")
	}
	diagnostic := devices.Diagnostics{ObservedAt: c.now().UTC()}
	commands := []struct {
		args  []string
		parse func(string, *devices.Diagnostics)
	}{
		{adb.DiagnosticsPropertiesArgv(), parseProperties},
		{[]string{"shell", "wm", "size"}, parseSize},
		{adb.DiagnosticsDensityArgv(), parseDensity},
		{adb.DiagnosticsBatteryArgv(), parseBattery},
		{adb.DiagnosticsStorageArgv(), parseStorage},
		{adb.DiagnosticsMemoryArgv(), parseMemory},
		{adb.DiagnosticsUptimeArgv(), parseUptime},
		{adb.DiagnosticsForegroundArgv(), parseForeground},
	}
	var succeeded int
	var failures []error
	for _, command := range commands {
		result, err := c.runner.RunAllowlisted(ctx, serial, command.args)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		command.parse(string(result.Stdout), &diagnostic)
		succeeded++
	}
	if succeeded == 0 {
		return devices.Diagnostics{}, fmt.Errorf("collect device diagnostics: %w", errors.Join(failures...))
	}
	return diagnostic, nil
}

func text(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return nil
	}
	return &value
}
func boundedUint32(value string, maximum uint64) *uint32 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
	if err != nil || parsed > maximum {
		return nil
	}
	result := uint32(parsed)
	return &result
}
func uint32Value(value string) *uint32 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
	if err != nil {
		return nil
	}
	result := uint32(parsed)
	return &result
}
func uint64Value(value string) *uint64 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseProperties(output string, diagnostic *devices.Diagnostics) {
	properties := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "]: [", 2)
		if len(parts) == 2 {
			properties[strings.TrimPrefix(parts[0], "[")] = strings.TrimSuffix(parts[1], "]")
		}
	}
	diagnostic.Brand = text(properties["ro.product.brand"])
	diagnostic.DeviceCodename = text(properties["ro.product.device"])
	diagnostic.Hardware = text(properties["ro.hardware"])
	diagnostic.AndroidVersion = text(properties["ro.build.version.release"])
	diagnostic.SDKLevel = boundedUint32(properties["ro.build.version.sdk"], 100)
}

var sizePattern = regexp.MustCompile(`(?m)(?:Override|Physical) size:\s*(\d+)x(\d+)`)

func parseSize(output string, diagnostic *devices.Diagnostics) {
	matches := sizePattern.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		match := matches[len(matches)-1]
		diagnostic.ScreenWidthPx = boundedUint32(match[1], 32768)
		diagnostic.ScreenHeightPx = boundedUint32(match[2], 32768)
	}
}

var densityPattern = regexp.MustCompile(`(?m)(?:Override|Physical) density:\s*(\d+)`)

func parseDensity(output string, diagnostic *devices.Diagnostics) {
	matches := densityPattern.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		diagnostic.DensityDPI = boundedUint32(matches[len(matches)-1][1], 2000)
	}
}

func parseBattery(output string, diagnostic *devices.Diagnostics) {
	values := colonValues(output)
	diagnostic.BatteryLevelPercent = boundedUint32(values["level"], 100)
	if raw, err := strconv.ParseFloat(values["temperature"], 64); err == nil {
		value := raw / 10
		// dumpsys battery uses sentinel values such as -200 when the
		// thermistor is unavailable. Keep those as absent rather than
		// presenting an impossible temperature as a captured fact.
		if raw != -200 && value >= -40 && value <= 100 {
			diagnostic.BatteryTemperatureCelsius = &value
		}
	}
	status := map[string]string{"1": "unknown", "2": "charging", "3": "discharging", "4": "not charging", "5": "full"}[values["status"]]
	diagnostic.BatteryStatus = text(status)
}

func colonValues(output string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

func parseStorage(output string, diagnostic *devices.Diagnostics) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return
	}
	if total := uint64Value(fields[1]); total != nil {
		value := *total * 1024
		diagnostic.StorageTotalBytes = &value
	}
	if free := uint64Value(fields[3]); free != nil {
		value := *free * 1024
		diagnostic.StorageFreeBytes = &value
	}
}

func parseMemory(output string, diagnostic *devices.Diagnostics) {
	values := colonValues(output)
	diagnostic.RAMTotalBytes = memoryBytes(values["MemTotal"])
	diagnostic.RAMFreeBytes = memoryBytes(values["MemFree"])
	diagnostic.RAMAvailableBytes = memoryBytes(values["MemAvailable"])
}
func memoryBytes(value string) *uint64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	parsed := uint64Value(fields[0])
	if parsed == nil {
		return nil
	}
	bytes := *parsed * 1024
	return &bytes
}

func parseUptime(output string, diagnostic *devices.Diagnostics) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return
	}
	parsed, err := strconv.ParseFloat(fields[0], 64)
	if err == nil && parsed >= 0 {
		value := uint64(parsed)
		diagnostic.UptimeSeconds = &value
	}
}

var foregroundPattern = regexp.MustCompile(`(?m)(?:mResumedActivity|topResumedActivity)[^\n]*?\s([A-Za-z0-9._]+)/([^\s}]+)`)

func parseForeground(output string, diagnostic *devices.Diagnostics) {
	matches := foregroundPattern.FindStringSubmatch(output)
	if len(matches) == 3 {
		diagnostic.ForegroundPackage = text(matches[1])
		diagnostic.ForegroundActivity = text(matches[2])
	}
}
