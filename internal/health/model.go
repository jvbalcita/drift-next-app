package health

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type SampleID string

type Status string

const (
	Healthy   Status = "healthy"
	Degraded  Status = "degraded"
	Unhealthy Status = "unhealthy"
	Unknown   Status = "unknown"
)

type Sample struct {
	ID            SampleID
	Workspace     organizations.WorkspaceID
	DeviceID      devices.DeviceID
	ObservationID string
	Status        Status
	Battery       *int
	SampledAt     time.Time
	DetailsJSON   string
}

type Current struct {
	ID           string
	Workspace    organizations.WorkspaceID
	DeviceID     devices.DeviceID
	SourceSample SampleID
	Status       Status
	SampledAt    time.Time
	UpdatedAt    time.Time
	RowVersion   uint64
}

func (s Status) Valid() bool {
	switch s {
	case Healthy, Degraded, Unhealthy, Unknown:
		return true
	default:
		return false
	}
}

func (s Sample) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(string(s.DeviceID)) == "" || !s.Status.Valid() || s.SampledAt.IsZero() {
		return fmt.Errorf("health sample identity, status, and timestamp are required")
	}
	if s.Battery != nil && (*s.Battery < 0 || *s.Battery > 100) {
		return fmt.Errorf("battery percentage must be between zero and one hundred")
	}
	if len(s.DetailsJSON) == 0 || len(s.DetailsJSON) > 32768 || !json.Valid([]byte(s.DetailsJSON)) || redaction.RedactString(s.DetailsJSON) != s.DetailsJSON {
		return fmt.Errorf("health details must be bounded sanitized JSON")
	}
	return nil
}
