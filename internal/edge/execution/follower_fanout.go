// Follower fan-out: one operator gesture on the SOURCE frame, dispatched to each
// selected follower as its own action through the kernel.
//
// The owner's reading of source/follower mirroring is that the followers must
// ALSO RECEIVE what the operator does on the source - watching is the preview
// fan-out that already exists, and receiving is this file. So a gesture the
// operator performs on the source is dispatched to the source exactly as it was
// before, and the same typed input is dispatched to each selected follower as its
// own action: its own lease, its own policy and capability decision, its own
// fresh observation, its own postcondition, its own evidence record and its own
// result.
//
// Three things this file deliberately does NOT do.
//
//   - It does not decide authority. Every follower dispatch travels the same
//     kernel a source dispatch does (authorization, leases, fencing, policy,
//     capability, emergency stop, evidence); this file resolves a candidate set
//     and carries one typed payload to each candidate. Nothing here can reach a
//     device the kernel refused.
//   - It does not re-derive the ONLINE reading. The candidate set is
//     DeviceFleetReader.Fleet - the ONE reading the console paints and every
//     other action resolves its candidates from - so a device the operator sees
//     as online and a device this run treats as online cannot disagree.
//   - It does not rescale a coordinate. Every follower receives the SOURCE's
//     declared render space unchanged, and each follower's own render-space gate
//     accepts it only if that follower presents at that size. A follower whose
//     frames cannot be reconciled is REFUSED and NAMED, never given a coordinate
//     that was scaled into a frame the operator never pointed at.
//
// Isolation is the property the whole shape exists for: one follower's outcome
// never changes another's. Each follower has its own run, its own lease, its own
// attempt, its own refusal and its own row in the report, and a follower that
// refuses, fails, is indeterminate or was never a candidate is NAMED with its own
// reason. There is no aggregate sentence standing for N devices and no count that
// reads as deliveries when fewer were delivered.
package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/leases"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// --- the outcome vocabulary -------------------------------------------------

// FollowerInputDisposition is what happened to ONE follower's copy of the
// gesture, in the plane's own closed vocabulary.
//
// It is deliberately four values rather than a boolean. `accepted` says the
// follower's action was dispatched through the kernel and the result beside it is
// the device's own; `refused` says the plane decided not to dispatch it and the
// reason beside it is why; `excluded` says the follower was never a candidate at
// all, so nothing about it was contacted OR failed; `indeterminate` says the action
// was dispatched and its outcome could not be established. An operator reading one
// follower's row can act on each of these and cannot act on a number.
type FollowerInputDisposition string

const (
	// FollowerInputAccepted: the follower's own action was dispatched through the
	// kernel and its result is recorded beside this row.
	FollowerInputAccepted FollowerInputDisposition = "accepted"
	// FollowerInputRefused: the plane did not dispatch this follower's action, and
	// named the reason. A refusal is the follower's OWN, never a shared one.
	FollowerInputRefused FollowerInputDisposition = "refused"
	// FollowerInputExcluded: the follower was not a candidate for this run, so the
	// run never contacted it. It is neither a success nor a failure.
	FollowerInputExcluded FollowerInputDisposition = "excluded"
	// FollowerInputIndeterminate: the action was dispatched and its outcome could
	// not be established - a timeout, an unreachable device mid-flight. It is its
	// own reading and is never reported as a success.
	FollowerInputIndeterminate FollowerInputDisposition = "indeterminate"
)

// Valid reports whether this disposition is one the plane states.
func (d FollowerInputDisposition) Valid() bool {
	switch d {
	case FollowerInputAccepted, FollowerInputRefused, FollowerInputExcluded, FollowerInputIndeterminate:
		return true
	default:
		return false
	}
}

// FollowerFanoutReason is the stable reason ONE follower's row carries.
//
// Every value names one thing an operator can act on, and no two of them share a
// remediation: an offline follower is fixed by attaching it, a follower under
// another operator's control is fixed by that operator releasing it, a follower
// whose render size does not match is fixed by re-authoring the gesture at that
// device's own frame. A single "fan-out failed" would name none of them.
type FollowerFanoutReason string

const (
	// FollowerReasonDelivered: the follower's action was dispatched and the
	// kernel's own outcome is recorded beside this row.
	FollowerReasonDelivered FollowerFanoutReason = "delivered"
	// FollowerReasonUnnamed: a follower entry that named no device.
	FollowerReasonUnnamed FollowerFanoutReason = "follower_unnamed"
	// FollowerReasonIsSource: the source was named as one of its own followers.
	FollowerReasonIsSource FollowerFanoutReason = "follower_is_source"
	// FollowerReasonNotRegistered: this workspace has never recorded the device,
	// so there is no transport to reach it at and nothing was sent.
	FollowerReasonNotRegistered FollowerFanoutReason = "follower_not_registered"
	// FollowerReasonNotOnline: the registry holds the device and the plane does not
	// read it as ONLINE, so nothing was sent and nothing failed.
	FollowerReasonNotOnline FollowerFanoutReason = "follower_not_online"
	// FollowerReasonNoTransport: the plane reads the device as online and cannot
	// name the transport to reach it at. Refused rather than silently skipped.
	FollowerReasonNoTransport FollowerFanoutReason = "follower_no_transport"
	// FollowerReasonKindNotFannable: the gesture is one this plane will not carry
	// to followers - typed content, whose value is released exactly once, and an
	// app launch, which names one device's package. Named, never silently dropped.
	FollowerReasonKindNotFannable FollowerFanoutReason = "follower_kind_not_fannable"
	// FollowerReasonPayloadIncomplete: the gesture's typed payload is absent,
	// doubled or incomplete, so there is nothing to fan out.
	FollowerReasonPayloadIncomplete FollowerFanoutReason = "follower_payload_incomplete"
	// FollowerReasonControlUnavailable: no control session could be opened on the
	// operator's behalf, so no follower was contacted. Stated as the run's own
	// condition rather than as each device's failure.
	FollowerReasonControlUnavailable FollowerFanoutReason = "follower_control_unavailable"
	// FollowerReasonLeaseRefused: the follower's own lease could not be held - it
	// is under another controller, its control session ended, or its fence is
	// stale. The kernel's own refusal reason travels beside this row.
	FollowerReasonLeaseRefused FollowerFanoutReason = "follower_lease_refused"
	// FollowerReasonRefusedByKernel: the follower's own dispatch was refused by the
	// kernel or the readiness probe. The refusal reason and failure class beside
	// this row are that boundary's own, never a generic internal error.
	FollowerReasonRefusedByKernel FollowerFanoutReason = "follower_refused"
	// FollowerReasonFrameNotReconciled: the follower does not present at the frame
	// the finger pointed in, so the coordinate cannot be carried to it. The row
	// names both sizes; the coordinate is never rescaled onto the follower.
	FollowerReasonFrameNotReconciled FollowerFanoutReason = "follower_frame_not_reconciled"
	// FollowerReasonOverloaded: the fan-out's own work queue was full, so the
	// follower's action was refused rather than queued without bound.
	FollowerReasonOverloaded FollowerFanoutReason = "follower_fanout_overloaded"
	// FollowerReasonFailed: the follower's action was dispatched and failed. The
	// kernel's outcome and failure class travel beside this row.
	FollowerReasonFailed FollowerFanoutReason = "follower_failed"
	// FollowerReasonIndeterminate: the follower's action was dispatched and its
	// outcome could not be established. Never reported as a success.
	FollowerReasonIndeterminate FollowerFanoutReason = "follower_indeterminate"
)

// Valid reports whether this reason is one the plane states.
func (r FollowerFanoutReason) Valid() bool {
	switch r {
	case FollowerReasonDelivered, FollowerReasonUnnamed, FollowerReasonIsSource,
		FollowerReasonNotRegistered, FollowerReasonNotOnline, FollowerReasonNoTransport,
		FollowerReasonKindNotFannable, FollowerReasonPayloadIncomplete,
		FollowerReasonControlUnavailable, FollowerReasonLeaseRefused,
		FollowerReasonRefusedByKernel, FollowerReasonFrameNotReconciled,
		FollowerReasonOverloaded, FollowerReasonFailed, FollowerReasonIndeterminate:
		return true
	default:
		return false
	}
}

// FollowerInputOutcome is ONE follower's row: what happened to its copy of the
// operator's gesture, in the follower's own terms.
//
// Detail is the plane's own fixed sentence for the outcome or the boundary's own
// refusal sentence - never a device, transport, database or stack-trace
// diagnostic, and never operator content.
type FollowerInputOutcome struct {
	DeviceID    string
	Disposition FollowerInputDisposition
	Reason      FollowerFanoutReason
	Detail      string
	// RefusalReason and FailureClass are the dispatch boundary's own vocabulary,
	// carried for the rows the kernel refused so a client classifies a refusal
	// without maintaining a lookup table of its own.
	RefusalReason RefusalReason
	FailureClass  domain.FailureClass
	// Outcome is the kernel's own reading for a row that was dispatched.
	Outcome action.Outcome
	// AttemptID identifies this follower's own action attempt, which is what an
	// append-only evidence record references. Empty for a row that reached no
	// device.
	AttemptID string
	// IdempotencyKey is the key this follower's action carries. It is this
	// follower's OWN, so a retried fan-out does not double-apply on a follower
	// that already took the action.
	IdempotencyKey string
	// Frame is the render space this follower was given. It is the SOURCE's
	// declared frame, unchanged: this plane refuses a follower whose frames cannot
	// be reconciled and never converts a coordinate into the follower's frame.
	Frame RenderSpace
	// AcceptanceLatency is the bounded queue admission time reported to the
	// source response. QueueWait and CompletionLatency belong to the final event.
	AcceptanceLatency time.Duration
	QueueWait         time.Duration
	CompletionLatency time.Duration
}

// FollowerFanoutReport is the whole of one fan-out, and the count an operator
// reads is the run's own TARGET SET rather than the registry's size.
//
// A follower that was excluded is in Followers with its own reason, so the report
// never has to be completed by counting what is absent from it.
type FollowerFanoutReport struct {
	// RunID identifies this fan-out. It is assigned once, before anything is
	// dispatched, so a retried fan-out is recognisable as the same gesture.
	RunID string
	// SourceDeviceID is the device the gesture was performed on.
	SourceDeviceID string
	// TargetCount is the size of the set this run TARGETED: the followers whose
	// own actions it dispatched. It is never the number of followers selected,
	// because a follower that is not online was never contacted and never failed.
	TargetCount int
	// Followers carries one row per follower the caller NAMED, in the order the
	// caller named them, deduplicated by device.
	Followers []FollowerInputOutcome
	// AcceptanceDuration covers resolution and queue admission only. It never
	// includes follower execution, which proceeds independently.
	AcceptanceDuration time.Duration
}

// Named counts the followers the report carries a row for, which is the set the
// caller named rather than the set that was targeted.
func (r FollowerFanoutReport) Named() int { return len(r.Followers) }

// Delivered counts the rows whose own action the kernel dispatched.
func (r FollowerFanoutReport) Delivered() int {
	delivered := 0
	for _, row := range r.Followers {
		if row.Disposition == FollowerInputAccepted {
			delivered++
		}
	}
	return delivered
}

// --- the seams --------------------------------------------------------------

// FollowerInputDispatch is the kernel one follower's action travels. It is the
// SAME dispatch boundary the source's own input travels, so a follower's action
// passes the same lease, fencing, policy, capability, readiness, fresh-observation,
// postcondition and evidence machinery - and, for a coordinate-bearing kind, the
// same render-space cross-check against the follower's OWN `wm size`.
// *InputDispatcher satisfies it.
type FollowerInputDispatch interface {
	Run(ctx context.Context, request InputRequest, actorType, actorID string) (action.Result, error)
}

// FollowerControlAccess is how one follower's own lease is taken and released.
//
// The fan-out is not the operator's own control of the follower, so it opens its
// own control session on the operator's behalf and takes one lease inside it for
// the one device it is about to act on. A follower that refuses the lease is
// refused ALONE: every other follower has its own session and its own lease, so
// one device under another controller cannot end the run for the rest.
type FollowerControlAccess interface {
	OpenSession(ctx context.Context, workspace, holderID, actorType, actorID string) (leases.ControlSessionID, error)
	AcquireLease(ctx context.Context, workspace, deviceID string, session leases.ControlSessionID, holderID, actorType, actorID string) (leases.DeviceLease, error)
	CloseSession(ctx context.Context, workspace string, session leases.ControlSessionID, actorType, actorID string)
}

// FollowerRunStarter accepts ONE follower's execution for dispatch and returns
// once it has been ACCEPTED, never once it has finished.
//
// This is the ordering decision of this whole surface, made explicit in a type:
// the followers proceed INDEPENDENTLY of the operator's gesture. A gesture on the
// source is answered from the source's own dispatch, so it cannot hang on the
// slowest follower - and a follower whose dispatch is slow, stuck or unreachable
// is reported in its own row rather than holding the operator's frame.
//
// An implementation that answered only after the follower's action finished would
// make the operator's gesture wait for the slowest device, which is the ordering
// this seam exists to refuse.
type FollowerRunStarter interface {
	Start(ctx context.Context, job FollowerInputJob) error
}

// FollowerFanoutIDSource assigns the identity of one fan-out run. A run with no
// identity cannot be named by the operator, cannot be read back, and cannot be
// recognised on a retry, so a fan-out without one is refused rather than
// performed.
type FollowerFanoutIDSource interface {
	NewID() (string, error)
}

// FollowerFanoutFleet reads the candidate set. It is DeviceFleetReader - the one
// place the ONLINE reading is made - and it is a named alias here rather than a
// second interface so a caller cannot satisfy this file with a reader that derives
// its own online reading.
type FollowerFanoutFleet = DeviceFleetReader

// --- the request ------------------------------------------------------------

// FollowerFanoutRequest is one operator gesture as the fan-out receives it: the
// source it was performed on, the followers the operator selected, and the typed
// payload to carry - all of which the caller already validated.
type FollowerFanoutRequest struct {
	Workspace      string
	SourceDeviceID string
	// FollowerDeviceIDs are the devices the operator selected as followers, in the
	// operator's own order. The set this run TARGETS is read from the registry, so
	// a selected device that is not online is NAMED rather than dropped.
	FollowerDeviceIDs []string
	// HolderID is the operator the followers' leases are taken for. A fan-out
	// holds control of a follower on the operator's behalf and releases it when
	// the follower's own run ends.
	HolderID string
	// RequestID is the caller's own identity for this gesture. Each follower's
	// idempotency key is derived from it, so a retried fan-out does not
	// double-apply an action on a follower that already took it.
	RequestID string
	// ObservationToken names the observation the gesture was resolved from: the
	// source frame's own live stream. It travels with every follower's copy
	// because it is the truth about where the coordinate came from - the follower
	// received a coordinate measured in the SOURCE's frame, not in its own.
	ObservationToken string
	// Target is the semantic target the gesture named, for a tap that named one.
	// An empty target and a point are mutually exclusive, exactly as they are on
	// the intent.
	Target action.SemanticTarget
	// Payload is the operator's own validated typed payload, carried to each
	// follower UNCHANGED. A payload this file had to adjust would dispatch to a
	// target the operator never named.
	Payload InputPayload
}

// FollowerInputJob is ONE follower's copy of the gesture, as the fan-out hands it
// to a run starter and back to Execute. It is the whole of what a follower's run
// needs: the identity of the fan-out, the device, and the payload in the source's
// frame.
type FollowerInputJob struct {
	RunID            string
	Workspace        string
	SourceDeviceID   string
	DeviceID         string
	Serial           string
	HolderID         string
	RequestID        string
	IdempotencyKey   string
	ObservationToken string
	Target           action.SemanticTarget
	Payload          InputPayload
	// Frame is the source's declared render space, carried unchanged.
	Frame RenderSpace
	// Kind is the kind the payload addresses, resolved once at accept time so a
	// follower's run cannot disagree with the row the operator was shown.
	Kind action.Kind
	// AcceptedAt starts the follower's queue-wait and completion clocks. It is
	// process-local telemetry and is never an authorization fact.
	AcceptedAt time.Time
}

// --- the fan-out ------------------------------------------------------------

// FollowerFanout resolves the candidate set for one gesture and carries it to the
// followers that are candidates.
type FollowerFanout struct {
	fleet    FollowerFanoutFleet
	control  FollowerControlAccess
	dispatch FollowerInputDispatch
	runs     FollowerRunStarter
	ids      FollowerFanoutIDSource
}

// NewFollowerFanout requires every part. A fan-out missing any of them would
// either act on a device without a lease, dispatch without the kernel, or accept
// work it cannot start, so it refuses to be constructed and the composition root
// mounts no surface rather than a surface that answers every request with a
// refusal.
func NewFollowerFanout(fleet FollowerFanoutFleet, control FollowerControlAccess, dispatch FollowerInputDispatch, ids FollowerFanoutIDSource, options ...FollowerFanoutOption) (*FollowerFanout, error) {
	switch {
	case fleet == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires the fleet reader that makes the online reading")
	case control == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires access to the followers' control sessions and leases")
	case dispatch == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires the device input dispatcher")
	case ids == nil:
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires a run identity source")
	}
	fanout := &FollowerFanout{fleet: fleet, control: control, dispatch: dispatch, ids: ids}
	for _, option := range options {
		if option == nil {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out option is required")
		}
		if err := option(fanout); err != nil {
			return nil, err
		}
	}
	return fanout, nil
}

// FollowerFanoutOption configures the fan-out at construction time.
type FollowerFanoutOption func(*FollowerFanout) error

// BindRunStarter binds the starter each accepted follower's run is handed to.
//
// It is a separate step because the two halves of this surface need each other:
// the fan-out accepts the work and the executor runs it, and the executor runs
// work through the fan-out. Binding is one-directional and one-time, so the cycle
// is broken without a setter that could silently replace a live starter and
// orphan the runs already accepted by it.
func (f *FollowerFanout) BindRunStarter(starter FollowerRunStarter) error {
	if f == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out is required")
	}
	if starter == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "a run starter is required, because the followers proceed independently of the source")
	}
	if f.runs != nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "this follower fan-out already has a run starter")
	}
	f.runs = starter
	return nil
}

// WithRunStarter binds the starter at construction time, for a caller that
// already holds it.
func WithRunStarter(starter FollowerRunStarter) FollowerFanoutOption {
	return func(fanout *FollowerFanout) error { return fanout.BindRunStarter(starter) }
}

// FannableFollowerKind reports whether this plane carries a gesture of this kind
// to followers.
//
// Three kinds are carried - tap, swipe and key event - and each is a gesture on
// the source's frame that means the same thing on a follower's. Two are
// deliberately not.
//
//   - Typed content is a reference RELEASED EXACTLY ONCE, in the workspace that
//     registered it. Carrying one reference to N followers would be either N
//     releases of one value or one release that N devices silently did without,
//     and both are the silent drop this surface exists to remove. It stays on the
//     source's own device, and a gesture that carries it is refused per follower
//     with its own reason rather than fanned out as though the value were shared.
//   - An app launch names ONE device's package. It is not a gesture on a frame,
//     and "the same input on each follower" has no meaning for it.
//
// Both remain fully dispatchable on the source. This function narrows what the
// fan-out carries and never what the operator can do.
func FannableFollowerKind(kind action.Kind) bool {
	switch kind {
	case action.Tap, action.Swipe, action.KeyEvent:
		return true
	default:
		return false
	}
}

// FanOut resolves the candidate set for one gesture, dispatches the source's own
// copy nowhere (it is the caller's, already dispatched), and hands each follower's
// own copy to the run starter.
//
// It returns as soon as every follower's run has been ACCEPTED. It never waits for
// a follower's action to finish, which is what keeps a gesture on the source from
// hanging on the slowest follower: the per-follower outcomes are read from the
// plane - each run records its own - rather than returned by this call.
//
// An error is returned only when the run as a whole could not be attempted - an
// unreadable fleet, a cancelled context, a request whose shape is invalid, or a
// fan-out identity that could not be assigned. Every follower the operator named
// is in the report either way, and a follower's own refusal never ends the run.
func (f *FollowerFanout) FanOut(ctx context.Context, request FollowerFanoutRequest) (FollowerFanoutReport, error) {
	acceptanceStarted := time.Now()
	report := FollowerFanoutReport{SourceDeviceID: strings.TrimSpace(request.SourceDeviceID)}
	if ctx == nil || f == nil {
		return report, platformerrors.New(platformerrors.CodeInvalidInput, "context and follower fan-out are required")
	}
	workspace := strings.TrimSpace(request.Workspace)
	holder := strings.TrimSpace(request.HolderID)
	requestID := strings.TrimSpace(request.RequestID)
	if workspace == "" || holder == "" || requestID == "" {
		return report, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires a workspace, a holder and a request id")
	}
	if report.SourceDeviceID == "" {
		return report, platformerrors.New(platformerrors.CodeInvalidInput, "a follower fan-out requires the source device the gesture was performed on")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	kind, kindErr := request.Payload.Kind()
	if kindErr != nil {
		// A payload that is absent, doubled or incomplete is refused before the
		// fleet is read: there is nothing to carry, and reading a fleet to find
		// that out would be work nobody asked for.
		return report, kindErr
	}
	if f.runs == nil {
		// A fan-out with nowhere to put the work would accept a gesture and
		// silently do nothing with it, which is the silent drop this whole surface
		// exists to remove.
		return report, platformerrors.New(platformerrors.CodeUnavailable, "this follower fan-out has no run starter, so no follower's action could be dispatched")
	}
	runID, idErr := f.ids.NewID()
	if idErr != nil {
		return report, platformerrors.Wrap(platformerrors.CodeUnavailable, "the fan-out identity could not be assigned, so nothing was dispatched", idErr)
	}
	report.RunID = runID

	// ONE read of the fleet for the whole run: the candidate set is this run's,
	// and a device whose status changed while the loop ran would otherwise be
	// judged twice against two different moments.
	fleet, fleetErr := f.fleet.Fleet(ctx, workspace)
	if fleetErr != nil {
		return report, fleetErr
	}

	// The set the run targets is decided HERE, before anything is dispatched, and
	// it is built in the operator's own order with the operator's own duplicates
	// collapsed: a device named twice is one follower with one row, because a
	// second row for one device would read as a second device.
	seen := make(map[string]struct{}, len(request.FollowerDeviceIDs))
	ordered := make([]string, 0, len(request.FollowerDeviceIDs))
	for _, named := range request.FollowerDeviceIDs {
		deviceID := strings.TrimSpace(named)
		switch {
		case deviceID == "":
			// An unnamed entry is NAMED rather than dropped: the operator's
			// selection carried a hole, and a hole is not a device.
			report.Followers = append(report.Followers, FollowerInputOutcome{
				Disposition: FollowerInputExcluded,
				Reason:      FollowerReasonUnnamed,
				Detail:      "This follower entry named no device, so nothing was sent to it.",
			})
			continue
		case deviceID == report.SourceDeviceID:
			report.Followers = append(report.Followers, FollowerInputOutcome{
				DeviceID:    deviceID,
				Disposition: FollowerInputExcluded,
				Reason:      FollowerReasonIsSource,
				Detail:      "This device is the source of the gesture, so it cannot also be one of its followers.",
			})
			continue
		}
		if _, duplicate := seen[deviceID]; duplicate {
			continue
		}
		seen[deviceID] = struct{}{}
		ordered = append(ordered, deviceID)
	}

	if !FannableFollowerKind(kind) {
		// The gesture is one this plane will not carry to followers. Every
		// selected follower gets its own row naming that - the alternative, a
		// silent drop, is the defect this whole surface exists to remove.
		for _, deviceID := range ordered {
			report.Followers = append(report.Followers, FollowerInputOutcome{
				DeviceID:    deviceID,
				Disposition: FollowerInputRefused,
				Reason:      FollowerReasonKindNotFannable,
				Detail:      fmt.Sprintf("A %s input is not carried to followers; this device received nothing. It was dispatched to the source only.", kind),
			})
		}
		report.AcceptanceDuration = time.Since(acceptanceStarted)
		return report, nil
	}

	jobs := make([]FollowerInputJob, 0, len(ordered))
	for _, deviceID := range ordered {
		reading := fleet[deviceID]
		switch {
		case !reading.Online:
			// The device is in the registry and the plane does not read it as
			// ONLINE, or the registry has never recorded it. The two are stated
			// apart because they are fixed differently: one is attached, the other
			// was never seen. Neither was contacted and neither failed.
			reason := FollowerReasonNotOnline
			detail := "This follower is not online, so the gesture was not sent to it and nothing about it failed."
			if _, registered := fleet[deviceID]; !registered {
				reason = FollowerReasonNotRegistered
				detail = "This workspace has never recorded this device, so the gesture was not sent to it."
			}
			report.Followers = append(report.Followers, FollowerInputOutcome{
				DeviceID:    deviceID,
				Disposition: FollowerInputExcluded,
				Reason:      reason,
				Detail:      detail,
			})
			continue
		case strings.TrimSpace(reading.Serial) == "":
			// The plane reads the device as online and cannot name the transport
			// to reach it at, which is a contradiction the reader should not be
			// able to produce. Refused and not contacted, rather than skipped.
			report.Followers = append(report.Followers, FollowerInputOutcome{
				DeviceID:    deviceID,
				Disposition: FollowerInputRefused,
				Reason:      FollowerReasonNoTransport,
				Detail:      "This follower reads as online and the plane could not name the transport to reach it at, so nothing was sent and nothing was contacted.",
			})
			continue
		}
		jobs = append(jobs, FollowerInputJob{
			RunID:            runID,
			Workspace:        workspace,
			SourceDeviceID:   report.SourceDeviceID,
			DeviceID:         deviceID,
			Serial:           reading.Serial,
			HolderID:         holder,
			RequestID:        requestID,
			IdempotencyKey:   FollowerIdempotencyKey(requestID, deviceID),
			ObservationToken: request.ObservationToken,
			Target:           request.Target,
			Payload:          request.Payload,
			Frame:            fanoutFrame(request.Payload),
			Kind:             kind,
		})
	}
	report.TargetCount = len(jobs)

	// Each follower's run is started independently. A run the starter could not
	// accept is NAMED as refused for this follower and never silently assumed to
	// have happened - and, being per-follower, it cannot end the run for the
	// followers whose runs WERE accepted.
	for _, job := range jobs {
		job.AcceptedAt = time.Now()
		admissionStarted := time.Now()
		startErr := f.runs.Start(ctx, job)
		acceptanceLatency := time.Since(admissionStarted)
		if startErr == nil {
			report.Followers = append(report.Followers, FollowerInputOutcome{
				DeviceID:          job.DeviceID,
				Disposition:       FollowerInputAccepted,
				Reason:            FollowerReasonDelivered,
				Detail:            "The gesture was dispatched to this follower as its own action. This row is the acceptance; the device's own outcome is recorded beside it as the run completes.",
				IdempotencyKey:    job.IdempotencyKey,
				Frame:             job.Frame,
				AcceptanceLatency: acceptanceLatency,
			})
			continue
		}
		detail := "The fan-out could not start this follower's action, so nothing was sent to it."
		reason := FollowerReasonOverloaded
		if !errors.Is(startErr, ErrFollowerFanoutOverloaded) {
			reason = FollowerReasonControlUnavailable
			detail = "The fan-out is not carrying work right now, so nothing was sent to this follower."
		}
		report.Followers = append(report.Followers, FollowerInputOutcome{
			DeviceID:          job.DeviceID,
			Disposition:       FollowerInputRefused,
			Reason:            reason,
			Detail:            detail,
			IdempotencyKey:    job.IdempotencyKey,
			Frame:             job.Frame,
			AcceptanceLatency: acceptanceLatency,
		})
	}
	report.AcceptanceDuration = time.Since(acceptanceStarted)
	return report, nil
}

// Execute dispatches ONE follower's copy of the gesture through the kernel and
// reports that follower's own outcome.
//
// It is the body of a follower's run, and it is written so that nothing it does
// can reach another follower: it opens its own control session, takes its own
// lease on its own device, dispatches one attempt and closes what it opened. It
// returns an outcome for EVERY path, including a failure, so a follower that went
// wrong is a row and never an absence.
func (f *FollowerFanout) Execute(ctx context.Context, job FollowerInputJob) FollowerInputOutcome {
	outcome := FollowerInputOutcome{
		DeviceID:       job.DeviceID,
		Disposition:    FollowerInputRefused,
		Reason:         FollowerReasonFailed,
		IdempotencyKey: job.IdempotencyKey,
		Frame:          job.Frame,
	}
	if f == nil || f.control == nil || f.dispatch == nil {
		outcome.Detail = "The fan-out is not constructed, so nothing was sent to this follower."
		return outcome
	}
	// The fan-out runs on a context of its own that outlives the operator's
	// request, but a run that was cancelled before it started dispatches nothing:
	// an input is never sent on the strength of a cancelled run.
	if ctx == nil || ctx.Err() != nil {
		outcome.Reason = FollowerReasonIndeterminate
		outcome.Detail = "This follower's run was cancelled before its action was dispatched, so nothing was sent."
		return outcome
	}

	session, sessionErr := f.control.OpenSession(ctx, job.Workspace, job.HolderID, job.ActorType(), job.ActorID())
	if sessionErr != nil {
		outcome.Reason = FollowerReasonControlUnavailable
		outcome.Detail = "No control session could be opened for this follower, so nothing was sent to it. Every other follower is unaffected."
		return outcome
	}
	defer f.control.CloseSession(context.WithoutCancel(ctx), job.Workspace, session, job.ActorType(), job.ActorID())

	held, leaseErr := f.control.AcquireLease(ctx, job.Workspace, job.DeviceID, session, job.HolderID, job.ActorType(), job.ActorID())
	if leaseErr != nil {
		// The follower's OWN lease refusal, carried with the kernel's own reason.
		// A device under another controller is refused here, alone: no other
		// follower's lease is touched by it.
		outcome.Reason = FollowerReasonLeaseRefused
		outcome.Detail = "This follower's own lease could not be held, so nothing was sent to it. Every other follower is unaffected."
		if refusal, ok := RefusalOf(leaseErr); ok {
			outcome.RefusalReason = refusal.Reason
			outcome.FailureClass = refusal.FailureClass
			outcome.Detail = refusal.Message
		} else {
			outcome.FailureClass = domain.FailureLeaseConflict
		}
		return outcome
	}

	result, runErr := f.dispatch.Run(ctx, InputRequest{
		IntentID: job.AttemptIdentity(),
		// The two facts this fan-out owns, and the only two it adds: the
		// follower's own device and the transport it was read as online at.
		Workspace: job.Workspace,
		DeviceID:  job.DeviceID,
		Serial:    job.Serial,
		// The follower's OWN lease and fence, taken above.
		LeaseID:      string(held.ID),
		HolderID:     job.HolderID,
		FencingToken: held.FencingToken,
		// This follower's own idempotency key, so a retried fan-out does not
		// double-apply on a follower that already took the action.
		IdempotencyKey: job.IdempotencyKey,
		// The observation the gesture was resolved from - the source frame's own
		// live stream. It is the truth about the coordinate's provenance, and
		// where the follower's own live session would be the delivery that
		// session refuses an input naming another observation, which is reported
		// here as this follower's own refusal rather than written to a stream the
		// coordinate was not measured in.
		ObservationToken: job.ObservationToken,
		// A fan-out is the manual surface: the followers' copies were asked for
		// by an operator, and the evidence record says so.
		InvocationSurface: action.SurfaceManual,
		Timeout:           DefaultInputTimeout,
		Target:            job.Target,
		Payload:           job.Payload,
	}, job.ActorType(), job.ActorID())
	if runErr != nil {
		return followerOutcomeFromError(outcome, runErr)
	}
	outcome.Disposition = FollowerInputAccepted
	outcome.Reason = FollowerReasonDelivered
	outcome.Outcome = result.Outcome
	outcome.AttemptID = result.Attempt.ID
	outcome.FailureClass = domain.FailureClass(result.Attempt.FailureClass)
	switch result.Outcome {
	case action.OutcomeVerified:
		outcome.Detail = "This follower received the gesture and its postcondition was verified."
	case action.OutcomePending, action.OutcomeIndeterminate, action.OutcomeTimedOut:
		// Dispatched and not established. Its own reading, never a success.
		outcome.Disposition = FollowerInputIndeterminate
		outcome.Reason = FollowerReasonIndeterminate
		outcome.Detail = "The gesture was dispatched to this follower and its outcome could not be established, so it is not reported as having taken it."
	default:
		outcome.Disposition = FollowerInputRefused
		outcome.Reason = FollowerReasonFailed
		outcome.Detail = "The gesture reached this follower and did not take effect. Every other follower's own result is unaffected."
	}
	return outcome
}

// followerOutcomeFromError classifies one follower's dispatch failure.
//
// It keeps the boundary's own vocabulary and never invents a class: a refusal
// keeps its reason, code, class and fixed sentence, a render-space mismatch is
// named as the frame it is, and everything else keeps the generic reading. No
// branch here reads an error's TEXT as a classification.
func followerOutcomeFromError(outcome FollowerInputOutcome, err error) FollowerInputOutcome {
	outcome.Disposition = FollowerInputRefused
	outcome.Reason = FollowerReasonFailed
	outcome.Detail = "This follower's action did not reach a device. Every other follower's own result is unaffected."
	if refusal, ok := RefusalOf(err); ok {
		outcome.Reason = FollowerReasonRefusedByKernel
		outcome.RefusalReason = refusal.Reason
		outcome.FailureClass = refusal.FailureClass
		outcome.Detail = refusal.Message
		return outcome
	}
	var mismatch *RenderSpaceMismatchError
	if errors.As(err, &mismatch) {
		// The coordinate cannot be carried to this follower: it does not present
		// at the frame the operator pointed in. Naming both sizes is what lets
		// the operator act, and nothing here rescales the point onto this device.
		//
		// The row carries no failure class: this is not one of the shared classes
		// (the device did not fail, timeout or go offline - it is a different
		// size), and the class vocabulary is not widened to accommodate a reading
		// the row's own reason already states exactly.
		outcome.Reason = FollowerReasonFrameNotReconciled
		if mismatch != nil {
			outcome.Detail = mismatch.Error()
		}
		return outcome
	}
	return outcome
}

// FollowerIdempotencyKey is the idempotency key ONE follower's copy of a gesture
// carries: the caller's own request identity, scoped to the follower's device.
//
// It is per follower per action, and that is the point. A fan-out whose request is
// retried - a console that timed out and asked again, an operator who pressed
// twice - produces the same key for the same follower, so a follower that already
// took the action takes it once; and the key cannot collide with the SOURCE's own
// key, because the source's action is a different action on a different device and
// a shared key would let one device's dispatch replay the other's.
func FollowerIdempotencyKey(requestID, deviceID string) string {
	return strings.TrimSpace(requestID) + ":follower:" + strings.TrimSpace(deviceID)
}

// ActorType and ActorID identify the fan-out in the audit trail of what it did.
//
// They are derived from the holder the operator's gesture named, so a follower's
// action is attributable to the operator who asked for it and never to a service
// account that nobody asked.
func (j FollowerInputJob) ActorType() string { return fanoutActorType }
func (j FollowerInputJob) ActorID() string   { return j.HolderID }

// AttemptIdentity is the identity this follower's attempt is identified by before
// the kernel assigns its own. It names the fan-out and the follower together, so
// an attempt read out of the evidence record can be traced back to the gesture
// that produced it.
func (j FollowerInputJob) AttemptIdentity() string {
	return j.RunID + ":" + j.DeviceID
}

// fanoutActorType is the actor type a follower's action is recorded under: the
// fan-out acts as the system, on the holder's behalf, and the holder is the actor
// id beside it.
const fanoutActorType = "system"

// fanoutFrame is the frame a follower's row names for this gesture: the render
// space the payload's coordinates were measured in, and the zero space for a kind
// that carries no coordinate.
//
// It reads the frame from the PAYLOAD rather than restating it, so a row can never
// name a frame the payload did not carry. The frame is the SOURCE's, unchanged:
// every follower receives it as it was measured, and each follower's own
// render-space gate decides whether that follower presents at it.
func fanoutFrame(payload InputPayload) RenderSpace {
	frame, _ := payload.renderSpace()
	return frame
}
