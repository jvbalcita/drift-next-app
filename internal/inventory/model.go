// Package inventory owns current device inventory and append-only snapshots.
// It stores sanitized metadata only; large evidence belongs to artifacts.
package inventory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type InventoryID string
type SnapshotID string

type Record struct {
	ID                  InventoryID
	Workspace           organizations.WorkspaceID
	DeviceID            devices.DeviceID
	SourceObservationID string
	InventoryJSON       string
	ObservedAt          time.Time
	RowVersion          uint64
}

type Snapshot struct {
	ID                  SnapshotID
	Workspace           organizations.WorkspaceID
	DeviceID            devices.DeviceID
	SourceObservationID string
	InventoryJSON       string
	ObservedAt          time.Time
}

func ValidateJSON(value string, maxBytes int) error {
	if len(value) == 0 || len(value) > maxBytes || !json.Valid([]byte(value)) {
		return fmt.Errorf("inventory JSON must be bounded valid JSON")
	}
	if redaction.RedactString(value) != value {
		return fmt.Errorf("inventory JSON contains sensitive material")
	}
	return nil
}

func (r Record) Validate() error {
	if strings.TrimSpace(string(r.ID)) == "" || strings.TrimSpace(string(r.Workspace)) == "" || strings.TrimSpace(string(r.DeviceID)) == "" || r.ObservedAt.IsZero() {
		return fmt.Errorf("inventory identity and timestamp are required")
	}
	return ValidateJSON(r.InventoryJSON, 65536)
}

func (s Snapshot) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || strings.TrimSpace(string(s.DeviceID)) == "" || s.ObservedAt.IsZero() {
		return fmt.Errorf("inventory snapshot identity and timestamp are required")
	}
	return ValidateJSON(s.InventoryJSON, 65536)
}
