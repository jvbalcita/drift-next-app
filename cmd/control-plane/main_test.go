package main

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/media"
)

type unusedMirrorDialer struct{}

func (unusedMirrorDialer) Dial(context.Context, string, string, media.MirrorViewerPurpose, media.MirrorPreview) (media.MirrorStream, error) {
	return nil, errors.New("the composition test never opens a device")
}

func TestMirrorInputDeliveryIsBoundExactlyWhenTheLiveEngineExists(t *testing.T) {
	withoutEngine, err := mirrorInputDelivery(nil)
	if err != nil || withoutEngine != nil {
		t.Fatalf("delivery without engine = (%T, %v), want (nil, nil)", withoutEngine, err)
	}
	engine, err := media.NewMirrorEngine(media.MirrorEngineConfig{Dialer: unusedMirrorDialer{}})
	if err != nil {
		t.Fatalf("construct mirror engine: %v", err)
	}
	delivery, err := mirrorInputDelivery(engine)
	if err != nil {
		t.Fatalf("bind mirror input delivery: %v", err)
	}
	if delivery == nil {
		t.Fatal("a constructed live engine did not bind the dispatcher delivery")
	}
	if delivery.Mirrored("device-1") {
		t.Fatal("an engine with no subscribed session reported a mirrored device")
	}
}

func TestLiveMirrorControlDefaultsOnWithExplicitOff(t *testing.T) {
	for _, value := range []string{"", "true", " TRUE ", "1", "yes", "on"} {
		if !liveMirrorControlEnabled(value) {
			t.Fatalf("setting %q should arm live control", value)
		}
	}
	for _, value := range []string{"false", "FALSE", "0", "off", "Off"} {
		if liveMirrorControlEnabled(value) {
			t.Fatalf("setting %q should disable live control", value)
		}
	}
}
