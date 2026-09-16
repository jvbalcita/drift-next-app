package execution

import (
	"context"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/adapter"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// ObservationSource reads the current observation of the one device it was built
// for. It is the read side of the input path: it never injects input, and the
// input boundary itself refuses to observe for the same reason.
type ObservationSource interface {
	Observe(ctx context.Context) (adapter.Observation, error)
}

// ObservationSourceFactory builds the observation source for one device's
// explicitly named serial. The serial is named rather than inferred, so a source
// can only ever read a device the boundary resolved.
type ObservationSourceFactory func(serial string) (ObservationSource, error)

// ObservationPostconditionObserver answers the postcondition port by reading the
// device's current observation and mapping it onto the facts a postcondition is
// evaluated against.
//
// It refuses rather than invents. Where an observation cannot supply a fact the
// payload's postcondition names, the observation is reported partial, which the
// boundary records as indeterminate: an action that may well have succeeded must
// not be failed for a mismatch nobody observed, and a count nobody read must not
// be presented as though it had been.
type ObservationPostconditionObserver struct {
	serials EndpointSerialResolver
	sources ObservationSourceFactory
}

// NewObservationPostconditionObserver requires both halves: a resolver, because a
// request names a device and a device must become a serial before anything can be
// read, and a source factory, because there is nothing to observe without one. An
// observer missing either cannot observe anything, so it refuses to be built
// rather than failing on every dispatch.
func NewObservationPostconditionObserver(serials EndpointSerialResolver, sources ObservationSourceFactory) (*ObservationPostconditionObserver, error) {
	if serials == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the postcondition observer requires a device-to-serial resolver")
	}
	if sources == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "the postcondition observer requires a source of a device's observation")
	}
	return &ObservationPostconditionObserver{serials: serials, sources: sources}, nil
}

// ObservePostcondition resolves the device the request names, reads its current
// observation, and maps it. The order is deliberate: a device that cannot be
// resolved is never read, so an unnameable device cannot be observed by accident
// and a refused resolution reaches no device.
func (o *ObservationPostconditionObserver) ObservePostcondition(ctx context.Context, intent action.Intent, payload InputPayload) (PostconditionObservation, error) {
	if o == nil {
		return PostconditionObservation{}, platformerrors.New(platformerrors.CodeUnavailable, "the postcondition observer is not constructed")
	}
	serial, err := o.serials.CurrentSerial(ctx, intent.Workspace, intent.DeviceID)
	if err != nil {
		return PostconditionObservation{}, err
	}
	source, err := o.sources(serial)
	if err != nil {
		return PostconditionObservation{}, err
	}
	if source == nil {
		return PostconditionObservation{}, platformerrors.New(platformerrors.CodeUnavailable, "the device's observation source could not be built")
	}
	observation, err := source.Observe(ctx)
	if err != nil {
		return PostconditionObservation{}, err
	}
	return mapObservationFor(payload, observation), nil
}

// mapObservationFor carries the facts an observation holds, and marks the
// observation partial wherever the payload's postcondition names a fact this
// observation cannot supply.
//
// Two such facts exist today, and both are recorded rather than faked:
//
//   - The addressed field's length. A typed-text reference names its field with an
//     opaque handle, and the resolver that turns a handle into a field is ARC-73's
//     open work, so the field cannot be read at all. The length is left at zero and
//     the observation is partial; the boundary's evaluator checks partial before it
//     compares anything, so that zero never reaches a comparison.
//   - The foreground package, when the observation reports none. An empty package
//     means "this observation cannot say which package is in the foreground", not
//     "the wrong one is": without partial, a launch that succeeded would be failed
//     for a mismatch nobody observed. The lab observation adapter does not populate
//     the package today, so every launch postcondition in production is
//     indeterminate until a source for that fact exists. That residual is recorded
//     on ARC-89 rather than worked around here.
func mapObservationFor(payload InputPayload, observation adapter.Observation) PostconditionObservation {
	mapped := PostconditionObservation{
		Token:             observation.Token,
		ForegroundPackage: observation.PackageName,
		Partial:           observation.Partial,
		FailureClass:      observation.FailureClass,
	}
	if payload.Text != nil {
		mapped.Partial = true
		return mapped
	}
	if payload.Launch != nil && observation.PackageName == "" {
		mapped.Partial = true
	}
	return mapped
}
