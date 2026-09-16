package execution_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Everything in this file runs against fakes: no process is spawned, no socket
// is opened, no SQLite database is created and no device is touched.

// --- fake kernel ------------------------------------------------------------

// fakeControl is a deterministic stand-in for ActionService. It records every
// call so a test can prove that a refused dispatch never reached the device
// boundary, and that a duplicate delivery never re-ran the action.
type fakeControl struct {
	mu sync.Mutex

	authorizeResult action.Result
	authorizeErr    error
	dispatchErr     error
	dispatchReplays bool

	calls       []string
	completions []action.Completion
}

func (f *fakeControl) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *fakeControl) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeControl) called(name string) bool {
	for _, call := range f.callNames() {
		if call == name {
			return true
		}
	}
	return false
}

func (f *fakeControl) Authorize(_ context.Context, intent action.Intent, _, _ string) (action.Result, error) {
	f.record("authorize")
	if f.authorizeErr != nil {
		return action.Result{}, f.authorizeErr
	}
	if f.authorizeResult.Attempt.ID != "" {
		return f.authorizeResult, nil
	}
	return action.Result{Attempt: action.Attempt{ID: intent.ID, State: action.AttemptAuthorized, Kind: intent.Kind}}, nil
}

func (f *fakeControl) Dispatch(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	f.record("dispatch")
	if f.dispatchErr != nil {
		return action.Result{Attempt: action.Attempt{ID: attemptID, State: action.AttemptFailed}}, f.dispatchErr
	}
	return action.Result{Attempt: action.Attempt{ID: attemptID, State: action.AttemptDispatched}, IdempotentReplay: f.dispatchReplays}, nil
}

func (f *fakeControl) Complete(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	f.record("complete")
	f.mu.Lock()
	f.completions = append(f.completions, completion)
	f.mu.Unlock()
	outcome := action.OutcomeFailed
	state := action.AttemptFailed
	if completion.Postcondition == action.PostconditionPassed {
		outcome, state = action.OutcomeVerified, action.AttemptVerified
	}
	return action.Result{
		Attempt:             action.Attempt{ID: completion.AttemptID, State: state, Postcondition: completion.Postcondition, FailureClass: completion.FailureClass},
		Outcome:             outcome,
		FailureClass:        completion.FailureClass,
		PostconditionPassed: completion.Postcondition == action.PostconditionPassed,
	}, nil
}

func (f *fakeControl) MarkIndeterminate(_ context.Context, completion action.Completion, _, _ string) (action.Result, error) {
	f.record("indeterminate")
	return action.Result{
		Attempt:          action.Attempt{ID: completion.AttemptID, State: action.AttemptIndeterminate, Postcondition: action.PostconditionUnknown, FailureClass: string(domain.FailureIndeterminate)},
		Outcome:          action.OutcomeIndeterminate,
		CleanupSucceeded: false,
	}, platformerrors.New(platformerrors.CodeIndeterminateCompletion, "completion is indeterminate")
}

func (f *fakeControl) Timeout(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	f.record("timeout")
	return action.Result{Attempt: action.Attempt{ID: attemptID, State: action.AttemptTimedOut}, Outcome: action.OutcomeTimedOut}, nil
}

func (f *fakeControl) Cancel(_ context.Context, _, attemptID, _ string, _ uint64, _, _ string) (action.Result, error) {
	f.record("cancel")
	return action.Result{Attempt: action.Attempt{ID: attemptID, State: action.AttemptCancelled}, Outcome: action.OutcomeCancelled}, nil
}

func (f *fakeControl) Cleanup(_ context.Context, _, attemptID, _, _ string, _ bool) (action.Result, error) {
	f.record("cleanup")
	return action.Result{Attempt: action.Attempt{ID: attemptID, State: action.AttemptVerified, Cleanup: action.CleanupSucceeded}}, nil
}

func (f *fakeControl) lastCompletion(t *testing.T) action.Completion {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.completions) == 0 {
		t.Fatal("no completion was recorded")
	}
	return f.completions[len(f.completions)-1]
}

func (f *fakeControl) lastFailureClass() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.completions) == 0 {
		return ""
	}
	return f.completions[len(f.completions)-1].FailureClass
}

// --- fake readiness probe and postcondition observer ------------------------

type fakeProbe struct {
	reason execution.RefusalReason
	err    error
	probes int
}

func (p *fakeProbe) ProbeControl(context.Context, execution.InputRequest) (execution.RefusalReason, error) {
	p.probes++
	return p.reason, p.err
}

type fakeObserver struct {
	observation  execution.PostconditionObservation
	err          error
	observations int
}

func (o *fakeObserver) ObservePostcondition(context.Context, action.Intent, execution.InputPayload) (execution.PostconditionObservation, error) {
	o.observations++
	return o.observation, o.err
}

// --- request builders -------------------------------------------------------

const (
	inputSerial          = testSerial
	inputWorkspace       = "input-workspace"
	inputDevice          = "device-alpha"
	inputLease           = "lease-alpha"
	inputHolder          = "holder-alpha"
	inputFencingToken    = 7
	inputObservation     = "observation-1"
	inputPostObservation = "observation-2"
)

func tapPayload() execution.InputPayload {
	return execution.InputPayload{Tap: &execution.TapRequest{Point: execution.Point{X: 540, Y: 960}, Space: testRenderSpace()}}
}

func swipePayload() execution.InputPayload {
	return execution.InputPayload{Swipe: &execution.SwipeRequest{
		Start:      execution.Point{X: 540, Y: 1600},
		End:        execution.Point{X: 540, Y: 400},
		DurationMS: 300,
		Space:      testRenderSpace(),
	}}
}

func keyEventPayload() execution.InputPayload {
	return execution.InputPayload{KeyEvent: &execution.KeyEventRequest{KeyCode: 4, Repeat: 1}}
}

func textPayload() execution.InputPayload {
	return execution.InputPayload{Text: &execution.TextReference{Handle: "clipboard-1", Length: uint32(len(typedValueFixture))}}
}

func launchPayload() execution.InputPayload {
	return execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "com.example.app"}}
}

func inputRequest(id, key string, payload execution.InputPayload) execution.InputRequest {
	return execution.InputRequest{
		IntentID:          id,
		Workspace:         inputWorkspace,
		DeviceID:          inputDevice,
		Serial:            inputSerial,
		LeaseID:           inputLease,
		HolderID:          inputHolder,
		FencingToken:      inputFencingToken,
		IdempotencyKey:    key,
		ObservationToken:  inputObservation,
		InvocationSurface: action.SurfaceManual,
		ApprovalGranted:   true,
		Timeout:           30 * time.Second,
		Payload:           payload,
	}
}

type dispatchFixture struct {
	control    *fakeControl
	probe      *fakeProbe
	observer   *fakeObserver
	transport  *fakeDeviceTransport
	resolver   *fakeResolver
	dispatcher *execution.InputDispatcher
}

func newDispatchFixture(t *testing.T) dispatchFixture {
	t.Helper()
	control := &fakeControl{}
	probe := &fakeProbe{}
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: inputPostObservation}}
	transport := newFakeDeviceTransport()
	resolver := &fakeResolver{value: typedValueFixture}
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, transport, resolver)
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	return dispatchFixture{control: control, probe: probe, observer: observer, transport: transport, resolver: resolver, dispatcher: dispatcher}
}

// --- refusals ---------------------------------------------------------------

// TestInputDispatcherRefusesEveryCaseDistinctlyWithZeroDeviceCalls is the
// central safety assertion of the slice: each refusal case is reported with its
// own reason and failure class, none falls through to a generic internal error,
// and not one of them reaches the device transport.
func TestInputDispatcherRefusesEveryCaseDistinctlyWithZeroDeviceCalls(t *testing.T) {
	cases := []struct {
		name         string
		probe        execution.RefusalReason
		authorizeErr error
		wantReason   execution.RefusalReason
		wantCode     platformerrors.Code
		wantClass    domain.FailureClass
	}{
		{
			name:       "expired lease",
			probe:      execution.RefusalLeaseExpired,
			wantReason: execution.RefusalLeaseExpired,
			wantCode:   platformerrors.CodeLeaseConflict,
			wantClass:  domain.FailureLeaseConflict,
		},
		{
			name:       "stale fencing token",
			probe:      execution.RefusalFenceStale,
			wantReason: execution.RefusalFenceStale,
			wantCode:   platformerrors.CodeLeaseConflict,
			wantClass:  domain.FailureLeaseConflict,
		},
		{
			name:       "no control session",
			probe:      execution.RefusalNoControlSession,
			wantReason: execution.RefusalNoControlSession,
			wantCode:   platformerrors.CodeLeaseConflict,
			wantClass:  domain.FailureLeaseConflict,
		},
		{
			name:       "device offline",
			probe:      execution.RefusalDeviceOffline,
			wantReason: execution.RefusalDeviceOffline,
			wantCode:   platformerrors.CodeUnavailable,
			wantClass:  domain.FailureDeviceOffline,
		},
		{
			name:       "device unauthorized",
			probe:      execution.RefusalDeviceUnauthorized,
			wantReason: execution.RefusalDeviceUnauthorized,
			wantCode:   platformerrors.CodeUnavailable,
			wantClass:  domain.FailureTransport,
		},
		{
			name:         "emergency stop engaged",
			authorizeErr: platformerrors.New(platformerrors.CodeEmergencyStopped, "emergency stop blocks dispatch"),
			wantReason:   execution.RefusalEmergencyStop,
			wantCode:     platformerrors.CodeEmergencyStopped,
			wantClass:    domain.FailureOperatorCancelled,
		},
		{
			name:         "policy denial",
			authorizeErr: platformerrors.New(platformerrors.CodePolicyDenied, "active policy denies this action"),
			wantReason:   execution.RefusalPolicyDenied,
			wantCode:     platformerrors.CodePolicyDenied,
			wantClass:    domain.FailurePolicyDenied,
		},
		{
			name:         "duplicate idempotency key for a different request",
			authorizeErr: platformerrors.New(platformerrors.CodeConflict, "idempotency key was reused with a different action"),
			wantReason:   execution.RefusalDuplicateIdempotencyKey,
			wantCode:     platformerrors.CodeConflict,
			wantClass:    domain.FailureInvalidTransition,
		},
	}

	reasons := make(map[execution.RefusalReason]string, len(cases))
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDispatchFixture(t)
			fixture.probe.reason = test.probe
			fixture.control.authorizeErr = test.authorizeErr

			result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-"+test.name, "key", tapPayload()), "operator", "operator-1")
			refusal, ok := execution.RefusalOf(err)
			if !ok {
				t.Fatalf("error = %v, want a typed refusal", err)
			}
			if refusal.Reason != test.wantReason {
				t.Fatalf("refusal reason = %q, want %q", refusal.Reason, test.wantReason)
			}
			if refusal.Code != test.wantCode {
				t.Fatalf("refusal code = %q, want %q", refusal.Code, test.wantCode)
			}
			if refusal.FailureClass != test.wantClass {
				t.Fatalf("refusal failure class = %q, want %q", refusal.FailureClass, test.wantClass)
			}
			if platformerrors.CodeOf(err) == platformerrors.CodeInternal {
				t.Fatalf("refusal %q fell through to a generic internal error: %v", refusal.Reason, err)
			}
			if result.Attempt.ID != "" || result.Outcome != "" {
				t.Fatalf("refused dispatch returned %#v, want an empty result", result)
			}
			// The whole point: a refused dispatch never touches the device.
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("refused dispatch issued %d device calls, want 0", calls)
			}
			if fixture.observer.observations != 0 {
				t.Fatalf("refused dispatch observed the device %d times, want 0", fixture.observer.observations)
			}
			if fixture.control.called("complete") || fixture.control.called("cleanup") {
				t.Fatalf("refused dispatch ran kernel transitions %q, want none", fixture.control.callNames())
			}
			if fixture.probe.probes != 1 {
				t.Fatalf("readiness probes = %d, want exactly 1", fixture.probe.probes)
			}
		})
	}

	// Pairwise distinct: no two refusal cases collapse into the same reason, and
	// each carries its own message.
	for _, test := range cases {
		if existing, ok := reasons[test.wantReason]; ok {
			t.Fatalf("refusal reason %q is shared by %q and %q", test.wantReason, existing, test.name)
		}
		reasons[test.wantReason] = test.name
	}
	if len(reasons) != len(cases) {
		t.Fatalf("distinct refusal reasons = %d, want %d", len(reasons), len(cases))
	}
}

func TestInputDispatcherRefusesAnIncompleteOrMismatchedPayload(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload execution.InputPayload
	}{
		{name: "absent payload", payload: execution.InputPayload{}},
		{
			name:    "two members of different kinds",
			payload: execution.InputPayload{Tap: tapPayload().Tap, KeyEvent: keyEventPayload().KeyEvent},
		},
		{
			name:    "swipe with an unbounded duration",
			payload: execution.InputPayload{Swipe: &execution.SwipeRequest{Start: execution.Point{X: 1, Y: 1}, End: execution.Point{X: 2, Y: 2}, DurationMS: 0, Space: testRenderSpace()}},
		},
		{
			name:    "text payload with an unbounded reference",
			payload: execution.InputPayload{Text: &execution.TextReference{Handle: "clipboard-1", Length: 0}},
		},
		{
			name:    "launch payload with a malformed package",
			payload: execution.InputPayload{Launch: &execution.LaunchAppRequest{PackageName: "not a package"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDispatchFixture(t)
			request := inputRequest("attempt-incomplete", "key-incomplete", test.payload)
			_, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1")
			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("payload refusal code = %v, want invalid_input", platformerrors.CodeOf(err))
			}
			if fixture.control.called("authorize") {
				t.Fatal("an incomplete payload reached the kernel's authorize step")
			}
			if fixture.transport.invocationCount() != 0 {
				t.Fatalf("incomplete payload issued %d device calls, want 0", fixture.transport.invocationCount())
			}
		})
	}
}

// --- the successful path ----------------------------------------------------

// TestInputDispatcherDispatchesEachInputKindThroughTheKernel proves each of the
// five typed inputs runs the whole contract: authorize, dispatch, one narrow
// device call, a fresh postcondition, completion and cleanup.
func TestInputDispatcherDispatchesEachInputKindThroughTheKernel(t *testing.T) {
	cases := []struct {
		name        string
		payload     execution.InputPayload
		target      action.SemanticTarget
		wantKind    action.Kind
		wantArgs    []string
		observation execution.PostconditionObservation
	}{
		{
			name:        "tap",
			payload:     tapPayload(),
			wantKind:    action.Tap,
			wantArgs:    []string{"shell", "input", "tap", "540", "960"},
			observation: execution.PostconditionObservation{Token: inputPostObservation},
		},
		{
			name:        "swipe",
			payload:     swipePayload(),
			wantKind:    action.Swipe,
			wantArgs:    []string{"shell", "input", "swipe", "540", "1600", "540", "400", "300"},
			observation: execution.PostconditionObservation{Token: inputPostObservation},
		},
		{
			name:        "key event",
			payload:     keyEventPayload(),
			wantKind:    action.KeyEvent,
			wantArgs:    []string{"shell", "input", "keyevent", "4"},
			observation: execution.PostconditionObservation{Token: inputPostObservation},
		},
		{
			name:        "typed text by reference",
			payload:     textPayload(),
			target:      action.SemanticTarget{ResourceID: "composer"},
			wantKind:    action.TextInput,
			wantArgs:    []string{"shell", "input", "text", typedValueFixture},
			observation: execution.PostconditionObservation{Token: inputPostObservation, FieldLength: len(typedValueFixture)},
		},
		{
			name:        "app launch",
			payload:     launchPayload(),
			wantKind:    action.LaunchApp,
			wantArgs:    []string{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"},
			observation: execution.PostconditionObservation{Token: inputPostObservation, ForegroundPackage: "com.example.app"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDispatchFixture(t)
			fixture.observer.observation = test.observation
			request := inputRequest("attempt-"+test.name, "key-"+test.name, test.payload)
			request.Target = test.target

			result, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if result.Outcome != action.OutcomeVerified || !result.PostconditionPassed {
				t.Fatalf("result = %#v, want verified with a passed postcondition", result)
			}
			if calls := fixture.transport.invocationCount(); calls != 1 {
				t.Fatalf("device calls = %d, want exactly 1", calls)
			}
			matchArgs(t, fixture.transport.invocation(0).args, test.wantArgs...)
			if got := fixture.transport.invocation(0).serial; got != inputSerial {
				t.Fatalf("device call serial = %q, want %q", got, inputSerial)
			}
			completion := fixture.control.lastCompletion(t)
			if completion.Postcondition != action.PostconditionPassed {
				t.Fatalf("completion postcondition = %q, want passed", completion.Postcondition)
			}
			if completion.ObservationToken != inputPostObservation {
				t.Fatalf("completion observation token = %q, want the fresh observation", completion.ObservationToken)
			}
			if completion.AttemptID != request.IntentID || completion.LeaseID != inputLease || completion.FencingToken != inputFencingToken {
				t.Fatalf("completion = %#v, want the authorized lease tuple", completion)
			}
			for _, want := range []string{"authorize", "dispatch", "complete", "cleanup"} {
				if !fixture.control.called(want) {
					t.Fatalf("kernel calls = %q, want %q", fixture.control.callNames(), want)
				}
			}
		})
	}
}

// --- postcondition evaluation ----------------------------------------------

// TestInputDispatcherEvaluatesTheCatalogPostcondition proves the declared
// postcondition is actually evaluated after the call, and that a satisfied or
// unsatisfied one is reported as such instead of as success.
func TestInputDispatcherEvaluatesTheCatalogPostcondition(t *testing.T) {
	cases := []struct {
		name        string
		payload     execution.InputPayload
		target      action.SemanticTarget
		observation execution.PostconditionObservation
		observerErr error
		wantPost    action.PostconditionState
		wantOutcome action.Outcome
		wantCode    platformerrors.Code
	}{
		{
			name:        "a tap with no observable effect fails its postcondition",
			payload:     tapPayload(),
			observation: execution.PostconditionObservation{Token: inputObservation},
			wantPost:    action.PostconditionFailed,
			wantOutcome: action.OutcomeFailed,
			wantCode:    platformerrors.CodeStaleObservation,
		},
		{
			name:        "a launch that lands on another package fails its postcondition",
			payload:     launchPayload(),
			observation: execution.PostconditionObservation{Token: inputPostObservation, ForegroundPackage: "com.somewhere.else"},
			wantPost:    action.PostconditionFailed,
			wantOutcome: action.OutcomeFailed,
			wantCode:    platformerrors.CodeInvalidInput,
		},
		{
			name:        "typed text that does not carry the referenced length fails its postcondition",
			payload:     textPayload(),
			target:      action.SemanticTarget{ResourceID: "composer"},
			observation: execution.PostconditionObservation{Token: inputPostObservation, FieldLength: 1},
			wantPost:    action.PostconditionFailed,
			wantOutcome: action.OutcomeFailed,
			wantCode:    platformerrors.CodeInvalidInput,
		},
		{
			name:        "an observation that cannot be taken leaves the postcondition unknown",
			payload:     tapPayload(),
			observation: execution.PostconditionObservation{},
			observerErr: context.DeadlineExceeded,
			wantPost:    action.PostconditionUnknown,
			wantOutcome: action.OutcomeIndeterminate,
			wantCode:    platformerrors.CodeIndeterminateCompletion,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDispatchFixture(t)
			fixture.observer.observation = test.observation
			fixture.observer.err = test.observerErr
			request := inputRequest("attempt-postcondition", "key-postcondition", test.payload)
			request.Target = test.target

			result, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1")
			if !fixture.control.called("complete") && !fixture.control.called("indeterminate") {
				t.Fatalf("kernel calls = %q, want a completion or indeterminate transition", fixture.control.callNames())
			}
			if result.Outcome != test.wantOutcome {
				t.Fatalf("outcome = %q, want %q (err=%v)", result.Outcome, test.wantOutcome, err)
			}
			if result.Outcome == action.OutcomeVerified {
				t.Fatal("an unsatisfied postcondition was reported as success")
			}
			if result.Attempt.Postcondition != test.wantPost {
				t.Fatalf("postcondition = %q, want %q", result.Attempt.Postcondition, test.wantPost)
			}
			if test.wantPost == action.PostconditionFailed {
				if failure := fixture.control.lastFailureClass(); failure != string(domain.FailurePostcondition) {
					t.Fatalf("failure class = %q, want %q", failure, string(domain.FailurePostcondition))
				}
			}
			if calls := fixture.transport.invocationCount(); calls != 1 {
				t.Fatalf("device calls = %d, want exactly 1", calls)
			}
		})
	}
}

// --- duplicate delivery and cancellation -----------------------------------

func TestInputDispatcherReturnsThePriorResultForDuplicateDelivery(t *testing.T) {
	fixture := newDispatchFixture(t)
	fixture.control.authorizeResult = action.Result{
		Attempt:             action.Attempt{ID: "attempt-original", State: action.AttemptVerified, Postcondition: action.PostconditionPassed},
		Outcome:             action.OutcomeVerified,
		PostconditionPassed: true,
		IdempotentReplay:    true,
	}
	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-duplicate", "key-shared", tapPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if result.Attempt.ID != "attempt-original" || result.Outcome != action.OutcomeVerified {
		t.Fatalf("duplicate result = %#v, want the recorded attempt", result)
	}
	if !result.IdempotentReplay {
		t.Fatal("duplicate result was not marked as a replay")
	}
	if fixture.transport.invocationCount() != 0 {
		t.Fatalf("duplicate delivery issued %d device calls, want 0", fixture.transport.invocationCount())
	}
	if fixture.control.called("dispatch") {
		t.Fatalf("duplicate delivery re-dispatched: %q", fixture.control.callNames())
	}
}

// signallingTransport wraps the recording transport and closes a channel when
// the in-flight device call returns, so a test can prove the call itself was
// stopped rather than merely abandoned by its waiter.
type signallingTransport struct {
	inner  *fakeDeviceTransport
	exited chan struct{}
	once   sync.Once
}

func (s *signallingTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	defer s.once.Do(func() { close(s.exited) })
	return s.inner.RunDeviceCommand(ctx, serial, args)
}

// TestInputDispatcherStopsTheInFlightCallOnCancellation proves cancellation
// reaches the device call itself: the call is blocked on its context, and it
// returns only because that context ended.
func TestInputDispatcherStopsTheInFlightCallOnCancellation(t *testing.T) {
	control := &fakeControl{}
	probe := &fakeProbe{}
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: inputPostObservation}}
	inner := newFakeDeviceTransport()
	never := make(chan struct{})
	signalled := &signallingTransport{inner: inner, exited: make(chan struct{})}
	entered := make(chan struct{})
	inner.mu.Lock()
	inner.blockOn = never
	inner.onCall = func(int, func()) { close(entered) }
	inner.mu.Unlock()
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, signalled, &fakeResolver{value: typedValueFixture})
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	defer func() { _ = dispatcher.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result action.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := dispatcher.Run(ctx, inputRequest("attempt-cancel", "key-cancel", tapPayload()), "operator", "operator-1")
		done <- outcome{result: result, err: runErr}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the device call was never entered")
	}
	cancel()

	select {
	case <-signalled.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the caller did not stop the in-flight device call")
	}
	select {
	case got := <-done:
		if inner.invocationCount() != 1 {
			t.Fatalf("device calls = %d, want exactly 1", inner.invocationCount())
		}
		if got.result.Outcome == action.OutcomeVerified {
			t.Fatalf("cancelled dispatch = %#v, want a non-success outcome", got.result)
		}
		if !control.called("cancel") && !control.called("timeout") && !control.called("indeterminate") {
			t.Fatalf("cancelled dispatch ran kernel transitions %q, want a cancellation-class transition", control.callNames())
		}
		if !control.called("cleanup") {
			t.Fatalf("kernel calls = %q, want cleanup after a cancelled action", control.callNames())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatcher did not return after cancellation")
	}
}

// --- the allow-list admission, proved against the real adapter --------------

// TestTheRealAdapterAdmitsTheArgumentArraysThePrimitivesBuild closes the loop
// between the two gates: the arrays the input primitives emit are exactly the
// arrays the ADB allow-list admits, and they run through the real adapter with
// the serial as its own token.
func TestTheRealAdapterAdmitsTheArgumentArraysThePrimitivesBuild(t *testing.T) {
	runner := adb.NewFakeRunner().RespondDefault(adb.FakeResponse{})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", runner, adb.WithOperationTimeout(time.Second))
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	cases := []struct {
		name     string
		build    func(*execution.Inputs) error
		wantOp   string
		wantArgs []string
	}{
		{
			name: "tap",
			build: func(inputs *execution.Inputs) error {
				return inputs.Tap(context.Background(), execution.TapRequest{Point: execution.Point{X: 540, Y: 960}, Space: testRenderSpace()})
			},
			wantArgs: []string{"shell", "input", "tap", "540", "960"},
		},
		{
			name: "swipe",
			build: func(inputs *execution.Inputs) error {
				return inputs.Swipe(context.Background(), execution.SwipeRequest{Start: execution.Point{X: 1, Y: 2}, End: execution.Point{X: 3, Y: 4}, DurationMS: 300, Space: testRenderSpace()})
			},
			wantArgs: []string{"shell", "input", "swipe", "1", "2", "3", "4", "300"},
		},
		{
			name: "key event",
			build: func(inputs *execution.Inputs) error {
				return inputs.KeyEvent(context.Background(), execution.KeyEventRequest{KeyCode: 4, Repeat: 1})
			},
			wantArgs: []string{"shell", "input", "keyevent", "4"},
		},
		{
			name: "typed text by reference",
			build: func(inputs *execution.Inputs) error {
				return inputs.TypeText(context.Background(), execution.TypeTextRequest{Text: execution.TextReference{Handle: "clipboard-1", Length: uint32(len(typedValueFixture))}})
			},
			wantArgs: []string{"shell", "input", "text", typedValueFixture},
		},
		{
			name: "app launch by package",
			build: func(inputs *execution.Inputs) error {
				return inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app"})
			},
			wantArgs: []string{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"},
		},
		{
			name: "app launch by component",
			build: func(inputs *execution.Inputs) error {
				return inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app", ActivityName: ".MainActivity"})
			},
			wantArgs: []string{"shell", "am", "start", "-n", "com.example.app/.MainActivity"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := len(runner.Invocations())
			inputs := newInputs(t, execution.NewADBInputTransport(adapter), &fakeResolver{value: typedValueFixture})
			if err := test.build(inputs); err != nil {
				t.Fatalf("primitive: %v", err)
			}
			invocations := runner.Invocations()
			if len(invocations) != before+1 {
				t.Fatalf("invocations = %d, want %d", len(invocations), before+1)
			}
			argv := invocations[len(invocations)-1].Args
			if len(argv) < 3 || argv[0] != "-s" || argv[1] != testSerial {
				t.Fatalf("argv = %q, want the serial as its own token", argv)
			}
			matchArgs(t, argv[2:], test.wantArgs...)
		})
	}
}
