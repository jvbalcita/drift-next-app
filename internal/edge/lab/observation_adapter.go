package lab

import (
	"context"
	"errors"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// ObservationAdapter is a typed edge adapter for read-only observation of one
// confirmed serial. It never injects input, never widens the ADB allow-list,
// and never executes a mutating catalog kind.
type ObservationAdapter struct {
	capturer   ObservationCapturer
	serial     string
	operatorID string
}

// ObservationCapturer is the lab service slice this adapter needs.
type ObservationCapturer interface {
	CaptureObservation(context.Context, CaptureRequest) (ObservationBundle, error)
	Status(context.Context) Status
}

func NewObservationAdapter(capturer ObservationCapturer, serial, operatorID string) (*ObservationAdapter, error) {
	if capturer == nil || operatorID == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "observation adapter requires a capturer and operator")
	}
	if err := adb.ValidateSerial(serial); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "observation adapter serial is invalid", err)
	}
	return &ObservationAdapter{capturer: capturer, serial: serial, operatorID: operatorID}, nil
}

func (a *ObservationAdapter) Capabilities() []action.Capability {
	return []action.Capability{action.CapabilityObserve, action.CapabilityHealth, action.CapabilityCapture}
}

func (a *ObservationAdapter) Observe(ctx context.Context) (adapter.Observation, error) {
	bundle, err := a.capture(ctx, action.Intent{Kind: action.Observe, IdempotencyKey: "observe:" + a.serial, Timeout: DefaultCaptureTimeout})
	if err != nil {
		return adapter.Observation{}, err
	}
	return adapter.Observation{
		Token:          bundle.HierarchyFreshnessToken,
		ScreenshotHash: bundle.ScreenshotHash,
		Partial:        !bundle.HierarchyComplete || bundle.PreviewTruncated || bundle.FailureClass != "",
		FailureClass:   bundle.FailureClass,
	}, nil
}

func (a *ObservationAdapter) Execute(ctx context.Context, intent action.Intent) (adapter.Execution, error) {
	switch intent.Kind {
	case action.Observe, action.Capture, action.HealthCheck:
	default:
		return adapter.Execution{}, &adapter.ExecutionError{
			Cause:        errors.New("observation adapter does not execute mutating actions"),
			FailureClass: domain.FailureCapabilityMismatch,
		}
	}
	status := a.capturer.Status(ctx)
	if status.ConfirmedSerial != a.serial {
		return adapter.Execution{}, &adapter.ExecutionError{
			Cause:        platformerrors.New(platformerrors.CodePreconditionFailed, "confirmed transport does not match the registered device"),
			FailureClass: domain.FailureStaleObservation,
		}
	}
	bundle, err := a.capture(ctx, intent)
	if err != nil {
		class := failureForBundle(bundle, err)
		return adapter.Execution{
			Outcome:          outcomeForBundle(bundle),
			Postcondition:    action.PostconditionUnknown,
			FailureClass:     class,
			Dispatched:       bundle.Indeterminate,
			ObservationToken: bundle.HierarchyFreshnessToken,
		}, &adapter.ExecutionError{Cause: err, Dispatched: bundle.Indeterminate, FailureClass: class}
	}
	postcondition := action.PostconditionUnknown
	if bundle.PostconditionVerified {
		postcondition = action.PostconditionPassed
	}
	return adapter.Execution{
		Outcome:          outcomeForBundle(bundle),
		Postcondition:    postcondition,
		FailureClass:     bundle.FailureClass,
		Dispatched:       true,
		ObservationToken: bundle.HierarchyFreshnessToken,
	}, nil
}

func (a *ObservationAdapter) Cleanup(context.Context, action.Intent) error { return nil }

func (a *ObservationAdapter) capture(ctx context.Context, intent action.Intent) (ObservationBundle, error) {
	key := intent.IdempotencyKey
	if key == "" {
		key = intent.ID
	}
	return a.capturer.CaptureObservation(ctx, CaptureRequest{
		Serial:         a.serial,
		IdempotencyKey: key,
		Timeout:        intent.Timeout,
		OperatorID:     a.operatorID,
		CorrelationID:  intent.ID,
	})
}

func outcomeForBundle(bundle ObservationBundle) action.Outcome {
	if bundle.Indeterminate {
		return action.OutcomeIndeterminate
	}
	if bundle.FailureClass != "" {
		return action.OutcomeFailed
	}
	return action.OutcomeVerified
}

func failureForBundle(bundle ObservationBundle, err error) domain.FailureClass {
	if bundle.FailureClass != "" {
		return bundle.FailureClass
	}
	if err == nil {
		return ""
	}
	if platformerrors.CodeOf(err) == platformerrors.CodeTimeout {
		return domain.FailureTimeout
	}
	return domain.FailureTransport
}
