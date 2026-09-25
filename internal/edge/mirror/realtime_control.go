package mirror

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

// RealtimeKernel is the narrow action-safety boundary used by live mirror input.
// Every gesture or key creates one policy decision and durable attempt; pointer
// moves only recheck the live authority and never write an attempt per frame.
type RealtimeKernel interface {
	ValidateRealtimeControl(context.Context, string, string, string, string, string, uint64) error
	Authorize(context.Context, action.Intent, string, string) (action.Result, error)
	Dispatch(context.Context, string, string, string, uint64, string, string) (action.Result, error)
	MarkIndeterminate(context.Context, action.Completion, string, string) (action.Result, error)
}

// RealtimeControl binds a selected Pion peer to the kernel and its live scrcpy
// session. It never grants authority merely because a peer can receive video.
type RealtimeControl struct {
	kernel        RealtimeKernel
	engine        LiveSessions
	ids           ids.IDGenerator
	evidence      RealtimeEvidenceRecorder
	checkInterval time.Duration
	gestureLimit  time.Duration
	followers     RealtimeFollowerFanout
}

const liveGestureMaxDuration = 30 * time.Second
const realtimeAuthorityCheckInterval = 250 * time.Millisecond
const realtimeFrameReadyTimeout = 3 * time.Second
const realtimeFrameReadyPollInterval = 10 * time.Millisecond
const realtimeFollowerFanoutQueueCapacity = 32
const realtimeFollowerFanoutAcceptTimeout = 5 * time.Second
const realtimeFollowerReportMaxBytes = 64 * 1024

type RealtimeEvidenceRecorder interface {
	Append(context.Context, store.ActionEvidence, string, string) error
}

type RealtimeFollowerFanout interface {
	FanOut(context.Context, execution.FollowerFanoutRequest) (execution.FollowerFanoutReport, error)
}

type RealtimeControlOption func(*RealtimeControl) error

func WithRealtimeFollowerFanout(fanout RealtimeFollowerFanout) RealtimeControlOption {
	return func(control *RealtimeControl) error {
		if fanout == nil {
			return platformerrors.New(platformerrors.CodeInvalidInput, "the realtime follower fan-out is required")
		}
		control.followers = fanout
		return nil
	}
}

func NewRealtimeControl(kernel RealtimeKernel, engine LiveSessions, generator ids.IDGenerator, evidence RealtimeEvidenceRecorder, options ...RealtimeControlOption) (*RealtimeControl, error) {
	if kernel == nil || engine == nil || generator == nil || evidence == nil {
		return nil, errors.New("mirror: realtime control requires a kernel, live sessions, IDs and evidence recorder")
	}
	control := &RealtimeControl{kernel: kernel, engine: engine, ids: generator, evidence: evidence, checkInterval: realtimeAuthorityCheckInterval, gestureLimit: liveGestureMaxDuration}
	for _, option := range options {
		if option == nil {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a realtime control option is required")
		}
		if err := option(control); err != nil {
			return nil, err
		}
	}
	return control, nil
}

// Bind verifies the exact opening claim, lease tuple and selected stream before
// arming its DataChannel. The generation is server-generated and unique to this
// binding; video-only peers never call this method.
func (c *RealtimeControl) Bind(ctx context.Context, peer media.MirrorControlPeer, binding media.MirrorControlBinding) (uint64, error) {
	if c == nil || peer == nil || ctx == nil {
		return 0, errors.New("mirror: realtime control requires a selected peer")
	}
	claim := media.MirrorViewingClaim{WorkspaceID: binding.WorkspaceID, ActorType: binding.ActorType, ActorID: binding.ActorID}
	if !peer.ViewingClaimMatches(claim) {
		return 0, errors.New("mirror: realtime control does not match the opening viewing")
	}
	// A stream opened by one operator must not borrow another holder's active
	// lease merely because its ID and fence were supplied in negotiation.
	if binding.HolderID == "" || binding.HolderID != binding.ActorID {
		return 0, platformerrors.New(platformerrors.CodeLeaseConflict, "live control requires the viewing operator's own lease")
	}
	stats, err := waitForActiveRenderFrame(ctx, peer, binding.DeviceID)
	if err != nil {
		return 0, err
	}
	if err := c.kernel.ValidateRealtimeControl(ctx, binding.WorkspaceID, binding.DeviceID, binding.SessionID, binding.LeaseID, binding.HolderID, binding.FencingToken); err != nil {
		return 0, err
	}
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return 0, fmt.Errorf("mirror: generate control stream generation: %w", err)
	}
	generation := binary.BigEndian.Uint64(entropy[:])
	if generation == 0 {
		generation = 1
	}
	handler := &liveGestureHandler{
		control: c, peer: peer, binding: binding, generation: generation,
		width: uint32(stats.RenderWidth), height: uint32(stats.RenderHeight), streamKey: stats.StreamKey,
		fanoutQueue: make(chan realtimeFollowerFanoutRequest, realtimeFollowerFanoutQueueCapacity),
	}
	if err := peer.BindControl(generation, handler.handle); err != nil {
		return 0, err
	}
	done := peer.ControlDone()
	if done == nil {
		peer.RevokeControl()
		return 0, errors.New("mirror: realtime control peer has no owned shutdown signal")
	}
	if c.followers != nil {
		go handler.runFollowerFanout(done)
	}
	go c.watchAuthority(peer, binding)
	return generation, nil
}

func waitForActiveRenderFrame(ctx context.Context, peer media.MirrorControlPeer, deviceID string) (media.StreamStats, error) {
	timeout := time.NewTimer(realtimeFrameReadyTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(realtimeFrameReadyPollInterval)
	defer ticker.Stop()
	for {
		stats := peer.Stats()
		if stats.DeviceID != "" && stats.DeviceID != deviceID {
			return media.StreamStats{}, errors.New("mirror: realtime control does not match the selected stream device")
		}
		if stats.DeviceID == deviceID && stats.RenderWidth > 0 && stats.RenderHeight > 0 && stats.StreamKey != "" {
			return stats, nil
		}
		select {
		case <-ctx.Done():
			return media.StreamStats{}, fmt.Errorf("mirror: waiting for the selected stream's active render frame: %w", ctx.Err())
		case <-timeout.C:
			return media.StreamStats{}, fmt.Errorf("mirror: selected stream did not publish an active render frame within %s", realtimeFrameReadyTimeout)
		case <-ticker.C:
		}
	}
}

// The opening request is not the lifetime of a viewing. Recheck the peer's
// authority while idle so an expired lease cannot leave input appearing armed.
func (c *RealtimeControl) watchAuthority(peer media.MirrorControlPeer, binding media.MirrorControlBinding) {
	done := peer.ControlDone()
	if done == nil {
		peer.RevokeControl()
		return
	}
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.checkInterval)
		err := c.kernel.ValidateRealtimeControl(ctx, binding.WorkspaceID, binding.DeviceID, binding.SessionID, binding.LeaseID, binding.HolderID, binding.FencingToken)
		cancel()
		if err != nil {
			peer.RevokeControl()
			return
		}
	}
}

type liveGestureHandler struct {
	control          *RealtimeControl
	peer             media.MirrorControlPeer
	binding          media.MirrorControlBinding
	generation       uint64
	width            uint32
	height           uint32
	streamKey        string
	active           *liveGestureAttempt
	followers        []string
	relayMu          sync.Mutex
	relayed          map[string]struct{}
	pressedFollowers map[string]struct{}
	fanoutQueue      chan realtimeFollowerFanoutRequest
}

type liveGestureAttempt struct {
	id        string
	gesture   uint64
	started   time.Time
	stop      func() bool
	timeout   *time.Timer
	finish    sync.Once
	finishErr error
	startX    uint32
	startY    uint32
	mu        sync.Mutex
	last      media.MirrorInput
	pressed   bool
	released  bool
}

func (h *liveGestureHandler) handle(ctx context.Context, message media.MirrorControlMessage) error {
	if err := message.Validate(); err != nil {
		return err
	}
	if h.active != nil && time.Since(h.active.started) > h.control.gestureLimit {
		return errors.New("mirror: live gesture exceeded its authorized duration")
	}
	stats := h.peer.Stats()
	touch := message.Kind != media.MirrorControlKey && message.Kind != media.MirrorControlSetFollowers
	// A mid-stream re-declaration changes the session's encode size. Adopt it
	// before the frame check so SetFollowers / a later gesture is not treated as
	// a stale generation and does not tear the control channel down.
	if stats.RenderWidth > 0 && stats.RenderHeight > 0 && (stats.RenderWidth != int(h.width) || stats.RenderHeight != int(h.height)) {
		h.width = uint32(stats.RenderWidth)
		h.height = uint32(stats.RenderHeight)
	}
	if message.Generation != h.generation || stats.StreamKey != h.streamKey || stats.DeviceID != h.binding.DeviceID || stats.RenderWidth != int(h.width) || stats.RenderHeight != int(h.height) ||
		(touch && (message.Width != h.width || message.Height != h.height)) {
		return errors.New("mirror: live control frame or generation changed")
	}
	if err := h.control.kernel.ValidateRealtimeControl(ctx, h.binding.WorkspaceID, h.binding.DeviceID, h.binding.SessionID, h.binding.LeaseID, h.binding.HolderID, h.binding.FencingToken); err != nil {
		return err
	}
	if message.Kind == media.MirrorControlSetFollowers {
		if h.active != nil {
			return errors.New("mirror: follower selection cannot change during a live gesture")
		}
		if h.control.followers == nil {
			return errors.New("mirror: realtime follower fan-out is not configured")
		}
		h.followers = append(h.followers[:0], message.FollowerDeviceIDs...)
		return nil
	}
	input, err := message.MirrorInput()
	if err != nil {
		return err
	}
	if input.Kind == media.MirrorInputKeyEvent {
		return h.dispatchLiveKey(ctx, message, input)
	}
	if message.Kind == media.MirrorControlTouchDown {
		if h.active != nil {
			return errors.New("mirror: another live gesture is active")
		}
		h.relayMu.Lock()
		h.relayed = map[string]struct{}{}
		h.pressedFollowers = map[string]struct{}{}
		h.relayMu.Unlock()
		id, err := h.control.ids.NewID()
		if err != nil {
			return err
		}
		intent := action.Intent{
			ID: id, Workspace: h.binding.WorkspaceID, DeviceID: h.binding.DeviceID,
			LeaseID: h.binding.LeaseID, HolderID: h.binding.HolderID, FencingToken: h.binding.FencingToken,
			Kind: action.LiveGesture, InvocationSurface: action.SurfaceMirror,
			LiveStart:    &action.LiveGestureStart{X: int(message.X), Y: int(message.Y), Width: int(message.Width), Height: int(message.Height)},
			Capabilities: []action.Capability{action.CapabilityGesture}, Timeout: h.control.gestureLimit,
			IdempotencyKey: "live:" + h.streamKey + ":" + strconv.FormatUint(h.generation, 10) + ":" + strconv.FormatUint(message.GestureID, 10),
		}
		accepted, err := h.control.kernel.Authorize(ctx, intent, h.binding.ActorType, h.binding.ActorID)
		if err != nil {
			return err
		}
		if accepted.IdempotentReplay {
			return errors.New("mirror: a live gesture cannot be replayed")
		}
		if _, err := h.control.kernel.Dispatch(ctx, intent.Workspace, id, intent.HolderID, intent.FencingToken, h.binding.ActorType, h.binding.ActorID); err != nil {
			return err
		}
		attempt := &liveGestureAttempt{id: id, gesture: message.GestureID, started: time.Now(), startX: message.X, startY: message.Y}
		h.active = attempt
		// An open but silent channel must not hold a finger down indefinitely.
		attempt.timeout = time.AfterFunc(h.control.gestureLimit, func() {
			h.peer.RevokeControl()
			if err := h.releaseAttempt(attempt); err != nil {
				log.Printf("live mirror timed safety release for %s failed: %v", h.binding.DeviceID, err)
			}
			_ = h.finishAttempt(attempt)
		})
		attempt.stop = context.AfterFunc(ctx, func() {
			attempt.timeout.Stop()
			if err := h.releaseAttempt(attempt); err != nil {
				log.Printf("live mirror safety release for %s failed: %v", h.binding.DeviceID, err)
			}
			_ = h.finishAttempt(attempt)
		})
	} else if h.active == nil || h.active.gesture != message.GestureID {
		return errors.New("mirror: touch event does not belong to an authorized gesture")
	}
	attempt := h.active
	if err := h.sendPhysical(ctx, attempt, input); err != nil {
		h.active = nil
		if attempt.stop != nil {
			attempt.stop()
		}
		if attempt.timeout != nil {
			attempt.timeout.Stop()
		}
		return errors.Join(err, h.releaseAttempt(attempt), h.finishAttempt(attempt))
	}
	// Followers that already have a mirror get the same down/move/up now.
	// A miss there must not close the source channel.
	h.relayPhysical(ctx, input)
	if message.Final {
		attempt := h.active
		h.active = nil
		if attempt.stop != nil {
			attempt.stop()
		}
		if attempt.timeout != nil {
			attempt.timeout.Stop()
		}
		if err := h.finishAttempt(attempt); err != nil {
			return err
		}
		if message.Kind == media.MirrorControlTouchUp && len(h.followersPendingSwipe()) > 0 {
			// A follower that accepted the physical down/move/up must not also get
			// the reconstructed swipe. A phone with no session, or one whose stream
			// refused the source frame, stays on release.
			kept := h.followers
			h.followers = h.followersPendingSwipe()
			payload, payloadErr := h.followerGesturePayload(attempt, message)
			if payloadErr != nil {
				h.followers = kept
				h.sendFollowerFanoutError("the source gesture was not eligible for follower fan-out")
				return nil
			}
			h.enqueueFollowerFanout(attempt.id, payload)
			h.followers = kept
		}
		return nil
	}
	return nil
}

func (h *liveGestureHandler) relayPhysical(ctx context.Context, input media.MirrorInput) {
	switch input.Kind {
	case media.MirrorInputTouchDown, media.MirrorInputTouchMove, media.MirrorInputTouchUp, media.MirrorInputTouchCancel:
	default:
		return
	}
	h.relayMu.Lock()
	defer h.relayMu.Unlock()
	if h.relayed == nil {
		h.relayed = map[string]struct{}{}
	}
	if h.pressedFollowers == nil {
		h.pressedFollowers = map[string]struct{}{}
	}
	for _, id := range h.followers {
		if _, ok := h.control.engine.Session(id); !ok {
			continue
		}
		if input.Kind == media.MirrorInputTouchCancel {
			if _, pressed := h.pressedFollowers[id]; !pressed {
				continue
			}
		} else if input.Kind != media.MirrorInputTouchDown {
			if _, accepted := h.relayed[id]; !accepted {
				continue
			}
		}
		if err := h.control.engine.Input(ctx, id, input); err != nil {
			log.Printf("event=realtime_follower_touch_relay_failed source=%s follower=%s kind=%s err=%v", h.binding.DeviceID, id, input.Kind, err)
			if _, pressed := h.pressedFollowers[id]; pressed {
				cancel := input
				cancel.Kind = media.MirrorInputTouchCancel
				if cancelErr := h.control.engine.Input(ctx, id, cancel); cancelErr != nil {
					log.Printf("event=realtime_follower_touch_cancel_failed source=%s follower=%s err=%v", h.binding.DeviceID, id, cancelErr)
				}
				delete(h.pressedFollowers, id)
			}
			continue
		}
		switch input.Kind {
		case media.MirrorInputTouchDown, media.MirrorInputTouchMove:
			h.relayed[id] = struct{}{}
			h.pressedFollowers[id] = struct{}{}
		case media.MirrorInputTouchUp, media.MirrorInputTouchCancel:
			h.relayed[id] = struct{}{}
			delete(h.pressedFollowers, id)
		}
	}
}

// followersPendingSwipe is the selected followers that did not accept this
// gesture on a live session. They still receive one reconstructed swipe on
// release. A follower already in relayed took the physical events and must not
// receive that swipe as well.
func (h *liveGestureHandler) followersPendingSwipe() []string {
	if len(h.followers) == 0 {
		return nil
	}
	h.relayMu.Lock()
	defer h.relayMu.Unlock()
	out := make([]string, 0, len(h.followers))
	for _, id := range h.followers {
		if _, accepted := h.relayed[id]; accepted {
			continue
		}
		out = append(out, id)
	}
	return out
}

// dispatchLiveKey creates one durable, typed action before sending one key
// through the selected stream's existing scrcpy control connection. Repeated
// browser keydowns are separate sequenced messages, so each audited action is
// exactly one bounded key event rather than an opaque repeat count.
func (h *liveGestureHandler) dispatchLiveKey(ctx context.Context, message media.MirrorControlMessage, input media.MirrorInput) error {
	if input.Repeat != 1 {
		return errors.New("mirror: a live key message must represent exactly one audited key event")
	}
	id, err := h.control.ids.NewID()
	if err != nil {
		return err
	}
	intent := action.Intent{
		ID: id, Workspace: h.binding.WorkspaceID, DeviceID: h.binding.DeviceID,
		LeaseID: h.binding.LeaseID, HolderID: h.binding.HolderID, FencingToken: h.binding.FencingToken,
		Kind: action.KeyEvent, InvocationSurface: action.SurfaceMirror,
		KeyCode: int(message.KeyCode), ObservationToken: h.streamKey,
		Capabilities: []action.Capability{action.CapabilitySystemInput}, Timeout: 5 * time.Second,
		IdempotencyKey: "live-key:" + h.streamKey + ":" + strconv.FormatUint(h.generation, 10) + ":" + strconv.FormatUint(message.Sequence, 10),
	}
	authorized, err := h.control.kernel.Authorize(ctx, intent, h.binding.ActorType, h.binding.ActorID)
	if err != nil {
		return err
	}
	if authorized.IdempotentReplay {
		return errors.New("mirror: a live key event cannot be replayed")
	}
	dispatched, err := h.control.kernel.Dispatch(ctx, intent.Workspace, id, intent.HolderID, intent.FencingToken, h.binding.ActorType, h.binding.ActorID)
	if err != nil {
		return err
	}
	if dispatched.IdempotentReplay {
		return errors.New("mirror: a live key event attempt was already dispatched")
	}
	if err := h.control.kernel.ValidateRealtimeControl(ctx, h.binding.WorkspaceID, h.binding.DeviceID, h.binding.SessionID, h.binding.LeaseID, h.binding.HolderID, h.binding.FencingToken); err != nil {
		return errors.Join(err, h.finishKeyAttempt(id))
	}
	inputErr := h.control.engine.Input(ctx, h.binding.DeviceID, input)
	if err := errors.Join(inputErr, h.finishKeyAttempt(id)); err != nil {
		return err
	}
	if len(h.followers) > 0 {
		h.enqueueFollowerFanout(intent.IdempotencyKey, execution.InputPayload{KeyEvent: &execution.KeyEventRequest{KeyCode: input.KeyCode, Repeat: input.Repeat}})
	}
	return nil
}

func (h *liveGestureHandler) finishKeyAttempt(attemptID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := h.control.kernel.MarkIndeterminate(ctx, action.Completion{
		Workspace: h.binding.WorkspaceID, AttemptID: attemptID, DeviceID: h.binding.DeviceID,
		LeaseID: h.binding.LeaseID, HolderID: h.binding.HolderID, FencingToken: h.binding.FencingToken,
	}, h.binding.ActorType, h.binding.ActorID)
	if platformerrors.CodeOf(err) == platformerrors.CodeIndeterminateCompletion {
		err = nil
	}
	if err != nil {
		return err
	}
	return h.control.evidence.Append(ctx, store.ActionEvidence{
		Workspace: h.binding.WorkspaceID, DeviceID: h.binding.DeviceID, Serial: h.peer.Stats().Serial,
		AttemptID: attemptID, Kind: action.KeyEvent, InvocationSurface: action.SurfaceMirror,
		Disposition: store.EvidenceDispatched, Outcome: action.OutcomeIndeterminate,
		Postcondition: action.PostconditionUnknown, FailureClass: domain.FailureIndeterminate,
	}, h.binding.ActorType, h.binding.ActorID)
}

func (h *liveGestureHandler) sendPhysical(ctx context.Context, attempt *liveGestureAttempt, input media.MirrorInput) error {
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.released {
		return errors.New("mirror: live touch has already been released")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.control.engine.Input(ctx, h.binding.DeviceID, input); err != nil {
		return err
	}
	attempt.pressed = true
	attempt.last = input
	if input.Kind == media.MirrorInputTouchUp || input.Kind == media.MirrorInputTouchCancel {
		attempt.released = true
	}
	return nil
}

// A channel ending mid-gesture cannot leave the device holding a finger down.
// This is only a release of an already-authorized touch, never a new gesture.
func (h *liveGestureHandler) releaseAttempt(attempt *liveGestureAttempt) error {
	attempt.mu.Lock()
	if attempt.released {
		attempt.mu.Unlock()
		return nil
	}
	attempt.released = true
	if !attempt.pressed {
		attempt.mu.Unlock()
		return nil
	}
	input := attempt.last
	input.Kind = media.MirrorInputTouchCancel
	attempt.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := h.control.engine.Input(ctx, h.binding.DeviceID, input)
	// Followers that already have a finger down get the same cancel. A miss
	// there must not leave the source's finger down, and it must not turn into
	// a second swipe.
	h.relayPhysical(ctx, input)
	return err
}

// A completed transport send does not prove the UI changed. Persist an honest
// indeterminate outcome once, including when the channel closes mid-gesture;
// a later fresh observation may reconcile it through the existing kernel.
func (h *liveGestureHandler) finishAttempt(attempt *liveGestureAttempt) error {
	if attempt == nil {
		return nil
	}
	attempt.finish.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, attempt.finishErr = h.control.kernel.MarkIndeterminate(ctx, action.Completion{
			Workspace: h.binding.WorkspaceID, AttemptID: attempt.id, DeviceID: h.binding.DeviceID,
			LeaseID: h.binding.LeaseID, HolderID: h.binding.HolderID, FencingToken: h.binding.FencingToken,
		}, h.binding.ActorType, h.binding.ActorID)
		if platformerrors.CodeOf(attempt.finishErr) == platformerrors.CodeIndeterminateCompletion {
			attempt.finishErr = nil
		}
		if attempt.finishErr != nil {
			return
		}
		attempt.finishErr = h.control.evidence.Append(ctx, store.ActionEvidence{
			Workspace: h.binding.WorkspaceID, DeviceID: h.binding.DeviceID, Serial: h.peer.Stats().Serial,
			AttemptID: attempt.id, Kind: action.LiveGesture, InvocationSurface: action.SurfaceMirror,
			Disposition: store.EvidenceDispatched, Outcome: action.OutcomeIndeterminate,
			Postcondition: action.PostconditionUnknown, FailureClass: domain.FailureIndeterminate,
		}, h.binding.ActorType, h.binding.ActorID)
	})
	return attempt.finishErr
}

type realtimeFollowerFanoutRequest struct {
	input execution.FollowerFanoutRequest
}

type realtimeFollowerFanoutReport struct {
	Type                 string                          `json:"type"`
	RunID                string                          `json:"runId"`
	SourceDeviceID       string                          `json:"sourceDeviceId"`
	TargetCount          int                             `json:"targetCount"`
	AcceptanceDurationMS int64                           `json:"acceptanceDurationMs"`
	Followers            []realtimeFollowerOutcomeReport `json:"followers"`
	Error                string                          `json:"error,omitempty"`
}

type realtimeFollowerOutcomeReport struct {
	DeviceID            string `json:"deviceId"`
	Disposition         string `json:"disposition"`
	Reason              string `json:"reason"`
	Detail              string `json:"detail"`
	Outcome             string `json:"outcome"`
	AttemptID           string `json:"attemptId"`
	IdempotencyKey      string `json:"idempotencyKey"`
	FrameWidth          uint32 `json:"frameWidth"`
	FrameHeight         uint32 `json:"frameHeight"`
	AcceptanceLatencyMS int64  `json:"acceptanceLatencyMs"`
}

// runFollowerFanout owns one bounded worker for this selected source stream.
// Source input has already reached scrcpy before an item enters this queue; a
// slow fleet read or follower queue therefore cannot hold the source gesture.
func (h *liveGestureHandler) runFollowerFanout(done <-chan struct{}) {
	process := func(item realtimeFollowerFanoutRequest) {
		ctx, cancel := context.WithTimeout(context.Background(), realtimeFollowerFanoutAcceptTimeout)
		report, err := h.control.followers.FanOut(ctx, item.input)
		cancel()
		if err != nil {
			h.sendFollowerFanoutError("the control plane could not queue follower actions")
			return
		}
		h.sendFollowerFanoutReport(report)
	}
	for {
		select {
		case item := <-h.fanoutQueue:
			process(item)
		case <-done:
			// A source gesture already delivered remains owed to the follower
			// queue even if its viewer closes in the small interval after release.
			for {
				select {
				case item := <-h.fanoutQueue:
					process(item)
				default:
					return
				}
			}
		}
	}
}

func (h *liveGestureHandler) enqueueFollowerFanout(requestID string, payload execution.InputPayload) {
	if h == nil || len(h.followers) == 0 {
		return
	}
	if h.control == nil || h.control.followers == nil || h.fanoutQueue == nil {
		h.sendFollowerFanoutError("realtime follower fan-out is not configured")
		return
	}
	request := execution.FollowerFanoutRequest{
		Workspace:         h.binding.WorkspaceID,
		SourceDeviceID:    h.binding.DeviceID,
		FollowerDeviceIDs: append([]string(nil), h.followers...),
		HolderID:          h.binding.ActorID,
		RequestID:         requestID,
		ObservationToken:  h.streamKey,
		Payload:           payload,
	}
	select {
	case h.fanoutQueue <- realtimeFollowerFanoutRequest{input: request}:
	default:
		h.sendFollowerFanoutError("the realtime follower queue is full; no follower action was queued")
	}
}

func (h *liveGestureHandler) followerGesturePayload(attempt *liveGestureAttempt, message media.MirrorControlMessage) (execution.InputPayload, error) {
	if attempt == nil {
		return execution.InputPayload{}, errors.New("a live gesture attempt is required")
	}
	space := execution.RenderSpace{Width: h.width, Height: h.height, ObservationToken: h.streamKey}
	switch message.GestureKind {
	case media.MirrorControlGestureTap:
		return execution.InputPayload{Tap: &execution.TapRequest{
			Point: execution.Point{X: attempt.startX, Y: attempt.startY}, Space: space,
		}}, nil
	case media.MirrorControlGestureSwipe:
		return execution.InputPayload{Swipe: &execution.SwipeRequest{
			Start: execution.Point{X: attempt.startX, Y: attempt.startY},
			End:   execution.Point{X: message.X, Y: message.Y}, DurationMS: message.DurationMS, Space: space,
		}}, nil
	default:
		return execution.InputPayload{}, errors.New("the realtime gesture kind is not carried to followers")
	}
}

func (h *liveGestureHandler) sendFollowerFanoutError(message string) {
	h.sendFollowerReport(realtimeFollowerFanoutReport{
		Type: "follower_fanout", SourceDeviceID: h.binding.DeviceID,
		Followers: []realtimeFollowerOutcomeReport{}, Error: message,
	})
}

func (h *liveGestureHandler) sendFollowerFanoutReport(report execution.FollowerFanoutReport) {
	result := realtimeFollowerFanoutReport{
		Type: "follower_fanout", RunID: report.RunID, SourceDeviceID: report.SourceDeviceID,
		TargetCount: report.TargetCount, AcceptanceDurationMS: report.AcceptanceDuration.Milliseconds(),
		Followers: make([]realtimeFollowerOutcomeReport, 0, len(report.Followers)),
	}
	for _, row := range report.Followers {
		result.Followers = append(result.Followers, realtimeFollowerOutcomeReport{
			DeviceID:    boundedRealtimeReportText(row.DeviceID, 128),
			Disposition: string(row.Disposition), Reason: boundedRealtimeReportText(string(row.Reason), 64),
			Detail: boundedRealtimeReportText(row.Detail, 256), Outcome: boundedRealtimeReportText(string(row.Outcome), 64),
			AttemptID: boundedRealtimeReportText(row.AttemptID, 128), IdempotencyKey: boundedRealtimeReportText(row.IdempotencyKey, 256),
			FrameWidth: row.Frame.Width, FrameHeight: row.Frame.Height,
			AcceptanceLatencyMS: row.AcceptanceLatency.Milliseconds(),
		})
	}
	h.sendFollowerReport(result)
}

func (h *liveGestureHandler) sendFollowerReport(report realtimeFollowerFanoutReport) {
	reporter, ok := h.peer.(media.MirrorControlReporter)
	if !ok {
		return
	}
	data, err := json.Marshal(report)
	if err != nil {
		return
	}
	if len(data) > realtimeFollowerReportMaxBytes {
		for index := range report.Followers {
			report.Followers[index].Detail = boundedRealtimeReportText(report.Followers[index].Detail, 128)
		}
		data, err = json.Marshal(report)
		if err != nil || len(data) > realtimeFollowerReportMaxBytes {
			return
		}
	}
	if err := reporter.SendControlReport(data); err != nil {
		log.Printf("event=realtime_follower_report_send_failed source=%s", h.binding.DeviceID)
	}
}

func boundedRealtimeReportText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}
