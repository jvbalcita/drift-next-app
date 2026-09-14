package recordings

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
)

var (
	ErrQueueFull  = errors.New("recording input queue is full")
	ErrNotRunning = errors.New("recording session is not running")
)

type CaptureRequest struct {
	Workspace string
	SessionID RecordingSessionID
	EventID   RecordingEventID
	DeviceID  string
	Phase     CapturePhase
}

// CaptureProvider is the fake observation seam for this phase. It returns
// metadata and authorized artifact references, never screenshot bytes or raw
// Android protocol data.
type CaptureProvider interface {
	Capture(context.Context, CaptureRequest) (Capture, error)
}

type AnnotationRequest struct {
	Workspace string
	SessionID RecordingSessionID
	EventID   RecordingEventID
	Capture   Capture
	Action    LogicalAction
}

type AnnotationProvider interface {
	Annotate(context.Context, AnnotationRequest) (EvidenceReference, error)
}

type EventSink interface {
	Save(context.Context, InteractionEvent) error
}

type RecorderOptions struct {
	QueueSize       int
	Grouping        GroupingOptions
	BeforeStability time.Duration
	AfterStability  time.Duration
	Clock           clock.Clock
	IDs             ids.IDGenerator
}

type Recorder struct {
	mu        sync.Mutex
	session   Session
	capture   CaptureProvider
	annotator AnnotationProvider
	sink      EventSink
	clock     clock.Clock
	ids       ids.IDGenerator
	options   RecorderOptions
	queue     chan RawInput
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	stopping  bool
	closed    bool
	workerErr error
	sequence  int
	lastState *Capture
}

func NewRecorder(session Session, capture CaptureProvider, annotator AnnotationProvider, sink EventSink, options RecorderOptions) (*Recorder, error) {
	if session.Cleanup == "" {
		session.Cleanup = CleanupNone
	}
	if session.Review == "" {
		session.Review = ReviewUnreviewed
	}
	if err := session.Validate(); err != nil {
		return nil, fmt.Errorf("recording session is invalid: %w", err)
	}
	if session.Source != SourceFake {
		return nil, fmt.Errorf("recorder accepts fake sources only")
	}
	if capture == nil || sink == nil {
		return nil, fmt.Errorf("capture provider and event sink are required")
	}
	if options.QueueSize <= 0 {
		options.QueueSize = 64
	}
	if options.QueueSize > 4096 {
		return nil, fmt.Errorf("recording queue is too large")
	}
	if options.Clock == nil {
		options.Clock = clock.System{}
	}
	if options.IDs == nil {
		options.IDs = ids.NewRandom()
	}
	return &Recorder{session: session, capture: capture, annotator: annotator, sink: sink, clock: options.Clock, ids: options.IDs, options: options, done: make(chan struct{})}, nil
}

func (r *Recorder) Session() Session {
	if r == nil {
		return Session{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *Recorder) WorkerError() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.workerErr
}

func (r *Recorder) Start() error {
	if r == nil {
		return fmt.Errorf("recorder is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return fmt.Errorf("recorder has already started or closed")
	}
	started, err := Start(r.session, r.clock.Now())
	if err != nil {
		return err
	}
	r.session = started
	r.queue = make(chan RawInput, r.options.QueueSize)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.started = true
	go r.run()
	return nil
}

// Submit is deliberately non-blocking so an input callback cannot wait on
// screenshot, UI-tree, filesystem, or SQLite work.
func (r *Recorder) Submit(ctx context.Context, input RawInput) error {
	if r == nil || ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and recorder are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.Sensitivity == "" {
		input.Sensitivity = SensitivityNone
	}
	if err := input.Validate(); err != nil {
		return platformerrors.Wrap(platformerrors.CodeInvalidInput, "raw recording input is invalid", err)
	}
	r.mu.Lock()
	if !r.started || r.stopping || r.closed || r.session.State != SessionRecording {
		r.mu.Unlock()
		return ErrNotRunning
	}
	if r.session.DeviceID != "" && string(input.DeviceID) != string(r.session.DeviceID) {
		r.mu.Unlock()
		return platformerrors.New(platformerrors.CodeConflict, "input targets another recording device")
	}
	queue := r.queue
	select {
	case queue <- input:
		r.mu.Unlock()
		return nil
	default:
		r.mu.Unlock()
		return ErrQueueFull
	}
}

func (r *Recorder) Stop(ctx context.Context) error {
	if r == nil || ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and recorder are required")
	}
	r.mu.Lock()
	if !r.started || r.closed || r.stopping {
		r.mu.Unlock()
		return ErrNotRunning
	}
	r.stopping = true
	close(r.queue)
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	if r.workerErr != nil {
		failed, err := Fail(r.session, r.clock.Now())
		if err == nil {
			r.session = failed
		}
		return r.workerErr
	}
	completed, err := Stop(r.session, r.clock.Now())
	if err != nil {
		return err
	}
	r.session = completed
	return nil
}

func (r *Recorder) Discard(ctx context.Context) error {
	if r == nil || ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and recorder are required")
	}
	r.mu.Lock()
	if !r.started || r.closed || r.stopping {
		r.mu.Unlock()
		return ErrNotRunning
	}
	r.stopping = true
	close(r.queue)
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	discarded, err := Discard(r.session, r.clock.Now())
	if err != nil {
		return err
	}
	r.session = discarded
	return r.workerErr
}

func (r *Recorder) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.Session().State == SessionRecording {
		return r.Stop(ctx)
	}
	return nil
}

func (r *Recorder) run() {
	grouper := NewEventGrouper(r.options.Grouping)
	defer close(r.done)
	for input := range r.queue {
		actions, err := grouper.Add(input)
		if err != nil {
			r.setWorkerError(err)
			continue
		}
		for _, grouped := range actions {
			r.process(grouped)
		}
	}
	actions, err := grouper.Flush()
	if err != nil {
		r.setWorkerError(err)
		return
	}
	for _, grouped := range actions {
		r.process(grouped)
	}
}

func (r *Recorder) process(grouped GroupedAction) {
	eventID, err := r.ids.NewID()
	if err != nil {
		r.setWorkerError(fmt.Errorf("generate recording event ID: %w", err))
		return
	}
	normalized, err := NormalizeAction(grouped)
	if err != nil {
		r.setWorkerError(err)
		return
	}
	normalized = RedactAction(normalized)
	before, beforeErrors := r.captureState(string(eventID), PhaseBefore, r.previousState())
	after, afterErrors := r.captureState(string(eventID), PhaseAfter, nil)
	event := InteractionEvent{
		ID:            RecordingEventID(eventID),
		Workspace:     r.session.Workspace,
		SessionID:     r.session.ID,
		Sequence:      r.sequence,
		CorrelationID: fmt.Sprintf("recording:%s:event:%d", r.session.ID, r.sequence),
		Action:        normalized,
		Before:        before,
		After:         after,
		CaptureErrors: append(beforeErrors, afterErrors...),
		Redaction:     RedactionNotRequired,
		Sensitive:     normalized.Sensitivity != SensitivityNone,
		Review:        ReviewUnreviewed,
		StartedAt:     grouped.StartedAt,
		FinishedAt:    grouped.FinishedAt,
		CreatedAt:     r.clock.Now().UTC(),
	}
	if event.Sensitive {
		event.Redaction = RedactionRedacted
	}
	event.Evidence = append(event.Evidence, evidenceFromCapture(before, PhaseBefore)...)
	event.Evidence = append(event.Evidence, evidenceFromCapture(after, PhaseAfter)...)
	if r.annotator != nil && !event.Sensitive && after != nil && after.Sanitization != Unsanitizable {
		annotationCapture := CloneCapture(after)
		annotationAction := CloneAction(normalized)
		reference, annotationErr := r.annotator.Annotate(r.ctx, AnnotationRequest{Workspace: string(r.session.Workspace), SessionID: r.session.ID, EventID: event.ID, Capture: *annotationCapture, Action: annotationAction})
		if annotationErr != nil {
			event.CaptureErrors = append(event.CaptureErrors, CaptureError{Phase: PhaseReview, Class: annotationFailure(annotationErr), Message: "annotation unavailable"})
		} else {
			reference.Phase = PhaseReview
			if reference.Kind != EvidenceAnnotatedScreenshot || reference.Omitted {
				event.CaptureErrors = append(event.CaptureErrors, CaptureError{Phase: PhaseReview, Class: domain.FailureObservation, Message: "annotation reference was invalid"})
			} else if err := reference.Validate(); err != nil {
				event.CaptureErrors = append(event.CaptureErrors, CaptureError{Phase: PhaseReview, Class: domain.FailureObservation, Message: "annotation reference was invalid"})
			} else {
				event.Evidence = append(event.Evidence, reference)
			}
		}
	}
	if err := event.Validate(); err != nil {
		r.setWorkerError(err)
		return
	}
	if err := r.sink.Save(r.ctx, event); err != nil {
		r.setWorkerError(err)
		return
	}
	r.sequence++
	if after != nil && after.Status == CaptureComplete && after.ErrorClass == "" && after.Sanitization != Unsanitizable {
		r.lastState = CloneCapture(after)
	}
}

func (r *Recorder) previousState() *Capture {
	if r.lastState == nil {
		return nil
	}
	return CloneCapture(r.lastState)
}

func (r *Recorder) captureState(eventID string, phase CapturePhase, previous *Capture) (*Capture, []CaptureError) {
	if previous != nil {
		if r.options.BeforeStability > 0 {
			waitFor(r.ctx, r.options.BeforeStability)
		}
		return previous, nil
	}
	if phase == PhaseAfter && r.options.AfterStability > 0 {
		waitFor(r.ctx, r.options.AfterStability)
	}
	capture, err := r.capture.Capture(r.ctx, CaptureRequest{Workspace: string(r.session.Workspace), SessionID: r.session.ID, EventID: RecordingEventID(eventID), DeviceID: string(r.session.DeviceID), Phase: phase})
	if capture.CapturedAt.IsZero() {
		capture.CapturedAt = r.clock.Now().UTC()
	}
	if capture.Status == "" {
		capture.Status = CaptureComplete
	}
	if capture.Sanitization == "" {
		capture.Sanitization = Sanitized
	}
	errorsForCapture := make([]CaptureError, 0, 2)
	if err != nil {
		capture.Status = CapturePartial
		capture.Partial = true
		if capture.ErrorClass == "" {
			capture.ErrorClass = domain.FailureObservation
		}
		errorsForCapture = append(errorsForCapture, CaptureError{Phase: phase, Class: capture.ErrorClass, Message: "observation capture unavailable"})
	} else if capture.Status != CaptureComplete {
		if capture.ErrorClass == "" {
			capture.ErrorClass = domain.FailureObservation
		}
		errorsForCapture = append(errorsForCapture, CaptureError{Phase: phase, Class: capture.ErrorClass, Message: "observation capture was partial"})
	}
	sanitized, omissionErrors := SanitizeCapture(capture, phase)
	errorsForCapture = append(errorsForCapture, omissionErrors...)
	if err := sanitized.Validate(); err != nil {
		sanitized = Capture{CapturedAt: capture.CapturedAt, Status: CapturePartial, Sanitization: Sanitized, Partial: true, ErrorClass: domain.FailureObservation}
		errorsForCapture = append(errorsForCapture, CaptureError{Phase: phase, Class: domain.FailureObservation, Message: "observation metadata unavailable"})
	}
	return &sanitized, errorsForCapture
}

func evidenceFromCapture(capture *Capture, phase CapturePhase) []EvidenceReference {
	if capture == nil {
		return nil
	}
	result := append([]EvidenceReference(nil), capture.Evidence...)
	for index := range result {
		result[index].Phase = phase
	}
	return result
}

func waitFor(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (r *Recorder) setWorkerError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	if r.workerErr == nil {
		r.workerErr = err
	}
	r.mu.Unlock()
}

func annotationFailure(_ error) domain.FailureClass {
	return domain.FailureObservation
}
