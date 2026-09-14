package recordings

import (
	"strings"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/platform/redaction"
)

// RedactAction returns a safe copy and never mutates the grouped action. It is
// also used by callers that need to normalize a manually assembled action.
func RedactAction(input LogicalAction) LogicalAction {
	result := input
	if result.Sensitivity == "" {
		result.Sensitivity = SensitivityNone
	}
	if result.Sensitivity != SensitivityNone || sensitiveTarget(result.Target) {
		if result.Sensitivity == SensitivityNone {
			result.Sensitivity = SensitivityUncertain
		}
		result.DisplayValue = redaction.Replacement
	}
	if input.Gesture != nil {
		gesture := *input.Gesture
		gesture.Points = append([]action.Coordinate(nil), input.Gesture.Points...)
		result.Gesture = &gesture
	}
	return result
}

// SanitizeCapture strips all evidence bytes that could not be reliably
// sanitized before artifact admission. The omission is explicit and keeps the
// partial event useful without falsely labeling a derivative as raw evidence.
func SanitizeCapture(input Capture, phase CapturePhase) (Capture, []CaptureError) {
	result := input
	if result.Status == "" {
		result.Status = CaptureComplete
	}
	if result.Sanitization == "" {
		result.Sanitization = Sanitized
	}
	result.Evidence = append([]EvidenceReference(nil), input.Evidence...)
	if result.Sanitization != Unsanitizable {
		return result, nil
	}
	result.Status = CapturePartial
	result.Partial = true
	result.ErrorClass = domain.FailureObservation
	result.ScreenshotHash = ""
	result.UITreeHash = ""
	result.Target = nil
	omitted := make([]EvidenceReference, 0, len(result.Evidence))
	for _, reference := range result.Evidence {
		if reference.Omitted {
			omitted = append(omitted, reference)
			continue
		}
		omitted = append(omitted, EvidenceReference{Kind: reference.Kind, Phase: phase, Omitted: true, OmissionReason: "capture could not be sanitized before artifact admission"})
	}
	result.Evidence = omitted
	return result, []CaptureError{{Phase: phase, Class: domain.FailureObservation, Message: "capture evidence omitted because sanitization was unavailable", Omitted: true}}
}

// IsSensitiveTarget identifies target metadata that must not cross a durable
// recording boundary. It is shared with persistence so the storage adapter
// cannot accidentally retain password-like selectors.
func IsSensitiveTarget(target TargetMetadata) bool {
	for _, value := range []string{target.Semantic.ResourceID, target.Semantic.AccessibilityLabel, target.Semantic.StableText, target.ClassName} {
		value = strings.ToLower(value)
		for _, marker := range []string{"password", "passcode", "secret", "token", "credential", "cvv", "pin"} {
			if strings.Contains(value, marker) {
				return true
			}
		}
	}
	return false
}

func sensitiveTarget(target TargetMetadata) bool {
	return IsSensitiveTarget(target)
}
