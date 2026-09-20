package media

import "strings"

// The workspace's preview setting (ARC-227).
//
// The console carried a Preview Quality level and a Frame Rate as local state
// that bounded nothing: nothing on the wire carried them, the plane read no
// encode bound anywhere, and the live mirror therefore ran at the device's
// native size and frame rate with scrcpy's own default bit rate. The worst case
// of an uncapped encoder is unbounded rather than merely large, and the cost is
// in MOTION: measured on this host, two idle native-resolution streams put
// 0.9 Mbps on the wire.
//
// This is the setting's own vocabulary and the plane's own reading of it. What
// the plane DOES with it is stated where the bound is applied - an ambient
// viewer is carried at the level the workspace states, and the operator's own
// frame is carried at its own profile and never at this setting (see
// internal/edge/adb for the table and internal/edge/mirror for the mapping).
//
// The vocabulary is spelled here as well as in the device adapter's table, and
// deliberately: the adapter's table is what reaches a device, so it keeps an
// independent opinion about what a level means, and a level this package gains
// without the table gaining it is a level that resolves to the table's default
// rather than to no bound at all.

// MirrorPreviewQuality is one of the workspace's preview levels.
type MirrorPreviewQuality string

const (
	// PreviewLow is a thumbnail-sized picture.
	PreviewLow MirrorPreviewQuality = "low"
	// PreviewMedium is the level an unstated or unrecognised setting resolves
	// to.
	PreviewMedium MirrorPreviewQuality = "medium"
	// PreviewHigh is the largest downscaled level.
	PreviewHigh MirrorPreviewQuality = "high"
	// PreviewExtra is the device's own size, still bounded by a bit rate.
	PreviewExtra MirrorPreviewQuality = "extra"
)

// The workspace's frame rate setting's own bounds, which are the range the
// console's control offers.
const (
	// MinPreviewFrameRate is the slowest capture a workspace may ask for.
	MinPreviewFrameRate = 1
	// MaxPreviewFrameRate is the fastest capture a workspace may ask for.
	MaxPreviewFrameRate = 24
	// DefaultPreviewFrameRate is the rate an unstated frame rate resolves to.
	DefaultPreviewFrameRate = 15
)

// DefaultPreviewQuality is the level an unstated or unrecognised preview setting
// resolves to.
//
// It is Medium, and the reason is the grid: the plane's default device-session
// capacity lets the console's grid carry several tiles at once, and each level
// above Medium is a per-stream cost a grid multiplies (High 2.5 Mbps, Extra
// 6 Mbps against Medium's 1.2 Mbps). Medium is still a picture an operator can
// read a device's screen from, and the levels above it stay selectable.
const DefaultPreviewQuality = PreviewMedium

// MirrorPreview is the workspace's preview setting: the level and the capture
// rate an AMBIENT viewer - one of the console's grid tiles - is carried at.
//
// It is not a global setting, and that is the whole of its design: the operator's
// own big frame is where the work happens, so it is carried at its own profile
// and never at a level an operator chose for a grid of thumbnails.
type MirrorPreview struct {
	// Quality is the level ambient viewers are carried at.
	Quality MirrorPreviewQuality
	// FrameRate is the capture rate ambient viewers are carried at.
	FrameRate int
}

// DefaultPreview is the workspace's preview setting when nothing states one.
func DefaultPreview() MirrorPreview {
	return MirrorPreview{Quality: DefaultPreviewQuality, FrameRate: DefaultPreviewFrameRate}
}

// PreviewQualityFromString resolves a level's name.
//
// Anything this plane does not know - including an empty or blank name - resolves
// to the documented default rather than to no bound at all. That is the point:
// the defect this setting closes is an encoder nothing bounded, and a name this
// product has never heard of is not a licence to run uncapped. The default level
// is itself a cap, so an unrecognised name is bounded either way.
func PreviewQualityFromString(raw string) MirrorPreviewQuality {
	switch MirrorPreviewQuality(strings.ToLower(strings.TrimSpace(raw))) {
	case PreviewLow:
		return PreviewLow
	case PreviewMedium:
		return PreviewMedium
	case PreviewHigh:
		return PreviewHigh
	case PreviewExtra:
		return PreviewExtra
	default:
		return DefaultPreviewQuality
	}
}

// previewFrameRateInRange reports a capture rate this product will ask for.
func previewFrameRateInRange(rate int) bool {
	return rate >= MinPreviewFrameRate && rate <= MaxPreviewFrameRate
}
