package recordings_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/recordings"
)

func TestNormalizeActionRedactsSensitiveTextBeforePersistence(t *testing.T) {
	input := recordings.GroupedAction{DeviceID: "device-1", Kind: action.TextInput, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Text: "secret-value", Sensitivity: recordings.SensitivitySensitive, Target: target("password-field")}
	normalized, err := recordings.NormalizeAction(input)
	if err != nil {
		t.Fatalf("NormalizeAction() error = %v", err)
	}
	if normalized.DisplayValue != "[REDACTED]" || normalized.ValueLength != len(input.Text) || normalized.Sensitivity != recordings.SensitivitySensitive {
		t.Fatalf("normalized action = %#v, want safe display metadata", normalized)
	}
}

func TestSanitizeCaptureOmitsUnsanitizableEvidence(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	capture := recordings.Capture{CapturedAt: now, ObservationID: "obs-1", FreshnessToken: "token-safe-id", CoordinateSpace: "display:1080x1920", PackageName: "com.example", ActivityName: ".Main", Status: recordings.CaptureComplete, Sanitization: recordings.Unsanitizable, ScreenshotHash: "sha256:private", UITreeHash: "sha256:tree", Evidence: []recordings.EvidenceReference{{Kind: recordings.EvidenceRawScreenshot, Phase: recordings.PhaseBefore, ContentHash: "sha256:private", MediaType: "image/png", SchemaVersion: 1, Authoritative: true}}}
	sanitized, errors := recordings.SanitizeCapture(capture, recordings.PhaseBefore)
	if len(errors) != 1 || sanitized.ScreenshotHash != "" || sanitized.UITreeHash != "" || sanitized.Target != nil || sanitized.Status != recordings.CapturePartial {
		t.Fatalf("sanitized capture = %#v, errors = %#v", sanitized, errors)
	}
	if len(sanitized.Evidence) != 1 || !sanitized.Evidence[0].Omitted || sanitized.Evidence[0].ContentHash != "" || sanitized.Evidence[0].OmissionReason == "" {
		t.Fatalf("sanitized evidence = %#v, want explicit omission", sanitized.Evidence)
	}
	if err := capture.Validate(); err == nil {
		t.Fatal("unsanitizable capture with retained evidence validated")
	}
	if err := sanitized.Validate(); err != nil {
		t.Fatalf("sanitized capture validation error = %v", err)
	}
}
