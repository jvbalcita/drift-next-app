package execution

import (
	"context"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// AllowlistedRunner is the device's own allow-listed runner: one operation over
// an argument array this repository's builders produced, which the adapter
// underneath re-derives against its allow-list before anything reaches a device.
//
// It is declared here, narrow, so the lab boundary can hand over the transport it
// already holds without either package importing the other — this package imports
// lab, so lab cannot import this one to name the port. The lab service's
// DeviceTransport satisfies this structurally.
type AllowlistedRunner interface {
	RunAllowlisted(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// NewInputTransportFromAllowlisted adapts the device's own allow-listed runner to
// the input transport port.
//
// It adds no behaviour: the same call reaches the same adapter, so a read-only
// precondition read — the render-size cross-check — cannot be counted as an
// attempted input by a wrapper in this position (ARC-76). It builds and rewrites
// nothing either: the array a primitive built is the array the device receives,
// and the adapter underneath still refuses any array that is not on its
// allow-list.
//
// A nil runner is refused here rather than bound. A transport over nothing would
// fail on every dispatch, so this fails closed at construction and the
// composition root mounts no input path at all: no transport, no route.
func NewInputTransportFromAllowlisted(runner AllowlistedRunner) (InputTransport, error) {
	if runner == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an input transport requires the device's own allow-listed runner")
	}
	return allowlistedTransport{runner: runner}, nil
}

// allowlistedTransport is the name adapter between the lab boundary's runner and
// this package's input port. It holds no state and makes no call of its own, so
// there is nothing here that could observe, count, retry or reshape a command
// between the port and the device.
type allowlistedTransport struct{ runner AllowlistedRunner }

func (t allowlistedTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	return t.runner.RunAllowlisted(ctx, serial, args)
}
