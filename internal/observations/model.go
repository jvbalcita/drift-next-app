// Package observations owns immutable device observations and current
// projection metadata. Large evidence bytes live in the artifact store later.
package observations

import (
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type ObservationID string

type State string

const (
	Recorded   State = "recorded"
	Superseded State = "superseded"
	Retained   State = "retained"
)

type CaptureSource string

const (
	SourceFake    CaptureSource = "fake"
	SourceDevice  CaptureSource = "device"
	SourceMirror  CaptureSource = "mirror"
	SourceUnknown CaptureSource = "unknown"
)

type CaptureStatus string

const (
	CaptureComplete CaptureStatus = "complete"
	CapturePartial  CaptureStatus = "partial"
	CaptureFailed   CaptureStatus = "failed"
)

type ObservationSnapshot struct {
	ID                   ObservationID
	Workspace            organizations.WorkspaceID
	DeviceID             devices.DeviceID
	CapturedAt           time.Time
	CaptureCorrelationID string
	CoordinateSpace      string
	PackageName          string
	ActivityName         string
	Orientation          string
	DisplayWidth         int
	DisplayHeight        int
	UITreeHash           string
	ScreenshotHash       string
	Source               CaptureSource
	ProtocolVersion      string
	ModelVersion         string
	Truncated            bool
	FreshnessToken       string
	CaptureSkewMillis    int64
	CaptureStatus        CaptureStatus
	ErrorClass           domain.FailureClass
	State                State
}

func (s CaptureSource) Valid() bool {
	switch s {
	case SourceFake, SourceDevice, SourceMirror, SourceUnknown:
		return true
	default:
		return false
	}
}

func (s CaptureStatus) Valid() bool {
	switch s {
	case CaptureComplete, CapturePartial, CaptureFailed:
		return true
	default:
		return false
	}
}

func (s ObservationSnapshot) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(string(s.DeviceID)) == "" || s.CapturedAt.IsZero() || strings.TrimSpace(s.CaptureCorrelationID) == "" || strings.TrimSpace(s.CoordinateSpace) == "" || strings.TrimSpace(s.ProtocolVersion) == "" || strings.TrimSpace(s.ModelVersion) == "" || strings.TrimSpace(s.FreshnessToken) == "" {
		return fmt.Errorf("observation identity and provenance are required")
	}
	if len(s.CoordinateSpace) > 128 || len(s.FreshnessToken) > 256 || len(s.CaptureCorrelationID) > 256 || len(s.PackageName) > 256 || len(s.ActivityName) > 256 || len(s.Orientation) > 64 || len(s.UITreeHash) > 256 || len(s.ScreenshotHash) > 256 || s.DisplayWidth < 0 || s.DisplayHeight < 0 || s.CaptureSkewMillis < -86400000 || s.CaptureSkewMillis > 86400000 {
		return fmt.Errorf("observation metadata is invalid or unbounded")
	}
	if !s.Source.Valid() || !s.CaptureStatus.Valid() || !s.State.Valid() {
		return fmt.Errorf("observation source, status, or state is invalid")
	}
	if s.CaptureStatus == CaptureComplete && s.ErrorClass != "" {
		return fmt.Errorf("complete observations cannot carry an error")
	}
	if s.CaptureStatus != CaptureComplete && !s.ErrorClass.Valid() {
		return fmt.Errorf("partial or failed observations require a known error class")
	}
	return nil
}

func (s State) Valid() bool {
	return s == Recorded || s == Superseded || s == Retained
}

func CanTransition(from, to State) bool {
	switch from {
	case Recorded:
		return to == Superseded || to == Retained
	case Superseded:
		return to == Retained
	case Retained:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("observation", string(from), string(to))
	}
	return nil
}
