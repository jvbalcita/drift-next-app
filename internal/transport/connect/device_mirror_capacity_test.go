package transportconnect_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/media"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The surface's half of the plane's device-session capacity: which purpose an open
// is made as, what a capacity refusal is answered with, that a refusal leaves a
// record naming it, and what the capacity request publishes.
//
// Every case runs against a fake transport, so a refusal is exercised without a
// device, a peer connection or a socket.

// planeCapacity is the engine's own bound, as a surface reads it.
type planeCapacity struct {
	capacity int
	reserve  int
}

func (p planeCapacity) Capacity() int        { return p.capacity }
func (p planeCapacity) OperatorReserve() int { return p.reserve }

// refusalRecorder is where a refused stream is written down, as the surface sees
// it: one call per capacity refusal and nothing else.
type refusalRecorder struct {
	refusals []transportconnect.StreamRefusal
	err      error
}

func (r *refusalRecorder) RecordStreamRefusal(_ context.Context, refusal transportconnect.StreamRefusal) error {
	r.refusals = append(r.refusals, refusal)
	return r.err
}

// mirrorHandlerWith builds the handler with the facts under test, over the same
// fake transport the other cases use.
func mirrorHandlerWith(t *testing.T, mirrors transportconnect.DeviceMirrors, capacity transportconnect.MirrorCapacitySource, refusals transportconnect.MirrorRefusalRecorder) *transportconnect.DeviceMirrorHandler {
	t.Helper()
	handler := transportconnect.NewDeviceMirrorHandler(mirrors, &fixedSerials{serial: mirrorSerial}, capacity, refusals)
	if handler == nil {
		t.Fatal("the live mirror handler was not constructed")
	}
	return handler
}

// purposeRequest asks for one device's stream as the purpose under test.
func purposeRequest(purpose driftv1.MirrorViewerPurpose) *connectrpc.Request[driftv1.StartMirrorStreamRequest] {
	request := startRequest(mirrorWorkspace, mirrorDevice, driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC)
	request.Msg.Purpose = purpose
	return request
}

// TestStartMirrorStreamCarriesThePurposeItWasGiven: the purpose is what the
// plane's capacity is spent against, so a surface that dropped it would open a
// grid of tiles as the operator's own frames and spend the place the operator's
// big frame is kept.
func TestStartMirrorStreamCarriesThePurposeItWasGiven(t *testing.T) {
	cases := []struct {
		name    string
		purpose driftv1.MirrorViewerPurpose
		want    string
	}{
		{name: "the console's grid", purpose: driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT, want: string(media.PurposeAmbient)},
		{name: "the operator's own frame", purpose: driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR, want: string(media.PurposeOperator)},
		{
			// The undeclared case is the demand the reserve is kept for: a caller
			// that has not been taught the distinction must not lose the operator a
			// place.
			name:    "nothing stated",
			purpose: driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_UNSPECIFIED,
			want:    string(media.PurposeOperator),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mirrors := newFakeMirrors()
			handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 5, reserve: 1}, nil)
			if _, err := handler.StartMirrorStream(context.Background(), purposeRequest(test.purpose)); err != nil {
				t.Fatalf("StartMirrorStream: %v", err)
			}
			if len(mirrors.purposes) != 1 || mirrors.purposes[0] != test.want {
				t.Fatalf("the open was made as %v, want %q", mirrors.purposes, test.want)
			}
		})
	}
}

// TestACapacityRefusalReachesTheCallerInThePlanesOwnSentence is the card's own
// criterion that a frame which cannot be carried says WHY in the plane's words.
//
// It matters that this goes through the surface rather than only through the
// engine: the shared error mapper renders an unclassified error as one fixed
// internal sentence, so a refusal carried by the mapper would reach the operator
// as "the stream failed" with the real reason left in the log.
func TestACapacityRefusalReachesTheCallerInThePlanesOwnSentence(t *testing.T) {
	mirrors := newFakeMirrors()
	mirrors.openErr = &media.SessionCapacityError{Capacity: 4, Purpose: media.PurposeOperator, AmbientShare: 4}
	recorder := &refusalRecorder{}
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 4, reserve: 1}, recorder)

	_, err := handler.StartMirrorStream(context.Background(), purposeRequest(driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR))
	if err == nil {
		t.Fatal("a plane holding its whole capacity opened a stream anyway")
	}
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the refusal was answered as %T, want a Connect error", err)
	}
	if !strings.Contains(connectErr.Message(), "4 devices are already being mirrored") {
		t.Fatalf("the caller is told %q, which does not name the capacity the plane reached", connectErr.Message())
	}
	if !strings.Contains(connectErr.Message(), "device session capacity") {
		t.Fatalf("the caller is told %q, which does not name the bound it is", connectErr.Message())
	}
	if connectErr.Code() != connectrpc.CodeUnavailable {
		t.Fatalf("the refusal answers %v, want unavailable", connectErr.Code())
	}
}

// TestACapacityRefusalLeavesAnEventNamingIt: a refusal that is only spoken is a
// refusal the next reader of the plane's database cannot see. Measured on the
// owner's host, sixteen sessions sat unfinished with zero mirror events, so
// nothing in the record said why a frame had carried nothing.
func TestACapacityRefusalLeavesAnEventNamingIt(t *testing.T) {
	mirrors := newFakeMirrors()
	mirrors.openErr = &media.SessionCapacityError{Capacity: 4, Purpose: media.PurposeOperator, AmbientShare: 4}
	recorder := &refusalRecorder{}
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 4, reserve: 1}, recorder)

	_, _ = handler.StartMirrorStream(context.Background(), purposeRequest(driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR))
	if len(recorder.refusals) != 1 {
		t.Fatalf("a capacity refusal left %d record(s), want one", len(recorder.refusals))
	}
	refusal := recorder.refusals[0]
	if refusal.WorkspaceID != mirrorWorkspace || refusal.DeviceID != mirrorDevice {
		t.Fatalf("the record names workspace %q device %q, want this request's own", refusal.WorkspaceID, refusal.DeviceID)
	}
	if refusal.ViewerPurpose != string(media.PurposeOperator) {
		t.Fatalf("the record names the purpose %q, want the operator's own frame", refusal.ViewerPurpose)
	}
	if refusal.Capacity != 4 {
		t.Fatalf("the record names a capacity of %d, want the bound that was spent", refusal.Capacity)
	}
	if !strings.Contains(refusal.Reason, "4 devices are already being mirrored") {
		t.Fatalf("the record's reason is %q, which is not the plane's own sentence", refusal.Reason)
	}
	if refusal.ActorID != "operator-1" {
		t.Fatalf("the record names the actor %q, want the caller this surface authenticated", refusal.ActorID)
	}
}

// TestAGridRefusalIsRecordedWithTheShareItWasRefused: the grid's own refusal names
// a different bound, and the record has to say which one so a reader is not sent
// looking for capacity that is in fact free.
func TestAGridRefusalIsRecordedWithTheShareItWasRefused(t *testing.T) {
	mirrors := newFakeMirrors()
	capacity := &media.SessionCapacityError{Capacity: 4, Purpose: media.PurposeAmbient, AmbientShare: 3}
	mirrors.openErr = capacity
	recorder := &refusalRecorder{}
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 4, reserve: 1}, recorder)

	_, _ = handler.StartMirrorStream(context.Background(), purposeRequest(driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_AMBIENT))
	if len(recorder.refusals) != 1 {
		t.Fatalf("the grid's refusal left %d record(s), want one", len(recorder.refusals))
	}
	refusal := recorder.refusals[0]
	if refusal.ViewerPurpose != string(media.PurposeAmbient) {
		t.Fatalf("the record names the purpose %q, want the grid", refusal.ViewerPurpose)
	}
	if refusal.OperatorReserve != 1 {
		t.Fatalf("the record names a reserve of %d, want the place the grid could not spend", refusal.OperatorReserve)
	}
	if !strings.Contains(refusal.Reason, "the operator's own frame") {
		t.Fatalf("the record's reason is %q, which does not say what the remaining place is kept for", refusal.Reason)
	}
}

// TestOnlyACapacityRefusalIsRecorded: the record exists for one fact, and a
// transport failure recorded as a capacity refusal would send a reader after a
// bound that was never reached.
func TestOnlyACapacityRefusalIsRecorded(t *testing.T) {
	mirrors := newFakeMirrors()
	mirrors.openErr = errors.New("media: the device's stream could not be opened")
	recorder := &refusalRecorder{}
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 4, reserve: 1}, recorder)

	if _, err := handler.StartMirrorStream(context.Background(), purposeRequest(driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR)); err == nil {
		t.Fatal("a failed open answered with a stream")
	}
	if len(recorder.refusals) != 0 {
		t.Fatalf("an open that failed for another reason left %d capacity record(s)", len(recorder.refusals))
	}
}

// TestARefusalIsAnsweredEvenWhenTheRecordCannotBeWritten: the operator's answer
// comes from the engine, never from the log. A store that could not take the row
// must not turn a capacity refusal into a different failure.
func TestARefusalIsAnsweredEvenWhenTheRecordCannotBeWritten(t *testing.T) {
	mirrors := newFakeMirrors()
	mirrors.openErr = &media.SessionCapacityError{Capacity: 2, Purpose: media.PurposeOperator, AmbientShare: 2}
	recorder := &refusalRecorder{err: errors.New("the store is closed")}
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 2, reserve: 1}, recorder)

	_, err := handler.StartMirrorStream(context.Background(), purposeRequest(driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR))
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the refusal was answered as %T, want a Connect error", err)
	}
	if !strings.Contains(connectErr.Message(), "2 devices are already being mirrored") {
		t.Fatalf("a record that could not be written changed the answer to %q", connectErr.Message())
	}
}

// capacityRequest asks for the plane's capacity the way the console asks: naming
// the workspace it is reading for.
func capacityRequest() *connectrpc.Request[driftv1.GetMirrorCapacityRequest] {
	return connectrpc.NewRequest(&driftv1.GetMirrorCapacityRequest{Workspace: &driftv1.WorkspaceRef{WorkspaceId: mirrorWorkspace}})
}

// TestACapacityRequestMustNameItsWorkspace: the bound is the plane's and is the
// same for every workspace on it, so a caller that named none is a caller whose
// request this surface could not account for - and it is refused rather than
// answered with a number that has no workspace attached to it.
func TestACapacityRequestMustNameItsWorkspace(t *testing.T) {
	handler := mirrorHandlerWith(t, newFakeMirrors(), planeCapacity{capacity: 4, reserve: 1}, nil)
	_, err := handler.GetMirrorCapacity(context.Background(), connectrpc.NewRequest(&driftv1.GetMirrorCapacityRequest{}))
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("a request naming no workspace was answered as %T, want a Connect error", err)
	}
	if connectErr.Code() != connectrpc.CodeInvalidArgument {
		t.Fatalf("a request naming no workspace answers %v, want invalid_argument", connectErr.Code())
	}
}

// TestGetMirrorCapacityPublishesThePlanesOwnBound: the console derives how many
// tiles it may carry from this, so the number has to be the engine's own rather
// than a second copy of it kept at the surface.
func TestGetMirrorCapacityPublishesThePlanesOwnBound(t *testing.T) {
	handler := mirrorHandlerWith(t, newFakeMirrors(), planeCapacity{capacity: 6, reserve: 2}, nil)
	response, err := handler.GetMirrorCapacity(context.Background(), capacityRequest())
	if err != nil {
		t.Fatalf("GetMirrorCapacity: %v", err)
	}
	capacity := response.Msg.GetCapacity()
	if capacity.GetSessionCapacity() != 6 || capacity.GetOperatorReserve() != 2 {
		t.Fatalf("the surface published capacity %d reserve %d, want the engine's own 6 and 2",
			capacity.GetSessionCapacity(), capacity.GetOperatorReserve())
	}
}

// TestAGridIsOfferedNoPlaceWhenTheReserveIsTheWholeCapacity: a deployment may keep
// every session for the operator's own frames, and the surface publishes that as
// the grid's share of none rather than as a capacity the grid may spend.
func TestAGridIsOfferedNoPlaceWhenTheReserveIsTheWholeCapacity(t *testing.T) {
	handler := mirrorHandlerWith(t, newFakeMirrors(), planeCapacity{capacity: 2, reserve: 3}, nil)
	response, err := handler.GetMirrorCapacity(context.Background(), capacityRequest())
	if err != nil {
		t.Fatalf("GetMirrorCapacity: %v", err)
	}
	capacity := response.Msg.GetCapacity()
	if capacity.GetSessionCapacity() != 2 || capacity.GetOperatorReserve() != 2 {
		t.Fatalf("a reserve larger than the capacity published %d of %d, want the whole capacity kept",
			capacity.GetOperatorReserve(), capacity.GetSessionCapacity())
	}
}

// TestACapacityRequestWithoutABoundIsRefusedRatherThanZerod: zero is not a bound
// this plane stated, and a console that read it as one would carry no tiles at all
// and blame its own allocation.
func TestACapacityRequestWithoutABoundIsRefusedRatherThanZerod(t *testing.T) {
	handler := mirrorHandlerWith(t, newFakeMirrors(), nil, nil)
	_, err := handler.GetMirrorCapacity(context.Background(), capacityRequest())
	if err == nil {
		t.Fatal("a surface with no bound to state answered with one")
	}
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the refusal was answered as %T, want a Connect error", err)
	}
	if connectErr.Code() != connectrpc.CodeUnavailable {
		t.Fatalf("a surface with no bound answers %v, want unavailable", connectErr.Code())
	}
}

// TestTheCapacitySurfaceReadsNothingItCouldChange: the request opens, negotiates,
// polls and ends nothing, so a capacity read leaves the transport untouched.
func TestTheCapacitySurfaceReadsNothingItCouldChange(t *testing.T) {
	mirrors := newFakeMirrors()
	handler := mirrorHandlerWith(t, mirrors, planeCapacity{capacity: 1, reserve: 1}, nil)
	for i := 0; i < 3; i++ {
		if _, err := handler.GetMirrorCapacity(context.Background(), capacityRequest()); err != nil {
			t.Fatalf("GetMirrorCapacity: %v", err)
		}
	}
	if len(mirrors.opened) != 0 || len(mirrors.purposes) != 0 {
		t.Fatalf("a capacity read opened %v with purposes %v", mirrors.opened, mirrors.purposes)
	}
}
