package devices

import "time"

// Diagnostics is bounded, non-sensitive device metadata collected from fixed
// read-only adb commands. Pointer fields preserve "not reported" separately
// from legitimate zero values.
type Diagnostics struct {
	ObservedAt                time.Time `json:"observed_at"`
	Brand                     *string   `json:"brand,omitempty"`
	DeviceCodename            *string   `json:"device_codename,omitempty"`
	Hardware                  *string   `json:"hardware,omitempty"`
	AndroidVersion            *string   `json:"android_version,omitempty"`
	SDKLevel                  *uint32   `json:"sdk_level,omitempty"`
	ScreenWidthPx             *uint32   `json:"screen_width_px,omitempty"`
	ScreenHeightPx            *uint32   `json:"screen_height_px,omitempty"`
	DensityDPI                *uint32   `json:"density_dpi,omitempty"`
	BatteryLevelPercent       *uint32   `json:"battery_level_percent,omitempty"`
	BatteryTemperatureCelsius *float64  `json:"battery_temperature_celsius,omitempty"`
	BatteryStatus             *string   `json:"battery_status,omitempty"`
	StorageTotalBytes         *uint64   `json:"storage_total_bytes,omitempty"`
	StorageFreeBytes          *uint64   `json:"storage_free_bytes,omitempty"`
	RAMTotalBytes             *uint64   `json:"ram_total_bytes,omitempty"`
	RAMFreeBytes              *uint64   `json:"ram_free_bytes,omitempty"`
	RAMAvailableBytes         *uint64   `json:"ram_available_bytes,omitempty"`
	UptimeSeconds             *uint64   `json:"uptime_seconds,omitempty"`
	ForegroundPackage         *string   `json:"foreground_package,omitempty"`
	ForegroundActivity        *string   `json:"foreground_activity,omitempty"`
}
