package product

import (
	"context"

	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// LabRuntimeEnumerator maps authorized lab discovery onto the Network Profile
// scanner seam. It never registers devices and never uses FakeScanner.
type LabRuntimeEnumerator struct {
	lab        *lab.Service
	operatorID string
}

func NewLabRuntimeEnumerator(service *lab.Service) *LabRuntimeEnumerator {
	return &LabRuntimeEnumerator{lab: service, operatorID: "control-plane"}
}

func (e *LabRuntimeEnumerator) Enumerate(ctx context.Context) ([]discovery.RuntimeDevice, error) {
	if e == nil || e.lab == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "lab runtime enumerator is required")
	}
	status, err := e.lab.Discover(ctx, e.operatorID)
	if err != nil {
		return nil, err
	}
	out := make([]discovery.RuntimeDevice, 0, len(status.Discovered))
	for _, device := range status.Discovered {
		out = append(out, discovery.RuntimeDevice{
			Serial:      device.Serial,
			TransportID: device.TransportID,
		})
	}
	return out, nil
}
