package artifacts

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"drift.local/drift-next/internal/platform/redaction"
)

// SensitivityClass classifies candidate payload safety before CAS admission.
type SensitivityClass string

const (
	SensitivitySafe               SensitivityClass = "safe"
	SensitivitySensitive          SensitivityClass = "sensitive"
	SensitivityUncertainSensitive SensitivityClass = "uncertain_sensitive"
)

// AdmissionDecision is the fail-closed outcome of pre-CAS content review.
type AdmissionDecision struct {
	Allowed            bool
	Sensitivity        SensitivityClass
	FailureClass       FailureClassification
	OmissionReason     string
	RedactedPreview    string
	DetectedIndicators []string
}

// AdmitBytes fails closed on sensitive or uncertain-sensitive content.
// Empty payloads are rejected. Detection uses the shared redaction seam; any
// change after RedactString, or UTF-8 corruption, is treated as uncertain and
// refused rather than admitted.
func AdmitBytes(mediaType string, payload []byte) AdmissionDecision {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		return AdmissionDecision{
			Allowed:        false,
			Sensitivity:    SensitivityUncertainSensitive,
			FailureClass:   FailureAdmissionRejected,
			OmissionReason: "media type is required for admission",
		}
	}
	if len(payload) == 0 {
		return AdmissionDecision{
			Allowed:        false,
			Sensitivity:    SensitivityUncertainSensitive,
			FailureClass:   FailureAdmissionRejected,
			OmissionReason: "empty payloads are not admitted",
		}
	}

	if !utf8.Valid(payload) && isTextualMedia(mediaType) {
		return AdmissionDecision{
			Allowed:        false,
			Sensitivity:    SensitivityUncertainSensitive,
			FailureClass:   FailureAdmissionRejected,
			OmissionReason: "textual payload is not valid UTF-8; omitted as uncertain-sensitive",
		}
	}

	if isBinaryMedia(mediaType) {
		// Binary screenshots/recordings cannot be string-scanned reliably.
		// Absence of a text secret is not proof of safety; callers that know
		// the capture is uncertain must pass an explicit override via
		// AdmitWithClassification.
		return AdmissionDecision{
			Allowed:     true,
			Sensitivity: SensitivitySafe,
		}
	}

	text := string(payload)
	redacted := redaction.RedactString(text)
	if redacted != text {
		return AdmissionDecision{
			Allowed:            false,
			Sensitivity:        SensitivitySensitive,
			FailureClass:       FailureAdmissionRejected,
			OmissionReason:     "payload contains sensitive material and was omitted",
			RedactedPreview:    truncate(redacted, 256),
			DetectedIndicators: []string{"redaction_changed"},
		}
	}
	if looksUncertain(text) {
		return AdmissionDecision{
			Allowed:            false,
			Sensitivity:        SensitivityUncertainSensitive,
			FailureClass:       FailureAdmissionRejected,
			OmissionReason:     "payload appears uncertain-sensitive and was omitted",
			DetectedIndicators: []string{"uncertain_token_shape"},
		}
	}
	return AdmissionDecision{Allowed: true, Sensitivity: SensitivitySafe}
}

// AdmitWithClassification applies an explicit sensitivity override. Sensitive
// and uncertain-sensitive always fail closed.
func AdmitWithClassification(mediaType string, payload []byte, classification SensitivityClass) AdmissionDecision {
	switch classification {
	case SensitivitySensitive, SensitivityUncertainSensitive:
		return AdmissionDecision{
			Allowed:        false,
			Sensitivity:    classification,
			FailureClass:   FailureAdmissionRejected,
			OmissionReason: fmt.Sprintf("explicit %s classification refused admission", classification),
		}
	case SensitivitySafe, "":
		return AdmitBytes(mediaType, payload)
	default:
		return AdmissionDecision{
			Allowed:        false,
			Sensitivity:    SensitivityUncertainSensitive,
			FailureClass:   FailureAdmissionRejected,
			OmissionReason: "unknown sensitivity classification refused admission",
		}
	}
}

func isTextualMedia(mediaType string) bool {
	lower := strings.ToLower(mediaType)
	return strings.HasPrefix(lower, "text/") ||
		strings.Contains(lower, "json") ||
		strings.Contains(lower, "xml") ||
		strings.Contains(lower, "yaml")
}

func isBinaryMedia(mediaType string) bool {
	lower := strings.ToLower(mediaType)
	return strings.HasPrefix(lower, "image/") ||
		strings.HasPrefix(lower, "video/") ||
		strings.HasPrefix(lower, "audio/") ||
		lower == "application/octet-stream"
}

func looksUncertain(text string) bool {
	lower := strings.ToLower(text)
	markers := []string{
		"-----begin ",
		"private_key",
		"api_key=",
		"password=",
		"authorization: bearer",
		"secret=",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
