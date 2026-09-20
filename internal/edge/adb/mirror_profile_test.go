package adb

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// The live mirror's encode bound (ARC-227). These tests are about the two
// properties the bound has to have to be worth anything:
//
//  1. every level is a CAP, and a level that is not in the table is not a level -
//     an unstated or unrecognised setting resolves to the documented default
//     rather than to an encoder nobody bounded; and
//  2. the bound REACHES THE DEVICE. A profile is only a cap if it is on the argv
//     as `max_size`, `max_fps` and `video_bit_rate`, on every launch, including
//     the level whose size is the device's own.

// levelFor is the cap each documented level is carried at, spelled here as the
// numbers the card states rather than read from the table: a test that read the
// table would agree with a table that changed under it.
var levelFor = map[string]struct {
	size    int
	bitRate int
}{
	"low":    {480, 500_000},
	"medium": {720, 1_200_000},
	"high":   {1080, 2_500_000},
	// Native size, and still bounded by the bit rate beside it.
	"extra": {0, 6_000_000},
}

// TestEveryPreviewLevelIsAHardCap is the table itself.
//
// Every level this product offers states BOTH numbers, and the numbers are the
// ones the card states. A level with no cap is the defect, not a level: the state
// this table replaced was a launch that asked the device for nothing but a codec
// option, so the encoder produced the device's native 1080x1920 at 60 fps with
// scrcpy's own default bit rate and nothing bounded it.
func TestEveryPreviewLevelIsAHardCap(t *testing.T) {
	if len(MirrorPreviewLevels) != len(levelFor) {
		t.Fatalf("the product offers %d preview levels (%v), want the %d the card states",
			len(MirrorPreviewLevels), MirrorPreviewLevels, len(levelFor))
	}
	for _, level := range MirrorPreviewLevels {
		want, known := levelFor[level]
		if !known {
			t.Fatalf("the product offers the level %q, which no cap is stated for: a level with no cap is the defect this table exists to close", level)
		}
		profile, err := MirrorAmbientEncodeProfile(level, MirrorDefaultFrameRate)
		if err != nil {
			t.Fatalf("MirrorAmbientEncodeProfile(%q) = %v", level, err)
		}
		if profile.MaxSize != want.size || profile.BitRate != want.bitRate {
			t.Fatalf("level %q = max_size %d, bit_rate %d; want %d, %d",
				level, profile.MaxSize, profile.BitRate, want.size, want.bitRate)
		}
		// The cadence is the ambient one, because this is the profile an ambient
		// viewer is carried at.
		if profile.IDRIntervalSeconds != MirrorAmbientIDRIntervalSeconds {
			t.Fatalf("level %q is carried at a keyframe interval of %ds, want the ambient %ds",
				level, profile.IDRIntervalSeconds, MirrorAmbientIDRIntervalSeconds)
		}
	}
}

// TestAnUnstatedOrUnknownQualityIsBoundedRatherThanUncapped is the acceptance
// property for the setting's absence.
//
// A name this product has never heard of, a blank name and no name at all all
// resolve to the documented default - which is itself a level with a cap. The
// alternative reading, that an unrecognised setting means "no bound", is exactly
// the uncapped encoder this table replaced, so it is asserted against directly:
// whatever is asked for, the profile that comes back states a size and a bit rate.
func TestAnUnstatedOrUnknownQualityIsBoundedRatherThanUncapped(t *testing.T) {
	for _, quality := range []string{"", "   ", "nonsense", "ULTRA", "medium ", "Low", "0", "native"} {
		profile, err := MirrorAmbientEncodeProfile(quality, MirrorDefaultFrameRate)
		if err != nil {
			t.Fatalf("MirrorAmbientEncodeProfile(%q) = %v", quality, err)
		}
		if profile.BitRate <= 0 {
			t.Fatalf("quality %q produced a profile with no bit rate (%+v): an unstated setting must resolve to a cap, never to no bound", quality, profile)
		}
		if profile.MaxFPS < MirrorMinFrameRate {
			t.Fatalf("quality %q produced a profile with no capture rate (%+v)", quality, profile)
		}
	}
	// The documented default is where an unknown name lands, and it is the
	// medium cap: the plane's own default capacity lets the grid carry several
	// tiles at once, so the default level must not be one of the per-stream costs
	// a grid multiplies.
	unknown, err := MirrorAmbientEncodeProfile("nonsense", MirrorDefaultFrameRate)
	if err != nil {
		t.Fatalf("MirrorAmbientEncodeProfile(nonsense) = %v", err)
	}
	stated, err := MirrorAmbientEncodeProfile(MirrorDefaultPreviewQuality, MirrorDefaultFrameRate)
	if err != nil {
		t.Fatalf("MirrorAmbientEncodeProfile(%s) = %v", MirrorDefaultPreviewQuality, err)
	}
	if unknown != stated {
		t.Fatalf("an unknown level resolved to %+v, want the documented default %s's cap %+v", unknown, MirrorDefaultPreviewQuality, stated)
	}
	if unknown.MaxSize != 720 || unknown.BitRate != 1_200_000 {
		t.Fatalf("the documented default cap is %+v, want medium's 720 at 1.2 Mbps", unknown)
	}
}

// TestAStatedFrameRateIsNeverSilentlyClamped is the other half of "a stated bound
// is never silently dropped": a rate outside the range this product asks for is
// REFUSED, because a clamped rate is a different bound from the one that was
// stated, delivered to the device without anyone being told.
func TestAStatedFrameRateIsNeverSilentlyClamped(t *testing.T) {
	for _, rate := range []int{0, -1, MirrorMaxFrameRate + 1, 60, 300} {
		if _, err := MirrorAmbientEncodeProfile("high", rate); !errors.Is(err, ErrMirrorShapeInvalid) {
			t.Fatalf("MirrorAmbientEncodeProfile(high, %d) = %v, want ErrMirrorShapeInvalid: a rate nobody stated must not become a bound silently", rate, err)
		}
	}
	// The ends of the range are asked for, not clamped to it.
	for _, rate := range []int{MirrorMinFrameRate, 15, MirrorMaxFrameRate} {
		profile, err := MirrorAmbientEncodeProfile("high", rate)
		if err != nil {
			t.Fatalf("MirrorAmbientEncodeProfile(high, %d) = %v", rate, err)
		}
		if profile.MaxFPS != rate {
			t.Fatalf("a stated rate of %d reached the profile as %d", rate, profile.MaxFPS)
		}
	}
}

// TestThePurposeDecidesTheBoundOnTheDeviceServerLaunch is the acceptance property
// the card states first: the argv carries max_size, max_fps and video_bit_rate,
// per purpose.
//
// The two purposes are two different bounds and not two values of one setting.
// An ambient viewer is one of the console's grid tiles, carried at the workspace's
// preview setting - a level chosen for a grid of thumbnails. The operator's own
// frame is where the work happens, and it is carried at its own profile at every
// level: a preview setting an operator chose for the grid must never make the
// frame they are working in blurry.
func TestThePurposeDecidesTheBoundOnTheDeviceServerLaunch(t *testing.T) {
	ambient, err := MirrorAmbientEncodeProfile("low", 8)
	if err != nil {
		t.Fatalf("MirrorAmbientEncodeProfile(low, 8) = %v", err)
	}
	launch, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", true, ambient)
	if err != nil {
		t.Fatalf("MirrorServerLaunchArgv(ambient) = %v", err)
	}
	bound := boundOnLaunch(t, launch)
	if bound.size != 480 || bound.fps != 8 || bound.bitRate != 500_000 {
		t.Fatalf("an ambient launch at the low level carries %+v, want max_size 480, max_fps 8, video_bit_rate 500000", bound)
	}

	// The operator's own frame at the SAME preview setting: its own profile, and
	// never the level that was stated.
	operator, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", true, MirrorOperatorEncodeProfile)
	if err != nil {
		t.Fatalf("MirrorServerLaunchArgv(operator) = %v", err)
	}
	bound = boundOnLaunch(t, operator)
	if bound.size != 1080 || bound.fps != 24 || bound.bitRate != 2_500_000 {
		t.Fatalf("the operator's own launch carries %+v, want its own profile of max_size 1080, max_fps 24, video_bit_rate 2500000", bound)
	}
	if bound.size == 480 {
		t.Fatal("the operator's own frame was carried at the preview setting chosen for the grid")
	}
}

// TestEveryLevelsBoundReachesTheDeviceAndIsAdmitted is the join between the two
// gates: what the table states is what the launch carries, and what the launch
// carries is what the allow-list admits. A level the builder can produce and the
// admission refuses is a mirror that dials a device and shows nothing.
func TestEveryLevelsBoundReachesTheDeviceAndIsAdmitted(t *testing.T) {
	profiles := map[string]MirrorEncodeProfile{MirrorDefaultPreviewQuality: MirrorOperatorEncodeProfile}
	for _, level := range MirrorPreviewLevels {
		profile, err := MirrorAmbientEncodeProfile(level, MirrorDefaultFrameRate)
		if err != nil {
			t.Fatalf("MirrorAmbientEncodeProfile(%q) = %v", level, err)
		}
		profiles[level] = profile
	}
	for level, profile := range profiles {
		launch, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", false, profile)
		if err != nil {
			t.Fatalf("MirrorServerLaunchArgv(%q) = %v", level, err)
		}
		bound := boundOnLaunch(t, launch)
		if bound.size != profile.MaxSize || bound.fps != profile.MaxFPS || bound.bitRate != profile.BitRate {
			t.Fatalf("%q: the launch carries %+v, want the profile %+v", level, bound, profile)
		}
		if name, ok := matchesAllowlist(launch); !ok || name != MirrorServerLaunchOperation {
			t.Fatalf("%q: matchesAllowlist(%q) = %q, %v; want %q, true", level, launch, name, ok, MirrorServerLaunchOperation)
		}
	}
	// The native level still STATES its size: `max_size=0` is scrcpy's own "no
	// downscale", so the token is present and the level is bounded by its bit rate
	// rather than by an omitted option, which the device would read as its own
	// default.
	extra, err := MirrorAmbientEncodeProfile("extra", MirrorDefaultFrameRate)
	if err != nil {
		t.Fatalf("MirrorAmbientEncodeProfile(extra) = %v", err)
	}
	launch, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", false, extra)
	if err != nil {
		t.Fatalf("MirrorServerLaunchArgv(extra) = %v", err)
	}
	if !strings.Contains(strings.Join(launch, " "), "max_size=0") {
		t.Fatalf("the native level's launch omits its size: %q", launch)
	}
}

// TestTheEncodeBoundIsRefusedWhereItIsSupplied is the fail-closed half: a profile
// the launch cannot render as admitted tokens is refused by the builder, so a
// bound nobody admitted is refused where it is supplied rather than reaching a
// device.
func TestTheEncodeBoundIsRefusedWhereItIsSupplied(t *testing.T) {
	cases := map[string]MirrorEncodeProfile{
		"no profile at all": {},
		"no capture rate":   {MaxSize: 720, BitRate: 1_200_000, IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds},
		"a rate above the range": {MaxSize: 720, MaxFPS: MirrorMaxFrameRate + 1,
			BitRate: 1_200_000, IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds},
		"a rate of zero":            {MaxSize: 720, BitRate: 1_200_000, IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds},
		"no bit rate":               {MaxSize: 720, MaxFPS: 15, IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds},
		"a negative size":           {MaxSize: -1, MaxFPS: 15, BitRate: 1_200_000, IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds},
		"a cadence nobody asks for": {MaxSize: 720, MaxFPS: 15, BitRate: 1_200_000, IDRIntervalSeconds: 60},
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := MirrorServerLaunchArgv(mirrorTestSessionID, "info", true, profile); !errors.Is(err, ErrMirrorShapeInvalid) {
				t.Fatalf("MirrorServerLaunchArgv(%+v) = %v, want ErrMirrorShapeInvalid", profile, err)
			}
		})
	}
}

// TestTheAdmissionAdmitsOnlyTheBoundsTheTableStates is the allow-list's half of
// the bound: a size or a bit rate that no level produces is refused even though it
// is a perfectly ordinary number. Admitting a numeric RANGE would admit a bound
// nobody chose, which is the same defect as admitting none.
func TestTheAdmissionAdmitsOnlyTheBoundsTheTableStates(t *testing.T) {
	launch := mirrorTestLaunch(t)
	for index, token := range map[int]string{
		13: "max_size=1024",
		14: "max_fps=30",
		15: "video_bit_rate=8000000",
	} {
		mutated := append([]string(nil), launch...)
		mutated[index] = token
		if name, ok := matchesAllowlist(mutated); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false: %q is not a bound any level states", mutated, name, token)
		}
	}
	// And the set the admission derives is the set the table states, so a level
	// added to one without the other is caught here rather than on a device.
	sizes := admittedPreviewSizes()
	rates := admittedPreviewBitRates()
	for level, want := range levelFor {
		if _, ok := sizes[strconv.Itoa(want.size)]; !ok {
			t.Fatalf("the admission does not admit %q's size %d", level, want.size)
		}
		if _, ok := rates[strconv.Itoa(want.bitRate)]; !ok {
			t.Fatalf("the admission does not admit %q's bit rate %d", level, want.bitRate)
		}
	}
	if len(sizes) != len(levelFor) || len(rates) != len(levelFor) {
		t.Fatalf("the admission admits %d sizes and %d bit rates, want one of each per level (%d)", len(sizes), len(rates), len(levelFor))
	}
}

// boundOnLaunch reads the three encode-bound tokens back off a launch.
func boundOnLaunch(t *testing.T, launch []string) struct {
	size    int
	fps     int
	bitRate int
} {
	t.Helper()
	read := func(prefix string) int {
		t.Helper()
		for _, token := range launch {
			value, ok := strings.CutPrefix(token, prefix)
			if !ok {
				continue
			}
			number, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("%s on the launch is not a number: %q", prefix, token)
			}
			return number
		}
		t.Fatalf("the launch carries no %s: %q", prefix, launch)
		return 0
	}
	return struct {
		size    int
		fps     int
		bitRate int
	}{read("max_size="), read("max_fps="), read("video_bit_rate=")}
}
