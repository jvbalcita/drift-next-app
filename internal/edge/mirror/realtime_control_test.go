package mirror

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

type controlTestPeer struct {
	statsMu       sync.RWMutex
	stats         media.StreamStats
	statsRead     chan struct{}
	statsReadOnce sync.Once
	claim         media.MirrorViewingClaim
	handler       media.MirrorControlHandler
	done          chan struct{}
	once          sync.Once
	reportsMu     sync.Mutex
	reports       [][]byte
}

func (p *controlTestPeer) Stats() media.StreamStats {
	p.statsMu.RLock()
	stats := p.stats
	p.statsMu.RUnlock()
	if p.statsRead != nil {
		p.statsReadOnce.Do(func() { close(p.statsRead) })
	}
	return stats
}
func (p *controlTestPeer) setStats(stats media.StreamStats) {
	p.statsMu.Lock()
	p.stats = stats
	p.statsMu.Unlock()
}
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
func (p *controlTestPeer) SendControlReport(data []byte) error {
	p.reportsMu.Lock()
	p.reports = append(p.reports, append([]byte(nil), data...))
	p.reportsMu.Unlock()
	return nil
}

type controlTestFanout struct {
	started chan execution.FollowerFanoutRequest
	release chan struct{}
	report  execution.FollowerFanoutReport
}

func (f *controlTestFanout) FanOut(ctx context.Context, request execution.FollowerFanoutRequest) (execution.FollowerFanoutReport, error) {
	f.started <- request
	select {
	case <-ctx.Done():
		return execution.FollowerFanoutReport{}, ctx.Err()
	case <-f.release:
		return f.report, nil
	}
}

type controlTestKernel struct {
	mu               sync.Mutex
	validations      int
	revoked          bool
	revokeOnDispatch bool
	authorized       []action.Intent
	dispatched       int
	finished         chan action.Completion
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
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dispatched++
	if k.revokeOnDispatch {
		k.revoked = true
	}
	return action.Result{}, nil
}
func (k *controlTestKernel) MarkIndeterminate(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	k.finished <- completion
	return action.Result{}, nil
}

type controlTestSessions struct {
	inputs  []media.MirrorInput
	devices []string
	live    map[string]bool
	refuse  map[string]error
}

func (s *controlTestSessions) Session(deviceID string) (media.MirrorSession, bool) {
	if s.live[deviceID] {
		return nil, true
	}
	return nil, false
}
func (s *controlTestSessions) Input(_ context.Context, deviceID string, input media.MirrorInput) error {
	s.inputs = append(s.inputs, input)
	s.devices = append(s.devices, deviceID)
	if err := s.refuse[deviceID]; err != nil {
		return err
	}
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

func TestLiveControlWaitsForTheSelectedStreamRenderFrameBeforeBinding(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats:     media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID},
		statsRead: make(chan struct{}),
		claim:     media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 1)}
	control, err := NewRealtimeControl(kernel, &controlTestSessions{}, ids.NewSequence("attempt-1"), &controlTestEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	type bindResult struct {
		generation uint64
		err        error
	}
	result := make(chan bindResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	go func() {
		generation, err := control.Bind(ctx, peer, binding)
		result <- bindResult{generation: generation, err: err}
	}()

	select {
	case <-peer.statsRead:
	case <-time.After(time.Second):
		t.Fatal("control binding did not inspect the selected stream")
	}
	peer.setStats(media.StreamStats{
		StreamKey: "stream-1", DeviceID: binding.DeviceID, RenderWidth: 1080, RenderHeight: 2280,
	})
	select {
	case bound := <-result:
		if bound.err != nil || bound.generation == 0 || peer.handler == nil {
			t.Fatalf("bind after the selected frame became ready = %d, %v; handler present = %t", bound.generation, bound.err, peer.handler != nil)
		}
		peer.RevokeControl()
	case <-time.After(time.Second):
		t.Fatal("control binding did not finish after the selected frame became ready")
	}
}

func TestLiveControlDoesNotBindBeforeTheSelectedStreamFrameIsReady(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 1)}
	control, err := NewRealtimeControl(kernel, &controlTestSessions{}, ids.NewSequence("attempt-1"), &controlTestEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	generation, err := control.Bind(ctx, peer, binding)
	if !errors.Is(err, context.DeadlineExceeded) || generation != 0 {
		t.Fatalf("bind without an active render frame = %d, %v; want deadline exceeded and no generation", generation, err)
	}
	if peer.handler != nil {
		t.Fatal("control was armed before the selected stream published its render frame")
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

func TestLiveKeyUsesOneKernelAttemptAndThePersistentMirrorSession(t *testing.T) {
	_, peer, kernel, sessions, evidence, generation := controlTestHarness(t)
	message := media.MirrorControlMessage{
		Kind: media.MirrorControlKey, Final: true, Sequence: 1, TimestampMS: 1,
		Generation: generation, KeyCode: 3, Repeat: 1,
	}
	if err := peer.handler(context.Background(), message); err != nil {
		t.Fatalf("deliver the live key: %v", err)
	}
	if len(kernel.authorized) != 1 || kernel.dispatched != 1 || kernel.validations != 3 || len(sessions.inputs) != 1 {
		t.Fatalf("authorized=%d dispatched=%d validations=%d physical inputs=%d; want 1, 1, 3, 1", len(kernel.authorized), kernel.dispatched, kernel.validations, len(sessions.inputs))
	}
	intent := kernel.authorized[0]
	if intent.Kind != action.KeyEvent || intent.InvocationSurface != action.SurfaceMirror || intent.KeyCode != 3 || intent.ObservationToken != "stream-1" || len(intent.Capabilities) != 1 || intent.Capabilities[0] != action.CapabilitySystemInput {
		t.Fatalf("key action was not bound to the selected stream and system-input capability: %#v", intent)
	}
	if got := sessions.inputs[0]; got.Kind != media.MirrorInputKeyEvent || got.KeyCode != 3 || got.Repeat != 1 {
		t.Fatalf("physical session input = %#v; want one key event", got)
	}
	if len(evidence.records) != 1 || evidence.records[0].AttemptID != intent.ID || evidence.records[0].Kind != action.KeyEvent || evidence.records[0].Outcome != action.OutcomeIndeterminate {
		t.Fatalf("key evidence = %#v; want one indeterminate key-event record", evidence.records)
	}
	select {
	case completion := <-kernel.finished:
		if completion.AttemptID != intent.ID {
			t.Fatalf("completed attempt = %q; want %q", completion.AttemptID, intent.ID)
		}
	default:
		t.Fatal("live key left no durable completion")
	}
}

func TestRealtimeFollowerFanoutRunsAfterAndIndependentlyOfSourceInput(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{}
	evidence := &controlTestEvidence{}
	fanout := &controlTestFanout{
		started: make(chan execution.FollowerFanoutRequest, 1),
		release: make(chan struct{}),
		report: execution.FollowerFanoutReport{
			RunID: "run-1", SourceDeviceID: binding.DeviceID, TargetCount: 1,
			Followers: []execution.FollowerInputOutcome{{
				DeviceID: "device-2", Disposition: execution.FollowerInputAccepted,
				Reason: execution.FollowerReasonDelivered, Detail: "accepted", Frame: execution.RenderSpace{Width: 1080, Height: 1920},
			}},
		},
	}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), evidence, WithRealtimeFollowerFanout(fanout))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.RevokeControl)

	selection := media.MirrorControlMessage{
		Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlSetFollowers,
		Sequence: 1, TimestampMS: 1, Generation: generation, FollowerDeviceIDs: []string{"device-2"},
	}
	if err := peer.handler(context.Background(), selection); err != nil {
		t.Fatalf("set the selected follower: %v", err)
	}
	key := media.MirrorControlMessage{
		Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlKey, Final: true,
		Sequence: 2, TimestampMS: 2, Generation: generation, KeyCode: 3, Repeat: 1,
	}
	keyDelivered := make(chan error, 1)
	go func() { keyDelivered <- peer.handler(context.Background(), key) }()
	select {
	case err := <-keyDelivered:
		if err != nil {
			t.Fatalf("source key waited for or failed with follower fan-out: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("source key waited for the blocked follower fan-out")
	}

	select {
	case request := <-fanout.started:
		if request.SourceDeviceID != binding.DeviceID || len(request.FollowerDeviceIDs) != 1 || request.FollowerDeviceIDs[0] != "device-2" {
			t.Fatalf("follower request source or selection = %#v", request)
		}
		if request.Payload.KeyEvent == nil || request.Payload.KeyEvent.KeyCode != 3 || request.Payload.KeyEvent.Repeat != 1 {
			t.Fatalf("follower request did not carry the source key event: %#v", request.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("the accepted source key never entered the follower worker")
	}
	if len(sessions.inputs) != 1 || sessions.inputs[0].Kind != media.MirrorInputKeyEvent || len(evidence.records) != 1 {
		t.Fatalf("source input=%#v evidence=%#v; want source key and its evidence before followers finish", sessions.inputs, evidence.records)
	}

	close(fanout.release)
	deadline := time.Now().Add(time.Second)
	for {
		peer.reportsMu.Lock()
		reportCount := len(peer.reports)
		peer.reportsMu.Unlock()
		if reportCount != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the completed follower acceptance report was not sent to the operator")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRealtimeSwipeFollowerKeepsTheSelectedSourceFrameAndDuration(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{}
	fanout := &controlTestFanout{started: make(chan execution.FollowerFanoutRequest, 1), release: make(chan struct{})}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), &controlTestEvidence{}, WithRealtimeFollowerFanout(fanout))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.RevokeControl)

	messages := []media.MirrorControlMessage{
		{
			Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlSetFollowers,
			Sequence: 1, TimestampMS: 1, Generation: generation, FollowerDeviceIDs: []string{"device-2"},
		},
		{
			Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchDown,
			Sequence: 2, GestureID: 7, TimestampMS: 2, Generation: generation,
			Width: 1080, Height: 1920, X: 100, Y: 200,
		},
		{
			Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchMove,
			Sequence: 3, GestureID: 7, TimestampMS: 80, Generation: generation,
			Width: 1080, Height: 1920, X: 300, Y: 400,
		},
		{
			Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchUp, Final: true,
			Sequence: 4, GestureID: 7, TimestampMS: 162, Generation: generation,
			Width: 1080, Height: 1920, X: 500, Y: 600,
			GestureKind: media.MirrorControlGestureSwipe, DurationMS: 160,
		},
	}
	for _, message := range messages {
		if err := peer.handler(context.Background(), message); err != nil {
			t.Fatalf("deliver source gesture event %d: %v", message.Sequence, err)
		}
	}
	select {
	case request := <-fanout.started:
		swipe := request.Payload.Swipe
		if swipe == nil || swipe.Start != (execution.Point{X: 100, Y: 200}) || swipe.End != (execution.Point{X: 500, Y: 600}) || swipe.DurationMS != 160 {
			t.Fatalf("follower swipe payload = %#v", request.Payload)
		}
		if request.Payload.Swipe.Space != (execution.RenderSpace{Width: 1080, Height: 1920, ObservationToken: "stream-1"}) {
			t.Fatalf("follower swipe source frame = %#v", request.Payload.Swipe.Space)
		}
	case <-time.After(time.Second):
		t.Fatal("the source swipe never entered the follower worker")
	}
	if len(sessions.inputs) != 3 {
		t.Fatalf("source physical gesture phases = %d; want down, move and up", len(sessions.inputs))
	}
	close(fanout.release)
}

func TestFollowerLiveSessionReceivesPhysicalTouchAndNotASecondSwipe(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{live: map[string]bool{"device-2": true}}
	fanout := &controlTestFanout{started: make(chan execution.FollowerFanoutRequest, 1), release: make(chan struct{})}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), &controlTestEvidence{}, WithRealtimeFollowerFanout(fanout))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.RevokeControl)
	messages := []media.MirrorControlMessage{
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlSetFollowers, Sequence: 1, TimestampMS: 1, Generation: generation, FollowerDeviceIDs: []string{"device-2"}},
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchDown, Sequence: 2, GestureID: 7, TimestampMS: 2, Generation: generation, Width: 1080, Height: 1920, X: 100, Y: 200},
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchMove, Sequence: 3, GestureID: 7, TimestampMS: 80, Generation: generation, Width: 1080, Height: 1920, X: 300, Y: 400},
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchUp, Final: true, Sequence: 4, GestureID: 7, TimestampMS: 162, Generation: generation, Width: 1080, Height: 1920, X: 500, Y: 600, GestureKind: media.MirrorControlGestureSwipe, DurationMS: 160},
	}
	for _, message := range messages {
		if err := peer.handler(context.Background(), message); err != nil {
			t.Fatalf("deliver source gesture event %d: %v", message.Sequence, err)
		}
	}
	select {
	case request := <-fanout.started:
		t.Fatalf("a follower that accepted the physical gesture also received a swipe: %#v", request)
	case <-time.After(200 * time.Millisecond):
	}
	var follower []media.MirrorInput
	for index, deviceID := range sessions.devices {
		if deviceID == "device-2" {
			follower = append(follower, sessions.inputs[index])
		}
	}
	if len(follower) != 3 || follower[0].Kind != media.MirrorInputTouchDown || follower[1].Kind != media.MirrorInputTouchMove || follower[2].Kind != media.MirrorInputTouchUp {
		t.Fatalf("follower physical inputs = %#v, want down, move, up", follower)
	}
	if follower[0].X != 100 || follower[2].X != 500 || follower[0].FrameWidth != 1080 || follower[0].FrameHeight != 1920 {
		t.Fatalf("follower coordinates = %#v, want the source frame unchanged", follower)
	}
}

func TestFollowerThatRefusesTheSourceFrameStaysOnTheReleaseSwipe(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{live: map[string]bool{"device-2": true}, refuse: map[string]error{"device-2": errors.New("frame refused")}}
	fanout := &controlTestFanout{started: make(chan execution.FollowerFanoutRequest, 1), release: make(chan struct{})}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), &controlTestEvidence{}, WithRealtimeFollowerFanout(fanout))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.RevokeControl)
	messages := []media.MirrorControlMessage{
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlSetFollowers, Sequence: 1, TimestampMS: 1, Generation: generation, FollowerDeviceIDs: []string{"device-2"}},
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchDown, Sequence: 2, GestureID: 7, TimestampMS: 2, Generation: generation, Width: 1080, Height: 1920, X: 100, Y: 200},
		{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchUp, Final: true, Sequence: 3, GestureID: 7, TimestampMS: 40, Generation: generation, Width: 1080, Height: 1920, X: 400, Y: 500, GestureKind: media.MirrorControlGestureSwipe, DurationMS: 40},
	}
	for _, message := range messages {
		if err := peer.handler(context.Background(), message); err != nil {
			t.Fatalf("deliver source gesture event %d: %v", message.Sequence, err)
		}
	}
	select {
	case request := <-fanout.started:
		if len(request.FollowerDeviceIDs) != 1 || request.FollowerDeviceIDs[0] != "device-2" {
			t.Fatalf("release swipe targets = %#v, want the follower whose stream refused the frame", request.FollowerDeviceIDs)
		}
	case <-time.After(time.Second):
		t.Fatal("a follower that refused the source frame was not given the release swipe")
	}
	close(fanout.release)
}

func TestClosingTheSourceChannelCancelsAFollowerFinger(t *testing.T) {
	binding := controlTestBinding()
	peer := &controlTestPeer{
		stats: media.StreamStats{StreamKey: "stream-1", DeviceID: binding.DeviceID, Serial: "SERIAL-1", RenderWidth: 1080, RenderHeight: 1920},
		claim: media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID},
	}
	kernel := &controlTestKernel{finished: make(chan action.Completion, 2)}
	sessions := &controlTestSessions{live: map[string]bool{"device-2": true}}
	fanout := &controlTestFanout{started: make(chan execution.FollowerFanoutRequest, 1), release: make(chan struct{})}
	control, err := NewRealtimeControl(kernel, sessions, ids.NewSequence("attempt-1"), &controlTestEvidence{}, WithRealtimeFollowerFanout(fanout))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := control.Bind(context.Background(), peer, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.RevokeControl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := peer.handler(ctx, media.MirrorControlMessage{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlSetFollowers, Sequence: 1, TimestampMS: 1, Generation: generation, FollowerDeviceIDs: []string{"device-2"}}); err != nil {
		t.Fatal(err)
	}
	if err := peer.handler(ctx, media.MirrorControlMessage{Version: media.MirrorControlCurrentVersion, Kind: media.MirrorControlTouchDown, Sequence: 2, GestureID: 7, TimestampMS: 2, Generation: generation, Width: 1080, Height: 1920, X: 100, Y: 200}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-kernel.finished:
	case <-time.After(time.Second):
		t.Fatal("closed channel left the gesture unaudited")
	}
	var followerCancels int
	for index, deviceID := range sessions.devices {
		if deviceID == "device-2" && sessions.inputs[index].Kind == media.MirrorInputTouchCancel {
			followerCancels++
		}
	}
	if followerCancels != 1 {
		t.Fatalf("follower inputs = %v / %#v, want one touch-cancel", sessions.devices, sessions.inputs)
	}
}

func TestRevokedLeaseRefusesLiveKeyBeforeDispatch(t *testing.T) {
	_, peer, kernel, sessions, evidence, generation := controlTestHarness(t)
	kernel.mu.Lock()
	kernel.revoked = true
	kernel.mu.Unlock()
	message := media.MirrorControlMessage{Kind: media.MirrorControlKey, Final: true, Sequence: 1, TimestampMS: 1, Generation: generation, KeyCode: 3, Repeat: 1}
	if err := peer.handler(context.Background(), message); err == nil {
		t.Fatal("a live key was accepted after its lease was revoked")
	}
	if len(kernel.authorized) != 0 || kernel.dispatched != 0 || len(sessions.inputs) != 0 || len(evidence.records) != 0 {
		t.Fatalf("revoked key reached authorize=%d dispatch=%d physical=%d evidence=%d", len(kernel.authorized), kernel.dispatched, len(sessions.inputs), len(evidence.records))
	}
}

func TestLeaseRevokedAfterDispatchDoesNotReachThePhysicalKeyInput(t *testing.T) {
	_, peer, kernel, sessions, evidence, generation := controlTestHarness(t)
	kernel.revokeOnDispatch = true
	message := media.MirrorControlMessage{Kind: media.MirrorControlKey, Final: true, Sequence: 1, TimestampMS: 1, Generation: generation, KeyCode: 3, Repeat: 1}
	if err := peer.handler(context.Background(), message); err == nil {
		t.Fatal("a key was delivered after its lease was revoked between dispatch and the device session")
	}
	if len(kernel.authorized) != 1 || kernel.dispatched != 1 || len(sessions.inputs) != 0 {
		t.Fatalf("authorized=%d dispatched=%d physical=%d; want a recorded dispatch and no physical key", len(kernel.authorized), kernel.dispatched, len(sessions.inputs))
	}
	if len(evidence.records) != 1 || evidence.records[0].Outcome != action.OutcomeIndeterminate {
		t.Fatalf("revoked dispatched key evidence = %#v; want one conservative indeterminate record", evidence.records)
	}
}

func TestRotationAdoptsTheNewFrameBeforeAKey(t *testing.T) {
	_, peer, kernel, sessions, _, generation := controlTestHarness(t)
	stats := peer.Stats()
	stats.RenderWidth, stats.RenderHeight = 1920, 1080
	peer.setStats(stats)
	message := media.MirrorControlMessage{Kind: media.MirrorControlKey, Final: true, Sequence: 1, TimestampMS: 1, Generation: generation, KeyCode: 3, Repeat: 1}
	if err := peer.handler(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if len(kernel.authorized) != 1 || len(sessions.inputs) != 1 || sessions.inputs[0].Kind != media.MirrorInputKeyEvent {
		t.Fatalf("authorized=%d inputs=%#v, want the key delivered after the channel adopted the rotated frame", len(kernel.authorized), sessions.inputs)
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
	stats := peer.Stats()
	stats.RenderWidth, stats.RenderHeight = 1920, 1080
	peer.setStats(stats)
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
