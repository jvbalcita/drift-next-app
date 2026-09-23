package mirror

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/media"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	store "drift.local/drift-next/internal/store/sqlite"
)

// RealtimeKernel is the narrow action-safety boundary used by a live gesture.
// Every gesture creates one policy decision and one durable attempt; pointer
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
}

const liveGestureMaxDuration = 30 * time.Second
const realtimeAuthorityCheckInterval = 250 * time.Millisecond

type RealtimeEvidenceRecorder interface {
	Append(context.Context, store.ActionEvidence, string, string) error
}

func NewRealtimeControl(kernel RealtimeKernel, engine LiveSessions, generator ids.IDGenerator, evidence RealtimeEvidenceRecorder) (*RealtimeControl, error) {
	if kernel == nil || engine == nil || generator == nil || evidence == nil {
		return nil, errors.New("mirror: realtime control requires a kernel, live sessions, IDs and evidence recorder")
	}
	return &RealtimeControl{kernel: kernel, engine: engine, ids: generator, evidence: evidence, checkInterval: realtimeAuthorityCheckInterval, gestureLimit: liveGestureMaxDuration}, nil
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
	stats := peer.Stats()
	if stats.DeviceID != binding.DeviceID || stats.RenderWidth <= 0 || stats.RenderHeight <= 0 || stats.StreamKey == "" {
		return 0, errors.New("mirror: realtime control requires the selected stream's active render frame")
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
	handler := &liveGestureHandler{control: c, peer: peer, binding: binding, generation: generation, width: uint32(stats.RenderWidth), height: uint32(stats.RenderHeight), streamKey: stats.StreamKey}
	if err := peer.BindControl(generation, handler.handle); err != nil {
		return 0, err
	}
	go c.watchAuthority(peer, binding)
	return generation, nil
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
	control    *RealtimeControl
	peer       media.MirrorControlPeer
	binding    media.MirrorControlBinding
	generation uint64
	width      uint32
	height     uint32
	streamKey  string
	active     *liveGestureAttempt
}

type liveGestureAttempt struct {
	id        string
	gesture   uint64
	started   time.Time
	stop      func() bool
	timeout   *time.Timer
	finish    sync.Once
	finishErr error
	mu        sync.Mutex
	last      media.MirrorInput
	pressed   bool
	released  bool
}

func (h *liveGestureHandler) handle(ctx context.Context, message media.MirrorControlMessage) error {
	if message.Kind == media.MirrorControlKey {
		return errors.New("mirror: live key input requires a separately approved action")
	}
	if h.active != nil && time.Since(h.active.started) > h.control.gestureLimit {
		return errors.New("mirror: live gesture exceeded its authorized duration")
	}
	stats := h.peer.Stats()
	if message.Generation != h.generation || stats.StreamKey != h.streamKey || stats.DeviceID != h.binding.DeviceID || stats.RenderWidth != int(h.width) || stats.RenderHeight != int(h.height) || message.Width != h.width || message.Height != h.height {
		return errors.New("mirror: live control frame or generation changed")
	}
	if err := h.control.kernel.ValidateRealtimeControl(ctx, h.binding.WorkspaceID, h.binding.DeviceID, h.binding.SessionID, h.binding.LeaseID, h.binding.HolderID, h.binding.FencingToken); err != nil {
		return err
	}
	input, err := message.MirrorInput()
	if err != nil {
		return err
	}
	if message.Kind == media.MirrorControlTouchDown {
		if h.active != nil {
			return errors.New("mirror: another live gesture is active")
		}
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
		attempt := &liveGestureAttempt{id: id, gesture: message.GestureID, started: time.Now()}
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
	if message.Final {
		attempt := h.active
		h.active = nil
		if attempt.stop != nil {
			attempt.stop()
		}
		if attempt.timeout != nil {
			attempt.timeout.Stop()
		}
		return h.finishAttempt(attempt)
	}
	return nil
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
	return h.control.engine.Input(ctx, h.binding.DeviceID, input)
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
