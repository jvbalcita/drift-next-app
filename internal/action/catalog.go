// Package action owns the typed, allow-listed action catalog. It does not
// expose raw device protocols, shell commands, or unrestricted coordinates.
package action

import (
	"fmt"
	"sort"
	"strings"
	"time"
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
	Back        Kind = "back"
	Home        Kind = "home"
	Enter       Kind = "enter"
	KeyEvent    Kind = "key_event"
	UIChange    Kind = "ui_change"
	StateChange Kind = "state_change"
)

type RiskClass string

const (
	RiskLow          RiskClass = "low"
	RiskMedium       RiskClass = "medium"
	RiskHigh         RiskClass = "high"
	RiskIrreversible RiskClass = "irreversible"
)

type RetryClass string

const (
	RetrySafe             RetryClass = "safe"
	RetryAfterObservation RetryClass = "after_observation"
	RetryNeverBlind       RetryClass = "never_blind"
)

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
)

type SemanticTarget struct {
	ResourceID         string
	AccessibilityLabel string
	StableText         string
	ContextFingerprint string
}

func (t SemanticTarget) Empty() bool {
	return strings.TrimSpace(t.ResourceID) == "" && strings.TrimSpace(t.AccessibilityLabel) == "" && strings.TrimSpace(t.StableText) == "" && strings.TrimSpace(t.ContextFingerprint) == ""
}

type Intent struct {
	ID                string
	Workspace         string
	DeviceID          string
	LeaseID           string
	HolderID          string
	FencingToken      uint64
	Kind              Kind
	Target            SemanticTarget
	IdempotencyKey    string
	ObservationToken  string
	InvocationSurface InvocationSurface
	Capabilities      []Capability
	ApprovalGranted   bool
	Timeout           time.Duration
	RequestHash       string
}

type Specification struct {
	Kind                 Kind
	RequiredCapabilities []Capability
	Risk                 RiskClass
	Retry                RetryClass
	Mutating             bool
	RequiresTarget       bool
	RequiresObservation  bool
	EvidenceRequired     bool
	AllowedSurfaces      []InvocationSurface
}

var catalog = map[Kind]Specification{
	Observe:     {Kind: Observe, RequiredCapabilities: []Capability{CapabilityObserve}, Risk: RiskLow, Retry: RetrySafe, EvidenceRequired: true, AllowedSurfaces: allSurfaces()},
	HealthCheck: {Kind: HealthCheck, RequiredCapabilities: []Capability{CapabilityHealth}, Risk: RiskLow, Retry: RetrySafe, EvidenceRequired: true, AllowedSurfaces: allSurfaces()},
	Capture:     {Kind: Capture, RequiredCapabilities: []Capability{CapabilityCapture}, Risk: RiskLow, Retry: RetrySafe, EvidenceRequired: true, AllowedSurfaces: allSurfaces()},
	Tap:         {Kind: Tap, RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	DoubleTap:   {Kind: DoubleTap, RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	LongPress:   {Kind: LongPress, RequiredCapabilities: []Capability{CapabilityTap}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	TextInput:   {Kind: TextInput, RequiredCapabilities: []Capability{CapabilityTextInput}, Risk: RiskHigh, Retry: RetryNeverBlind, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	TextDelete:  {Kind: TextDelete, RequiredCapabilities: []Capability{CapabilityTextInput}, Risk: RiskHigh, Retry: RetryNeverBlind, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	Clear:       {Kind: Clear, RequiredCapabilities: []Capability{CapabilityTextInput}, Risk: RiskHigh, Retry: RetryNeverBlind, Mutating: true, RequiresTarget: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	Swipe:       {Kind: Swipe, RequiredCapabilities: []Capability{CapabilityGesture}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	Scroll:      {Kind: Scroll, RequiredCapabilities: []Capability{CapabilityGesture}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	Drag:        {Kind: Drag, RequiredCapabilities: []Capability{CapabilityGesture}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	Back:        {Kind: Back, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	Home:        {Kind: Home, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror}},
	Enter:       {Kind: Enter, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskMedium, Retry: RetryAfterObservation, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	KeyEvent:    {Kind: KeyEvent, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskHigh, Retry: RetryNeverBlind, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	UIChange:    {Kind: UIChange, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskHigh, Retry: RetryNeverBlind, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay}},
	StateChange: {Kind: StateChange, RequiredCapabilities: []Capability{CapabilitySystemInput}, Risk: RiskIrreversible, Retry: RetryNeverBlind, Mutating: true, RequiresObservation: true, EvidenceRequired: true, AllowedSurfaces: []InvocationSurface{SurfaceManual}},
}

func allSurfaces() []InvocationSurface {
	return []InvocationSurface{SurfaceManual, SurfaceRecorder, SurfaceReplay, SurfaceMirror, SurfaceAISuggestion}
}

func Lookup(kind Kind) (Specification, bool) {
	spec, ok := catalog[kind]
	return spec, ok
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
	if spec.RequiresTarget && i.Target.Empty() {
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
	if !Supports(i.Capabilities, spec.RequiredCapabilities) {
		return fmt.Errorf("action %q requires unavailable capability", i.Kind)
	}
	return nil
}
