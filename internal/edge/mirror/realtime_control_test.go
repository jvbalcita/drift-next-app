package mirror

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

type controlTestPeer struct {
	stats   media.StreamStats
	claim   media.MirrorViewingClaim
	handler media.MirrorControlHandler
	done    chan struct{}
	once    sync.Once
}

func (p *controlTestPeer) Stats() media.StreamStats { return p.stats }
func (p *controlTestPeer) ViewingClaimMatches(claim media.MirrorViewingClaim) bool {
	return p.claim == claim
}
func (p *controlTestPeer) BindControl(_ uint64, handler media.MirrorControlHandler) error {
	p.handler = handler
	p.done = make(chan struct{})
	return nil
}
func (p *controlTestPeer) ControlDone() <-chan struct{} { return p.done }
func (p *controlTestPeer) RevokeControl()               { p.once.Do(func() { close(p.done) }) }

type controlTestKernel struct {
	mu          sync.Mutex
	validations int
	revoked     bool
	authorized  []action.Intent
	dispatched  int
	finished    chan action.Completion
}

func (k *controlTestKernel) ValidateRealtimeControl(context.Context, string, string, string, string, string, uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.validations++
	if k.revoked {
		return errors.New("lease revoked")
	}
	return nil
}
func (k *controlTestKernel) Authorize(_ context.Context, intent action.Intent, _, _ string) (action.Result, error) {
	if err := intent.Validate(); err != nil {
		return action.Result{}, err
	}
	k.authorized = append(k.authorized, intent)
	return action.Result{}, nil
}
func (k *controlTestKernel) Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error) {
	k.dispatched++
	return action.Result{}, nil
}
func (k *controlTestKernel) MarkIndeterminate(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	k.finished <- completion
	return action.Result{}, nil
}

type controlTestSessions struct{ inputs []media.MirrorInput }

func (*controlTestSessions) Session(string) (media.MirrorSession, bool) { return nil, false }
func (s *controlTestSessions) Input(_ context.Context, _ string, input media.MirrorInput) error {
	s.inputs = append(s.inputs, input)
	return nil
}

type controlTestEvidence struct{ records []store.ActionEvidence }

func (e *controlTestEvidence) Append(_ context.Context, record store.ActionEvidence, _, _ string) error {
	e.records = append(e.records, record)
	return nil
}

func controlTestBinding() media.MirrorControlBinding {
	return media.MirrorControlBinding{WorkspaceID: "workspace", DeviceID: "device-1", SessionID: "session-1", LeaseID: "lease-1", HolderID: "operator-1", FencingToken: 7, ActorType: "operator", ActorID: "operator-1"}
}

func controlTestMessage(kind media.MirrorControlEventKind, generation, sequence uint64) media.MirrorControlMessage {
	return media.MirrorControlMessage{Kind: kind, Final: kind == media.MirrorControlTouchUp || kind == media.MirrorControlTouchCancel, Sequence: sequence, GestureID: 19, TimestampMS: 1, Generation: generation, Width: 1080, Height: 1920, X: 200, Y: 300}
}

func controlTestHarness(t *testing.T) (*RealtimeControl, *controlTestPeer, *controlTestKernel, *controlTestSessions, *controlTestEvidence, uint64) {
	t.Helper()
	binding := controlTestBinding()
	peer := &controlTestPeer{stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920}, claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID}}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{}
	evidence := &controlTestEvidence{}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), evidence)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil || generation == 0 || peer.handler == nil {
		t.Fatalf("bind control = %d, %v; handler present = %t", generation, err, peer.handler != nil)
	}
	t.Cleanup(peer.RevokeControl)
	return control, peer, kernel, sessions, evidence, generation
}

func TestLiveControlRevokesIdleChannelAfterLeaseLoss(t *testing.T) {
	_, peer, kernel, _, _, _ := controlTestHarness(t)
	kernel.mu.Lock()
	kernel.revoked = true
	kernel.mu.Unlock()
	select {
	case <-peer.ControlDone():
	case <-time.After(time.Second):
		t.Fatal("idle control remained armed after lease revocation")
	}
}

func TestLiveControlRefusesAViewingWhoseOperatorDoesNotHoldTheLease(t *testing.T) {
	binding := controlTestBinding()
	binding.HolderID = "another-operator"
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 1)}
	control, err := NewRealtimeControl(kernel, &controlTestSessions{}, ids.NewSequence("attempt-1"), &controlTestEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Bind(context.Background(), peer, binding); err == nil {
		peer.RevokeControl()
		t.Fatal("another operator's lease armed realtime input")
	}
	if peer.handler != nil || kernel.validations != 0 {
		t.Fatalf("unauthorized binding reached handler=%t validations=%d", peer.handler != nil, kernel.validations)
	}
}

func TestLiveGestureUsesOneKernelAttemptAndThreePhysicalPhases(t *testing.T) {
	_, peer, kernel, sessions, evidence, generation := controlTestHarness(t)
	for index, kind := range []media.MirrorControlEventKind{media.MirrorControlTouchDown, media.MirrorControlTouchMove, media.MirrorControlTouchUp} {
		if err := peer.handler(context.Background(), controlTestMessage(kind, generation, uint64(index+1))); err != nil {
			t.Fatalf("deliver phase %d: %v", index, err)
		}
	}
	if len(kernel.authorized) != 1 || kernel.dispatched != 1 || len(sessions.inputs) != 3 {
		t.Fatalf("authorized = %d, dispatched = %d, delivered = %d; want 1, 1, 3", len(kernel.authorized), kernel.dispatched, len(sessions.inputs))
	}
	if kernel.authorized[0].Kind != action.LiveGesture || kernel.validations != 4 {
		t.Fatalf("kind = %q, validations = %d; want live gesture and binding plus each event", kernel.authorized[0].Kind, kernel.validations)
	}
	if len(evidence.records) != 1 || evidence.records[0].AttemptID != "attempt-1" || evidence.records[0].Outcome != action.OutcomeIndeterminate {
		t.Fatalf("gesture evidence = %#v, want one indeterminate record", evidence.records)
	}
	select {
	case completion := <-kernel.finished:
		if completion.AttemptID != "attempt-1" {
			t.Fatalf("completed attempt = %q", completion.AttemptID)
		}
	default:
		t.Fatal("terminal gesture left no durable completion")
	}
}

func TestRevokedLeaseClosesGestureWithoutAnotherPhysicalEvent(t *testing.T) {
	_, peer, kernel, sessions, _, generation := controlTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := peer.handler(ctx, controlTestMessage(media.MirrorControlTouchDown, generation, 1)); err != nil {
		t.Fatal(err)
	}
	kernel.mu.Lock()
	kernel.revoked = true
	kernel.mu.Unlock()
	if err := peer.handler(ctx, controlTestMessage(media.MirrorControlTouchMove, generation, 2)); err == nil {
		t.Fatal("revoked lease accepted a touch move")
	}
	cancel()
	select {
	case <-kernel.finished:
	case <-time.After(time.Second):
		t.Fatal("closed channel left the dispatched gesture unaudited")
	}
	if len(sessions.inputs) != 2 || sessions.inputs[1].Kind != media.MirrorInputTouchCancel {
		t.Fatalf("physical inputs = %#v, want touch-down then one safety release", sessions.inputs)
	}
}

func TestClosingTheControlChannelReleasesAHeldTouch(t *testing.T) {
	_, peer, kernel, sessions, _, generation := controlTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := peer.handler(ctx, controlTestMessage(media.MirrorControlTouchDown, generation, 1)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-kernel.finished:
	case <-time.After(time.Second):
		t.Fatal("closed channel left the held touch without a durable outcome")
	}
	if len(sessions.inputs) != 2 || sessions.inputs[1].Kind != media.MirrorInputTouchCancel {
		t.Fatalf("physical inputs = %#v, want touch-down then one safety release", sessions.inputs)
	}
}

func TestSilentOpenControlChannelReleasesHeldTouchAtGestureLimit(t *testing.T) {
	control, peer, kernel, sessions, _, generation := controlTestHarness(t)
	control.gestureLimit = 20 * time.Millisecond
	if err := peer.handler(context.Background(), controlTestMessage(media.MirrorControlTouchDown, generation, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-kernel.finished:
	case <-time.After(time.Second):
		t.Fatal("silent open channel kept its touch held beyond the gesture limit")
	}
	select {
	case <-peer.ControlDone():
	default:
		t.Fatal("expired gesture left the control channel armed")
	}
	if len(sessions.inputs) != 2 || sessions.inputs[1].Kind != media.MirrorInputTouchCancel {
		t.Fatalf("physical inputs = %#v, want touch-down and timed safety release", sessions.inputs)
	}
}

func TestSafetyReleaseUsesTheLastDeliveredMove(t *testing.T) {
	_, peer, kernel, sessions, _, generation := controlTestHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := peer.handler(ctx, controlTestMessage(media.MirrorControlTouchDown, generation, 1)); err != nil {
		t.Fatal(err)
	}
	move := controlTestMessage(media.MirrorControlTouchMove, generation, 2)
	move.X, move.Y = 360, 720
	if err := peer.handler(ctx, move); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-kernel.finished:
	case <-time.After(time.Second):
		t.Fatal("held touch was not finalized")
	}
	if len(sessions.inputs) != 3 || sessions.inputs[2].Kind != media.MirrorInputTouchCancel || sessions.inputs[2].X != 360 || sessions.inputs[2].Y != 720 {
		t.Fatalf("physical inputs = %#v, want one release at the last delivered move", sessions.inputs)
	}
}

func TestRotationRefusesStaleCoordinatesBeforeDelivery(t *testing.T) {
	_, peer, _, sessions, _, generation := controlTestHarness(t)
	peer.stats.RenderWidth, peer.stats.RenderHeight = 1920, 1080
	if err := peer.handler(context.Background(), controlTestMessage(media.MirrorControlTouchDown, generation, 1)); err == nil {
		t.Fatal("rotated stream accepted coordinates measured in the old frame")
	}
	if len(sessions.inputs) != 0 {
		t.Fatalf("rotated stream delivered %d physical inputs", len(sessions.inputs))
	}
}

func TestLiveGestureDurationIsBoundedWithoutTrustingClientTimestamps(t *testing.T) {
	handler := &liveGestureHandler{control: &RealtimeControl{gestureLimit: liveGestureMaxDuration}, active: &liveGestureAttempt{started: time.Now().Add(-liveGestureMaxDuration - time.Second)}}
	if err := handler.handle(context.Background(), controlTestMessage(media.MirrorControlTouchMove, 1, 2)); err == nil {
		t.Fatal("gesture beyond its authorized duration accepted another move")
	}
}
