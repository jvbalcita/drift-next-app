package execution_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// This file is the emission half of the action evidence boundary (ARC-64): one
// append-only record per dispatched device action, carrying the action identity,
// the target device, the outcome and the resulting observation, so a later
// recording can reference what happened instead of what was intended. Everything
// here runs against fakes: no process is spawned, no socket is opened and no
// device is touched.

// recordingEvidence is a deterministic fake of the append-only evidence
// recorder. It mirrors the store's two properties: an append is an append, and
// an append never rewrites an earlier record.
type recordingEvidence struct {
	mu      sync.Mutex
	records []store.ActionEvidence
	err     error
}

func (r *recordingEvidence) Append(_ context.Context, record store.ActionEvidence, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	record.ID = fmt.Sprintf("evidence-%d", len(r.records)+1)
	r.records = append(r.records, record)
	return nil
}

func (r *recordingEvidence) all() []store.ActionEvidence {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]store.ActionEvidence(nil), r.records...)
}

// evidenceDispatchFixture composes the dispatcher with the evidence recorder in
// front of the same fakes the other dispatch tests use, so the record a test
// reads is the record the real contract would append.
func evidenceDispatchFixture(t *testing.T) (dispatchFixture, *recordingEvidence) {
	t.Helper()
	control := &fakeControl{}
	probe := &fakeProbe{}
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: postToken}}
	transport := newFakeDeviceTransport()
	recorder := &recordingEvidence{}
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, transport, &fakeResolver{value: typedValueFixture},
		execution.WithRenderSizeSourceFactory(testRenderSizeSource), execution.WithEvidenceRecorder(recorder))
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	return dispatchFixture{control: control, probe: probe, observer: observer, transport: transport, dispatcher: dispatcher}, recorder
}

// TestEveryDispatchedDeviceInputRecordsOneEvidenceRecord is the acceptance
// assertion: for each of the five typed inputs, exactly one record is appended,
// and it carries the action identity, the target device, the outcome and the
// observation the postcondition was evaluated against.
func TestEveryDispatchedDeviceInputRecordsOneEvidenceRecord(t *testing.T) {
	cases := []struct {
		name        string
		payload     execution.InputPayload
		target      action.SemanticTarget
		observation execution.PostconditionObservation
		wantKind    action.Kind
		wantPackage string
		wantLength  int
	}{
		{
			name:        "tap",
			payload:     tapPayload(),
			observation: execution.PostconditionObservation{Token: postToken},
			wantKind:    action.Tap,
		},
		{
			name:        "swipe",
			payload:     swipePayload(),
			observation: execution.PostconditionObservation{Token: postToken},
			wantKind:    action.Swipe,
		},
		{
			name:        "key event",
			payload:     keyEventPayload(),
			observation: execution.PostconditionObservation{Token: postToken},
			wantKind:    action.KeyEvent,
		},
		{
			name:        "typed text by reference",
			payload:     textPayload(),
			target:      action.SemanticTarget{ResourceID: "composer"},
			observation: execution.PostconditionObservation{Token: postToken, FieldLength: len(typedValueFixture)},
			wantKind:    action.TextInput,
			wantLength:  len(typedValueFixture),
		},
		{
			name:        "app launch",
			payload:     launchPayload(),
			observation: execution.PostconditionObservation{Token: postToken, ForegroundPackage: "com.example.app"},
			wantKind:    action.LaunchApp,
			wantPackage: "com.example.app",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture, recorder := evidenceDispatchFixture(t)
			fixture.observer.observation = test.observation
			request := inputRequest("attempt-evidence", "key-evidence", test.payload)
			request.Target = test.target

			result, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if result.Outcome != action.OutcomeVerified {
				t.Fatalf("outcome = %q, want verified", result.Outcome)
			}
			if calls := fixture.transport.invocationCount(); calls != 1 {
				t.Fatalf("device calls = %d, want exactly 1", calls)
			}
			records := recorder.all()
			if len(records) != 1 {
				t.Fatalf("evidence records = %d, want exactly one per dispatched action", len(records))
			}
			record := records[0]
			if record.AttemptID != request.IntentID || record.Kind != test.wantKind {
				t.Fatalf("evidence identity = (%q, %q), want (%q, %q)", record.AttemptID, record.Kind, request.IntentID, test.wantKind)
			}
			if record.DeviceID != inputDevice || record.Serial != inputSerial {
				t.Fatalf("evidence target = (%q, %q), want (%q, %q)", record.DeviceID, record.Serial, inputDevice, inputSerial)
			}
			if record.Disposition != store.EvidenceDispatched {
				t.Fatalf("disposition = %q, want %q", record.Disposition, store.EvidenceDispatched)
			}
			if record.Outcome != action.OutcomeVerified || record.Postcondition != action.PostconditionPassed {
				t.Fatalf("evidence outcome = (%q, %q), want verified with a passed postcondition", record.Outcome, record.Postcondition)
			}
			if record.RefusalReason != "" || record.FailureClass != "" {
				t.Fatalf("a dispatched action carried a refusal: (%q, %q)", record.RefusalReason, record.FailureClass)
			}
			if record.Observation.Token != test.observation.Token {
				t.Fatalf("evidence observation = %q, want the fresh observation %q", record.Observation.Token, test.observation.Token)
			}
			if record.Observation.ForegroundPackage != test.wantPackage || record.Observation.FieldLength != test.wantLength {
				t.Fatalf("evidence observation = %#v, want package %q and length %d", record.Observation, test.wantPackage, test.wantLength)
			}
		})
	}
}

// TestARefusedDeviceInputRecordsEvidenceAndReachesNoDevice asserts the decision
// this boundary makes explicit: a refusal records evidence too, because "this
// was refused, for this reason, and no input was sent" is as much a fact about
// what happened as a dispatch is.
func TestARefusedDeviceInputRecordsEvidenceAndReachesNoDevice(t *testing.T) {
	cases := []struct {
		name         string
		probe        execution.RefusalReason
		authorizeErr error
		wantReason   execution.RefusalReason
		wantClass    domain.FailureClass
	}{
		{
			name:       "an expired lease",
			probe:      execution.RefusalLeaseExpired,
			wantReason: execution.RefusalLeaseExpired,
			wantClass:  domain.FailureLeaseConflict,
		},
		{
			name:         "a policy denial",
			authorizeErr: platformerrors.New(platformerrors.CodePolicyDenied, "active policy denies this action"),
			wantReason:   execution.RefusalPolicyDenied,
			wantClass:    domain.FailurePolicyDenied,
		},
		{
			name:         "a reused idempotency key with a different request",
			authorizeErr: platformerrors.New(platformerrors.CodeConflict, "idempotency key reused with another request"),
			wantReason:   execution.RefusalDuplicateIdempotencyKey,
			wantClass:    domain.FailureInvalidTransition,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture, recorder := evidenceDispatchFixture(t)
			fixture.probe.reason = test.probe
			fixture.control.authorizeErr = test.authorizeErr

			if _, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-refused", "key-refused", tapPayload()), "operator", "operator-1"); err == nil {
				t.Fatal("a refused input was reported as success")
			}
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("a refusal issued %d device calls, want 0", calls)
			}
			records := recorder.all()
			if len(records) != 1 {
				t.Fatalf("evidence records = %d, want exactly one per refused action", len(records))
			}
			record := records[0]
			if record.Disposition != store.EvidenceRefused {
				t.Fatalf("disposition = %q, want %q", record.Disposition, store.EvidenceRefused)
			}
			if record.RefusalReason != string(test.wantReason) || record.FailureClass != test.wantClass {
				t.Fatalf("evidence refusal = (%q, %q), want (%q, %q)", record.RefusalReason, record.FailureClass, test.wantReason, test.wantClass)
			}
			if record.AttemptID != "attempt-refused" || record.Kind != action.Tap || record.DeviceID != inputDevice {
				t.Fatalf("evidence identity = %#v, want the refused tap on %q", record, inputDevice)
			}
			if record.Observation.Token != "" || record.Observation.ForegroundPackage != "" || record.Observation.FieldLength != 0 {
				t.Fatalf("a refusal carried an observation: %#v", record.Observation)
			}
			if record.Outcome != "" || record.Postcondition != "" {
				t.Fatalf("a refusal invented a terminal outcome: (%q, %q)", record.Outcome, record.Postcondition)
			}
		})
	}
}

// TestARequestThatNamesNoTypedActionRecordsNoEvidence draws the boundary of the
// claim above: evidence is recorded for a device action, and a request that does
// not name exactly one typed action kind is not an action. It reaches no device
// and appends nothing.
func TestARequestThatNamesNoTypedActionRecordsNoEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload execution.InputPayload
	}{
		{name: "no payload at all", payload: execution.InputPayload{}},
		{name: "two payloads at once", payload: execution.InputPayload{Tap: tapPayload().Tap, Swipe: swipePayload().Swipe}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, recorder := evidenceDispatchFixture(t)
			if _, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-malformed", "key-malformed", test.payload), "operator", "operator-1"); err == nil {
				t.Fatal("a malformed request was accepted")
			}
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("a malformed request issued %d device calls, want 0", calls)
			}
			if records := recorder.all(); len(records) != 0 {
				t.Fatalf("a malformed request recorded evidence: %#v", records)
			}
		})
	}
}

// TestADuplicateDeliveryRecordsAReplayAndCopiesNoObservation asserts what a
// replay's evidence may claim: the replay happened, it changed nothing, and it
// observed nothing. Copying the first record's observation would claim the
// duplicate delivery took an observation it never took.
func TestADuplicateDeliveryRecordsAReplayAndCopiesNoObservation(t *testing.T) {
	fixture, recorder := evidenceDispatchFixture(t)
	fixture.control.authorizeResult = action.Result{
		Attempt:             action.Attempt{ID: "attempt-original", State: action.AttemptVerified, Postcondition: action.PostconditionPassed, ObservationToken: postToken},
		Outcome:             action.OutcomeVerified,
		PostconditionPassed: true,
		IdempotentReplay:    true,
	}
	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-replay", "key-replay", tapPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if !result.IdempotentReplay || result.Attempt.ID != "attempt-original" {
		t.Fatalf("duplicate result = %#v, want the recorded attempt", result)
	}
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("a duplicate delivery issued %d device calls, want 0", calls)
	}
	records := recorder.all()
	if len(records) != 1 {
		t.Fatalf("evidence records = %d, want exactly one per delivery", len(records))
	}
	if records[0].Disposition != store.EvidenceReplayed {
		t.Fatalf("disposition = %q, want %q", records[0].Disposition, store.EvidenceReplayed)
	}
	if records[0].Outcome != action.OutcomeVerified {
		t.Fatalf("replay outcome = %q, want the recorded outcome", records[0].Outcome)
	}
	if records[0].Observation.Token != "" || records[0].Observation.ForegroundPackage != "" {
		t.Fatalf("a replay copied an observation it never took: %#v", records[0].Observation)
	}
}

// TestTheEvidenceForTypedTextCarriesNeitherTheHandleNorTheValue asserts the
// redaction rule on the one input that has a value behind it: the device
// receives the value, and no form of the record - string, struct, debug or JSON
// rendering - carries the value or the handle that names it. What the record
// carries is the addressed field's length, which is a count.
func TestTheEvidenceForTypedTextCarriesNeitherTheHandleNorTheValue(t *testing.T) {
	fixture, recorder := evidenceDispatchFixture(t)
	fixture.observer.observation = execution.PostconditionObservation{Token: postToken, FieldLength: len(typedValueFixture)}
	payload := textPayload()
	request := inputRequest("attempt-typed-text", "key-typed-text", payload)
	request.Target = action.SemanticTarget{ResourceID: "composer"}

	if _, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if calls := fixture.transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	matchArgs(t, fixture.transport.invocation(0).args, "shell", "input", "text", typedValueFixture)

	records := recorder.all()
	if len(records) != 1 {
		t.Fatalf("evidence records = %d, want exactly one", len(records))
	}
	record := records[0]
	handle := payload.Text.Handle
	for _, rendered := range []string{fmt.Sprintf("%v", record), fmt.Sprintf("%+v", record), fmt.Sprintf("%#v", record)} {
		if strings.Contains(rendered, typedValueFixture) {
			t.Fatalf("the evidence record carries the typed value: %s", rendered)
		}
		if strings.Contains(rendered, handle) {
			t.Fatalf("the evidence record carries the handle to the typed value: %s", rendered)
		}
	}
	if record.Observation.FieldLength != len(typedValueFixture) {
		t.Fatalf("addressed field length = %d, want the count %d", record.Observation.FieldLength, len(typedValueFixture))
	}
}

// TestAnOutcomeThatCannotBeRecordedIsReportedAndIsNotRepeated asserts what
// happens when evidence cannot be written: the device action already happened,
// so the dispatch is not repeated and its result is still returned - and the
// caller is told, rather than being left with an unexplained action.
func TestAnOutcomeThatCannotBeRecordedIsReportedAndIsNotRepeated(t *testing.T) {
	fixture, recorder := evidenceDispatchFixture(t)
	recorder.err = errors.New("the evidence store is unavailable")

	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-unrecorded", "key-unrecorded", tapPayload()), "operator", "operator-1")
	if err == nil {
		t.Fatal("an outcome that could not be recorded was reported as success")
	}
	var failure *execution.EvidenceRecordError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want a typed evidence failure", err)
	}
	if failure.Operation == "" || failure.Reason == "" {
		t.Fatalf("evidence failure = %#v, want the operation and a redacted reason", failure)
	}
	if platformerrors.CodeOf(err) != platformerrors.CodeInternal {
		t.Fatalf("evidence failure code = %v, want internal", platformerrors.CodeOf(err))
	}
	if result.Outcome != action.OutcomeVerified {
		t.Fatalf("dispatch outcome = %q, want the action's own outcome to survive", result.Outcome)
	}
	if calls := fixture.transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want 1: an unrecorded outcome must not repeat the action", calls)
	}
}
