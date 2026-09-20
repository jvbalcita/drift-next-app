package adb

const (
	diagnosticsPropertiesOperation = "diagnostics-properties"
	diagnosticsBatteryOperation    = "diagnostics-battery"
	diagnosticsStorageOperation    = "diagnostics-storage"
	diagnosticsMemoryOperation     = "diagnostics-memory"
	diagnosticsUptimeOperation     = "diagnostics-uptime"
	diagnosticsDensityOperation    = "diagnostics-density"
	diagnosticsForegroundOperation = "diagnostics-foreground"
)

func DiagnosticsPropertiesArgv() []string { return []string{"shell", "getprop"} }
func DiagnosticsBatteryArgv() []string    { return []string{"shell", "dumpsys", "battery"} }
func DiagnosticsStorageArgv() []string    { return []string{"shell", "df", "-k", "/data"} }
func DiagnosticsMemoryArgv() []string     { return []string{"shell", "cat", "/proc/meminfo"} }
func DiagnosticsUptimeArgv() []string     { return []string{"shell", "cat", "/proc/uptime"} }
func DiagnosticsDensityArgv() []string    { return []string{"shell", "wm", "density"} }
func DiagnosticsForegroundArgv() []string {
	return []string{"shell", "dumpsys", "activity", "activities"}
}

func matchesDiagnosticsAllowlist(args []string) (string, bool) {
	switch {
	case equalArgv(args, []string{"shell", "getprop"}):
		return diagnosticsPropertiesOperation, true
	case equalArgv(args, []string{"shell", "dumpsys", "battery"}):
		return diagnosticsBatteryOperation, true
	case equalArgv(args, []string{"shell", "df", "-k", "/data"}):
		return diagnosticsStorageOperation, true
	case equalArgv(args, []string{"shell", "cat", "/proc/meminfo"}):
		return diagnosticsMemoryOperation, true
	case equalArgv(args, []string{"shell", "cat", "/proc/uptime"}):
		return diagnosticsUptimeOperation, true
	case equalArgv(args, []string{"shell", "wm", "density"}):
		return diagnosticsDensityOperation, true
	case equalArgv(args, []string{"shell", "dumpsys", "activity", "activities"}):
		return diagnosticsForegroundOperation, true
	default:
		return "", false
	}
}
