package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	// The two formats this encoder accepts are registered by their own packages:
	// image.Decode reads the format from the capture's own bytes and refuses what
	// nothing registered, so the decoder's admission and the capture path's PNG
	// check are the same list rather than two copies of one.
	_ "image/png"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The fleet grid's stills.
//
// A grid tile used to be a live stream, which made the number of tiles a grid can
// draw a property of the plane's DEVICE-SESSION capacity: one tile is one place
// and one encoder, so `capacity - reserve` tiles is the ceiling at any preview
// level and "every device" is not reachable from there. A still spends neither:
// the capture path already produces one bounded screenshot per call, so a tile is
// one bounded transaction and the grid's cost is `devices x still bytes / cadence`
// rather than a session count.
//
// What a still needs that the capture does not do is the LEVEL: `adb exec-out
// screencap` returns the device's screen at its own size, and a native 1080x2280
// PNG is megabytes - a grid of those is not a preview, and the product's own
// preview bound (DefaultPreviewLimit) would refuse every one of them as
// truncated. A level therefore caps the delivered picture's width and states the
// JPEG quality it is encoded at, and this file is the one place that bound is
// applied: the levels, the arithmetic behind them, and the encoder that produces
// them.

// GridStillLevel names a still level: a hard cap on the picture one tile is
// shown. It is a bound rather than a preference, exactly as the live preview
// levels are, because the state it removes is a grid of unbounded captures.
type GridStillLevel string

const (
	// GridStillLevelLow is the level a large fleet on a slow path is carried at.
	GridStillLevelLow GridStillLevel = "low"
	// GridStillLevelMedium is the level an unstated or unrecognised setting
	// resolves to.
	GridStillLevelMedium GridStillLevel = "medium"
	// GridStillLevelHigh is the level an operator asks for when the tiles are read
	// closely rather than scanned.
	GridStillLevelHigh GridStillLevel = "high"

	// DefaultGridStillLevel is the resolved level when a deployment states none.
	DefaultGridStillLevel = GridStillLevelMedium
)

// GridStillProfile is one level's own numbers: the widest picture it may deliver,
// the quality it encodes at, and the largest still the plane will deliver at all.
//
// The three travel together because a level that stated only its name would leave
// every reader to look up what it costs, and the console states what it was shown
// rather than a label: a tile told "medium" cannot say whether its picture is 360
// or 720 pixels wide, and the difference is the whole reason a deployment chooses
// one level over another.
type GridStillProfile struct {
	// Level is the level's name, as a deployment states it.
	Level GridStillLevel
	// MaxWidth is the widest picture this level delivers. The height follows the
	// device's own aspect ratio - a portrait phone is taller than it is wide, and
	// a level that bounded both would letterbox one orientation to satisfy the
	// other.
	MaxWidth int
	// JPEGQuality is the quality the still is encoded at, stated rather than left
	// to a default: the quality IS the level's cost.
	JPEGQuality int
	// ByteBound is the largest still the plane will deliver for one tile. It is
	// the capture path's own preview bound, reused rather than widened - the grid
	// reuses the discipline the one-shot preview already applies instead of
	// inventing a second one - and a level whose stills did not fit it would be a
	// level that delivers nothing but truncations.
	ByteBound int
}

// The levels' own numbers, in one place.
//
// The widths are the sizes a tile of a fleet grid is READ at rather than round
// numbers chosen for their looks, and the quality is the level's cost. What makes
// a level deliverable at all is that the byte bound is met by SPENDING QUALITY
// rather than by refusing the picture: measured on the attached lab units, one
// 1080x2280 screen encoded at 360 px wide and q65 came out at 37 KB against the
// product's 32 KiB bound, and the same picture fitted comfortably at a lower
// quality. A level whose stills did not fit the bound would be delivered as a
// truncation with no picture at all, which is the one outcome a level exists to
// prevent (evidence: tests/compatibility/grid/evidence-2026-09-21-fleet-still-grid.md).
var gridStillLevels = map[GridStillLevel]struct {
	maxWidth    int
	jpegQuality int
}{
	GridStillLevelLow:    {maxWidth: 240, jpegQuality: 60},
	GridStillLevelMedium: {maxWidth: 360, jpegQuality: 65},
	GridStillLevelHigh:   {maxWidth: 480, jpegQuality: 70},
}

// GridStillLevelFromString reads a level's name from a deployment's own
// configuration. A name this plane does not know resolves to the documented
// default rather than to no level at all: the default is itself a level with a
// cap, so an unrecognised name is bounded, where refusing it would leave the grid
// in the unbounded state the level exists to remove (the rule the live preview
// quality already follows).
func GridStillLevelFromString(raw string) GridStillLevel {
	switch GridStillLevel(strings.ToLower(strings.TrimSpace(raw))) {
	case GridStillLevelLow:
		return GridStillLevelLow
	case GridStillLevelHigh:
		return GridStillLevelHigh
	case GridStillLevelMedium:
		return GridStillLevelMedium
	default:
		return DefaultGridStillLevel
	}
}

// GridStillProfileFor returns a level's numbers. The bound is the product's own
// preview bound in every case, so a still the plane delivers is a still the
// one-shot snapshot preview could have delivered too.
func GridStillProfileFor(level GridStillLevel) GridStillProfile {
	numbers, known := gridStillLevels[level]
	if !known {
		numbers = gridStillLevels[DefaultGridStillLevel]
		level = DefaultGridStillLevel
	}
	return GridStillProfile{
		Level:       level,
		MaxWidth:    numbers.maxWidth,
		JPEGQuality: numbers.jpegQuality,
		ByteBound:   DefaultPreviewLimit,
	}
}

// DefaultGridStillProfile is the profile a deployment that stated no level gets.
func DefaultGridStillProfile() GridStillProfile { return GridStillProfileFor(DefaultGridStillLevel) }

// TargetSize is the size this profile delivers a screen of this size at: the cap
// applied to the width, with the height following the screen's own aspect ratio.
//
// It never upscales - a device whose screen is narrower than the cap is delivered
// at its own size, because enlarging a picture adds no detail and costs bytes -
// and it never returns a zero dimension, so a caller cannot be handed a still it
// cannot encode.
func (p GridStillProfile) TargetSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	if width <= p.MaxWidth {
		return width, height
	}
	target := height * p.MaxWidth / width
	if target < 1 {
		target = 1
	}
	return p.MaxWidth, target
}

// Report renders the profile as the one line a deployment reads back: the level,
// what it caps, and the bound a still must fit.
func (p GridStillProfile) Report() string {
	return fmt.Sprintf("%s (at most %d px wide, JPEG quality %d, stills up to %d bytes)",
		p.Level, p.MaxWidth, p.JPEGQuality, p.ByteBound)
}

// StillMediaType is what the bytes an encoded still carries ARE. It is stated on
// the wire beside them rather than assumed by a reader: a console that guessed
// would paint a JPEG that arrived as a PNG as nothing at all.
const StillMediaType = "image/jpeg"

// GridStillImage is one encoded still: the bytes a console paints, the size they
// decode to, and the quality they were actually encoded at.
type GridStillImage struct {
	// JPEG is the still as it is delivered.
	JPEG []byte
	// Width and Height are the still's own size, after the level's cap.
	Width  int
	Height int
	// JPEGQuality is the quality the bytes above were encoded at. It is the
	// LEVEL's quality whenever the still fitted at it, and a lower one when the
	// level's quality did not fit the byte bound - stated because it is the reason
	// two tiles at one level can differ in sharpness, and a plane that did not say
	// so would be bounding a picture by a number it never mentioned.
	JPEGQuality int
}

// ErrStillNotAnImage reports a capture this encoder cannot turn into a still.
// It is a condition of the capture path rather than of the encoder: the one-shot
// capture validates that its payload is a PNG, so this is what a payload that got
// past that check and is still not decodable looks like.
var ErrStillNotAnImage = errors.New("the capture could not be decoded as an image")

// StillEncoder turns one device capture into the still one tile is shown.
//
// It is a seam because the transform is the only part of the grid whose cost and
// whose output can be measured without a device: a test drives the engine with a
// fake encoder and asserts what the engine does with a capture, a refusal and an
// oversize still, while the real encoder is exercised against a real capture.
type StillEncoder interface {
	EncodeStill(png []byte, profile GridStillProfile) (GridStillImage, error)
}

// ImageStillEncoder is the real encoder: the standard library's PNG decoder, an
// area-average downscale to the level's width, and a JPEG encode at the level's
// quality.
//
// It adds no image dependency on purpose. The transform is a box filter over
// integer source rectangles - exact, deterministic, and the right filter for the
// job, which is averaging many source pixels into one delivered pixel rather than
// resampling between them - so a scaling library would be a dependency whose
// output this package could not state as precisely as its own arithmetic.
type ImageStillEncoder struct{}

// MinStillJPEGQuality is the floor the encoder will not go below to fit a byte
// bound. Under it a tile's picture stops being readable at the size a grid draws
// it, so a still that cannot fit even here is reported as truncated rather than
// blurred into an unreadable one - the bound is a real limit on the wire and this
// is where the plane stops paying for it.
const MinStillJPEGQuality = 30

// stillQualityLadder is the qualities one still is tried at, in order: the level's
// own quality first, then progressively cheaper encodes down to the floor.
//
// It is a fixed, short ladder rather than a search: every rung is one bounded
// encode, so the transform's cost stays bounded whatever the screen it is given
// contains - which is what lets the still path be one bounded transaction.
func stillQualityLadder(start int) []int {
	if start < MinStillJPEGQuality {
		return []int{MinStillJPEGQuality}
	}
	ladder := []int{start}
	for quality := start * 3 / 4; quality > MinStillJPEGQuality; quality = quality * 3 / 4 {
		ladder = append(ladder, quality)
	}
	if last := ladder[len(ladder)-1]; last > MinStillJPEGQuality {
		ladder = append(ladder, MinStillJPEGQuality)
	}
	return ladder
}

// EncodeStill decodes a capture, downscales it to the profile's cap, and encodes
// it at the profile's quality.
//
// A capture that is not a decodable image is refused as a failure of the capture
// rather than delivered as an empty tile: a tile reporting "no picture" beside a
// capture that arrived but could not be read would send an operator looking at
// the device instead of at this path.
func (ImageStillEncoder) EncodeStill(capture []byte, profile GridStillProfile) (GridStillImage, error) {
	if len(capture) == 0 {
		return GridStillImage{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "a still requires a capture to encode", ErrStillNotAnImage)
	}
	decoded, format, err := image.Decode(bytes.NewReader(capture))
	if err != nil {
		return GridStillImage{}, platformerrors.Wrap(platformerrors.CodeInvalidInput,
			"a still requires a capture the plane can decode as an image", ErrStillNotAnImage)
	}
	if format != "png" && format != "jpeg" {
		// The capture path admits PNG only. A second format is accepted here
		// rather than refused so the encoder is not a second, narrower copy of a
		// rule the capture path already enforces - but what it decoded as is
		// still stated, so nothing about the delivered picture is a guess.
		return GridStillImage{}, platformerrors.Wrap(platformerrors.CodeInvalidInput,
			fmt.Sprintf("a still encodes a capture in png or jpeg, and this one decoded as %s", format), ErrStillNotAnImage)
	}
	bounds := decoded.Bounds()
	width, height := profile.TargetSize(bounds.Dx(), bounds.Dy())
	if width <= 0 || height <= 0 {
		return GridStillImage{}, platformerrors.Wrap(platformerrors.CodeInvalidInput,
			"a still requires a capture with a readable size", ErrStillNotAnImage)
	}
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	downscaleArea(decoded, scaled)
	// The still has to be DELIVERED inside the profile's byte bound, so quality is
	// what gives way when it does not fit. An encoder that stopped at the level's
	// quality would leave a device whose screen carried more detail than the level
	// budgeted for with no picture at all - which is a bound the operator pays for
	// twice. Every rung is one bounded encode, so the cost of fitting stays
	// bounded; if even the floor cannot fit, the smallest attempt is returned and
	// the engine's own truncation rule reports it rather than this encoder
	// claiming a bound it did not meet.
	var smallest GridStillImage
	for _, quality := range stillQualityLadder(profile.JPEGQuality) {
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: quality}); err != nil {
			return GridStillImage{}, platformerrors.Wrap(platformerrors.CodeInternal,
				"the still could not be encoded", err)
		}
		candidate := GridStillImage{JPEG: encoded.Bytes(), Width: width, Height: height, JPEGQuality: quality}
		if smallest.JPEG == nil || len(candidate.JPEG) < len(smallest.JPEG) {
			smallest = candidate
		}
		if profile.ByteBound <= 0 || len(candidate.JPEG) <= profile.ByteBound {
			return candidate, nil
		}
	}
	return smallest, nil
}

// downscaleArea writes src into dst by averaging the source pixels that fall in
// each destination pixel's rectangle.
//
// The rectangles tile the source exactly - destination pixel i owns the source
// columns [i*sw/dw, (i+1)*sw/dw) - so every source pixel is used once and the
// result is a function of the source alone: two runs over one capture produce one
// still, byte for byte, which is what lets a test assert the encoder's output
// rather than its plausibility.
//
// The average is taken over premultiplied components and alpha together, which is
// exact for the captures this encoder sees: a screencap is an opaque image, so
// every source pixel's alpha is fully opaque and the averaging is an average of
// the colors alone.
func downscaleArea(src image.Image, dst *image.RGBA) {
	source := src.Bounds()
	dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy()
	sw, sh := source.Dx(), source.Dy()
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	for y := 0; y < dh; y++ {
		y0 := source.Min.Y + y*sh/dh
		y1 := source.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := source.Min.X + x*sw/dw
			x1 := source.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, a := src.At(sx, sy).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
					count++
				}
			}
			if count == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(red / count >> 8),
				G: uint8(green / count >> 8),
				B: uint8(blue / count >> 8),
				A: uint8(alpha / count >> 8),
			})
		}
	}
}

// GridStillState is what a tile's still IS, in the plane's own vocabulary: the
// fact the console renders rather than a state it derives from a timestamp.
type GridStillState string

const (
	// GridStillPending is a subscribed device no capture has succeeded for yet.
	GridStillPending GridStillState = "pending"
	// GridStillCurrent is a still that was captured and that no later attempt has
	// invalidated, so it is the device's screen as of CapturedAt.
	GridStillCurrent GridStillState = "current"
	// GridStillStale is a device whose still was captured and whose LATER attempt
	// failed: the plane holds no picture that is the device's screen now.
	GridStillStale GridStillState = "stale"
	// GridStillUnavailable is a device that cannot be captured at all.
	GridStillUnavailable GridStillState = "unavailable"
)

// GridStillFailureClass is the class stated beside a still the plane could not
// capture because it has no transport to capture from. It is the same class the
// capture path uses for an observation it could not read, so a surface that groups
// by class groups this with it rather than inventing a category.
const GridStillFailureClass = domain.FailureObservation

// StillFailureClass is the class a capture this path could not turn into a still
// is recorded under.
//
// It is the one place that distinction is drawn. A capture that arrived and could
// not be read is an OBSERVATION failure - the device's own screen came back
// unusable, which is what the one-shot capture path records for the same condition
// - while a transform that failed on the host is infrastructure, because the device
// did its part and this process did not.
func StillFailureClass(err error) domain.FailureClass {
	if platformerrors.CodeOf(err) == platformerrors.CodeInternal {
		return domain.FailureInfrastructure
	}
	return GridStillFailureClass
}

// GridStillDuration renders a duration as the whole milliseconds a still's
// measured cadence is stated in, saturating rather than wrapping at zero.
func GridStillDuration(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	millis := d.Milliseconds()
	if millis > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(millis)
}
