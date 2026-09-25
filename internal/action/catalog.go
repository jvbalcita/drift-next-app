// Package action owns the typed, allow-listed action catalog. It does not
// expose raw device protocols, shell commands, or unrestricted coordinates.
package action

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
)

type Kind string

const (
	Observe     Kind = "observe"
	HealthCheck Kind = "health_check"
	Capture     Kind = "capture"
	Tap         Kind = "tap"
	DoubleTap   Kind = "double_tap"
	LongPress   Kind = "long_press"
	TextInput   Kind = "text_input"
	TextDelete  Kind = "text_delete"
	Clear       Kind = "clear"
	Swipe       Kind = "swipe"
	Scroll      Kind = "scroll"
	Drag        Kind = "drag"
	// LiveGesture begins at touch-down, before the path or terminal point is
	// known. One bounded attempt covers its down, moves and terminal event.
	LiveGesture Kind = "live_gesture"
	Back        Kind = "back"
	Home        Kind = "home"
	Enter       Kind = "enter"
	KeyEvent    Kind = "key_event"
	LaunchApp   Kind = "launch_app"
	UIChange    Kind = "ui_change"
	StateChange Kind = "state_change"
	// RotationLock and AutofillOff are the two device settings the fleet
	// actually requires, as CATALOGUED general commands: each is a named
	// operation with a fixed argument array and no caller-supplied parameter,
	// so nothing an operator types can reach the device through either kind.
	RotationLock Kind = "rotation_lock"
	AutofillOff  Kind = "autofill_off"
	// The big-frame control panel's remaining commands, each a CATALOGUED
	// general command of AGENTS.md section 3 (a named operation with a bounded
	// typed parameter set) rather than a raw command string:
	//
	//   - Reboot is a lifecycle operation whose argument array carries no
	//     caller value at all;
	//   - KeyboardSwitch reads the device's own enabled-IME list, sets the
	//     component the PLANE selected from that list, and reads the device's
	//     secure default back;
	//   - InstallApk, ImportFile and ExportFile move bytes between the
	//     workspace's content-addressed artifact store and one device
	//     directory this product owns, and every device path is composed by
	//     the plane from a bounded file NAME rather than supplied as a path;
	//   - AdvancedCommand is the ADVANCED form of AGENTS.md section 3: an
	//     operator-entered argument array, dispatched only after the operator
	//     confirms the exact argv, with its own append-only audit record.
	//
	// AdvancedCommand is deliberately a kind of its own. It shares the kernel
	// with every catalogued kind and nothing else: it is reached by its own
	// request, its own recogniser and its own confirmation, so a request that
	// means a catalogued operation can never arrive at it.
	Reboot          Kind = "reboot"
	KeyboardSwitch  Kind = "keyboard_switch"
	InstallApk      Kind = "install_apk"
	ImportFile      Kind = "import_file"
	ExportFile      Kind = "export_file"
	AdvancedCommand Kind = "advanced_command"
)

type RiskClass string

const (
	RiskLow          RiskClass = "low"
	RiskMedium       RiskClass = "medium"
	RiskHigh         RiskClass = "high"
	RiskIrreversible RiskClass = "irreversible"
)

func (r RiskClass) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskIrreversible:
		return true
	default:
		return false
	}
}

type RetryClass string

const (
	RetrySafe             RetryClass = "safe"
	RetryAfterObservation RetryClass = "after_observation"
	RetryNeverBlind       RetryClass = "never_blind"
)

func (r RetryClass) Valid() bool {
	switch r {
	case RetrySafe, RetryAfterObservation, RetryNeverBlind:
		return true
	default:
		return false
	}
}

type InvocationSurface string

const (
	SurfaceManual       InvocationSurface = "manual"
	SurfaceRecorder     InvocationSurface = "recorder"
	SurfaceReplay       InvocationSurface = "replay"
	SurfaceMirror       InvocationSurface = "mirror"
	SurfaceAISuggestion InvocationSurface = "ai_suggestion"
)

type Capability string

const (
	CapabilityObserve     Capability = "device.observe"
	CapabilityHealth      Capability = "device.health"
	CapabilityCapture     Capability = "device.capture"
	CapabilityTap         Capability = "device.input.tap"
	CapabilityGesture     Capability = "device.input.gesture"
	CapabilityTextInput   Capability = "device.input.text"
	CapabilitySystemInput Capability = "device.input.system"
	// CapabilityDeviceSettings is what a device must be able to do for a
	// catalogued settings operation to run on it: change its own system and
	// secure settings and read them back. It is a capability of its own rather
	// than a reuse of device.input.system, because an operator granting input
	// authority is not thereby granting authority to rewrite the device's
	// settings.
	CapabilityDeviceSettings Capability = "device.settings"
	// CapabilityDeviceLifecycle is what a device must be able to do for a
	// catalogued LIFECYCLE operation to run on it: restart itself. It is its
	// own capability rather than a reuse of device.input.system, because an
	// operator granting input authority is not thereby granting authority to
	// take a device out of service and bring it back.
	CapabilityDeviceLifecycle Capability = "device.lifecycle"
	// CapabilityDeviceFiles is what a device must be able to do for a
	// catalogued FILE operation to run on it: receive named bytes into, and
	// give named bytes back from, the one device directory this product owns.
	// It is its own capability because moving bytes onto a device is neither
	// input into it nor a change to its settings.
	CapabilityDeviceFiles Capability = "device.files"
	// CapabilityDeviceCommand is what a device must be able to do for the
	// ADVANCED form to run on it: accept one operator-confirmed argument
	// array. It is deliberately separate from every other capability, so a
	// policy that admits typed input, settings and lifecycle operations does
	// not thereby admit an operator-entered argv.
	CapabilityDeviceCommand Capability = "device.command"
)

func (c Capability) Valid() bool {
	switch c {
	case CapabilityObserve, CapabilityHealth, CapabilityCapture, CapabilityTap, CapabilityGesture, CapabilityTextInput, CapabilitySystemInput, CapabilityDeviceSettings,
		CapabilityDeviceLifecycle, CapabilityDeviceFiles, CapabilityDeviceCommand:
		return true
	default:
		return false
	}
}

type SemanticTarget struct {
	ResourceID         string
	AccessibilityLabel string
	StableText         string
	ContextFingerprint string
}

func (t SemanticTarget) Empty() bool {
	return strings.TrimSpace(t.ResourceID) == "" && strings.TrimSpace(t.AccessibilityLabel) == "" && strings.TrimSpace(t.StableText) == "" && strings.TrimSpace(t.ContextFingerprint) == ""
}

// Coordinate is a bounded, explicitly scoped fallback input. It is never a
// substitute for a semantic target during skill promotion; replay may use it
// only after semantic resolution failed and an operator has confirmed the
// constrained fallback.
type Coordinate struct {
	Space string
	X     int
	Y     int
}

// CoordinateFallback carries a typed coordinate or gesture endpoint for the
// last-resort replay path. It contains no raw protocol payload or shell data.
type CoordinateFallback struct {
	Start     Coordinate
	End       *Coordinate
	Path      []Coordinate
	Duration  time.Duration
	Confirmed bool
}

// GesturePath is the typed representation of a swipe, scroll, or drag. It
// is retained as evidence and as a compiled skill payload, never as an
// unbounded raw input stream.
type GesturePath struct {
	Points     []Coordinate
	DurationMs int64
}

func (g GesturePath) Validate() error {
	if len(g.Points) < 2 || len(g.Points) > 128 || g.DurationMs < 0 || g.DurationMs > 5*60*1000 {
		return fmt.Errorf("gesture path is invalid")
	}
	space := strings.TrimSpace(g.Points[0].Space)
	if space == "" {
		return fmt.Errorf("gesture coordinate space is required")
	}
	for _, point := range g.Points {
		if point.Space != space || !validCoordinate(point) {
			return fmt.Errorf("gesture coordinates are invalid")
		}
	}
	return nil
}

type Intent struct {
	ID           string
	Workspace    string
	DeviceID     string
	LeaseID      string
	HolderID     string
	FencingToken uint64
	Kind         Kind
	Target       SemanticTarget
	TextValue    string
	ValueLength  int
	Gesture      *GesturePath
	// LiveStart is the bounded down point known when a streamed gesture is
	// authorized; later moves are checked against the same encoded frame.
	LiveStart          *LiveGestureStart
	KeyCode            int
	IdempotencyKey     string
	ObservationToken   string
	InvocationSurface  InvocationSurface
	Capabilities       []Capability
	ApprovalGranted    bool
	Timeout            time.Duration
	RequestHash        string
	CoordinateFallback *CoordinateFallback
	// Launch is what an app launch names: the package, and optionally the one
	// activity of it, that the launch brings to the foreground. It is nil for
	// every other kind, and it is covered by the request hash, so two launches
	// naming different targets stay two different requests.
	Launch *LaunchTarget
}

type LiveGestureStart struct {
	X, Y          int
	Width, Height int
}

func (s LiveGestureStart) Validate() error {
	if s.Width <= 0 || s.Height <= 0 || s.Width > 10000 || s.Height > 10000 || s.X < 0 || s.Y < 0 || s.X >= s.Width || s.Y >= s.Height {
		return fmt.Errorf("live gesture start is outside its bounded render frame")
	}
	return nil
}

// LaunchTarget is the target of an app launch. Both names are bounded
// component names rather than command text, which is what lets the target reach
// the device as an allow-listed argument array and nowhere else.
type LaunchTarget struct {
	PackageName  string
	ActivityName string
}

// Validate refuses a launch target that is not a bounded package name and, when
// an activity is named, not a bounded component name. The rule is the domain's
// rule, so the kernel cannot admit a target that the boundary building the
// argument array would refuse.
func (l LaunchTarget) Validate() error {
	if !domain.IsPackageName(l.PackageName) {
		return fmt.Errorf("an app launch requires a package name")
	}
	if strings.TrimSpace(l.ActivityName) == "" {
		return nil
	}
	if !domain.IsActivityComponent(l.ActivityName) {
		return fmt.Errorf("an app launch activity must be a bounded component name")
	}
	return nil
}

// Postcondition is the explicit statement of what must be true after a
// successful dispatch of a kind. It is the declarative counterpart of
// PostconditionState: the state records whether a postcondition was observed,
// this declares what has to be observed. An entry without one is incomplete and
// is refused rather than dispatched.
type Postcondition string

// DeferralLift records, on the entry itself, why a kind that a documented
// deferral previously refused is dispatchable. A dispatchable kind must not owe
// its availability to a silently flipped flag: the record names the deferral's
// precondition and the reason this kind may now be authorized, so a reviewer can
// see the difference between a kind that was never deferred and one that was.
type DeferralLift struct {
	// Record is the document that records the lift.
	Record string
	// Precondition is the condition the deferral named before the kind could be
	// dispatchable.
	Precondition string
	// Reason is why this kind is permitted now that the precondition is met.
	Reason string
}

type Specification struct {
	Kind                 Kind
	RequiredCapabilities []Capability
	Risk                 RiskClass
	Retry                RetryClass
	// Mutating is the entry's classification: true means dispatch delivers
	// input or a state change the device acts on, false means dispatch injects
	// no input into the device. MutationReason states why it is classified so.
	Mutating            bool
	MutationReason      string
	Postcondition       Postcondition
	RequiresTarget      bool
	RequiresObservation bool
	EvidenceRequired    bool
	AllowedSurfaces     []InvocationSurface
	// Lifted is nil for a kind that was never deferred, and carries the lift
	// record for a kind a deferral previously refused.
	Lifted *DeferralLift
}

// Validate refuses a catalog entry that a reviewer or the policy evaluator
// could not act on: it must declare its identity, at least one allow-listed
// capability, a risk and retry class, why it is classified mutating or
// read-only, what must be true after a successful dispatch, and where it may be
// invoked from.
func (s Specification) Validate() error {
	if strings.TrimSpace(string(s.Kind)) == "" {
		return fmt.Errorf("action identity is required")
	}
	if len(s.RequiredCapabilities) == 0 {
		return fmt.Errorf("action %q declares no capability", s.Kind)
	}
	for _, capability := range s.RequiredCapabilities {
		if !capability.Valid() {
			return fmt.Errorf("action %q requires capability %q that is not allow-listed", s.Kind, capability)
		}
	}
	if !s.Risk.Valid() {
		return fmt.Errorf("action %q declares an invalid risk class", s.Kind)
	}
	if !s.Retry.Valid() {
		return fmt.Errorf("action %q declares an invalid retry class", s.Kind)
	}
	if strings.TrimSpace(s.MutationReason) == "" {
		return fmt.Errorf("action %q does not state its mutation reason (mutating=%t)", s.Kind, s.Mutating)
	}
	if strings.TrimSpace(string(s.Postcondition)) == "" {
		return fmt.Errorf("action %q declares no postcondition", s.Kind)
	}
	if len(s.AllowedSurfaces) == 0 {
		return fmt.Errorf("action %q declares no allowed invocation surface", s.Kind)
	}
	if s.Lifted != nil {
		for name, value := range map[string]string{"record": s.Lifted.Record, "precondition": s.Lifted.Precondition, "reason": s.Lifted.Reason} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("action %q carries a deferral lift with no %s", s.Kind, name)
			}
		}
	}
	return nil
}

// Classification and lift metadata shared by the entries below. The five typed
// device inputs each state their own classification and lift reason, because
// each has a different reason to be permitted; every other kind reuses the
// reason for its class.
const (
	// readOnlyMutationReason is the classification reason for a kind whose
	// dispatch injects no input into the device.
	readOnlyMutationReason = "dispatch reads device state and records the observation or artifact locally; it injects no input into the device"
	// mutatingMutationReason is the classification reason for a kind whose
	// dispatch delivers input or a state change the device acts on.
	mutatingMutationReason = "dispatch delivers input the device acts on, so its state after the action may differ from the observation it was resolved against"
	// deviceInputLiftRecord is the accepted record for the domain registration of
	// the typed device inputs, and liftedInputPrecondition is the precondition
	// the device-command deferral named before any input could be dispatchable.
	// A kind that a deferral previously refused carries both on its entry.
	deviceInputLiftRecord   = "docs/adr/0009-device-input-catalog-registration.md"
	liftedInputPrecondition = "a per-device lease with a fencing token, idempotency, a policy and capability decision, control-session authority and an emergency stop are wired and enforced before dispatch"
	// retiredCommandBanRecord is the accepted record that retired the blanket
	// ban on a general device command, and liftedCommandPrecondition is the
	// precondition that retirement named before a catalogued operation could be
	// dispatchable. The two settings kinds below are CATALOGUED general
	// commands, so each carries both: a reviewer can tell a newly permitted
	// kind from one that was never deferred.
	retiredCommandBanRecord   = "docs/adr/0016-catalogued-device-settings.md"
	liftedCommandPrecondition = "the operation is a CATALOGUED named operation with a bounded typed parameter set, its argument array is a fixed builder shape the device adapter admits by its own recogniser, and the lease, fencing, policy, control-session and emergency-stop kernel authorizes and dispatches it as an argv array spawned without a shell"
)

var catalog = map[Kind]Specification{
	Observe: {
		Kind:                 Observe,
		RequiredCapabilities: []Capability{CapabilityObserve},
		Risk:                 RiskLow,
		Retry:                RetrySafe,
		MutationReason:       readOnlyMutationReason,
		Postcondition:        "a fresh observation of the named device is recorded with its capture provenance and freshness token",
		EvidenceRequired:     true,
		AllowedSurfaces:      allSurfaces(),
	},
	HealthCheck: {
		Kind:                 HealthCheck,
		RequiredCapabilities: []Capability{CapabilityHealth},
		Risk:                 RiskLow,
		Retry:                RetrySafe,
		MutationReason:       readOnlyMutationReason,
		Postcondition:        "the device readiness at the moment of the check is recorded, and no input was injected",
		EvidenceRequired:     true,
		AllowedSurfaces:      allSurfaces(),
	},
	Capture: {
		Kind:                 Capture,
		RequiredCapabilities: []Capability{CapabilityCapture},
		Risk:                 RiskLow,
		Retry:                RetrySafe,
		MutationReason:       readOnlyMutationReason,
		Postcondition:        "an artifact is written atomically and referenced by the observation that produced it",
		EvidenceRequired:     true,
		AllowedSurfaces:      allSurfaces(),
	},
	Tap: {
		Kind:                 Tap,
		RequiredCapabilities: []Capability{CapabilityTap},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       "dispatch injects a tap the device acts on, so the surface under the resolved target can change",
		Postcondition:        "a fresh observation shows the effect of the tap, measured against the observation the tap was resolved from",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "ADR-0008 lifts the device-command deferral for typed input; a tap carries a typed payload and no command text, and is refused before authorization when that payload is absent or incomplete",
		},
	},
	DoubleTap: {
		Kind:                 DoubleTap,
		RequiredCapabilities: []Capability{CapabilityTap},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the effect of the double tap",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
	},
	LongPress: {
		Kind:                 LongPress,
		RequiredCapabilities: []Capability{CapabilityTap},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the completed long press",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
	},
	TextInput: {
		Kind:                 TextInput,
		RequiredCapabilities: []Capability{CapabilityTextInput},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch injects typed text into the device; the value is resolved from its reference handle at dispatch and is never carried by this specification, an error or a log",
		Postcondition:        "a fresh observation shows the targeted field holding the referenced text, resolved through its reference handle and recorded in no attempt row, error or log",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "ADR-0008 lifts the device-command deferral and admits typed text only as an opaque reference handle, so no plaintext value can be logged, rendered in an error or persisted; dispatch still requires a resolver for that handle, and until one exists this entry refuses a plaintext stand-in instead of dispatching content",
		},
	},
	TextDelete: {
		Kind:                 TextDelete,
		RequiredCapabilities: []Capability{CapabilityTextInput},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the targeted field with the deleted range removed",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
	},
	Clear: {
		Kind:                 Clear,
		RequiredCapabilities: []Capability{CapabilityTextInput},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the targeted field empty",
		RequiresTarget:       true,
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
	},
	Swipe: {
		Kind:                 Swipe,
		RequiredCapabilities: []Capability{CapabilityGesture},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       "dispatch injects a gesture the device acts on, so the content it moves can land anywhere inside the render space the action states",
		Postcondition:        "a fresh observation shows the content moved by the swipe at its new position",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "ADR-0008 lifts the device-command deferral for typed input; a swipe is a bounded gesture whose endpoints must lie inside the render space the action states, so it cannot address a frame or an observation the action was not resolved against",
		},
	},
	Scroll: {
		Kind:                 Scroll,
		RequiredCapabilities: []Capability{CapabilityGesture},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the scrolled content at its new position",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
	},
	Drag: {
		Kind:                 Drag,
		RequiredCapabilities: []Capability{CapabilityGesture},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the dragged item at its dropped position",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
	},
	LiveGesture: {
		Kind:                 LiveGesture,
		RequiredCapabilities: []Capability{CapabilityGesture},
		Risk:                 RiskMedium,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch begins a live touch sequence whose terminal point is unknown until the operator releases it",
		Postcondition:        "a fresh observation after the terminal touch event establishes the result of the gesture",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceMirror},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "the bounded binary touch sequence is authorized at down through the same lease, fencing, policy, session and emergency-stop kernel as other typed input",
		},
	},
	Back: {
		Kind:                 Back,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the surface the back key moved to",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
	},
	Home: {
		Kind:                 Home,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the device home surface in the foreground",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
	},
	Enter: {
		Kind:                 Enter,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the effect of the submitted input",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
	},
	KeyEvent: {
		Kind:                 KeyEvent,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch injects a key event the device acts on; the event is a bounded key code from the allow-list, never a command, an argument list or free-form text",
		Postcondition:        "a fresh observation shows the effect of the delivered key event",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "ADR-0008 lifts the device-command deferral for typed input; a mirror key event is one bounded key code carried by the selected, lease-bound live control channel, not a command, an argv list or free-form text",
		},
	},
	LaunchApp: {
		Kind:                 LaunchApp,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       "dispatch brings another package's activity to the foreground, so what the device is running differs from the observation the launch was resolved against",
		Postcondition:        "a fresh observation shows the named package's activity in the foreground",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
		Lifted: &DeferralLift{
			Record:       deviceInputLiftRecord,
			Precondition: liftedInputPrecondition,
			Reason:       "ADR-0008 lifts the device-command deferral and gives app launch a first-class identity (ACTION_KIND_LAUNCH_APP); its payload names a bounded package and an optional activity component, which are names rather than command text",
		},
	},
	UIChange: {
		Kind:                 UIChange,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the requested UI change relative to the observation it was resolved from",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay},
	},
	StateChange: {
		Kind:                 StateChange,
		RequiredCapabilities: []Capability{CapabilitySystemInput},
		Risk:                 RiskIrreversible,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       mutatingMutationReason,
		Postcondition:        "a fresh observation shows the requested device state change",
		RequiresObservation:  true,
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
	},
	RotationLock: {
		Kind:                 RotationLock,
		RequiredCapabilities: []Capability{CapabilityDeviceSettings},
		Risk:                 RiskMedium,
		Retry:                RetrySafe,
		Mutating:             true,
		MutationReason:       "dispatch writes the device's own rotation settings (accelerometer_rotation and user_rotation), so the orientation the device presents at after the action may differ from the observation the action was resolved against",
		Postcondition:        "the device's own read-back shows accelerometer_rotation at 0 and user_rotation at 0, so auto-rotate is off and the device is held in its natural orientation",
		// No observation token is required: the action is a settings write whose
		// effect is confirmed by reading the setting back, not resolved against
		// a device observation it would have to be measured from.
		EvidenceRequired: true,
		AllowedSurfaces:  []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation with no caller-supplied parameter at all, so the argument array is a fixed builder shape and cannot express command text",
		},
	},
	AutofillOff: {
		Kind:                 AutofillOff,
		RequiredCapabilities: []Capability{CapabilityDeviceSettings},
		Risk:                 RiskHigh,
		Retry:                RetrySafe,
		Mutating:             true,
		MutationReason:       "dispatch removes the device's selected autofill service and disables the augmented autofill service, so the device's own secure settings differ from the observation the action was resolved against",
		Postcondition:        "the device's own read-back shows no autofill service selected (secure autofill_service is empty or null) and the augmented autofill service disabled, so no autofill popup can land on a form this fleet drives",
		EvidenceRequired:     true,
		// A high-risk kind, so the policy evaluator requires explicit operator
		// approval before the kernel authorizes it. It is deliberately not a
		// low-risk preference: it rewrites a secure setting.
		AllowedSurfaces: []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation whose three device commands are fixed admissions with no caller-supplied token, and whose own read-back is what reports it applied",
		},
	},
	Reboot: {
		Kind:                 Reboot,
		RequiredCapabilities: []Capability{CapabilityDeviceLifecycle},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch restarts the device, so everything the device was running - including the transport the action was resolved against - is gone after it",
		Postcondition:        "the device's transport stops answering within the operation's own settle window, so the departure the reboot caused was OBSERVED rather than assumed; a device that never departs inside that window does not satisfy this postcondition, and a device that returns resolves to the same identity",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation whose one admitted argument array carries no caller-supplied token at all, and whose postcondition is the departure a transport observer reads rather than the exit status of the command",
		},
	},
	KeyboardSwitch: {
		Kind:                 KeyboardSwitch,
		RequiredCapabilities: []Capability{CapabilityDeviceSettings},
		Risk:                 RiskMedium,
		Retry:                RetrySafe,
		Mutating:             true,
		MutationReason:       "dispatch writes the device's secure default input method, so the keyboard the device presents after the action may differ from the one it presented before",
		Postcondition:        "the device's own read-back of `secure default_input_method` names the component this operation set, and that component came from the device's OWN enabled-IME list rather than from anything an operator typed",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation whose write is a single bounded component NAME the plane selected from the device's own enabled-IME list, which is pattern-checked and cannot express a path, a flag or a second command",
		},
	},
	ImportFile: {
		Kind:                 ImportFile,
		RequiredCapabilities: []Capability{CapabilityDeviceFiles},
		Risk:                 RiskMedium,
		Retry:                RetryAfterObservation,
		Mutating:             true,
		MutationReason:       "dispatch writes the artifact's bytes onto the device, so the directory the file lands in differs from the observation the action was resolved against",
		Postcondition:        "the device's own read-back of the destination file's size equals the artifact's byte length, so the file is on the device as a fact the device reported rather than as a push that exited zero",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation whose SOURCE is an artifact the workspace already holds and whose device path the plane composes from one bounded file NAME inside the one directory this product owns, so no caller supplies a path on either side",
		},
	},
	InstallApk: {
		Kind:                 InstallApk,
		RequiredCapabilities: []Capability{CapabilityDeviceFiles},
		Risk:                 RiskHigh,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch installs a package on the device, so what the device can run differs from the observation the action was resolved against",
		Postcondition:        "the device's own `pm path` read-back names a code path for the package, so the package is present on the device after the install rather than merely pushed to it",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation whose installer argument is the package NAME the operator chose (the same bounded name an app launch already admits) and whose device path is composed by the plane inside the one directory this product owns",
		},
	},
	ExportFile: {
		Kind:                 ExportFile,
		RequiredCapabilities: []Capability{CapabilityDeviceFiles},
		Risk:                 RiskMedium,
		Retry:                RetrySafe,
		Mutating:             false,
		MutationReason:       readOnlyMutationReason,
		Postcondition:        "an artifact is written from the bytes the device answered with, and the artifact's recorded byte length is the size the device itself reported for that path before the read",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "the retired ban on a general device command is what previously refused this kind; it is dispatchable now as a CATALOGUED operation that reads ONE file by a bounded NAME the plane composes into the path this product owns, and it injects no input into the device",
		},
	},
	AdvancedCommand: {
		Kind:                 AdvancedCommand,
		RequiredCapabilities: []Capability{CapabilityDeviceCommand},
		Risk:                 RiskIrreversible,
		Retry:                RetryNeverBlind,
		Mutating:             true,
		MutationReason:       "dispatch runs an argument array this product did not compose, so the device's state after the action is whatever that array did",
		Postcondition:        "an append-only audit record names the EXACT argument array that was dispatched, the actor, the device and the outcome, and the device answered it",
		EvidenceRequired:     true,
		AllowedSurfaces:      []InvocationSurface{SurfaceManual},
		Lifted: &DeferralLift{
			Record:       retiredCommandBanRecord,
			Precondition: liftedCommandPrecondition,
			Reason:       "this is the ADVANCED form the retirement admitted: an operator-entered argument array, dispatched only after the operator confirms the exact argv, carried as discrete arguments rather than one joined command string, spawned without a shell, with its own append-only audit record and its own refusal of a host-side executable path",
		},
	},
}

func allSurfaces() []InvocationSurface {
	return []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror, SurfaceAISuggestion}
}

// Lookup returns the specification for a kind. An entry that does not declare
// its safety metadata in full is refused rather than returned: an incomplete
// entry is not dispatchable, and a caller must not be able to authorize one.
func Lookup(kind Kind) (Specification, bool) {
	spec, ok := catalog[kind]
	if !ok || spec.Validate() != nil {
		return Specification{}, false
	}
	return spec, true
}

func Catalog() []Specification {
	result := make([]Specification, 0, len(catalog))
	for _, spec := range catalog {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

func (c Capability) String() string { return string(c) }

func Supports(available []Capability, required []Capability) bool {
	set := make(map[Capability]struct{}, len(available))
	for _, capability := range available {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := set[capability]; !ok {
			return false
		}
	}
	return true
}

func ContainsSurface(allowed []InvocationSurface, requested InvocationSurface) bool {
	for _, surface := range allowed {
		if surface == requested {
			return true
		}
	}
	return false
}

func (i Intent) Validate() error {
	spec, ok := Lookup(i.Kind)
	if !ok {
		return fmt.Errorf("unsupported action kind %q", i.Kind)
	}
	for name, value := range map[string]string{"action ID": i.ID, "workspace": i.Workspace, "device ID": i.DeviceID, "lease ID": i.LeaseID, "holder ID": i.HolderID, "idempotency key": i.IdempotencyKey, "invocation surface": string(i.InvocationSurface)} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if i.FencingToken == 0 {
		return fmt.Errorf("fencing token is required")
	}
	if !ContainsSurface(spec.AllowedSurfaces, i.InvocationSurface) {
		return fmt.Errorf("action %q is not allowed from %q", i.Kind, i.InvocationSurface)
	}
	if spec.RequiresTarget && i.Target.Empty() && !validConfirmedCoordinateFallback(i.CoordinateFallback) {
		return fmt.Errorf("action %q requires a semantic target", i.Kind)
	}
	if spec.RequiresObservation && strings.TrimSpace(i.ObservationToken) == "" {
		return fmt.Errorf("action %q requires a fresh observation token", i.Kind)
	}
	if i.Timeout <= 0 || i.Timeout > 5*time.Minute {
		return fmt.Errorf("action timeout must be between one nanosecond and five minutes")
	}
	if len(i.IdempotencyKey) > 256 || len(i.ObservationToken) > 256 {
		return fmt.Errorf("action metadata is too long")
	}
	if len(i.TextValue) > 1<<20 || i.ValueLength < 0 || i.ValueLength > 1<<20 || i.KeyCode < 0 || i.KeyCode > 10000 {
		return fmt.Errorf("typed action payload is invalid or unbounded")
	}
	// The kind that names a launch target must carry one, and no other kind may:
	// a launch with no package would have to be interpreted by the transport, and
	// a payload the contract does not describe must not reach a device.
	if i.Kind == LaunchApp {
		if i.Launch == nil {
			return fmt.Errorf("action %q requires the target it launches", i.Kind)
		}
		if err := i.Launch.Validate(); err != nil {
			return err
		}
	} else if i.Launch != nil {
		return fmt.Errorf("action %q carries a launch target", i.Kind)
	}
	if i.Gesture != nil {
		if err := i.Gesture.Validate(); err != nil {
			return err
		}
	}
	if i.Kind == LiveGesture {
		if i.LiveStart == nil || i.Gesture != nil {
			return fmt.Errorf("a live gesture requires exactly its initial render-frame point")
		}
		if err := i.LiveStart.Validate(); err != nil {
			return err
		}
	} else if i.LiveStart != nil {
		return fmt.Errorf("action %q carries a live gesture start", i.Kind)
	}
	if i.CoordinateFallback != nil {
		if !validConfirmedCoordinateFallback(i.CoordinateFallback) || !i.ApprovalGranted {
			return fmt.Errorf("coordinate fallback requires bounded coordinates and explicit approval")
		}
	}
	for _, capability := range i.Capabilities {
		if !capability.Valid() {
			return fmt.Errorf("action capability %q is not allow-listed", capability)
		}
	}
	if !Supports(i.Capabilities, spec.RequiredCapabilities) {
		return fmt.Errorf("action %q requires unavailable capability", i.Kind)
	}
	return nil
}

func validConfirmedCoordinateFallback(fallback *CoordinateFallback) bool {
	if fallback == nil || !fallback.Confirmed || strings.TrimSpace(fallback.Start.Space) == "" || !validCoordinate(fallback.Start) || fallback.Duration < 0 || fallback.Duration > 5*time.Minute {
		return false
	}
	if fallback.End != nil && (strings.TrimSpace(fallback.End.Space) == "" || !validCoordinate(*fallback.End) || fallback.End.Space != fallback.Start.Space) {
		return false
	}
	if len(fallback.Path) > 0 {
		if len(fallback.Path) < 2 || len(fallback.Path) > 128 {
			return false
		}
		for index, point := range fallback.Path {
			if point.Space != fallback.Start.Space || !validCoordinate(point) || index == 0 && point != fallback.Start {
				return false
			}
		}
		last := fallback.Path[len(fallback.Path)-1]
		if fallback.End == nil || last != *fallback.End {
			return false
		}
	}
	return true
}

func validCoordinate(coordinate Coordinate) bool {
	return coordinate.X >= 0 && coordinate.X <= 10000 && coordinate.Y >= 0 && coordinate.Y <= 10000 && len(coordinate.Space) <= 128
}
