package transportconnect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

type recordingDispatcher struct {
	requests []execution.InputRequest
	actors   []string
	result   action.Result
	err      error
}

func (d *recordingDispatcher) Run(_ context.Context, request execution.InputRequest, actorType, actorID string) (action.Result, error) {
	d.requests = append(d.requests, request)
	d.actors = append(d.actors, actorType+":"+actorID)
	return d.result, d.err
}

type fixedSerials struct {
	serial string
	err    error
	calls  int
}

func (s *fixedSerials) CurrentSerial(context.Context, string, string) (string, error) {
	s.calls++
	return s.serial, s.err
}

type sequenceIDs struct {
	ids []string
	at  int
	err error
}

func (s *sequenceIDs) NewID() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.at >= len(s.ids) {
		return "", errors.New("no identity left")
	}
	id := s.ids[s.at]
	s.at++
	return id, nil
}

func boundaryFor(t *testing.T, dispatch transportconnect.DeviceInputDispatcher, serials transportconnect.DeviceSerialResolver, ids transportconnect.DeviceAttemptIDSource) *transportconnect.DeviceInputBoundary {
	t.Helper()
	boundary, err := transportconnect.NewDeviceInputBoundary(dispatch, serials, ids)
	if err != nil {
		t.Fatalf("building the device input boundary failed: %v", err)
	}
	return boundary
}

func sampleTarget() transportconnect.DeviceInputTarget {
	return transportconnect.DeviceInputTarget{
		Workspace:        "workspace-a",
		DeviceID:         "device-1",
		LeaseID:          "lease-1",
		FencingToken:     7,
		IdempotencyKey:   "tap-1",
		ObservationToken: "obs-before",
		ApprovalGranted:  true,
		Timeout:          3 * time.Second,
		Target:           action.SemanticTarget{ResourceID: "node-1"},
		Payload:          execution.InputPayload{Tap: &execution.TapRequest{}},
	}
}

// TestTheBoundaryCarriesEveryFieldTheCallerSupplied: the boundary resolves the two
// facts the transport deliberately does not carry and passes everything else
// through untouched. A field that silently changed on the way to the dispatcher
// would be a dispatch to a target the caller never named.
func TestTheBoundaryCarriesEveryFieldTheCallerSupplied(t *testing.T) {
	dispatch := &recordingDispatcher{}
	boundary := boundaryFor(t, dispatch, &fixedSerials{serial: "serial-alpha"}, &sequenceIDs{ids: []string{"attempt-1"}})

	if _, err := boundary.Run(context.Background(), sampleTarget(), "operator", "op-1"); err != nil {
		t.Fatalf("dispatching an input through the boundary failed: %v", err)
	}
	if len(dispatch.requests) != 1 {
		t.Fatalf("the dispatcher was called %d times for one input", len(dispatch.requests))
	}
	got := dispatch.requests[0]
	target := sampleTarget()
	switch {
	case got.Workspace != target.Workspace:
		t.Fatalf("workspace = %q, want %q", got.Workspace, target.Workspace)
	case got.DeviceID != target.DeviceID:
		t.Fatalf("device = %q, want %q", got.DeviceID, target.DeviceID)
	case got.LeaseID != target.LeaseID:
		t.Fatalf("lease = %q, want %q", got.LeaseID, target.LeaseID)
	case got.FencingToken != target.FencingToken:
		t.Fatalf("fencing token = %d, want %d", got.FencingToken, target.FencingToken)
	case got.IdempotencyKey != target.IdempotencyKey:
		t.Fatalf("idempotency key = %q, want %q", got.IdempotencyKey, target.IdempotencyKey)
	case got.ObservationToken != target.ObservationToken:
		t.Fatalf("observation token = %q, want %q", got.ObservationToken, target.ObservationToken)
	case got.ApprovalGranted != target.ApprovalGranted:
		t.Fatalf("approval = %v, want %v", got.ApprovalGranted, target.ApprovalGranted)
	case got.Timeout != target.Timeout:
		t.Fatalf("timeout = %s, want %s", got.Timeout, target.Timeout)
	case got.Target.ResourceID != target.Target.ResourceID:
		t.Fatalf("target = %+v, want %+v", got.Target, target.Target)
	case got.Payload.Tap == nil:
		t.Fatal("the typed payload did not reach the dispatcher")
	case got.InvocationSurface != action.SurfaceManual:
		t.Fatalf("invocation surface = %q, want the manual surface a local operator RPC is", got.InvocationSurface)
	}
	// The two facts the transport deliberately omits, resolved here.
	if got.Serial != "serial-alpha" {
		t.Fatalf("serial = %q, want the device's resolved current serial", got.Serial)
	}
	if got.HolderID != "op-1" {
		t.Fatalf("holder = %q, want the actor the request was made as", got.HolderID)
	}
	if got.IntentID != "attempt-1" {
		t.Fatalf("attempt identity = %q, want the identity this boundary assigned", got.IntentID)
	}
	if dispatch.actors[0] != "operator:op-1" {
		t.Fatalf("actor = %q, want it passed through unchanged", dispatch.actors[0])
	}
}

// TestNoDispatchWithoutAResolvableSerial: a device that cannot be resolved is not
// a target, so nothing is dispatched and the failure carries the resolver's own
// refusal rather than a boundary-invented one.
func TestNoDispatchWithoutAResolvableSerial(t *testing.T) {
	wantErr := errors.New("device has no current transport endpoint")
	dispatch := &recordingDispatcher{}
	boundary := boundaryFor(t, dispatch, &fixedSerials{err: wantErr}, &sequenceIDs{ids: []string{"attempt-1"}})

	if _, err := boundary.Run(context.Background(), sampleTarget(), "operator", "op-1"); !errors.Is(err, wantErr) {
		t.Fatalf("refusal = %v, want the resolver's own refusal", err)
	}
	if len(dispatch.requests) != 0 {
		t.Fatal("a device with no resolvable serial still reached the dispatcher")
	}
}

// TestEveryDispatchCarriesItsOwnAttemptIdentity: two inputs are two attempts. A
// reused identity would make the kernel treat a second dispatch as a duplicate of
// the first, and the evidence store would then hold one record for two actions.
func TestEveryDispatchCarriesItsOwnAttemptIdentity(t *testing.T) {
	dispatch := &recordingDispatcher{}
	boundary := boundaryFor(t, dispatch, &fixedSerials{serial: "serial-alpha"}, &sequenceIDs{ids: []string{"attempt-1", "attempt-2"}})

	for range 2 {
		if _, err := boundary.Run(context.Background(), sampleTarget(), "operator", "op-1"); err != nil {
			t.Fatalf("dispatching failed: %v", err)
		}
	}
	if len(dispatch.requests) != 2 {
		t.Fatalf("the dispatcher was called %d times, want 2", len(dispatch.requests))
	}
	first, second := dispatch.requests[0].IntentID, dispatch.requests[1].IntentID
	if first == second {
		t.Fatalf("both dispatches carried identity %q; each attempt needs its own", first)
	}
	if first == "" || second == "" {
		t.Fatalf("an attempt identity was empty: %q, %q", first, second)
	}
}

// TestNoDispatchWhenTheIdentityCannotBeAssigned: an attempt with no identity cannot
// be recorded, so it is not dispatched.
func TestNoDispatchWhenTheIdentityCannotBeAssigned(t *testing.T) {
	dispatch := &recordingDispatcher{}
	boundary := boundaryFor(t, dispatch, &fixedSerials{serial: "serial-alpha"}, &sequenceIDs{err: errors.New("identity source unavailable")})

	if _, err := boundary.Run(context.Background(), sampleTarget(), "operator", "op-1"); err == nil {
		t.Fatal("an input was dispatched without an attempt identity")
	}
	if len(dispatch.requests) != 0 {
		t.Fatal("an input with no attempt identity reached the dispatcher")
	}
}

// TestTheBoundaryRefusesIncompleteConstruction: a boundary missing any part cannot
// dispatch anything, so it refuses to be built and the composition root mounts no
// route rather than one that answers every request with a refusal.
func TestTheBoundaryRefusesIncompleteConstruction(t *testing.T) {
	dispatch := &recordingDispatcher{}
	serials := &fixedSerials{serial: "serial-alpha"}
	ids := &sequenceIDs{ids: []string{"attempt-1"}}
	for _, testCase := range []struct {
		name     string
		dispatch transportconnect.DeviceInputDispatcher
		serials  transportconnect.DeviceSerialResolver
		ids      transportconnect.DeviceAttemptIDSource
	}{
		{name: "no dispatcher", serials: serials, ids: ids},
		{name: "no resolver", dispatch: dispatch, ids: ids},
		{name: "no identity source", dispatch: dispatch, serials: serials},
	} {
		if _, err := transportconnect.NewDeviceInputBoundary(testCase.dispatch, testCase.serials, testCase.ids); err == nil {
			t.Fatalf("%s: an incomplete boundary was built", testCase.name)
		}
	}
}
