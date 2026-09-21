package transportconnect_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// The grid preview surface's own cases: every device named is answered, a device
// the plane cannot capture says so in the plane's words, the devices a sweep bound
// did not reach are NAMED, and a still that is not current is never carried as a
// picture.
//
// Every case runs against a fake capture set and a fake resolver, so what is
// asserted is the surface's own contract rather than an engine's behaviour - the
// engine's behaviour has its own tests.
//
// The fixtures carry no device data: the payloads are a synthetic string, and no
// case reaches a device, a socket or a process.

const (
	gridWorkspace = "workspace-grid"
	gridRequestID = "request-grid"
)

// fakeGrid is the plane's capture set as this surface calls it. It records what it
// was asked for, so a case can assert the reconciliation rather than only the
// answer.
type fakeGrid struct {
	serials   []string
	admitted  []string
	refused   []string
	invalid   []string
	released  int
	frames    map[string]media.Frame
	cost      media.GridCost
	syncCalls int
	stopCalls int
}

func newFakeGrid() *fakeGrid {
	return &fakeGrid{
		frames: map[string]media.Frame{},
		cost: media.GridCost{
			Cadence:    4 * time.Second,
			Profile:    media.GridStillProfileFor(media.GridStillLevelLow),
			MaxDevices: 64,
			Subscribed: 0,
		},
	}
}

func (g *fakeGrid) SyncSubscriptions(serials []string) media.GridSubscription {
	g.syncCalls++
	g.serials = append([]string(nil), serials...)
	subscription := media.GridSubscription{Refused: append([]string(nil), g.refused...), Invalid: append([]string(nil), g.invalid...)}
	for _, serial := range serials {
		if contains(g.refused, serial) || contains(g.invalid, serial) {
			continue
		}
		subscription.Admitted = append(subscription.Admitted, serial)
	}
	g.admitted = subscription.Admitted
	g.cost.Subscribed = len(subscription.Admitted)
	return subscription
}

func (g *fakeGrid) Frame(serial string) (media.Frame, bool) {
	frame, ok := g.frames[serial]
	return frame, ok
}

func (g *fakeGrid) GridCost() media.GridCost { return g.cost }

func (g *fakeGrid) StopSubscriptions() int {
	g.stopCalls++
	released := g.released
	g.released = 0
	return released
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// fakeGridResolver resolves the devices a case names, and refuses the ones it
// states it cannot resolve.
type fakeGridResolver struct {
	serials    map[string]string
	unroutable map[string]error
	resolved   []string
}

func (r *fakeGridResolver) CurrentSerial(_ context.Context, _ string, deviceID string) (string, error) {
	if err, refused := r.unroutable[deviceID]; refused {
		return "", err
	}
	serial, found := r.serials[deviceID]
	if !found {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "device has no current transport endpoint")
	}
	r.resolved = append(r.resolved, deviceID)
	return serial, nil
}

// currentStill is a delivered picture: the state a tile paints.
func currentStill(serial string) media.Frame {
	return media.Frame{
		Serial:        serial,
		CapturedAt:    time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC),
		ContentHash:   "sha256:" + serial,
		Bytes:         204800,
		MediaType:     media.StillMediaType,
		Width:         240,
		Height:        506,
		StillBytes:    18342,
		PreviewBase64: base64.StdEncoding.EncodeToString([]byte("a still")),
		Frames:        3,
	}
}

// gridHandlerWith builds the surface over the fakes a case states.
func gridHandlerWith(t *testing.T, grid transportconnect.GridStills, resolver transportconnect.DeviceSerialResolver) *transportconnect.GridPreviewHandler {
	t.Helper()
	handler := transportconnect.NewGridPreviewHandler(grid, resolver)
	if handler == nil {
		t.Fatal("the grid preview handler was not constructed")
	}
	return handler
}

func syncRequest(deviceIDs ...string) *connectrpc.Request[driftv1.SyncGridPreviewsRequest] {
	return connectrpc.NewRequest(&driftv1.SyncGridPreviewsRequest{
		Context:   &driftv1.RequestContext{RequestId: gridRequestID, ActorId: "operator-1"},
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: gridWorkspace},
		DeviceIds: deviceIDs,
	})
}

// TestSyncGridPreviewsAnswersEveryDeviceItWasGiven pins the surface's whole point:
// EVERY device the console draws is answered, in the order it named them, and the
// plane's own numbers travel with the answer so a tile can state what it is
// carried at.
//
// It is asserted as a COUNT against the devices asked for rather than as "a still
// came back": the defect this surface removes is a grid that shows as many devices
// as the plane's session capacity allows, so a case that asked for four and got
// three has to fail.
func TestSyncGridPreviewsAnswersEveryDeviceItWasGiven(t *testing.T) {
	grid := newFakeGrid()
	grid.frames["serial-1"] = currentStill("serial-1")
	grid.frames["serial-2"] = currentStill("serial-2")
	grid.frames["serial-3"] = currentStill("serial-3")
	resolver := &fakeGridResolver{serials: map[string]string{"device-1": "serial-1", "device-2": "serial-2", "device-3": "serial-3"}}
	handler := gridHandlerWith(t, grid, resolver)

	response, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1", "device-2", "device-3"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if len(response.Msg.GetStills()) != 3 {
		t.Fatalf("answered %d still(s) for 3 devices", len(response.Msg.GetStills()))
	}
	for index, want := range []string{"device-1", "device-2", "device-3"} {
		if got := response.Msg.GetStills()[index].GetDeviceId(); got != want {
			t.Fatalf("still %d is for %q, want %q: the answer follows the order asked", index, got, want)
		}
	}
	if len(response.Msg.GetRefusedDeviceIds()) != 0 {
		t.Fatalf("refused = %v, want none", response.Msg.GetRefusedDeviceIds())
	}
	if got := grid.serials; len(got) != 3 || got[0] != "serial-1" || got[2] != "serial-3" {
		t.Fatalf("the plane's capture set was reconciled to %v, want the devices asked for", got)
	}

	still := response.Msg.GetStills()[0]
	if still.GetState() != driftv1.GridStillState_GRID_STILL_STATE_CURRENT {
		t.Fatalf("state = %v, want current", still.GetState())
	}
	if still.GetMediaType() != media.StillMediaType || still.GetStillBase64() == "" {
		t.Fatalf("a current still carries no picture: %+v", still)
	}
	if still.GetCapturedAt() != "2026-09-21T08:00:00Z" {
		t.Fatalf("captured_at = %q, want the capture's own time", still.GetCapturedAt())
	}
	if still.GetBytes() != 18342 || still.GetSourceBytes() != 204800 {
		t.Fatalf("still bytes = %d of %d source byte(s), want the delivered and captured sizes", still.GetBytes(), still.GetSourceBytes())
	}

	profile := response.Msg.GetProfile()
	if profile.GetCadenceMillis() != 4000 {
		t.Fatalf("cadence = %d ms, want the plane's 4000 ms", profile.GetCadenceMillis())
	}
	if profile.GetLevel() != driftv1.GridStillLevel_GRID_STILL_LEVEL_LOW {
		t.Fatalf("level = %v, want low", profile.GetLevel())
	}
	if profile.GetLevelMaxWidth() != uint32(grid.cost.Profile.MaxWidth) || profile.GetLevelJpegQuality() != uint32(grid.cost.Profile.JPEGQuality) {
		t.Fatalf("profile = %+v, want the level's own numbers %+v", profile, grid.cost.Profile)
	}
	if profile.GetStillByteBound() != uint32(grid.cost.Profile.ByteBound) {
		t.Fatalf("still byte bound = %d, want %d", profile.GetStillByteBound(), grid.cost.Profile.ByteBound)
	}
	if profile.GetMaxDevices() != 64 || profile.GetSubscribed() != 3 {
		t.Fatalf("profile states max_devices=%d subscribed=%d, want the sweep bound and what it is carrying",
			profile.GetMaxDevices(), profile.GetSubscribed())
	}
}

// TestSyncGridPreviewsReconcilesTheCaptureSetToExactlyWhatWasNamed pins the
// property that keeps the plane from capturing a fleet nobody is watching: the set
// the plane is told is the devices named, and the devices that left it are dropped
// rather than accumulated.
func TestSyncGridPreviewsReconcilesTheCaptureSetToExactlyWhatWasNamed(t *testing.T) {
	grid := newFakeGrid()
	grid.frames["serial-1"] = currentStill("serial-1")
	grid.frames["serial-2"] = currentStill("serial-2")
	resolver := &fakeGridResolver{serials: map[string]string{"device-1": "serial-1", "device-2": "serial-2"}}
	handler := gridHandlerWith(t, grid, resolver)

	if _, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1", "device-2")); err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if _, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-2")); err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if len(grid.serials) != 1 || grid.serials[0] != "serial-2" {
		t.Fatalf("the plane's capture set after the grid narrowed is %v, want only the device still drawn", grid.serials)
	}
	if grid.syncCalls != 2 {
		t.Fatalf("the plane was reconciled %d time(s), want one per request", grid.syncCalls)
	}
	// A device that left the set is answered by OMISSION from the stills and from
	// the refusals, which is the honest reading: this console is not showing it.
	last, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-2"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if len(last.Msg.GetStills()) != 1 || last.Msg.GetStills()[0].GetDeviceId() != "device-2" {
		t.Fatalf("stills = %+v, want only the device asked for", last.Msg.GetStills())
	}
}

// TestSyncGridPreviewsStatesADeviceItCannotCapture pins that a device the plane
// cannot reach a transport for is ANSWERED with the plane's own reason rather than
// dropped: a response that silently left it out would leave the console unable to
// tell "the plane said no" from "the plane said nothing".
func TestSyncGridPreviewsStatesADeviceItCannotCapture(t *testing.T) {
	grid := newFakeGrid()
	grid.frames["serial-1"] = currentStill("serial-1")
	resolver := &fakeGridResolver{
		serials:    map[string]string{"device-1": "serial-1"},
		unroutable: map[string]error{"device-offline": platformerrors.New(platformerrors.CodePreconditionFailed, "device has no current transport endpoint")},
	}
	handler := gridHandlerWith(t, grid, resolver)

	response, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1", "device-offline"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if len(response.Msg.GetStills()) != 2 {
		t.Fatalf("answered %d still(s) for 2 devices: an uncapturable device must still be answered",
			len(response.Msg.GetStills()))
	}
	uncapturable := response.Msg.GetStills()[1]
	if uncapturable.GetDeviceId() != "device-offline" {
		t.Fatalf("the second answer is for %q, want the device that could not be captured", uncapturable.GetDeviceId())
	}
	if uncapturable.GetState() != driftv1.GridStillState_GRID_STILL_STATE_UNAVAILABLE {
		t.Fatalf("state = %v, want unavailable", uncapturable.GetState())
	}
	if uncapturable.GetStillBase64() != "" {
		t.Fatal("a device that could not be captured was answered with a picture")
	}
	if uncapturable.GetFailureClass() != string(media.GridStillFailureClass) {
		t.Fatalf("failure class = %q, want %q", uncapturable.GetFailureClass(), media.GridStillFailureClass)
	}
	if !strings.Contains(uncapturable.GetFailureDetail(), "transport") {
		t.Fatalf("failure detail = %q, want the plane's own reason about the device's transport", uncapturable.GetFailureDetail())
	}
	if len(response.Msg.GetRefusedDeviceIds()) != 0 {
		t.Fatalf("refused = %v, want none: this device was not refused by a bound", response.Msg.GetRefusedDeviceIds())
	}
	// The device that could not be captured is not named to the plane's capture
	// set: a subscription for a device that cannot be captured is work with nothing
	// to capture.
	if len(grid.serials) != 1 || grid.serials[0] != "serial-1" {
		t.Fatalf("the plane was asked to capture %v, want only the device it can reach", grid.serials)
	}
}

// TestSyncGridPreviewsNamesTheDevicesItsSweepBoundDidNotReach pins that the one
// bound the grid does have is stated with the devices it applies to: a tile the
// bound does not reach says WHICH bound it did not reach, rather than reading as a
// device that failed.
func TestSyncGridPreviewsNamesTheDevicesItsSweepBoundDidNotReach(t *testing.T) {
	grid := newFakeGrid()
	grid.refused = []string{"serial-3"}
	grid.frames["serial-1"] = currentStill("serial-1")
	grid.frames["serial-2"] = currentStill("serial-2")
	grid.cost.MaxDevices = 2
	resolver := &fakeGridResolver{serials: map[string]string{
		"device-1": "serial-1", "device-2": "serial-2", "device-3": "serial-3",
	}}
	handler := gridHandlerWith(t, grid, resolver)

	response, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1", "device-2", "device-3"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if len(response.Msg.GetRefusedDeviceIds()) != 1 || response.Msg.GetRefusedDeviceIds()[0] != "device-3" {
		t.Fatalf("refused = %v, want the device the sweep bound did not reach", response.Msg.GetRefusedDeviceIds())
	}
	for _, still := range response.Msg.GetStills() {
		if still.GetDeviceId() == "device-3" {
			t.Fatal("a refused device was also answered as a still")
		}
	}
	if response.Msg.GetProfile().GetMaxDevices() != 2 {
		t.Fatalf("profile states max_devices=%d, want the bound that produced the refusal", response.Msg.GetProfile().GetMaxDevices())
	}
}

// TestSyncGridPreviewsCarriesNoPictureForAStillThatIsNotCurrent pins the rule a
// grid of screens rests on: the last picture a device produced is not its screen
// now, so a still whose later capture failed is carried by its hash and its time
// and its classified failure - and NOT by a picture a tile could paint.
func TestSyncGridPreviewsCarriesNoPictureForAStillThatIsNotCurrent(t *testing.T) {
	grid := newFakeGrid()
	stale := currentStill("serial-1")
	stale.FailureClass = domain.FailureDeviceOffline
	stale.FailureDetail = "the device's transport stopped answering"
	stale.Failures = 2
	grid.frames["serial-1"] = stale
	resolver := &fakeGridResolver{serials: map[string]string{"device-1": "serial-1"}}
	handler := gridHandlerWith(t, grid, resolver)

	response, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	still := response.Msg.GetStills()[0]
	if still.GetState() != driftv1.GridStillState_GRID_STILL_STATE_STALE {
		t.Fatalf("state = %v, want stale", still.GetState())
	}
	if still.GetStillBase64() != "" || still.GetWidth() != 0 || still.GetBytes() != 0 {
		t.Fatalf("a stale still was carried as a picture: %+v", still)
	}
	if still.GetFailureClass() != string(domain.FailureDeviceOffline) || still.GetFailureDetail() == "" {
		t.Fatalf("a stale still carries no classified reason: %+v", still)
	}
	if still.GetContentHash() == "" || still.GetCapturedAt() == "" {
		t.Fatalf("a stale still lost the capture it came from: %+v", still)
	}
	if still.GetFailures() != 2 {
		t.Fatalf("failures = %d, want the device's own history", still.GetFailures())
	}
}

// TestSyncGridPreviewsCarriesTheCadenceThePlaneMeasured pins that the number a tile
// states its freshness from is the plane's MEASURED interval between two captures,
// not a cadence the console assumes from the request it sent.
func TestSyncGridPreviewsCarriesTheCadenceThePlaneMeasured(t *testing.T) {
	grid := newFakeGrid()
	measured := currentStill("serial-1")
	measured.ObservedCadence = 7200 * time.Millisecond
	grid.frames["serial-1"] = measured
	resolver := &fakeGridResolver{serials: map[string]string{"device-1": "serial-1"}}
	handler := gridHandlerWith(t, grid, resolver)

	response, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1"))
	if err != nil {
		t.Fatalf("SyncGridPreviews: %v", err)
	}
	if got := response.Msg.GetStills()[0].GetObservedCadenceMillis(); got != 7200 {
		t.Fatalf("observed cadence = %d ms, want the measured 7200 ms", got)
	}
}

// TestSyncGridPreviewsRequiresAWorkspaceAndAnActor pins the two facts every request
// on this surface states: which workspace it is reading for, and who is asking.
func TestSyncGridPreviewsRequiresAWorkspaceAndAnActor(t *testing.T) {
	grid := newFakeGrid()
	resolver := &fakeGridResolver{serials: map[string]string{}}
	handler := gridHandlerWith(t, grid, resolver)

	noActor := connectrpc.NewRequest(&driftv1.SyncGridPreviewsRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: gridWorkspace},
		DeviceIds: []string{"device-1"},
	})
	if _, err := handler.SyncGridPreviews(context.Background(), noActor); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("a request with no actor was answered with %v", err)
	}
	noWorkspace := connectrpc.NewRequest(&driftv1.SyncGridPreviewsRequest{
		Context:   &driftv1.RequestContext{RequestId: gridRequestID},
		DeviceIds: []string{"device-1"},
	})
	if _, err := handler.SyncGridPreviews(context.Background(), noWorkspace); connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("a request with no workspace was answered with %v", err)
	}
	if grid.syncCalls != 0 {
		t.Fatalf("the plane's capture set was reconciled %d time(s) for requests that named no workspace", grid.syncCalls)
	}
}

// TestStopGridPreviewsReleasesTheCaptureSet pins the end of the grid's life: a
// console that went away stops the plane capturing for it, and a second stop is
// not an error.
func TestStopGridPreviewsReleasesTheCaptureSet(t *testing.T) {
	grid := newFakeGrid()
	grid.released = 3
	handler := gridHandlerWith(t, grid, &fakeGridResolver{serials: map[string]string{}})

	request := connectrpc.NewRequest(&driftv1.StopGridPreviewsRequest{
		Context:   &driftv1.RequestContext{RequestId: gridRequestID, ActorId: "operator-1"},
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: gridWorkspace},
	})
	response, err := handler.StopGridPreviews(context.Background(), request)
	if err != nil {
		t.Fatalf("StopGridPreviews: %v", err)
	}
	if response.Msg.GetReleased() != 3 {
		t.Fatalf("released = %d, want 3", response.Msg.GetReleased())
	}
	again, err := handler.StopGridPreviews(context.Background(), request)
	if err != nil {
		t.Fatalf("a second StopGridPreviews: %v", err)
	}
	if again.Msg.GetReleased() != 0 {
		t.Fatalf("a second stop released %d device(s), want 0", again.Msg.GetReleased())
	}
	if grid.stopCalls != 2 {
		t.Fatalf("the plane was told to stop %d time(s), want one per request", grid.stopCalls)
	}
}

// TestGridPreviewHandlerIsNotConstructedWithoutItsDependencies pins the mount rule:
// a surface with nothing to call is never built, so a deployment without a capture
// set exposes no grid at all rather than a grid of dead tiles.
func TestGridPreviewHandlerIsNotConstructedWithoutItsDependencies(t *testing.T) {
	grid := newFakeGrid()
	resolver := &fakeGridResolver{serials: map[string]string{}}
	if handler := transportconnect.NewGridPreviewHandler(nil, resolver); handler != nil {
		t.Fatal("a handler was built with no capture set")
	}
	if handler := transportconnect.NewGridPreviewHandler(grid, nil); handler != nil {
		t.Fatal("a handler was built with no device-to-serial resolver")
	}
	var nilGrid *fakeGrid
	if handler := transportconnect.NewGridPreviewHandler(nilGrid, resolver); handler != nil {
		t.Fatal("a handler was built over a typed-nil capture set")
	}
}

// TestGridPreviewHandlerWithNoCaptureSetAnswersUnavailable pins what an
// unmounted-but-invoked surface answers: not a still, and not a zero-value profile
// a console could read as a bound.
func TestGridPreviewHandlerWithNoCaptureSetAnswersUnavailable(t *testing.T) {
	handler := &transportconnect.GridPreviewHandler{}
	if _, err := handler.SyncGridPreviews(context.Background(), syncRequest("device-1")); connectrpc.CodeOf(err) != connectrpc.CodeUnavailable {
		t.Fatalf("an unconstructed surface answered with %v, want unavailable", err)
	}
	if _, err := handler.StopGridPreviews(context.Background(), connectrpc.NewRequest(&driftv1.StopGridPreviewsRequest{
		Context:   &driftv1.RequestContext{RequestId: gridRequestID},
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: gridWorkspace},
	})); connectrpc.CodeOf(err) != connectrpc.CodeUnavailable {
		t.Fatalf("an unconstructed surface answered a stop with %v, want unavailable", err)
	}
}
