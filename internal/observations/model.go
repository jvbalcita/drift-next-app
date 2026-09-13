// Package observations owns immutable device observations and current
// projection metadata. Large evidence bytes live in the artifact store later.
package observations

import (
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
	CaptureStatus        CaptureStatus
	ErrorClass           domain.FailureClass
	State                State
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
