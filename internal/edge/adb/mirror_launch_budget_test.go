package adb

import (
	"errors"
	"math"
	"strconv"
	"testing"
)

// TestMirrorLaunchFitsTheDeviceCommandLineBudget is the device property this
// launch is bounded by, and the regression the lab fleet paid for.
//
// The device's own codec stack copies the CALLING process's command line into a
// fixed 264-byte buffer while it names the app whose encoder it is configuring -
// the string its crash log prints as `Cmdline:`. A device-side server launched
// with a line that reaches that buffer's end dies inside
// `ACodec::reconfigEncoderOtherApps` with `stack corruption detected`, before it
// has sent a single frame, and the host reads a stream header that never
// arrives. Measured on a lab SM-G9750 (Android 12) against server 4.1 by hand,
// one token set at a time: a command line of 263 bytes streams, 264 aborts.
//
// Every launch this product can build must therefore fit the budget with room to
// spare, whatever profile, cadence, log level and wake setting it is built for.
// A profile whose numbers are admitted but whose line is too long is refused
// where it is supplied - a refusal the host can act on, rather than a device
// that aborts during encoder setup.
func TestMirrorLaunchFitsTheDeviceCommandLineBudget(t *testing.T) {
	longest := 0
	longestLine := ""
	checked := 0

	build := func(what string, profile MirrorEncodeProfile) {
		t.Helper()
		for logLevel := range mirrorLogLevels {
			for _, keepAwake := range []bool{true, false} {
				args, err := MirrorServerLaunchArgv(mirrorTestSessionID, logLevel, keepAwake, profile)
				if err != nil {
					t.Fatalf("MirrorServerLaunchArgv(%s, %s, keepAwake=%v) = %v", what, logLevel, keepAwake, err)
				}
				checked++
				line := mirrorLaunchCommandLine(args)
				if len(line) > MirrorLaunchCommandLineLimit {
					t.Fatalf("the launch for %s at %s logs a %d-byte command line, over the %d the device tolerates:\n%s",
						what, logLevel, len(line), MirrorLaunchCommandLineLimit, line)
				}
				if len(line) > longest {
					longest, longestLine = len(line), line
				}
				// The admission is a second gate over the same launch: what the
				// builder produces must still be recognised as a mirror launch
				// after the budget has been applied.
				if name, ok := matchesAllowlist(args); !ok || name != MirrorServerLaunchOperation {
					t.Fatalf("matchesAllowlist(%q) = %q, %v; want %q, true", args, name, ok, MirrorServerLaunchOperation)
				}
			}
		}
	}

	for _, level := range MirrorPreviewLevels {
		for _, frameRate := range []int{MirrorMinFrameRate, MirrorDefaultFrameRate, MirrorMaxFrameRate} {
			profile, err := MirrorAmbientEncodeProfile(level, frameRate)
			if err != nil {
				t.Fatalf("MirrorAmbientEncodeProfile(%q, %d) = %v", level, frameRate, err)
			}
			build(level+" at "+strconv.Itoa(frameRate)+" fps", profile)
		}
	}
	build("the operator's own frame", MirrorOperatorEncodeProfile)

	// The number, not just the inequality: the longest line this product can
	// build is recorded here so a change that spends the budget is visible as a
	// number in the diff rather than only as a failure on a device.
	const wantLongest = 255
	if longest != wantLongest {
		t.Fatalf("the longest launch this product builds is %d bytes, want %d:\n%s", longest, wantLongest, longestLine)
	}
	if margin := MirrorLaunchCommandLineLimit - longest; margin < 4 {
		t.Fatalf("the longest launch leaves %d bytes under the device's %d-byte budget, which is too little room for the next option", margin, MirrorLaunchCommandLineLimit)
	}
	t.Logf("checked %d launches; the longest is %d bytes, %d under the device's limit", checked, longest, MirrorLaunchCommandLineLimit-longest)
}

// TestTheBuilderRefusesALaunchTheDeviceCannotRead covers the other half: a
// profile whose numbers pass the builder's own bound but whose command line
// would reach the device's buffer is refused where it is supplied, so the
// refusal is a host-side error rather than a device-side abort.
func TestTheBuilderRefusesALaunchTheDeviceCannotRead(t *testing.T) {
	fat := MirrorEncodeProfile{
		// Every one of these passes Validate: a size is only bounded from
		// below, a bit rate only from below, and a cadence is one of the two
		// this product asks for. What they are not is a line the device reads.
		MaxSize:            math.MaxInt,
		MaxFPS:             MirrorMaxFrameRate,
		BitRate:            math.MaxInt,
		IDRIntervalSeconds: MirrorAmbientIDRIntervalSeconds,
	}
	if err := fat.Validate(); err != nil {
		t.Fatalf("the fixture profile is refused by Validate() = %v; it is meant to be a value the builder's own bound admits", err)
	}
	if _, err := MirrorServerLaunchArgv(mirrorTestSessionID, "verbose", true, fat); !errors.Is(err, ErrMirrorShapeInvalid) {
		t.Fatalf("MirrorServerLaunchArgv(fat profile) = %v, want ErrMirrorShapeInvalid", err)
	}
}
