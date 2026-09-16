package execution

import (
	"context"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// LabStatusReader reports the lab boundary's current status, which carries the
// attached candidate set the readiness probe reads device state from.
//
// It is a read and nothing else: the status snapshot issues no command, records
// no evidence and needs no operator identity, so the probe can consult it before
// authorization — which is where the probe runs. This package may name lab's
// status type because execution already imports lab; the reverse import is what
// the narrow seam avoids.
type LabStatusReader interface {
	Status(ctx context.Context) lab.Status
}

// NewTransportObserverFromAttached builds the readiness probe's device-state
// observer over the lab boundary's attached set.
//
// It exists so the composition root does not need the concrete *adb.Adapter to
// answer "may this device be attempted at all": the LAB boundary owns that
// adapter, does not expose it, and should not have to. The probe reads what the
// boundary already observes.
//
// A serial the boundary does not list is reported OFFLINE rather than assumed
// reachable, matching the ADB-backed observer. Enumeration is not selection, and
// a device nobody can see is not a device this boundary may dispatch input to.
func NewTransportObserverFromAttached(reader LabStatusReader) (DeviceTransportObserver, error) {
	if reader == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a transport observer requires the lab boundary's attached device set")
	}
	return DeviceTransportObserverFunc(func(ctx context.Context, serial string) (adb.DeviceAuthState, error) {
		for _, device := range reader.Status(ctx).Discovered {
			if device.Serial == serial {
				return device.State, nil
			}
		}
		return adb.StateOffline, nil
	}), nil
}
