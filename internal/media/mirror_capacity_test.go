package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The plane's capacity is spent against the PURPOSE a viewer is, and these tests
// are that rule from both sides: the console's grid may never hold the place the
// operator's own big frame needs, and the frame that cannot be carried is told why
// in the plane's own sentence.
//
// The defect they reproduce was measured on the owner's host: the console allocated
// four tiles (apps/console/src/lib/live-tiles.ts) and the engine's own bound was
// four (DefaultMirrorSessionCapacity), so the grid spent the whole plane and the
// fifth device - the frame the operator opened - met "media: 4 devices are already
// being mirrored, which is the configured bound" and never carried a picture.

// capacityDialerCount is how many devices a capacity case mirrors. It is larger
// than the console's grid because the case that failed had to reach a FIFTH device:
// a bound of four, four tiles spent, and the frame the operator opened on a device
// none of them was showing.
const capacityDialerCount = 8

// ambientGrid opens the console's full tile allocation, one ambient viewer per
// device, and returns the devices it spent.
//
// It is written as the console's own allocation is derived (see the capacity
// surface): the grid holds at most `capacity - reserve` sessions, so a grid that
// is drawing everything it is allowed to draw is exactly this set.
func ambientGrid(t *testing.T, engine *MirrorEngine) []string {
	t.Helper()
	spent := make([]string, 0, engine.AmbientCapacity())
	for i := 0; i < engine.AmbientCapacity(); i++ {
		deviceID := fmt.Sprintf("tile-%02d", i)
		if _, _, err := engine.StartViewer(context.Background(), deviceID, "SERIAL-"+deviceID, PurposeAmbient, MirrorPreview{}); err != nil {
			t.Fatalf("the grid could not open tile %d of its own allocation: %v", i, err)
		}
		spent = append(spent, deviceID)
	}
	return spent
}

// TestTheGridsFullAllocationStillLeavesTheOperatorsOwnFrameAStream is the card's
// own acceptance case, and the code before this change fails it.
//
// A console draws every tile its allocation allows, and the operator then opens a
// device none of those tiles is showing. The frame must get a stream: the operator
// works devices, and a grid that has spent the plane's whole capacity has made the
// control room unable to act on anything it draws.
func TestTheGridsFullAllocationStillLeavesTheOperatorsOwnFrameAStream(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{MaxSessions: 6, OperatorReserve: 1})
	spent := ambientGrid(t, engine)
	if len(spent) != 5 {
		t.Fatalf("the grid's own allocation is %d tile(s), want the capacity less the operator's place (5)", len(spent))
	}

	session, viewer, err := engine.StartViewer(context.Background(), "operator-device", "SERIAL-operator", PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("the operator's own frame was refused while the grid drew its full allocation: %v", err)
	}
	if session.DeviceID() != "operator-device" {
		t.Fatalf("the operator's frame opened %q, want the device it named", session.DeviceID())
	}
	if viewer == nil {
		t.Fatal("the operator's frame was given a session and no viewer on it")
	}
	sessionReady(t, session)

	// The place the frame took is the one that was kept for it: the grid is still
	// holding its own allocation and the plane is carrying one more session than
	// the grid alone would have.
	if sessions := engine.Sessions(); len(sessions) != 6 {
		t.Fatalf("the plane carries %d session(s), want the grid's 5 and the operator's frame", len(sessions))
	}
}

// TestTheDefaultDeploymentLeavesRoomForTheOperatorsFrame is the same case against
// a deployment that configured nothing, because the failure was measured there:
// the console's four tiles equalled the plane's four-session default, so the
// default itself has to keep a place.
func TestTheDefaultDeploymentLeavesRoomForTheOperatorsFrame(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{})
	if engine.Capacity() != DefaultMirrorSessionCapacity {
		t.Fatalf("the engine carries a capacity of %d with nothing stated, want %d", engine.Capacity(), DefaultMirrorSessionCapacity)
	}
	if engine.AmbientCapacity() >= DefaultMirrorSessionCapacity {
		t.Fatalf("the grid may hold %d of the plane's %d session(s): the operator's own frame is not kept a place",
			engine.AmbientCapacity(), engine.Capacity())
	}

	spent := ambientGrid(t, engine)
	if len(spent) != DefaultMirrorSessionCapacity-DefaultOperatorReserve {
		t.Fatalf("the grid holds %d tile(s), want %d", len(spent), DefaultMirrorSessionCapacity-DefaultOperatorReserve)
	}
	if _, _, err := engine.StartViewer(context.Background(), "operator-device", "SERIAL-operator", PurposeOperator, MirrorPreview{}); err != nil {
		t.Fatalf("the operator's own frame was refused on a plane carrying its default capacity: %v", err)
	}
}

// TestAGridTileIsRefusedOnceTheGridsShareIsSpent is the other half: the grid may
// not spend the operator's place, and the refusal says WHICH bound was reached -
// the grid's share, not the plane's capacity - so a reader is not sent looking for
// capacity that is in fact free.
func TestAGridTileIsRefusedOnceTheGridsShareIsSpent(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{MaxSessions: 3, OperatorReserve: 1})
	ambientGrid(t, engine)

	_, _, err := engine.StartViewer(context.Background(), "tile-overflow", "SERIAL-tile-overflow", PurposeAmbient, MirrorPreview{})
	var capacity *SessionCapacityError
	if !errors.As(err, &capacity) {
		t.Fatalf("a grid tile beyond the grid's share was refused with %v, want a capacity refusal", err)
	}
	if capacity.Purpose != PurposeAmbient {
		t.Fatalf("the refusal names the purpose %q, want the grid's own", capacity.Purpose)
	}
	if capacity.AmbientShare != 2 || capacity.Capacity != 3 {
		t.Fatalf("the refusal names a share of %d of %d, want 2 of 3", capacity.AmbientShare, capacity.Capacity)
	}
	if !strings.Contains(capacity.Error(), "the operator's own frame") {
		t.Fatalf("the grid's refusal does not say what the remaining place is kept for: %q", capacity.Error())
	}
}

// TestTheOperatorsFrameIsToldThePlanesOwnSentenceWhenThePlaneIsFull is the card's
// second acceptance case: when the capacity genuinely IS spent, the refusal the
// frame reads is the plane's own sentence, naming the bound it reached.
func TestTheOperatorsFrameIsToldThePlanesOwnSentenceWhenThePlaneIsFull(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{MaxSessions: 2, OperatorReserve: 1})
	for i := 0; i < engine.Capacity(); i++ {
		deviceID := fmt.Sprintf("worked-%d", i)
		if _, _, err := engine.StartViewer(context.Background(), deviceID, "SERIAL-"+deviceID, PurposeOperator, MirrorPreview{}); err != nil {
			t.Fatalf("the operator's frame %d could not open inside the capacity: %v", i, err)
		}
	}

	_, _, err := engine.StartViewer(context.Background(), "one-too-many", "SERIAL-one-too-many", PurposeOperator, MirrorPreview{})
	var capacity *SessionCapacityError
	if !errors.As(err, &capacity) {
		t.Fatalf("a plane holding its whole capacity refused with %v, want a capacity refusal", err)
	}
	sentence := err.Error()
	if !strings.Contains(sentence, "2 devices are already being mirrored") {
		t.Fatalf("the operator's refusal does not name the capacity that was spent: %q", sentence)
	}
	if !strings.Contains(sentence, "device session capacity") {
		t.Fatalf("the operator's refusal names a bound this plane does not carry: %q", sentence)
	}
	// The operator's own refusal is the capacity itself, not the grid's share: an
	// operator reading "the grid spent it" when the grid was not what filled the
	// plane would be told to close tiles that do not exist.
	if strings.Contains(sentence, "the console's grid") {
		t.Fatalf("the operator's refusal blames the grid for a plane that was simply full: %q", sentence)
	}
	if capacity.AmbientShare != capacity.Capacity {
		t.Fatalf("a full-plane refusal reports a share of %d of %d, want the whole capacity", capacity.AmbientShare, capacity.Capacity)
	}
}

// TestAFrameJoinsTheSessionATileAlreadyHolds: one device has one session, and a
// frame opened for a device the grid is already showing attaches to that session
// instead of being refused. A tile watching a device is not a reason the operator
// may not work it.
func TestAFrameJoinsTheSessionATileAlreadyHolds(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{MaxSessions: 2, OperatorReserve: 1})
	spent := ambientGrid(t, engine)
	if len(spent) != 1 {
		t.Fatalf("the grid holds %d tile(s), want 1 for this capacity", len(spent))
	}

	session, _, err := engine.StartViewer(context.Background(), spent[0], "SERIAL-"+spent[0], PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("a frame opened for a device the grid is showing was refused: %v", err)
	}
	if session.DeviceID() != spent[0] {
		t.Fatalf("the frame opened %q, want the tile's own device %q", session.DeviceID(), spent[0])
	}
	// The tile's own capture is the only dial there will ever be: the frame
	// attached to the session the grid was already carrying this device with, so
	// the device is captured once however many viewers watch it.
	dialer.streamFor(t, spent[0])
	if dials := dialer.dialed(); len(dials) != 1 {
		t.Fatalf("the frame opened a second capture of a watched device (%d dial(s), want 1)", len(dials))
	}
}

// TestAFrameThatJoinsATileFreesTheGridsShare: the place a session counts for is
// what an operator is watching NOW. A device the operator has open is one the
// operator's frame needs, so it is not the grid's place - and the grid may open a
// tile on another device with it.
func TestAFrameThatJoinsATileFreesTheGridsShare(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{MaxSessions: 2, OperatorReserve: 1})
	spent := ambientGrid(t, engine)

	// The grid's share is spent, so a second tile is refused...
	if _, _, err := engine.StartViewer(context.Background(), "tile-extra", "SERIAL-tile-extra", PurposeAmbient, MirrorPreview{}); err == nil {
		t.Fatal("the grid opened a tile past its share of the plane")
	}
	// ...until the operator opens the device that tile is showing, which makes the
	// session the operator's own and gives the grid its place back.
	if _, _, err := engine.StartViewer(context.Background(), spent[0], "SERIAL-"+spent[0], PurposeOperator, MirrorPreview{}); err != nil {
		t.Fatalf("the operator's frame was refused: %v", err)
	}
	if _, _, err := engine.StartViewer(context.Background(), "tile-extra", "SERIAL-tile-extra", PurposeAmbient, MirrorPreview{}); err != nil {
		t.Fatalf("the grid was still refused after the operator took a session over: %v", err)
	}
}

// TestAnOperatorsFrameReleasesThePlaceItHeldAgainstTheReserve: the reserve is not
// spent by a frame that is gone. What the grid may hold is read from the sessions
// an operator is watching NOW, so a frame that closes gives its place back and the
// session it was holding ends with it.
func TestAnOperatorsFrameReleasesThePlaceItHeldAgainstTheReserve(t *testing.T) {
	dialer := newFakeDialer()
	engine := newEngine(t, dialer, MirrorEngineConfig{MaxSessions: 2, OperatorReserve: 1, Idle: 20 * time.Millisecond})
	session, viewer, err := engine.StartViewer(context.Background(), "worked", "SERIAL-worked", PurposeOperator, MirrorPreview{})
	if err != nil {
		t.Fatalf("the operator's frame could not open: %v", err)
	}
	concrete, ok := session.(*mirrorSession)
	if !ok {
		t.Fatalf("the engine reported a session it did not build: %T", session)
	}
	if concrete.operatorViewers.Load() != 1 {
		t.Fatalf("the session counts %d operator viewer(s), want the frame that opened it", concrete.operatorViewers.Load())
	}

	viewer.Close()
	if concrete.operatorViewers.Load() != 0 {
		t.Fatalf("a closed frame still counts as %d operator viewer(s): the place it held against the reserve is not given back",
			concrete.operatorViewers.Load())
	}
	// The session has no viewer left at all, so it ends on its own idle bound and
	// the plane carries nothing - which is what makes the next frame a fresh place
	// rather than a session the grid is holding.
	waitFor(t, "the plane to stop carrying the operator's session", func() bool {
		return len(engine.Sessions()) == 0
	})
}

// TestAPurposeNobodyStatedIsTheOperatorsOwnFrame: the undeclared case is read as
// the demand the reserve is kept for, because the two mistakes are not equal. A
// caller read as the grid loses the operator a place; a caller read as the
// operator's frame can only spend a place that was held for it.
func TestAPurposeNobodyStatedIsTheOperatorsOwnFrame(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{MaxSessions: 1, OperatorReserve: 1})
	if _, _, err := engine.StartViewer(context.Background(), "unstated", "SERIAL-unstated", "", MirrorPreview{}); err != nil {
		t.Fatalf("a start that stated no purpose was refused a plane with a place free: %v", err)
	}
	sessions := engine.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("the plane carries %d session(s), want 1", len(sessions))
	}
	concrete := sessions[0].(*mirrorSession)
	if concrete.operatorViewers.Load() != 1 {
		t.Fatalf("a start that stated no purpose was counted as %d operator viewer(s), want the operator's own frame",
			concrete.operatorViewers.Load())
	}
}

// TestStartIsTheOperatorsOwnFrame pins the entry point the transport uses for a
// caller that has one purpose to express: Start is the operator's own frame, so the
// shape it has always had keeps the meaning it needs.
func TestStartIsTheOperatorsOwnFrame(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{MaxSessions: 4, OperatorReserve: 3})
	if _, _, err := engine.Start(context.Background(), "worked", "SERIAL-worked"); err != nil {
		t.Fatalf("Start was refused: %v", err)
	}
	// The grid's share is one session on this plane, so a single tile may open and
	// the plane's report of that share has to be the number the grid acts on.
	if engine.AmbientCapacity() != 1 {
		t.Fatalf("the grid may hold %d session(s), want 1", engine.AmbientCapacity())
	}
	if _, _, err := engine.StartViewer(context.Background(), "tile-1", "SERIAL-tile-1", PurposeAmbient, MirrorPreview{}); err != nil {
		t.Fatalf("the grid's one place could not be spent: %v", err)
	}
	if _, _, err := engine.StartViewer(context.Background(), "tile-2", "SERIAL-tile-2", PurposeAmbient, MirrorPreview{}); err == nil {
		t.Fatal("the grid spent a second place on a share of one")
	}
}
