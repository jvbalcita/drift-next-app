package recordings_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	"drift.local/drift-next/internal/recordings"
)

func TestRecorderCapturesBeforeActionAfterAndSeparateAnnotation(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	provider := &fakeCaptureProvider{captures: []recordings.Capture{fakeCapture("before"), fakeCapture("after")}}
	sink := &memoryEventSink{}
	annotator := fakeAnnotator{}
	recorder, err := recordings.NewRecorder(fakeSession(now), provider, annotator, sink, recordings.RecorderOptions{Clock: clock.NewFixed(now), IDs: ids.NewSequence("event-1")})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	input := recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputTap, At: now.Add(time.Second), EndAt: now.Add(time.Second + 50*time.Millisecond), Start: action.Coordinate{Space: "display:1080x1920", X: 100, Y: 200}, Sensitivity: recordings.SensitivityNone, Target: target("save")}
	if err := recorder.Submit(context.Background(), input); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := recorder.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("saved events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Before == nil || event.After == nil || event.Before.ObservationID != "before" || event.After.ObservationID != "after" {
		t.Fatalf("event captures = %#v, want distinct before and after captures", event)
	}
	if len(event.Evidence) != 3 {
		t.Fatalf("event evidence = %#v, want raw before, raw after, and annotation", event.Evidence)
	}
	if event.Evidence[2].Kind != recordings.EvidenceAnnotatedScreenshot || event.Evidence[2].Phase != recordings.PhaseReview {
		t.Fatalf("annotation evidence = %#v, want separate review evidence", event.Evidence[2])
	}
	if recorder.Session().State != recordings.SessionCompleted {
		t.Fatalf("session state = %q, want completed", recorder.Session().State)
	}
}

func TestRecorderRedactsSensitiveInputAndKeepsCapturePartial(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	provider := &fakeCaptureProvider{captures: []recordings.Capture{fakeCapture("before"), {CapturedAt: now, Status: recordings.CapturePartial, Partial: true, Sanitization: recordings.Sanitized, ErrorClass: domain.FailureObservation}}}
	sink := &memoryEventSink{}
	annotator := &countingAnnotator{}
	recorder, err := recordings.NewRecorder(fakeSession(now), provider, annotator, sink, recordings.RecorderOptions{Clock: clock.NewFixed(now), IDs: ids.NewSequence("event-1")})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Start(); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Submit(context.Background(), recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputText, At: now.Add(time.Second), Text: "secret", Sensitivity: recordings.SensitivitySensitive, Target: target("password-field")}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := sink.Events()[0]
	if event.Action.DisplayValue != "[REDACTED]" || event.Action.Sensitivity != recordings.SensitivitySensitive || !event.Sensitive {
		t.Fatalf("sensitive action = %#v, want redacted metadata", event.Action)
	}
	if event.After == nil || event.After.Status != recordings.CapturePartial || len(event.CaptureErrors) == 0 {
		t.Fatalf("partial after capture = %#v errors=%#v", event.After, event.CaptureErrors)
	}
	if annotator.calls != 0 {
		t.Fatalf("sensitive event annotation calls = %d, want none", annotator.calls)
	}
}

func TestRecorderQueueIsNonBlockingAndAnnotationFailureDoesNotLoseEvent(t *testing.T) {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	provider := &blockingCaptureProvider{started: make(chan struct{}), release: make(chan struct{})}
	sink := &memoryEventSink{}
	recorder, err := recordings.NewRecorder(fakeSession(now), provider, failingAnnotator{}, sink, recordings.RecorderOptions{QueueSize: 1, Clock: clock.NewFixed(now), IDs: ids.NewSequence("event-1", "event-2", "event-3")})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Start(); err != nil {
		t.Fatal(err)
	}
	swipe := func(at time.Duration) recordings.RawInput {
		return recordings.RawInput{DeviceID: "device-1", Kind: recordings.InputSwipe, At: now.Add(at), EndAt: now.Add(at + time.Millisecond), Start: action.Coordinate{Space: "display:1080x1920", X: 10, Y: 10}, End: &action.Coordinate{Space: "display:1080x1920", X: 20, Y: 20}, Sensitivity: recordings.SensitivityNone}
	}
	if err := recorder.Submit(context.Background(), swipe(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("capture worker did not start")
	}
	if err := recorder.Submit(context.Background(), swipe(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Submit(context.Background(), swipe(3*time.Second)); !errors.Is(err, recordings.ErrQueueFull) {
		t.Fatalf("third Submit() error = %v, want ErrQueueFull", err)
	}
	close(provider.release)
	if err := recorder.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(sink.Events()) != 2 {
		t.Fatalf("saved events = %d, want queued events drained", len(sink.Events()))
	}
	if len(sink.Events()[0].CaptureErrors) == 0 || sink.Events()[0].CaptureErrors[len(sink.Events()[0].CaptureErrors)-1].Message != "annotation unavailable" {
		t.Fatalf("annotation failure was not retained: %#v", sink.Events()[0].CaptureErrors)
	}
}

func fakeSession(now time.Time) recordings.Session {
	return recordings.Session{ID: "session-1", Workspace: "workspace-1", DeviceID: "device-1", Number: 1, State: recordings.SessionRequested, Source: recordings.SourceFake, CreatedAt: now, Cleanup: recordings.CleanupNone, Review: recordings.ReviewUnreviewed}
}

func fakeCapture(id string) recordings.Capture {
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	return recordings.Capture{ObservationID: id, FreshnessToken: id + "-fresh", CapturedAt: now, CoordinateSpace: "display:1080x1920", PackageName: "com.example.fake", ActivityName: ".MainActivity", AppVersion: "1.0.0", DisplayWidth: 1080, DisplayHeight: 1920, Status: recordings.CaptureComplete, Sanitization: recordings.Sanitized, ScreenshotHash: "sha256:" + id, Evidence: []recordings.EvidenceReference{{Kind: recordings.EvidenceRawScreenshot, Phase: recordings.PhaseBefore, ContentHash: "sha256:" + id, MediaType: "image/png", SchemaVersion: 1, Authoritative: true}}}
}

type fakeCaptureProvider struct {
	mu       sync.Mutex
	captures []recordings.Capture
}

func (p *fakeCaptureProvider) Capture(_ context.Context, _ recordings.CaptureRequest) (recordings.Capture, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.captures) == 0 {
		return recordings.Capture{}, errors.New("capture script exhausted")
	}
	capture := p.captures[0]
	p.captures = p.captures[1:]
	return capture, nil
}

type blockingCaptureProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingCaptureProvider) Capture(_ context.Context, _ recordings.CaptureRequest) (recordings.Capture, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
	case <-time.After(time.Second):
		return recordings.Capture{}, errors.New("fake capture wait timed out")
	}
	return fakeCapture("blocked"), nil
}

type memoryEventSink struct {
	mu     sync.Mutex
	events []recordings.InteractionEvent
}

func (s *memoryEventSink) Save(_ context.Context, event recordings.InteractionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordings.CloneEvent(event))
	return nil
}

func (s *memoryEventSink) Events() []recordings.InteractionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]recordings.InteractionEvent, len(s.events))
	for index, event := range s.events {
		result[index] = recordings.CloneEvent(event)
	}
	return result
}

type fakeAnnotator struct{}

func (fakeAnnotator) Annotate(_ context.Context, _ recordings.AnnotationRequest) (recordings.EvidenceReference, error) {
	return recordings.EvidenceReference{Kind: recordings.EvidenceAnnotatedScreenshot, ContentHash: "sha256:annotated", MediaType: "image/png", SchemaVersion: 1}, nil
}

type failingAnnotator struct{}

func (failingAnnotator) Annotate(context.Context, recordings.AnnotationRequest) (recordings.EvidenceReference, error) {
	return recordings.EvidenceReference{}, errors.New("annotation failed")
}

type countingAnnotator struct{ calls int }

func (a *countingAnnotator) Annotate(context.Context, recordings.AnnotationRequest) (recordings.EvidenceReference, error) {
	a.calls++
	return recordings.EvidenceReference{Kind: recordings.EvidenceAnnotatedScreenshot, ContentHash: "sha256:annotated", MediaType: "image/png", SchemaVersion: 1}, nil
}
