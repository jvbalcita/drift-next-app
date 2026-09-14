package recordings_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/recordings"
)

func TestEventGrouperCombinesTextWithoutPersistingCharacters(t *testing.T) {
	base := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	grouper := recordings.NewEventGrouper(recordings.GroupingOptions{TextWindow: 500 * time.Millisecond})
	first := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputText, At: base, Text: "dr", Sensitivity: recordings.SensitivityNone, Target: target("name")}
	second := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputText, At: base.Add(100 * time.Millisecond), Text: "ift", Sensitivity: recordings.SensitivityNone, Target: target("name")}
	if actions, err := grouper.Add(first); err != nil || len(actions) != 0 {
		t.Fatalf("first Add() = %#v, %v; want buffered", actions, err)
	}
	if actions, err := grouper.Add(second); err != nil || len(actions) != 0 {
		t.Fatalf("second Add() = %#v, %v; want buffered", actions, err)
	}
	actions, err := grouper.Flush()
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != action.TextInput || actions[0].Text != "drift" || actions[0].ValueLength != 5 {
		t.Fatalf("Flush() = %#v, want one grouped text input", actions)
	}
}

func TestEventGrouperRecognizesDoubleTapAndPreservesGesturePath(t *testing.T) {
	base := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	grouper := recordings.NewEventGrouper(recordings.GroupingOptions{DoubleTapWindow: 300 * time.Millisecond})
	first := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputTap, At: base, EndAt: base.Add(40 * time.Millisecond), Start: action.Coordinate{Space: "display:1080x1920", X: 40, Y: 80}, Sensitivity: recordings.SensitivityNone, Target: target("button")}
	second := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputTap, At: base.Add(100 * time.Millisecond), EndAt: base.Add(140 * time.Millisecond), Start: first.Start, Sensitivity: recordings.SensitivityNone, Target: target("button")}
	if _, err := grouper.Add(first); err != nil {
		t.Fatal(err)
	}
	if _, err := grouper.Add(second); err != nil {
		t.Fatal(err)
	}
	actions, err := grouper.Flush()
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != action.DoubleTap || actions[0].Start != first.Start {
		t.Fatalf("Flush() = %#v, want one double tap at the original coordinate", actions)
	}

	swipe := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputSwipe, At: base.Add(time.Second), EndAt: base.Add(1500 * time.Millisecond), Start: action.Coordinate{Space: "display:1080x1920", X: 100, Y: 500}, End: &action.Coordinate{Space: "display:1080x1920", X: 900, Y: 500}, Sensitivity: recordings.SensitivityNone, Target: recordings.TargetMetadata{Source: "fake"}}
	actions, err = grouper.Add(swipe)
	if err != nil {
		t.Fatalf("swipe Add() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != action.Swipe || actions[0].End == nil || actions[0].End.X != 900 {
		t.Fatalf("swipe Add() = %#v, want preserved path", actions)
	}
}

func TestEventGrouperRejectsUnknownOrCrossDeviceInput(t *testing.T) {
	base := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	grouper := recordings.NewEventGrouper(recordings.GroupingOptions{})
	if _, err := grouper.Add(recordings.RawInput{DeviceID: "device-1", Kind: recordings.RawInputKind("shell"), At: base, Sensitivity: recordings.SensitivityNone}); err == nil {
		t.Fatal("unknown input error = nil")
	}
	first := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputText, At: base, Text: "a", Sensitivity: recordings.SensitivityNone, Target: target("field")}
	if _, err := grouper.Add(first); err != nil {
		t.Fatal(err)
	}
	second := recordings.RawInput{DeviceID: "device-2", Kind: recordings.InputText, At: base.Add(time.Millisecond), Text: "b", Sensitivity: recordings.SensitivityNone, Target: target("field")}
	if _, err := grouper.Add(second); err == nil {
		t.Fatal("cross-device input error = nil")
	}
}
