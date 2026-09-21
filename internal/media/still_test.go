package media_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// solidPNG builds a real, decodable PNG of one colour. The encoder is the first
// thing in this product that PARSES a capture rather than passing it through, so
// its tests need captures a decoder accepts - not payloads of a stated length
// behind the PNG signature.
func solidPNG(t *testing.T, width, height int, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encoding the fixture PNG: %v", err)
	}
	return buffer.Bytes()
}

// decodeJPEG decodes a delivered still back into pixels, so an assertion about a
// still is an assertion about the picture an operator would be shown.
func decodeJPEG(t *testing.T, payload []byte) image.Image {
	t.Helper()
	decoded, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("the delivered still is not a JPEG: %v", err)
	}
	return decoded
}

// TestGridStillProfileStatesItsOwnNumbers pins that a level publishes the cap and
// the quality it applies: a tile told "medium" cannot say whether its picture is
// 360 or 720 pixels wide, and the difference is why a deployment picks a level.
func TestGridStillProfileStatesItsOwnNumbers(t *testing.T) {
	t.Parallel()
	profile := media.GridStillProfileFor(media.GridStillLevelMedium)
	if profile.Level != media.GridStillLevelMedium {
		t.Fatalf("level = %q, want medium", profile.Level)
	}
	if profile.MaxWidth <= 0 || profile.JPEGQuality <= 0 {
		t.Fatalf("profile = %+v, want a stated cap and quality", profile)
	}
	if profile.ByteBound != media.DefaultPreviewLimit {
		t.Fatalf("byte bound = %d, want the one-shot preview bound %d", profile.ByteBound, media.DefaultPreviewLimit)
	}
	report := profile.Report()
	for _, want := range []string{"medium", "JPEG quality"} {
		if !bytes.Contains([]byte(report), []byte(want)) {
			t.Fatalf("profile report %q does not state %q", report, want)
		}
	}
}

// TestGridStillLevelFromStringIsBoundedByDefault pins the reading rule: an
// unrecognised level resolves to the documented default rather than to no level at
// all, because the default is itself a level with a cap - where refusing the name
// would leave a grid unbounded, which is the state the level exists to remove.
func TestGridStillLevelFromStringIsBoundedByDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want media.GridStillLevel
	}{
		{raw: "low", want: media.GridStillLevelLow},
		{raw: "  HIGH ", want: media.GridStillLevelHigh},
		{raw: "medium", want: media.GridStillLevelMedium},
		{raw: "ultra", want: media.GridStillLevelMedium},
		{raw: "", want: media.GridStillLevelMedium},
	}
	for _, testCase := range cases {
		if got := media.GridStillLevelFromString(testCase.raw); got != testCase.want {
			t.Fatalf("GridStillLevelFromString(%q) = %q, want %q", testCase.raw, got, testCase.want)
		}
	}
}

// TestGridStillProfileTargetSizeCapsTheWidthAndKeepsTheAspect pins the one piece of
// arithmetic the level is: a portrait phone is capped on its width with its height
// following the screen, a screen narrower than the cap is never enlarged, and no
// size is ever zero.
func TestGridStillProfileTargetSizeCapsTheWidthAndKeepsTheAspect(t *testing.T) {
	t.Parallel()
	profile := media.GridStillProfileFor(media.GridStillLevelMedium)
	cases := []struct {
		name                  string
		width, height         int
		wantWidth, wantHeight int
	}{
		{name: "a portrait phone is capped on its width", width: 1080, height: 2280, wantWidth: profile.MaxWidth, wantHeight: 2280 * profile.MaxWidth / 1080},
		{name: "a landscape screen follows its own aspect", width: 2280, height: 1080, wantWidth: profile.MaxWidth, wantHeight: 1080 * profile.MaxWidth / 2280},
		{name: "a screen narrower than the cap is not enlarged", width: 120, height: 240, wantWidth: 120, wantHeight: 240},
		{name: "a screen exactly at the cap is unchanged", width: profile.MaxWidth, height: 800, wantWidth: profile.MaxWidth, wantHeight: 800},
	}
	for _, testCase := range cases {
		width, height := profile.TargetSize(testCase.width, testCase.height)
		if width != testCase.wantWidth || height != testCase.wantHeight {
			t.Fatalf("%s: TargetSize(%d, %d) = %dx%d, want %dx%d",
				testCase.name, testCase.width, testCase.height, width, height, testCase.wantWidth, testCase.wantHeight)
		}
	}
	if width, height := profile.TargetSize(0, 0); width != 0 || height != 0 {
		t.Fatalf("TargetSize(0, 0) = %dx%d, want no size at all", width, height)
	}
}

// TestImageStillEncoderDeliversThePictureAtTheLevelsSize pins what the encoder
// produces: a JPEG at the cap, of the size the cap implies, carrying the picture
// rather than a placeholder.
func TestImageStillEncoderDeliversThePictureAtTheLevelsSize(t *testing.T) {
	t.Parallel()
	capture := solidPNG(t, 200, 400, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	profile := media.GridStillProfile{Level: media.GridStillLevelMedium, MaxWidth: 50, JPEGQuality: 70, ByteBound: media.DefaultPreviewLimit}

	still, err := media.ImageStillEncoder{}.EncodeStill(capture, profile)
	if err != nil {
		t.Fatalf("EncodeStill() = %v", err)
	}
	decoded := decodeJPEG(t, still.JPEG)
	if decoded.Bounds().Dx() != 50 || decoded.Bounds().Dy() != 100 {
		t.Fatalf("delivered still is %dx%d, want 50x100", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	if still.Width != 50 || still.Height != 100 {
		t.Fatalf("still states %dx%d, want the size it delivered", still.Width, still.Height)
	}
	if len(still.JPEG) >= len(capture) {
		t.Fatalf("still is %d byte(s) for a %d byte capture: the level did not reduce it", len(still.JPEG), len(capture))
	}
	// A level's whole purpose is the size of what it delivers, and a solid colour
	// is the smallest a picture can get: if even this does not fit the bound, the
	// level's own bound is unreachable and every tile would be a truncation.
	if len(still.JPEG) > profile.ByteBound {
		t.Fatalf("still is %d byte(s), over the %d byte bound", len(still.JPEG), profile.ByteBound)
	}
	// JPEG is lossy, so the picture is asserted as the colour it is rather than as
	// the bytes it was: a still that decoded to something else would be a tile
	// showing a device's screen wrong.
	centre := color.NRGBAModel.Convert(decoded.At(25, 50)).(color.NRGBA)
	if abs(int(centre.R)-10) > 8 || abs(int(centre.G)-20) > 8 || abs(int(centre.B)-30) > 8 {
		t.Fatalf("delivered still centre = %+v, want approximately the captured colour", centre)
	}
}

// TestImageStillEncoderIsDeterministic pins that one capture produces one still,
// byte for byte: it is what lets this package's tests assert an encoder's output
// rather than its plausibility, and what makes two tiles showing the same hash two
// tiles showing the same picture.
func TestImageStillEncoderIsDeterministic(t *testing.T) {
	t.Parallel()
	capture := solidPNG(t, 64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	profile := media.DefaultGridStillProfile()
	first, err := media.ImageStillEncoder{}.EncodeStill(capture, profile)
	if err != nil {
		t.Fatalf("EncodeStill() = %v", err)
	}
	second, err := media.ImageStillEncoder{}.EncodeStill(capture, profile)
	if err != nil {
		t.Fatalf("EncodeStill() = %v", err)
	}
	if !bytes.Equal(first.JPEG, second.JPEG) {
		t.Fatal("two runs over one capture produced different stills")
	}
}

// TestImageStillEncoderRefusesACaptureItCannotRead pins the failure side: a capture
// that is not an image is a refusal of this path, never an empty tile. A tile
// reporting "no picture" beside a capture that arrived and could not be read is a
// tile that sends an operator looking at the device.
func TestImageStillEncoderRefusesACaptureItCannotRead(t *testing.T) {
	t.Parallel()
	profile := media.DefaultGridStillProfile()
	for name, capture := range map[string][]byte{
		"empty":        nil,
		"not an image": []byte("this is not an image at all"),
		"a broken PNG": {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01},
		"a text file":  []byte("screencap returned a permission error\n"),
	} {
		still, err := media.ImageStillEncoder{}.EncodeStill(capture, profile)
		if err == nil {
			t.Fatalf("%s: EncodeStill() delivered a still of %d byte(s)", name, len(still.JPEG))
		}
		if !errors.Is(err, media.ErrStillNotAnImage) {
			t.Fatalf("%s: error = %v, want ErrStillNotAnImage", name, err)
		}
		if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
			t.Fatalf("%s: code = %q, want invalid input", name, platformerrors.CodeOf(err))
		}
	}
}

// TestImageStillEncoderAcceptsTheFormatsItsDecoderDoes pins that a JPEG capture is
// not refused for being the "wrong" type: what the capture path admits and what
// this encoder decodes are one list, and a second, narrower copy of that rule is
// how a still comes to be refused for a reason nobody stated.
func TestImageStillEncoderAcceptsTheFormatsItsDecoderDoes(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 40, 80)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encoding the fixture JPEG: %v", err)
	}
	still, err := media.ImageStillEncoder{}.EncodeStill(buffer.Bytes(), media.DefaultGridStillProfile())
	if err != nil {
		t.Fatalf("EncodeStill() on a JPEG capture = %v", err)
	}
	if decoded := decodeJPEG(t, still.JPEG); decoded.Bounds().Dx() != 40 || decoded.Bounds().Dy() != 80 {
		t.Fatalf("delivered still is %dx%d, want the capture's own 40x80", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// busyPNG builds a deterministic picture of block-level noise, which is what a
// screen full of text, icons and photographs costs to encode: a smooth picture
// fits any bound, and per-pixel noise is a picture no screen contains - so block
// noise is the honest middle the byte bound is exercised with.
func busyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	const block = 8
	screen := image.NewRGBA(image.Rect(0, 0, width, height))
	state := uint32(1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x%block == 0 && y%block == 0 {
				state = state*1664525 + 1013904223
			}
			shade := uint8(state >> 24)
			screen.SetRGBA(x, y, color.RGBA{shade, uint8(state >> 16), uint8(state >> 8), 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, screen); err != nil {
		t.Fatalf("encoding the test capture: %v", err)
	}
	return encoded.Bytes()
}

// TestImageStillEncoderSpendsQualityToFitTheStillByteBound pins the rule a fleet
// grid rests on: a still is DELIVERED inside the level's byte bound rather than
// refused for being over it.
//
// The measurement that produced this rule is on the lab fleet: one unit's 360 px
// wide, q65 still came out at 37 KB against the product's 32 KiB bound, and at the
// level's quality that device's tile would have shown no picture at all. The
// quality is what gives way, and the quality used is stated on the still rather
// than left for a reader to infer from the size.
func TestImageStillEncoderSpendsQualityToFitTheStillByteBound(t *testing.T) {
	t.Parallel()
	profile := media.GridStillProfileFor(media.GridStillLevelMedium)
	picture := busyPNG(t, 720, 1520)

	// What this picture costs at the LEVEL's own quality, so the bound it is then
	// given is one this still actually misses rather than a number chosen to make
	// the encoder miss it.
	unbounded := profile
	unbounded.ByteBound = 1 << 20
	atLevelQuality, err := media.ImageStillEncoder{}.EncodeStill(picture, unbounded)
	if err != nil {
		t.Fatalf("EncodeStill: %v", err)
	}
	if atLevelQuality.JPEGQuality != profile.JPEGQuality {
		t.Fatalf("a still inside a bound it cannot reach was encoded at q%d, want the level's q%d",
			atLevelQuality.JPEGQuality, profile.JPEGQuality)
	}
	profile.ByteBound = len(atLevelQuality.JPEG) - len(atLevelQuality.JPEG)/10

	still, err := media.ImageStillEncoder{}.EncodeStill(picture, profile)
	if err != nil {
		t.Fatalf("EncodeStill: %v", err)
	}
	if len(still.JPEG) > profile.ByteBound {
		t.Fatalf("the still is %d byte(s) against a %d byte bound: a tile would be delivered nothing",
			len(still.JPEG), profile.ByteBound)
	}
	if still.JPEGQuality >= profile.JPEGQuality {
		t.Fatalf("the still was encoded at q%d and fitted a bound that q%d's own %d byte(s) missed: quality did not give way",
			still.JPEGQuality, profile.JPEGQuality, len(atLevelQuality.JPEG))
	}
	if still.JPEGQuality < media.MinStillJPEGQuality {
		t.Fatalf("the still was encoded at q%d, below the floor a readable tile is drawn at", still.JPEGQuality)
	}
	if decoded := decodeJPEG(t, still.JPEG); decoded.Bounds().Dx() != still.Width {
		t.Fatalf("the delivered still is %d px wide, want %d", decoded.Bounds().Dx(), still.Width)
	}
	if still.Width != profile.MaxWidth {
		t.Fatalf("the still is %d px wide, want the level's %d: fitting the bound must not change the level's size",
			still.Width, profile.MaxWidth)
	}
}

// TestImageStillEncoderKeepsTheLevelsQualityWhenTheStillFits pins the other side:
// the level's quality is what a still is normally carried at, and the encoder must
// not spend quality a still did not need to spend.
func TestImageStillEncoderKeepsTheLevelsQualityWhenTheStillFits(t *testing.T) {
	t.Parallel()
	profile := media.GridStillProfileFor(media.GridStillLevelMedium)
	still, err := media.ImageStillEncoder{}.EncodeStill(solidPNG(t, 240, 506, color.RGBA{12, 34, 56, 0xff}), profile)
	if err != nil {
		t.Fatalf("EncodeStill: %v", err)
	}
	if still.JPEGQuality != profile.JPEGQuality {
		t.Fatalf("a still that fitted was encoded at q%d, want the level's q%d", still.JPEGQuality, profile.JPEGQuality)
	}
}

// TestImageStillEncoderReportsTheFloorWhenNoQualityFitsTheBound pins what happens
// when the bound cannot be met at all: the smallest attempt is delivered and the
// floor quality is what it was encoded at, so the engine's own truncation rule -
// not this encoder - is what reports a still over the bound. An encoder that
// silently returned an over-bound still as if it had fitted would be claiming a
// bound it did not meet.
func TestImageStillEncoderReportsTheFloorWhenNoQualityFitsTheBound(t *testing.T) {
	t.Parallel()
	profile := media.GridStillProfileFor(media.GridStillLevelMedium)
	profile.ByteBound = 64
	still, err := media.ImageStillEncoder{}.EncodeStill(busyPNG(t, 360, 760), profile)
	if err != nil {
		t.Fatalf("EncodeStill: %v", err)
	}
	if still.JPEGQuality != media.MinStillJPEGQuality {
		t.Fatalf("an unmeetable bound was left at q%d, want the floor q%d", still.JPEGQuality, media.MinStillJPEGQuality)
	}
	if len(still.JPEG) == 0 {
		t.Fatal("no still was produced at all: the engine has nothing to report")
	}
}
