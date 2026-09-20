package adb

import (
	"fmt"
	"strconv"
	"strings"
)

// The live mirror's encode bound (ARC-227).
//
// The mirror used to ask the device for nothing but a codec option: the launch
// carried no `max_size`, no `max_fps` and no `video_bit_rate`, so the device
// encoded at its own native size and frame rate with scrcpy's default bit rate,
// and the workspace's Quality and Frame Rate settings - which existed in the
// console's local state - bounded nothing at all. Measured on this host: the
// live argv carried none of the three, the encode target was the device's native
// 1080x1920 at 60 fps, and two live native-resolution streams put 0.9 Mbps on
// the wire while their screens sat idle - so the cost is in MOTION, and an
// uncapped encoder's worst case is unbounded rather than merely large.
//
// This file is the table that makes the worst case a number. It is a HARD CAP
// per level: every level states the size the encoder may produce and the bit
// rate it may spend, and a level with no cap is a level this table does not
// have. A level that is not in the table is not a level, so an unknown or
// unstated setting resolves to the default below rather than to no bound.
//
// The table lives beside the launch builder and its admission on purpose: the
// values it states are the values the allow-list admits, and the allow-list
// re-derives that set here rather than importing a second copy of it.

// MirrorPreviewLevels are the workspace's preview setting's own vocabulary, in
// the order a surface offers them.
var MirrorPreviewLevels = []string{"low", "medium", "high", "extra"}

// MirrorDefaultPreviewQuality is the level an unstated or unrecognised preview
// setting resolves to.
//
// It is Medium, and the arithmetic is the reason: at the plane's own default
// device-session capacity the grid may carry several tiles at once, and the
// levels above Medium (High at 2.5 Mbps, Extra at 6 Mbps) are per-stream costs a
// grid multiplies. Medium is 720p at 1.2 Mbps, which is a picture an operator
// can read a device's screen from, and High and Extra remain selectable for a
// deployment that wants them.
const MirrorDefaultPreviewQuality = "medium"

// The workspace's frame rate setting's own bounds. They are the range the
// console's control offers (1-24 fps), stated here because the launch's
// `max_fps` token is bounded by them.
const (
	// MirrorMinFrameRate is the slowest capture this product will ask for.
	MirrorMinFrameRate = 1
	// MirrorMaxFrameRate is the fastest capture this product will ask for.
	// Beyond it the encoder spends bits on motion the transport cannot carry to
	// a fleet's worth of tiles.
	MirrorMaxFrameRate = 24
	// MirrorDefaultFrameRate is the rate an unstated frame rate resolves to.
	MirrorDefaultFrameRate = 15
)

// The keyframe cadence each kind of viewer is carried at.
//
// The ambient interval is the shorter one and that is the whole point of the
// split: a tile that attaches waits for the next IDR before it has a first
// picture, and that wait IS the tile's "Opening" state. A grid of tiles
// attaching at once therefore pays one IDR interval each, so the grid's cadence
// is the one an operator feels. The operator's own frame is a single stream an
// operator is already looking at, so it keeps the interval this mirror has
// always asked for.
const (
	// MirrorAmbientIDRIntervalSeconds is the keyframe cadence of an ambient
	// tile's stream.
	MirrorAmbientIDRIntervalSeconds = 1
	// MirrorOperatorIDRIntervalSeconds is the keyframe cadence of the
	// operator's own frame's stream.
	MirrorOperatorIDRIntervalSeconds = 2
)

// MirrorEncodeProfile is one live stream's encode bound, in the device
// encoder's own terms: what it may produce, how often, and how many bits it may
// spend doing it.
//
// It is the value the launch builder renders as `max_size`, `max_fps`,
// `video_bit_rate` and the codec's own keyframe interval, and it is deliberately
// numbers rather than a level's name: a stream is encoded under a bound, and a
// bound that reached the builder as a name would be a second place where the
// name means something.
type MirrorEncodeProfile struct {
	// MaxSize is the largest dimension the encoder may produce. Zero is the
	// device's own size, which is what "native" means: scrcpy reads a
	// `max_size` of 0 as no downscale, so the token is still STATED and the
	// level is still bounded - by its bit rate.
	MaxSize int
	// MaxFPS is the capture rate the encoder is asked for.
	MaxFPS int
	// BitRate is the video bit rate, in bits per second.
	BitRate int
	// IDRIntervalSeconds is how often the encoder is asked for a key frame.
	IDRIntervalSeconds int
}

// mirrorPreviewLevel is one level's cap: the two numbers a level states.
type mirrorPreviewLevel struct {
	maxSize int
	bitRate int
}

// mirrorPreviewLevels is the cap each level is carried at.
//
// Every value here is admitted by the launch's allow-list (see
// matchesMirrorServerLaunch), which re-derives the same set, so a level added
// here without a matching admission is refused rather than reaching a device.
var mirrorPreviewLevels = map[string]mirrorPreviewLevel{
	"low":    {maxSize: 480, bitRate: 500_000},
	"medium": {maxSize: 720, bitRate: 1_200_000},
	"high":   {maxSize: 1080, bitRate: 2_500_000},
	// Native, and still bounded: this is the device's own size with a bit rate
	// an encoder cannot exceed, which is the only bound native size leaves to
	// state.
	"extra": {maxSize: 0, bitRate: 6_000_000},
}

// MirrorOperatorEncodeProfile is the bound the operator's own big frame is
// carried at.
//
// The big frame is where the work happens, so it takes its own profile and never
// the workspace's preview setting: a preview level an operator chose for a grid
// of thumbnails must not make the frame they are working in blurry. It is the
// level the frame's own work needs - 1080p at 2.5 Mbps, 24 fps - and its
// keyframe cadence is the one this mirror has always asked for.
var MirrorOperatorEncodeProfile = MirrorEncodeProfile{
	MaxSize:            1080,
	MaxFPS:             24,
	BitRate:            2_500_000,
	IDRIntervalSeconds: MirrorOperatorIDRIntervalSeconds,
}

// MirrorAmbientEncodeProfile builds the bound an ambient viewer of the
// workspace's preview setting is carried at.
//
// An unrecognised level is read as the default level rather than as no bound at
// all: the defect this table exists to close is an encoder nothing bounded, and
// a level this product does not know is not a licence to run uncapped. The frame
// rate is the workspace's own setting, bounded to the range the control offers,
// and a rate outside it is refused rather than clamped - a clamped rate is a
// different bound from the one that was stated, delivered silently.
func MirrorAmbientEncodeProfile(quality string, frameRate int) (MirrorEncodeProfile, error) {
	level, known := mirrorPreviewLevels[strings.ToLower(strings.TrimSpace(quality))]
	if !known {
		level = mirrorPreviewLevels[MirrorDefaultPreviewQuality]
	}
	if frameRate < MirrorMinFrameRate || frameRate > MirrorMaxFrameRate {
		return MirrorEncodeProfile{}, fmt.Errorf(
			"%w: an ambient preview frame rate must be in %d..%d, got %d",
			ErrMirrorShapeInvalid, MirrorMinFrameRate, MirrorMaxFrameRate, frameRate)
	}
	return MirrorEncodeProfile{
		MaxSize:            level.maxSize,
		MaxFPS:             frameRate,
		BitRate:            level.bitRate,
		IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds,
	}, nil
}

// MirrorPreviewBitRate reports the video bit rate cap a preview level is carried
// at, in bits per second, and false for a level this product does not have.
//
// It is exported because the cap is not only an encoder setting: it is what ONE
// live stream at that level costs the transport, so the control plane's own
// live-stream budget is derived from it (see internal/media's live-stream budget).
// The table stays HERE, where the launch builder and the allow-list read it, and
// the budget reads it from here rather than keeping a second copy of the same
// numbers - a cap that moved here without the budget moving with it would be a
// plane budgeting against a stream nobody is producing.
func MirrorPreviewBitRate(quality string) (bitsPerSecond int, known bool) {
	level, known := mirrorPreviewLevels[strings.ToLower(strings.TrimSpace(quality))]
	if !known {
		return 0, false
	}
	return level.bitRate, true
}

// Validate refuses a profile the launch could not carry, so an unbuildable bound
// is refused where it is supplied rather than after a device has been touched.
//
// A zero profile is refused rather than defaulted: a stream whose bound was never
// stated is exactly the uncapped encoder this table exists to close, so a caller
// that states nothing is told what it is missing.
func (p MirrorEncodeProfile) Validate() error {
	if p.MaxSize < 0 {
		return fmt.Errorf("%w: a maximum size cannot be negative, got %d", ErrMirrorShapeInvalid, p.MaxSize)
	}
	if p.MaxFPS < MirrorMinFrameRate || p.MaxFPS > MirrorMaxFrameRate {
		return fmt.Errorf("%w: a capture rate must be in %d..%d, got %d",
			ErrMirrorShapeInvalid, MirrorMinFrameRate, MirrorMaxFrameRate, p.MaxFPS)
	}
	if p.BitRate <= 0 {
		return fmt.Errorf("%w: a video bit rate must be positive, got %d", ErrMirrorShapeInvalid, p.BitRate)
	}
	if _, err := mirrorIDRIntervalOption(p.IDRIntervalSeconds); err != nil {
		return err
	}
	return nil
}

// mirrorIDRIntervalOption renders the codec option that asks the device's
// encoder for a key frame at this cadence.
//
// The two cadences this product asks for are admitted by the allow-list as their
// own tokens, so a third cadence is a change to that admission rather than a
// value that slips through it.
func mirrorIDRIntervalOption(seconds int) (string, error) {
	switch seconds {
	case MirrorAmbientIDRIntervalSeconds:
		return MirrorAmbientIDRIntervalOption, nil
	case MirrorOperatorIDRIntervalSeconds:
		return MirrorIDRIntervalOption, nil
	default:
		return "", fmt.Errorf("%w: a keyframe interval of %d second(s) is not one this product asks for",
			ErrMirrorShapeInvalid, seconds)
	}
}

// admittedPreviewSizes and admittedPreviewBitRates are the sets the allow-list
// admits, derived from the table above rather than spelled a second time. A
// table edit that the admission does not follow is a level the builder produces
// and the transport refuses, which is a loud failure rather than a silent one.
func admittedPreviewSizes() map[string]struct{} {
	sizes := make(map[string]struct{}, len(mirrorPreviewLevels))
	for _, level := range mirrorPreviewLevels {
		sizes[strconv.Itoa(level.maxSize)] = struct{}{}
	}
	return sizes
}

func admittedPreviewBitRates() map[string]struct{} {
	rates := make(map[string]struct{}, len(mirrorPreviewLevels))
	for _, level := range mirrorPreviewLevels {
		rates[strconv.Itoa(level.bitRate)] = struct{}{}
	}
	return rates
}

// admittedIDRIntervalOptions are the codec-option tokens the allow-list admits:
// the ambient cadence and the operator's, and nothing else.
var admittedIDRIntervalOptions = map[string]struct{}{
	MirrorAmbientIDRIntervalOption: {},
	MirrorIDRIntervalOption:        {},
}
