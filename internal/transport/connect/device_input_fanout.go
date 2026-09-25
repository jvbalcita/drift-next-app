package transportconnect

import (
	"context"
	"log"
	"math"
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"

	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// The follower fan-out at the application boundary.
//
// The operator's gesture reaches the SOURCE through the dispatch contract above,
// unchanged. What is added here is the second half of the owner's reading of
// source/follower mirroring: the followers also RECEIVE what the operator does on
// the source, each as its own action through the same kernel.
//
// The ordering is stated once, in the type below, because it is a decision rather
// than an implementation detail: the followers proceed INDEPENDENTLY. The source's
// own dispatch is answered from the source's own result, so a gesture on the source
// cannot hang on the slowest follower - and the followers' own outcomes are
// recorded on the plane as their runs complete rather than returned by this call.

// DeviceFollowerFanout carries one operator gesture to the followers the operator
// selected. *execution.FollowerFanout satisfies it.
//
// It returns once every follower's run has been ACCEPTED, never once they have
// finished: an implementation that waited for the followers would make the
// operator's gesture wait for the slowest device, which is the ordering this port
// exists to refuse.
type DeviceFollowerFanout interface {
	FanOut(ctx context.Context, request execution.FollowerFanoutRequest) (execution.FollowerFanoutReport, error)
}

// DeviceInputBoundaryOption configures the boundary at construction time.
type DeviceInputBoundaryOption func(*DeviceInputBoundary) error

// WithFollowerFanout binds the fan-out a gesture is carried to the operator's
// followers through.
//
// It is an OPTION rather than a required part, because a deployment that carries a
// gesture to the source alone is a complete deployment: this plane refuses a
// request that names followers when no fan-out is bound, so an operator is told the
// capability is absent rather than shown followers that never received anything.
func WithFollowerFanout(fanout DeviceFollowerFanout) DeviceInputBoundaryOption {
	return func(boundary *DeviceInputBoundary) error {
		if fanout == nil {
			return platformerrors.New(platformerrors.CodeInvalidInput, "the follower fan-out port is required")
		}
		boundary.fanout = fanout
		return nil
	}
}

// RunFollowers carries one gesture the source has already performed to each
// follower the operator selected.
//
// It is called only after the SOURCE's own input was dispatched: the followers
// receive the gesture the source actually performed, and a gesture the source's own
// device refused is not mirrored onto N followers as though it had happened.
func (b *DeviceInputBoundary) RunFollowers(ctx context.Context, input DeviceInputTarget, followers []string, actorType, actorID string) (execution.FollowerFanoutReport, error) {
	report := execution.FollowerFanoutReport{}
	if b == nil || b.fanout == nil {
		// The deployment has no fan-out, so there is nothing to carry the gesture
		// with. This is stated as the surface's own absence and never as a
		// follower that received nothing quietly.
		return report, platformerrors.New(platformerrors.CodeUnavailable, "this deployment does not carry a gesture to followers")
	}
	return b.fanout.FanOut(ctx, execution.FollowerFanoutRequest{
		Workspace:         input.Workspace,
		SourceDeviceID:    input.DeviceID,
		FollowerDeviceIDs: followers,
		HolderID:          actorID,
		// The idempotency key the caller supplied identifies the GESTURE; each
		// follower's own key is derived from it, so a retried fan-out does not
		// double-apply on a follower that already took the action.
		RequestID:        input.IdempotencyKey,
		ObservationToken: input.ObservationToken,
		Target:           input.Target,
		Payload:          input.Payload,
	})
}

// followerFanoutMessage renders one fan-out report in the contract's terms.
//
// It is a total rendering: every row the fan-out produced reaches a client, with
// the plane's own disposition, its own stable reason and its own sentence. A row
// whose reason the contract has no enum value for still reaches a client - the
// reason travels as its own stable string - so no follower the operator selected
// can be quietly absent from the report.
//
// A refused row carries the dispatch boundary's typed refusal, built from the same
// vocabulary table the refusal mapping uses, so a client reads one reason and never
// a reason re-derived from an error's text.
func followerFanoutMessage(report execution.FollowerFanoutReport) *driftv1.FollowerInputFanout {
	message := &driftv1.FollowerInputFanout{RunId: report.RunID, TargetCount: int32(report.TargetCount), AcceptanceDurationMs: boundedDurationMillis(report.AcceptanceDuration)}
	for _, row := range report.Followers {
		out := &driftv1.FollowerInputOutcome{
			DeviceId:            row.DeviceID,
			Disposition:         protoFollowerDisposition(row.Disposition),
			Reason:              string(row.Reason),
			Detail:              row.Detail,
			Outcome:             string(row.Outcome),
			AttemptId:           row.AttemptID,
			IdempotencyKey:      row.IdempotencyKey,
			FrameWidth:          row.Frame.Width,
			FrameHeight:         row.Frame.Height,
			AcceptanceLatencyMs: boundedDurationMillis(row.AcceptanceLatency),
		}
		if row.RefusalReason != "" {
			if definition, ok := refusalDefinitionFor(row.RefusalReason); ok {
				out.Refusal = &driftv1.DeviceInputRefusal{
					Reason:       deviceInputRefusalReason(row.RefusalReason),
					Code:         string(definition.Code),
					FailureClass: string(definition.FailureClass),
					Message:      definition.Message,
				}
			} else {
				// The boundary refuses to answer UNSPECIFIED for a reason it
				// publishes (see the mapping test), so an unmapped reason is
				// recorded and the row still reaches a client with its own reason
				// string rather than a refusal that says "refused, somehow".
				log.Printf("event=follower_fanout_refusal_unmapped reason=%s", row.RefusalReason)
			}
		}
		message.Followers = append(message.Followers, out)
	}
	return message
}

func boundedDurationMillis(value time.Duration) uint32 {
	if value <= 0 {
		return 0
	}
	millis := value.Milliseconds()
	if millis > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(millis)
}

// protoFollowerDisposition maps one disposition onto the contract's enum. A value
// the contract does not carry resolves to UNSPECIFIED, which a client can only read
// as "this plane said something this build does not know" rather than as a
// delivered row.
func protoFollowerDisposition(disposition execution.FollowerInputDisposition) driftv1.FollowerInputDisposition {
	switch disposition {
	case execution.FollowerInputAccepted:
		return driftv1.FollowerInputDisposition_FOLLOWER_INPUT_DISPOSITION_ACCEPTED
	case execution.FollowerInputRefused:
		return driftv1.FollowerInputDisposition_FOLLOWER_INPUT_DISPOSITION_REFUSED
	case execution.FollowerInputExcluded:
		return driftv1.FollowerInputDisposition_FOLLOWER_INPUT_DISPOSITION_EXCLUDED
	case execution.FollowerInputIndeterminate:
		return driftv1.FollowerInputDisposition_FOLLOWER_INPUT_DISPOSITION_INDETERMINATE
	default:
		return driftv1.FollowerInputDisposition_FOLLOWER_INPUT_DISPOSITION_UNSPECIFIED
	}
}

// refusalDefinitionFor reads the boundary's own refusal vocabulary, so the code and
// failure class a refused follower's row carries are the ones that vocabulary binds
// to that reason rather than a second copy of the table.
func refusalDefinitionFor(reason execution.RefusalReason) (execution.RefusalDefinition, bool) {
	for _, definition := range execution.RefusalDefinitions() {
		if definition.Reason == reason {
			return definition, true
		}
	}
	return execution.RefusalDefinition{}, false
}
