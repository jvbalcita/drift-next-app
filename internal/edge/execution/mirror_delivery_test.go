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
)

// This file is the delivery half of the input path: the cases below prove that a
// device being mirrored has its typed input carried by the live session instead
// of by an `adb shell input` process, that a refused attempt reaches neither,
// and that a session that does not take the input is a refusal rather than a
// silent fall-back to the other transport.
//
// Every case runs against fakes: no process is spawned, no socket is opened, no
// device is touched.

// --- the fake live session --------------------------------------------------

// fakeTextDelivery is one typed-text value as a session received it. It exists
// so a test can prove the value travels this path, and that the assertions which
// report a leak never print it.
type fakeTextDelivery struct {
	deviceID string
	value    string
}

// fakeMirrorDelivery is a deterministic stand-in for a device's live mirror
// session. It records what it was asked to carry, so a test can tell "the
// session carried the input" from "an adb process ran", and it can be told to
// fail the way a session that ended mid-dispatch fails.
type fakeMirrorDelivery struct {
	mu       sync.Mutex
	mirrored map[string]bool
	inputs   []execution.MirrorDeliveryInput
	texts    []fakeTextDelivery
	inputErr error
	textErr  error
}

func newFakeMirrorDelivery(mirrored ...string) *fakeMirrorDelivery {
	delivery := &fakeMirrorDelivery{mirrored: make(map[string]bool, len(mirrored))}
	for _, deviceID := range mirrored {
		delivery.mirrored[deviceID] = true
	}
	return delivery
}

func (f *fakeMirrorDelivery) Mirrored(deviceID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mirrored[deviceID]
}

func (f *fakeMirrorDelivery) DeliverInput(ctx context.Context, input execution.MirrorDeliveryInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	f.inputs = append(f.inputs, input)
	return f.inputErr
}

func (f *fakeMirrorDelivery) DeliverText(ctx context.Context, deviceID string, _ execution.RenderSpace, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	f.texts = append(f.texts, fakeTextDelivery{deviceID: deviceID, value: value})
	return f.textErr
}

func (f *fakeMirrorDelivery) delivered() []execution.MirrorDeliveryInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execution.MirrorDeliveryInput(nil), f.inputs...)
}

func (f *fakeMirrorDelivery) textDeliveries() []fakeTextDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeTextDelivery(nil), f.texts...)
}

// --- the fixture ------------------------------------------------------------

type mirrorDispatchFixture struct {
	control    *fakeControl
	probe      *fakeProbe
	observer   *fakeObserver
	transport  *fakeDeviceTransport
	resolver   *fakeResolver
	mirror     *fakeMirrorDelivery
	dispatcher *execution.InputDispatcher
}

// newMirrorDispatchFixture is the composed dispatch path with a live-session
// delivery bound, over the same fakes every other dispatch case uses: the
// kernel, the readiness probe, the postcondition observer, the allow-listed
// transport and the render-size seam.
func newMirrorDispatchFixture(t *testing.T, delivery execution.MirrorDelivery, resolver *fakeResolver) mirrorDispatchFixture {
	t.Helper()
	fixture, _ := newMirrorEvidenceFixture(t, delivery, resolver)
	return fixture
}

// newMirrorEvidenceFixture is the same composition with the append-only evidence
// recorder bound, so a case can read the record the real contract would append
// as well as the transports the input did and did not travel.
func newMirrorEvidenceFixture(t *testing.T, delivery execution.MirrorDelivery, resolver *fakeResolver) (mirrorDispatchFixture, *recordingEvidence) {
	t.Helper()
	if resolver == nil {
		resolver = &fakeResolver{value: typedValueFixture}
	}
	control := &fakeControl{}
	probe := &fakeProbe{}
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: postToken}}
	transport := newFakeDeviceTransport()
	recorder := &recordingEvidence{}
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, transport, resolver,
		execution.WithRenderSizeSourceFactory(testRenderSizeSource),
		execution.WithMirrorDelivery(delivery),
		execution.WithEvidenceRecorder(recorder))
	if err != nil {
		t.Fatalf("new input dispatcher: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	mirror, _ := delivery.(*fakeMirrorDelivery)
	return mirrorDispatchFixture{control: control, probe: probe, observer: observer, transport: transport, resolver: resolver, mirror: mirror, dispatcher: dispatcher}, recorder
}

// typedTextRequest is a typed-text dispatch for the mirror path. Typed text
// names the field it is typed into, so the intent carries a semantic target: the
// action contract requires one for this kind, and the mirror does not change
// that.
func typedTextRequest(id, key string) execution.InputRequest {
	request := inputRequest(id, key, textPayload())
	request.Target = action.SemanticTarget{ResourceID: "search-field"}
	return request
}

// --- the delivery is chosen, and it is the session --------------------------

// TestALiveSessionCarriesTheTypedInputAndNoAdbProcessRuns is the core assertion
// of this slice: for a device being mirrored, the typed input reaches the device
// through the session - the write to an already-open control socket that was
// measured at 5-15 ms - and NOT through `adb shell input`, which spawns a
// process on the device per action (100-300 ms) and is the latency the mirror
// exists to avoid.
func TestALiveSessionCarriesTheTypedInputAndNoAdbProcessRuns(t *testing.T) {
	cases := []struct {
		name    string
		payload execution.InputPayload
		check   func(*testing.T, execution.MirrorDeliveryInput)
	}{
		{
			name:    "tap",
			payload: tapPayload(),
			check: func(t *testing.T, input execution.MirrorDeliveryInput) {
				if input.Kind != action.Tap {
					t.Fatalf("kind = %q, want %q", input.Kind, action.Tap)
				}
				if input.Point.X != 540 || input.Point.Y != 960 {
					t.Fatalf("point = (%d,%d), want (540,960)", input.Point.X, input.Point.Y)
				}
				if input.Frame.Width != testRenderWidth || input.Frame.Height != testRenderHeight {
					t.Fatalf("frame = %dx%d, want %dx%d - the frame the coordinate was measured in must travel with it", input.Frame.Width, input.Frame.Height, testRenderWidth, testRenderHeight)
				}
			},
		},
		{
			name:    "swipe",
			payload: swipePayload(),
			check: func(t *testing.T, input execution.MirrorDeliveryInput) {
				if input.Kind != action.Swipe {
					t.Fatalf("kind = %q, want %q", input.Kind, action.Swipe)
				}
				if input.Point.Y != 1600 || input.End.Y != 400 || input.DurationMS != 300 {
					t.Fatalf("swipe = (%d -> %d, %dms), want (1600 -> 400, 300ms)", input.Point.Y, input.End.Y, input.DurationMS)
				}
			},
		},
		{
			name:    "keyevent",
			payload: keyEventPayload(),
			check: func(t *testing.T, input execution.MirrorDeliveryInput) {
				if input.Kind != action.KeyEvent || input.KeyCode != 4 || input.Repeat != 1 {
					t.Fatalf("key event = %#v, want key code 4 repeated once", input)
				}
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMirrorDispatchFixture(t, newFakeMirrorDelivery(inputDevice), nil)
			result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-mirror-"+test.name, "key-mirror", test.payload), "operator", "operator-1")
			if err != nil {
				t.Fatalf("dispatch through the live session: %v", err)
			}
			if result.Outcome != action.OutcomeVerified || !result.PostconditionPassed {
				t.Fatalf("result = %#v, want verified with a passed postcondition", result)
			}
			delivered := fixture.mirror.delivered()
			if len(delivered) != 1 {
				t.Fatalf("the session carried %d inputs, want exactly 1", len(delivered))
			}
			test.check(t, delivered[0])
			if delivered[0].ObservationToken != obsToken {
				t.Fatalf("delivered observation = %q, want %q: the observation the kernel authorized has to travel with the input, because the session is the only place that can reconcile it with the stream it writes to (ARC-195)", delivered[0].ObservationToken, obsToken)
			}
			if delivered[0].DeviceID != inputDevice {
				t.Fatalf("delivered to %q, want %q", delivered[0].DeviceID, inputDevice)
			}
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("the dispatch ran %d adb command(s) (%v), want 0: the input must not spawn a process on the device", calls, fixture.transport.invocation(0).args)
			}
		})
	}
}

// TestADeviceWithNoLiveSessionKeepsTheAllowListedPath is the other half of the
// routing rule: with no session, nothing about the existing argv path changes.
// A mirror that is armed but has no viewer is not a device being captured, and
// its input must still work.
func TestADeviceWithNoLiveSessionKeepsTheAllowListedPath(t *testing.T) {
	fixture := newMirrorDispatchFixture(t, newFakeMirrorDelivery(), nil)
	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-no-session", "key-no-session", tapPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("dispatch with no live session: %v", err)
	}
	if result.Outcome != action.OutcomeVerified || !result.PostconditionPassed {
		t.Fatalf("result = %#v, want verified with a passed postcondition", result)
	}
	if delivered := fixture.mirror.delivered(); len(delivered) != 0 {
		t.Fatalf("a session with no mirror carried %d inputs, want 0", len(delivered))
	}
	if calls := fixture.transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	matchArgs(t, fixture.transport.invocation(0).args, "shell", "input", "tap", "540", "960")
}

// TestAKindASessionCannotCarryKeepsTheAllowListedPath: a session carries pointer
// gestures, typed text and key events. An app launch is a package-manager
// operation, and it must keep travelling the argv path even while the device is
// mirrored - a routing rule that "prefers the mirror" for every kind would
// silently drop the kinds the session cannot express.
func TestAKindASessionCannotCarryKeepsTheAllowListedPath(t *testing.T) {
	fixture := newMirrorDispatchFixture(t, newFakeMirrorDelivery(inputDevice), nil)
	// The launch kind's own postcondition is a foreground package, which this
	// fake does not report; what this case is about is which transport the
	// input travelled, so it asserts that and not the outcome.
	_, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-launch-mirrored", "key-launch", launchPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("dispatch a launch while mirrored: %v", err)
	}
	if delivered := fixture.mirror.delivered(); len(delivered) != 0 {
		t.Fatalf("the session carried %d inputs, want 0 for a launch", len(delivered))
	}
	if calls := fixture.transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}
	matchArgs(t, fixture.transport.invocation(0).args, "shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1")
}

// TestARefusedAttemptReachesNeitherTheSessionNorTheDevice is the safety
// assertion for the new path: scrcpy has a control channel of its own, and that
// must not make the policy kernel decorative. Every refusal the kernel already
// produces still reaches neither transport.
func TestARefusedAttemptReachesNeitherTheSessionNorTheDevice(t *testing.T) {
	cases := []struct {
		name         string
		probe        execution.RefusalReason
		authorizeErr error
	}{
		{name: "expired lease", probe: execution.RefusalLeaseExpired},
		{name: "stale fencing token", probe: execution.RefusalFenceStale},
		{name: "device offline", probe: execution.RefusalDeviceOffline},
		{name: "emergency stop engaged", authorizeErr: platformerrors.New(platformerrors.CodeEmergencyStopped, "emergency stop blocks dispatch")},
		{name: "policy denial", authorizeErr: platformerrors.New(platformerrors.CodePolicyDenied, "active policy denies this action")},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMirrorDispatchFixture(t, newFakeMirrorDelivery(inputDevice), nil)
			fixture.probe.reason = test.probe
			fixture.control.authorizeErr = test.authorizeErr

			if _, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-mirror-refused", "key-refused", tapPayload()), "operator", "operator-1"); err == nil {
				t.Fatal("a refused attempt returned no error")
			}
			if delivered := fixture.mirror.delivered(); len(delivered) != 0 {
				t.Fatalf("a refused attempt carried %d inputs to the session, want 0", len(delivered))
			}
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("a refused attempt ran %d adb command(s), want 0", calls)
			}
			if fixture.control.called("dispatch") {
				t.Fatal("a refused attempt reached the kernel's dispatch step")
			}
		})
	}
}

// TestACoordinateInAStaleFrameIsRefusedBeforeTheSessionIsAsked: the frame gate
// runs before the delivery, so a coordinate measured in a frame the device does
// not present at never reaches the session either.
func TestACoordinateInAStaleFrameIsRefusedBeforeTheSessionIsAsked(t *testing.T) {
	fixture := newMirrorDispatchFixture(t, newFakeMirrorDelivery(inputDevice), nil)
	stale := tapPayload()
	stale.Tap.Space.Width = testRenderWidth + 1
	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-stale-frame", "key-stale", stale), "operator", "operator-1")
	if err != nil {
		t.Fatalf("redispatch returned an error: %v", err)
	}
	if result.Outcome == action.OutcomeVerified {
		t.Fatal("a coordinate in a stale frame was reported as verified")
	}
	if class := fixture.control.lastFailureClass(); class != string(domain.FailureStaleObservation) {
		t.Fatalf("failure class = %q, want %q", class, domain.FailureStaleObservation)
	}
	if delivered := fixture.mirror.delivered(); len(delivered) != 0 {
		t.Fatalf("a stale-frame coordinate carried %d inputs to the session, want 0", len(delivered))
	}
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("a stale-frame coordinate ran %d adb command(s), want 0", calls)
	}
}

// --- a session that does not take the input --------------------------------

// TestASessionThatRefusesTheInputIsRefusedAndNotRetriedElsewhere: the two
// transports are not interchangeable to retry across. A session that ended
// between the routing decision and the write must produce a refusal, because
// re-sending the same tap down the argv path could deliver it twice to a device
// whose state already changed.
func TestASessionThatRefusesTheInputIsRefusedAndNotRetriedElsewhere(t *testing.T) {
	delivery := newFakeMirrorDelivery(inputDevice)
	delivery.inputErr = platformerrors.New(platformerrors.CodeUnavailable, "the live session ended before the input was carried")
	fixture := newMirrorDispatchFixture(t, delivery, nil)

	result, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-session-ended", "key-ended", tapPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("a session that did not carry the input returned an error rather than a failed attempt: %v", err)
	}
	if result.Outcome == action.OutcomeVerified || result.PostconditionPassed {
		t.Fatalf("an input the session did not carry was reported as %#v", result)
	}
	if class := fixture.control.lastFailureClass(); class != string(domain.FailureTransport) {
		t.Fatalf("failure class = %q, want %q", class, domain.FailureTransport)
	}
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("the refused input was retried as %d adb command(s), want 0", calls)
	}
	// The refusal is completed against a reading taken AFTER it, so the boundary
	// does take one - and that is the point: the kernel completes an attempt only
	// against a fresh observation, so an input refused before the device still
	// has to be read for, or its attempt cannot be completed at all. What must
	// not happen is the input being retried or reported as verified.
	if fixture.observer.observations != 1 {
		t.Fatalf("a refused input observed the device %d times, want exactly 1: the completion names the reading it was evaluated against", fixture.observer.observations)
	}
	if completion := fixture.control.lastCompletion(t); completion.ObservationToken != postToken {
		t.Fatalf("the refused input was completed against %q, want the fresh reading %q", completion.ObservationToken, postToken)
	}
	if completion := fixture.control.lastCompletion(t); completion.Postcondition != action.PostconditionFailed {
		t.Fatalf("the refused input was completed with postcondition %q, want %q: no device call was made, so the action did not happen", completion.Postcondition, action.PostconditionFailed)
	}
}

// TestAFrameRefusedByTheStreamIsClassifiedAsTheRenderSpaceGate: the session is
// the second gate on the coordinate frame, and its refusal must carry the
// render-space class. An operator reading "the transport failed" would look at
// the device, when what is stale is the frame their console is measuring in.
func TestAFrameRefusedByTheStreamIsClassifiedAsTheRenderSpaceGate(t *testing.T) {
	delivery := newFakeMirrorDelivery(inputDevice)
	delivery.inputErr = platformerrors.Wrap(platformerrors.CodePreconditionFailed, "the coordinate is refused because the stream is not encoded at the frame it was measured in", errors.New("frame mismatch"))
	fixture := newMirrorDispatchFixture(t, delivery, nil)

	_, err := fixture.dispatcher.Run(context.Background(), inputRequest("attempt-stream-frame", "key-stream-frame", tapPayload()), "operator", "operator-1")
	if err != nil {
		t.Fatalf("a frame the stream refused returned an error rather than a failed attempt: %v", err)
	}
	if class := fixture.control.lastFailureClass(); class != string(domain.FailureStaleObservation) {
		t.Fatalf("failure class = %q, want %q", class, domain.FailureStaleObservation)
	}
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("a frame-refused coordinate ran %d adb command(s), want 0", calls)
	}
}

// --- typed text -------------------------------------------------------------

// TestATypedTextValueTravelsTheSessionAndIsNeverHeld asserts the two halves of
// the typed-text rule on this path: the value reaches the session (it is the one
// input a session can carry that the argv path cannot express at all), and it is
// released into that one call - it never becomes an argument, an error, or a
// field the dispatch carries after the call.
func TestATypedTextValueTravelsTheSessionAndIsNeverHeld(t *testing.T) {
	fixture, recorder := newMirrorEvidenceFixture(t, newFakeMirrorDelivery(inputDevice), nil)
	// The text kind's postcondition is the addressed field's length read back
	// off the device, so the observer reports the length the value has.
	fixture.observer.observation.FieldLength = len(typedValueFixture)
	result, err := fixture.dispatcher.Run(context.Background(), typedTextRequest("attempt-text", "key-text"), "operator", "operator-1")
	if err != nil {
		t.Fatalf("dispatch typed text through the live session: %v", err)
	}
	if result.Outcome != action.OutcomeVerified {
		t.Fatalf("result = %#v, want verified", result)
	}
	texts := fixture.mirror.textDeliveries()
	if len(texts) != 1 {
		t.Fatalf("the session received %d typed-text values, want exactly 1", len(texts))
	}
	if texts[0].value != typedValueFixture {
		t.Fatal("the value the session received is not the value that was registered")
	}
	if texts[0].deviceID != inputDevice {
		t.Fatalf("typed text delivered to %q, want %q", texts[0].deviceID, inputDevice)
	}
	// Nothing about the value reaches a device command: this path runs none.
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("typed text ran %d adb command(s), want 0", calls)
	}
	if delivered := fixture.mirror.delivered(); len(delivered) != 0 {
		t.Fatalf("typed text was also delivered as %d coordinate or key input(s), want 0", len(delivered))
	}
	// The value reached the device through one call and nowhere else: it is not
	// in the append-only record that outlives the dispatch, in any field.
	assertNoTypedValueInRecords(t, recorder)
}

// TestAResolverFailureNeverQuotesTheValue: the resolver's own error can quote
// what it was asked to release, and this failure is rendered into evidence and
// operator surfaces. The refusal class names the reference rather than the
// transport - nothing was dispatched - and the value appears nowhere.
func TestAResolverFailureNeverQuotesTheValue(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("resolver could not release " + typedValueFixture)}
	fixture, recorder := newMirrorEvidenceFixture(t, newFakeMirrorDelivery(inputDevice), resolver)
	result, err := fixture.dispatcher.Run(context.Background(), typedTextRequest("attempt-text-failed", "key-text-failed"), "operator", "operator-1")
	if err != nil {
		t.Fatalf("a reference that could not be released returned an error rather than a failed attempt: %v", err)
	}
	if result.Outcome == action.OutcomeVerified {
		t.Fatalf("a value that could not be released was reported as %#v", result)
	}
	if class := fixture.control.lastFailureClass(); class != string(domain.FailureReferenceUnreleased) {
		t.Fatalf("failure class = %q, want %q", class, domain.FailureReferenceUnreleased)
	}
	if len(fixture.mirror.textDeliveries()) != 0 {
		t.Fatal("a value that could not be released reached the session")
	}
	if calls := fixture.transport.invocationCount(); calls != 0 {
		t.Fatalf("a value that could not be released ran %d adb command(s), want 0", calls)
	}
	assertNoTypedValueInRecords(t, recorder)
}

// TestASessionFailureOnTypedTextNeverQuotesTheValue: the session's own error can
// quote the value it was handed, so this path drops it exactly as the resolver
// path does, and the failure still names the reference rather than the transport.
func TestASessionFailureOnTypedTextNeverQuotesTheValue(t *testing.T) {
	delivery := newFakeMirrorDelivery(inputDevice)
	delivery.textErr = errors.New("the control socket rejected the text " + typedValueFixture)
	fixture, recorder := newMirrorEvidenceFixture(t, delivery, nil)
	result, err := fixture.dispatcher.Run(context.Background(), typedTextRequest("attempt-text-refused", "key-text-refused"), "operator", "operator-1")
	if err != nil {
		t.Fatalf("typed text the session did not carry returned an error rather than a failed attempt: %v", err)
	}
	if result.Outcome == action.OutcomeVerified {
		t.Fatalf("typed text the session did not carry was reported as %#v", result)
	}
	if class := fixture.control.lastFailureClass(); class != string(domain.FailureReferenceUnreleased) {
		t.Fatalf("failure class = %q, want %q", class, domain.FailureReferenceUnreleased)
	}
	assertNoTypedValueInRecords(t, recorder)
}

// assertNoTypedValueInRecords renders every field of every evidence record this
// dispatch appended and fails if the typed-text value appears in one. The
// assertion reports which surface leaked, never the value.
func assertNoTypedValueInRecords(t *testing.T, recorder *recordingEvidence) {
	t.Helper()
	records := recorder.all()
	if len(records) == 0 {
		t.Fatal("no evidence record was appended for the dispatch")
	}
	for index, record := range records {
		if rendered := fmt.Sprintf("%+v", record); strings.Contains(rendered, typedValueFixture) {
			t.Fatalf("evidence record %d carries the typed-text value; it must carry the handle and its length instead", index)
		}
	}
}

// --- the option itself ------------------------------------------------------

// TestDisablingTheSessionDeliveryIsTheOldPath: a deployment that arms no mirror
// is not an error, and every input then takes the path it took before the mirror
// existed. The option is refused only for a delivery the caller stated and left
// empty, which is a composition mistake rather than a deployment choice.
func TestDisablingTheSessionDeliveryIsTheOldPath(t *testing.T) {
	control := &fakeControl{}
	probe := &fakeProbe{}
	observer := &fakeObserver{observation: execution.PostconditionObservation{Token: postToken}}
	transport := newFakeDeviceTransport()
	dispatcher, err := execution.NewInputDispatcher(control, probe, observer, transport, &fakeResolver{value: typedValueFixture},
		execution.WithRenderSizeSourceFactory(testRenderSizeSource))
	if err != nil {
		t.Fatalf("new input dispatcher with no delivery: %v", err)
	}
	t.Cleanup(func() { _ = dispatcher.Close() })
	result, runErr := dispatcher.Run(context.Background(), inputRequest("attempt-no-delivery", "key-no-delivery", tapPayload()), "operator", "operator-1")
	if runErr != nil {
		t.Fatalf("dispatch with no delivery bound: %v", runErr)
	}
	if result.Outcome != action.OutcomeVerified {
		t.Fatalf("result = %#v, want verified", result)
	}
	if calls := transport.invocationCount(); calls != 1 {
		t.Fatalf("device calls = %d, want exactly 1", calls)
	}

	if _, err := execution.NewInputDispatcher(control, probe, observer, transport, nil, execution.WithMirrorDelivery(nil)); err == nil {
		t.Fatal("a nil live-session delivery was accepted as an option")
	}
}
