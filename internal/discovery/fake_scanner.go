package discovery

import (
	"context"
	"fmt"

	"drift.local/drift-next/internal/networkprofiles"
)

// Scanner is the narrow discovery adapter seam. Implementations return
// observations only; persisting a device belongs to the control plane.
type Scanner interface {
	Scan(context.Context, networkprofiles.NetworkProfile) ([]ObservedDevice, error)
}

// FakeScanner is deterministic and has no network, Android, or filesystem
// side effects. It returns a private copy of its configured observations.
type FakeScanner struct {
	Devices []ObservedDevice
	Err     error
}

func NewFakeScanner(devices []ObservedDevice) *FakeScanner {
	return &FakeScanner{Devices: append([]ObservedDevice(nil), devices...)}
}

func (s *FakeScanner) Scan(ctx context.Context, profile networkprofiles.NetworkProfile) ([]ObservedDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("fake scanner is required")
	}
	if s.Err != nil {
		return nil, s.Err
	}
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("scan profile is invalid: %w", err)
	}
	result := append([]ObservedDevice(nil), s.Devices...)
	return result, nil
}
