package media

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The workspace's preview setting (ARC-227): the plane's own reading of it, and
// the one place it is resolved.
//
// The setting arrives from two directions - a viewer may state it with the stream
// it asks for, and the deployment may configure it - and the rule that keeps the
// two from disagreeing is that there is ONE resolution point. These tests are
// about that point: whatever is stated, or not stated, the preview a session is
// carried at is a setting with a level and a rate, never "no bound".

// TestTheUnstatedPreviewIsTheDocumentedDefault is the acceptance property for the
// setting's absence at the plane's own edge.
//
// An unset deployment input, a blank one, a name this plane does not know and a
// level spelled in the wrong case all resolve to the documented default - which is
// itself a level with a cap. The reading this rules out is the one that matters:
// an unrecognised setting must never mean "the encoder is unbounded", because
// that is precisely the state the bound replaced.
func TestTheUnstatedPreviewIsTheDocumentedDefault(t *testing.T) {
	for name, lookup := range map[string]EnvLookup{
		"nothing configured": func(string) (string, bool) { return "", false },
		"a blank level":      func(key string) (string, bool) { return "", key == EnvPreviewQuality },
		"an unknown level": func(key string) (string, bool) {
			if key == EnvPreviewQuality {
				return "ultra", true
			}
			return "", false
		},
		"a level in the wrong case": func(key string) (string, bool) {
			if key == EnvPreviewQuality {
				return "  MEDIUM ", true
			}
			return "", false
		},
	} {
		preview, err := PreviewFromEnv(lookup)
		if err != nil {
			t.Fatalf("%s: PreviewFromEnv = %v", name, err)
		}
		if preview.Quality != DefaultPreviewQuality {
			t.Fatalf("%s: level = %q, want the documented default %q", name, preview.Quality, DefaultPreviewQuality)
		}
		if preview.FrameRate != DefaultPreviewFrameRate {
			t.Fatalf("%s: rate = %d, want the documented default %d", name, preview.FrameRate, DefaultPreviewFrameRate)
		}
		if previewFrameRateInRange(preview.FrameRate) == false {
			t.Fatalf("%s: the documented default rate is outside the range this product asks for", name)
		}
	}
	// The default is the medium cap and not one of the levels above it: the
	// plane's own default capacity lets the grid carry several tiles at once, and
	// each level above medium is a per-stream cost a grid multiplies.
	if DefaultPreviewQuality != PreviewMedium {
		t.Fatalf("the documented default level is %q, want medium", DefaultPreviewQuality)
	}
}

// TestTheFrameRateInputIsRefusedWhereItIsConfigured is the other half: a level is
// a name, and a name this plane does not know is bounded by the default, but a
// rate is a number, and a number nobody meant is refused where the deployment is
// configured rather than carried onto the wire as a bound.
func TestTheFrameRateInputIsRefusedWhereItIsConfigured(t *testing.T) {
	for _, configured := range []string{"0", "-1", "25", "300", "fifteen", "15fps"} {
		_, err := PreviewFromEnv(func(key string) (string, bool) {
			if key == EnvPreviewFrameRate {
				return configured, true
			}
			return "", false
		})
		if err == nil {
			t.Fatalf("PreviewFromEnv(%s=%q) was accepted", EnvPreviewFrameRate, configured)
		}
		if !strings.Contains(err.Error(), configured) {
			t.Fatalf("the refusal does not name the value that was configured: %v", err)
		}
	}
	// The ends of the range are configuration, not mistakes.
	for _, configured := range []string{"1", "15", "24"} {
		preview, err := PreviewFromEnv(func(key string) (string, bool) {
			if key == EnvPreviewFrameRate {
				return configured, true
			}
			return "", false
		})
		if err != nil {
			t.Fatalf("PreviewFromEnv(%s=%q) = %v", EnvPreviewFrameRate, configured, err)
		}
		if preview.FrameRate < MinPreviewFrameRate || preview.FrameRate > MaxPreviewFrameRate {
			t.Fatalf("a configured rate of %q resolved to %d", configured, preview.FrameRate)
		}
	}
}

// TestAViewerThatStatesNothingGetsTheSettingThePlaneIsConfiguredWith is the
// resolution itself, asserted at the seam the device is actually asked at.
//
// A console that has never been told the workspace's setting states nothing, and
// an unrecognised level is treated the same way: both get the plane's own setting,
// which is a bound. A viewer that does state a setting gets exactly it - and a
// rate no control offers is not applied, because a rate nobody chose is not a
// bound anybody chose.
func TestAViewerThatStatesNothingGetsTheSettingThePlaneIsConfiguredWith(t *testing.T) {
	configured := MirrorPreview{Quality: PreviewLow, FrameRate: 8}
	cases := map[string]struct {
		stated MirrorPreview
		want   MirrorPreview
	}{
		"nothing stated": {MirrorPreview{}, configured},
		// One half stated and one half not: the half that was stated is read and
		// the half that was not is filled from the plane's own setting, because a
		// level with no rate (or a rate with no level) is half a bound.
		"a level with no rate":           {MirrorPreview{Quality: PreviewHigh}, MirrorPreview{Quality: PreviewHigh, FrameRate: 8}},
		"a rate with no level":           {MirrorPreview{FrameRate: 20}, MirrorPreview{Quality: PreviewLow, FrameRate: 20}},
		"a level this plane knows":       {MirrorPreview{Quality: PreviewHigh, FrameRate: 20}, MirrorPreview{Quality: PreviewHigh, FrameRate: 20}},
		"a level this plane never heard": {MirrorPreview{Quality: "ultra", FrameRate: 20}, MirrorPreview{Quality: DefaultPreviewQuality, FrameRate: 20}},
		"a rate no control offers":       {MirrorPreview{Quality: PreviewHigh, FrameRate: 60}, MirrorPreview{Quality: PreviewHigh, FrameRate: 8}},
		"a rate below the range":         {MirrorPreview{Quality: PreviewHigh, FrameRate: -3}, MirrorPreview{Quality: PreviewHigh, FrameRate: 8}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			dialer := newFakeDialer()
			engine := newEngine(t, dialer, MirrorEngineConfig{Preview: configured, Idle: releaseIdle})
			session, viewer, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeAmbient, test.stated)
			if err != nil {
				t.Fatalf("StartViewer: %v", err)
			}
			defer releaseViewer(t, engine, viewer)

			sessionReady(t, session)
			ask := dialer.askFor(t, "device-1")
			if ask.preview != test.want {
				t.Fatalf("the device was asked for %+v, want %+v", ask.preview, test.want)
			}
			if ask.purpose != PurposeAmbient {
				t.Fatalf("the device was asked to carry %q, want an ambient viewer", ask.purpose)
			}
			// Whatever was stated, the bound the device is asked for states both
			// numbers: a level with no rate, or a rate with no level, would be a
			// stream bounded by half a bound.
			if ask.preview.Quality == "" || ask.preview.FrameRate == 0 {
				t.Fatalf("the device was asked for an incomplete bound: %+v", ask.preview)
			}
		})
	}
}

// TestTheEngineThatWasBuiltWithNoSettingStillCarriesOne pins the composition
// root's half: an engine built without a stated preview carries the documented
// default rather than "no bound", so a plane whose deployment configured nothing
// still bounds every ambient stream.
func TestTheEngineThatWasBuiltWithNoSettingStillCarriesOne(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Idle: releaseIdle})
	if got := engine.Preview(); got != DefaultPreview() {
		t.Fatalf("an engine built with no stated setting carries %+v, want the documented default %+v", got, DefaultPreview())
	}
	session, viewer, err := engine.StartViewer(context.Background(), "device-1", "SERIAL-1", PurposeAmbient, MirrorPreview{})
	if err != nil {
		t.Fatalf("StartViewer: %v", err)
	}
	defer releaseViewer(t, engine, viewer)
	sessionReady(t, session)
	if ask := dialer.askFor(t, "device-1"); ask.preview != DefaultPreview() {
		t.Fatalf("the device was asked for %+v, want the documented default", ask.preview)
	}
}

// TestTheStartupLineStatesTheSettingThePlaneIsApplying is the ARC-168 rule for
// this input: the bound the plane is actually carrying is read off the plane's own
// line rather than inferred from a console's local state.
func TestTheStartupLineStatesTheSettingThePlaneIsApplying(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{Preview: MirrorPreview{Quality: PreviewExtra, FrameRate: 9}, Idle: releaseIdle})
	line := NewMirrorHost(MirrorHostConfig{Engine: engine}).State()
	for _, want := range []string{"extra", "9", "capacity"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the startup line does not state %q: %s", want, line)
		}
	}
	if !strings.Contains(line, "preview setting") {
		t.Fatalf("the startup line does not say what the setting is: %s", line)
	}
	// A host with no engine says why rather than reading as a plane whose bound
	// is nothing.
	if line := NewMirrorHost(MirrorHostConfig{}).State(); !strings.Contains(line, "not started") {
		t.Fatalf("an unarmed mirror's line is %q", line)
	}
}

// releaseIdle is how long a session is held after its last viewer detaches, set
// short so a test does not wait out the production bound.
const releaseIdle = 50 * time.Millisecond

// releaseViewer detaches a viewer and waits for the plane to stop carrying the
// session, so a test does not leave a capture running into the next one. A session
// with no viewer left ends on its own idle bound, which is what the engine's
// design requires: a device is captured only while a viewer is subscribed.
func releaseViewer(t *testing.T, engine *MirrorEngine, viewer MirrorViewer) {
	t.Helper()
	viewer.Close()
	waitFor(t, "the plane to stop carrying the session once its viewer detached", func() bool {
		return len(engine.Sessions()) == 0
	})
}
