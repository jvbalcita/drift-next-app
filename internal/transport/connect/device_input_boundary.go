package transportconnect

import (
	"context"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// DeviceInputDispatcher runs one typed device input that the boundary has already
// resolved to a serial and identified as an attempt.
// *execution.InputDispatcher satisfies it.
type DeviceInputDispatcher interface {
	Run(ctx context.Context, request execution.InputRequest, actorType, actorID string) (action.Result, error)
}

// DeviceSerialResolver resolves a device to the serial of its single current
// transport endpoint. *execution.Registry satisfies it.
type DeviceSerialResolver interface {
	CurrentSerial(ctx context.Context, workspace, deviceID string) (string, error)
}

// DeviceAttemptIDSource assigns the identity of one attempt. An attempt with no
// identity cannot be recorded as evidence, so a dispatch without one is refused
// rather than performed and then found unrecordable.
type DeviceAttemptIDSource interface {
	NewID() (string, error)
}

// DeviceInputBoundary is the application boundary a device input RPC calls — the
// one seam between this transport and the dispatch contract.
//
// It does exactly the two things ADR-0012 says the transport must not do: it
// resolves the device the caller named to that device's current serial, and it
// assigns the attempt identity. Both were deliberately left out of the wire
// contract because a transport that resolved them would be inventing a target the
// caller never named; here, at the application boundary, they are the boundary's
// job.
//
// Everything else the caller supplied is passed through unchanged. A boundary that
// adjusted a field on the way would dispatch to a target the caller never named,
// and the evidence record would then disagree with the request that produced it.
//
// It refuses rather than guesses: with no resolvable serial there is no target, and
// with no attempt identity there is nothing to record, so neither reaches the
// dispatcher.
type DeviceInputBoundary struct {
	dispatch DeviceInputDispatcher
	serials  DeviceSerialResolver
	ids      DeviceAttemptIDSource
}

// NewDeviceInputBoundary requires all three parts. A boundary missing any of them
// cannot dispatch anything, so it refuses to be constructed and the composition
// root mounts no route rather than a route that answers every request with a
// refusal.
func NewDeviceInputBoundary(dispatch DeviceInputDispatcher, serials DeviceSerialResolver, ids DeviceAttemptIDSource) (*DeviceInputBoundary, error) {
	switch {
	case dispatch == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the device input boundary requires a dispatcher")
	case serials == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the device input boundary requires a device-to-serial resolver")
	case ids == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the device input boundary requires an attempt identity source")
	}
	return &DeviceInputBoundary{dispatch: dispatch, serials: serials, ids: ids}, nil
}

// Run resolves the device, assigns the attempt identity, and dispatches. The order
// is deliberate: a device that cannot be resolved is never dispatched to, and an
// attempt that cannot be identified is never performed.
func (b *DeviceInputBoundary) Run(ctx context.Context, input DeviceInputTarget, actorType, actorID string) (action.Result, error) {
	if b == nil {
		return action.Result{}, platformerrors.New(platformerrors.CodeUnavailable, "the device input boundary is not constructed")
	}
	serial, err := b.serials.CurrentSerial(ctx, input.Workspace, input.DeviceID)
	if err != nil {
		return action.Result{}, err
	}
	attemptID, err := b.ids.NewID()
	if err != nil {
		return action.Result{}, platformerrors.Wrap(platformerrors.CodeUnavailable, "the attempt identity could not be assigned, so nothing was dispatched", err)
	}
	return b.dispatch.Run(ctx, execution.InputRequest{
		IntentID:  attemptID,
		Workspace: input.Workspace,
		DeviceID:  input.DeviceID,
		// The two facts this boundary owns, and the only two it adds.
		Serial:   serial,
		HolderID: actorID,
		// Everything the caller supplied, unchanged.
		LeaseID:          input.LeaseID,
		FencingToken:     input.FencingToken,
		IdempotencyKey:   input.IdempotencyKey,
		ObservationToken: input.ObservationToken,
		// A local operator RPC is the manual surface. The wire contract does not
		// carry a surface, and the evidence record needs a known one.
		InvocationSurface: action.SurfaceManual,
		ApprovalGranted:   input.ApprovalGranted,
		Timeout:           input.Timeout,
		Target:            input.Target,
		Payload:           input.Payload,
	}, actorType, actorID)
}
