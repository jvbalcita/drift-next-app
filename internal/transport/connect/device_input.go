package transportconnect

import (
	"regexp"
	"strings"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
)

// Bounds for the typed device input contract. Each mirrors the bound the action
// catalog enforces for the same payload, so a value that reaches the kernel is
// bounded twice.
const (
	maxRenderDimension  = 10000
	maxSwipeDurationMS  = 300000
	maxTypedTextLength  = 1 << 20
	maxKeyCode          = 10000
	maxTextHandleLength = 128
	maxComponentLength  = 255
)

var (
	// A text reference handle is an opaque, bounded reference: no whitespace and
	// no content punctuation, so a caller cannot smuggle text content through the
	// only string this contract offers for the typed text input.
	textHandlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
	// A launch target is a package name (reverse-DNS) and, optionally, an
	// activity component. Both are names, never command text.
	packageNamePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	componentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
)

// ValidateDeviceInputIntent enforces the typed device input contract for the
// five first-class inputs: tap, swipe, typed text, key event and app launch.
//
// An input must name exactly one action kind and carry exactly that kind's
// payload. A payload that is absent, incomplete, or the member of a different
// kind is refused here, before the kernel is asked to authorize anything, and
// so is a coordinate that arrives without the render space it was measured in.
//
// The kinds that carry no typed input payload — observation, health check,
// capture and the remaining system-key actions — are unaffected: this contract
// narrows what a caller may submit, and never widens it.
func ValidateDeviceInputIntent(msg *driftv1.ActionIntent) error {
	if msg == nil {
		return invalidArgument("action intent is required")
	}
	switch msg.GetKind() {
	case driftv1.ActionKind_ACTION_KIND_TAP:
		return validateTapInput(msg)
	case driftv1.ActionKind_ACTION_KIND_SWIPE:
		return validateSwipeInput(msg)
	case driftv1.ActionKind_ACTION_KIND_TEXT_INPUT:
		return validateTypeTextInput(msg)
	case driftv1.ActionKind_ACTION_KIND_KEY_EVENT:
		return validateKeyEventInput(msg)
	case driftv1.ActionKind_ACTION_KIND_LAUNCH_APP:
		return validateLaunchAppInput(msg)
	default:
		return nil
	}
}

func validateTapInput(msg *driftv1.ActionIntent) error {
	return validateTapPayload(msg.GetTap(), msg.GetObservationToken())
}

// validateTapPayload is the one implementation of the tap contract. The action
// intent path and the device input surface both call it, so a requirement added
// to one cannot go missing from the other: two copies of a validation rule is
// two chances for them to disagree, and the disagreement would be a payload the
// device receives that only one boundary checked.
func validateTapPayload(tap *driftv1.TapInput, observationToken string) error {
	if tap == nil {
		return invalidArgument("a tap requires its typed tap payload")
	}
	hasTarget := semanticTargetPresent(tap.GetTarget())
	hasPoint := tap.GetPoint() != nil
	if hasTarget == hasPoint {
		return invalidArgument("a tap names exactly one target: a semantic target or an approved render-space point")
	}
	if !hasPoint {
		if tap.GetRenderSpace() != nil {
			return invalidArgument("a tap with a semantic target must not carry a coordinate render space")
		}
		return nil
	}
	return validateDevicePoint(tap.GetPoint(), tap.GetRenderSpace(), observationToken)
}

func validateSwipeInput(msg *driftv1.ActionIntent) error {
	return validateSwipePayload(msg.GetSwipe(), msg.GetObservationToken())
}

// validateSwipePayload is the one implementation of the swipe contract, shared
// for the same reason validateTapPayload is.
func validateSwipePayload(swipe *driftv1.SwipeInput, observationToken string) error {
	if swipe == nil {
		return invalidArgument("a swipe requires its typed swipe payload")
	}
	if swipe.GetStart() == nil || swipe.GetEnd() == nil {
		return invalidArgument("a swipe requires both endpoints")
	}
	if duration := swipe.GetDurationMs(); duration == 0 || duration > maxSwipeDurationMS {
		return invalidArgument("a swipe duration must be between 1 and 300000 milliseconds")
	}
	if err := validateDevicePoint(swipe.GetStart(), swipe.GetRenderSpace(), observationToken); err != nil {
		return err
	}
	return validateDevicePoint(swipe.GetEnd(), swipe.GetRenderSpace(), observationToken)
}

func validateTypeTextInput(msg *driftv1.ActionIntent) error {
	typed := msg.GetTypeText()
	if typed == nil {
		return invalidArgument("typed text requires its typed text payload")
	}
	return validateTypeTextReference(typed.GetText())
}

// validateTypeTextReference is the one implementation of the typed-text
// reference contract. The action intent path and the device input surface both
// call it, so a requirement added to one cannot go missing from the other.
//
// It reads the reference and never a value, because there is no field for one:
// the handle is opaque by pattern (no whitespace, no content punctuation, so
// content cannot be smuggled through the only string this carries) and the
// length is what a caller may bound an input by without the content being read.
func validateTypeTextReference(reference *driftv1.SensitiveTextReference) error {
	if reference == nil {
		return invalidArgument("typed text requires a text reference")
	}
	handle := reference.GetHandle()
	if handle == "" || len(handle) > maxTextHandleLength || !textHandlePattern.MatchString(handle) {
		return invalidArgument("a text reference handle must be an opaque bounded reference")
	}
	if length := reference.GetValueLength(); length == 0 || length > maxTypedTextLength {
		return invalidArgument("a text reference requires a bounded value length")
	}
	return nil
}

func validateKeyEventInput(msg *driftv1.ActionIntent) error {
	return validateKeyEventPayload(msg.GetKeyEvent())
}

// validateKeyEventPayload is the one implementation of the key event contract,
// shared by the action intent path and the device input surface.
func validateKeyEventPayload(keyEvent *driftv1.KeyEventInput) error {
	if keyEvent == nil {
		return invalidArgument("a key event requires its typed key event payload")
	}
	if code := keyEvent.GetKeyCode(); code == 0 || code > maxKeyCode {
		return invalidArgument("a key event requires a bounded key code")
	}
	return nil
}

func validateLaunchAppInput(msg *driftv1.ActionIntent) error {
	launch := msg.GetLaunchApp()
	if launch == nil {
		return invalidArgument("an app launch requires its typed launch payload")
	}
	if name := launch.GetPackageName(); len(name) > maxComponentLength || !packageNamePattern.MatchString(name) {
		return invalidArgument("an app launch requires a package name")
	}
	if activity := launch.GetActivityName(); activity != "" {
		if len(activity) > maxComponentLength || !componentNamePattern.MatchString(activity) || !strings.Contains(activity, ".") || strings.Contains(activity, "..") {
			return invalidArgument("an app launch activity must be a bounded component name")
		}
	}
	return nil
}

// validateDevicePoint requires the frame a coordinate was measured in and
// refuses a coordinate that lies outside it. Nothing is scaled: a coordinate
// resolved against a different frame is a defect, not a rounding problem.
func validateDevicePoint(point *driftv1.DevicePoint, space *driftv1.DeviceRenderSpace, observationToken string) error {
	if space == nil {
		return invalidArgument("a device coordinate requires the render space it was measured in")
	}
	width, height := space.GetRenderWidth(), space.GetRenderHeight()
	if width == 0 || height == 0 || width > maxRenderDimension || height > maxRenderDimension {
		return invalidArgument("a device render space requires a bounded render size")
	}
	token := strings.TrimSpace(space.GetObservationToken())
	if token == "" || token != strings.TrimSpace(observationToken) {
		return invalidArgument("a device render space must name the observation the action was resolved against")
	}
	if point.GetX() >= width || point.GetY() >= height {
		return invalidArgument("a device coordinate lies outside its render space")
	}
	return nil
}

func semanticTargetPresent(target *driftv1.SemanticTarget) bool {
	if target == nil {
		return false
	}
	for _, value := range []string{target.GetResourceId(), target.GetAccessibilityLabel(), target.GetStableText(), target.GetContextFingerprint()} {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
