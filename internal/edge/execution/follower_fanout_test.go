package execution

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/leases"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// --- fakes ------------------------------------------------------------------

// fanoutFleet is a fixed fleet reading: the ONLINE reading, stated directly so a
// case asserts the behaviour of the fan-out rather than of the store beneath it.
type fanoutFleet struct {
	devices map[string]FleetDevice
	err     error
}

func (f fanoutFleet) Fleet(context.Context, string) (map[string]FleetDevice, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.devices, nil
}

type fanoutControl struct {
	mu       sync.Mutex
	opened   int
	acquired []string
	refuse   map[string]error
}

func (c *fanoutControl) OpenSession(context.Context, string, string, string, string) (leases.ControlSessionID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened++
	return leases.ControlSessionID("session-1"), nil
}

func (c *fanoutControl) AcquireLease(_ context.Context, _ string, deviceID string, _ leases.ControlSessionID, _ string, _, _ string) (leases.DeviceLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, refused := c.refuse[deviceID]; refused {
		return leases.DeviceLease{}, err
	}
	c.acquired = append(c.acquired, deviceID)
	return leases.DeviceLease{ID: leases.DeviceLeaseID("lease-" + deviceID), DeviceID: "device", FencingToken: 7}, nil
}

func (c *fanoutControl) CloseSession(context.Context, string, leases.ControlSessionID, string, string) {
}

// fanoutDispatch is the kernel stand-in. It records every request it was given,
// so a case can assert what each follower actually received - and, in particular,
// that no follower was given a coordinate in a frame that was not the operator's.
type fanoutDispatch struct {
	mu       sync.Mutex
	requests map[string]InputRequest
	results  map[string]action.Result
	errors   map[string]error
	block    map[string]chan struct{}
}

func newFanoutDispatch() *fanoutDispatch {
	return &fanoutDispatch{
		requests: map[string]InputRequest{},
		results:  map[string]action.Result{},
		errors:   map[string]error{},
		block:    map[string]chan struct{}{},
	}
}

func (d *fanoutDispatch) Run(ctx context.Context, request InputRequest, _, _ string) (action.Result, error) {
	d.mu.Lock()
	d.requests[request.DeviceID] = request
	blocked := d.block[request.DeviceID]
	result, hasResult := d.results[request.DeviceID]
	runErr, hasErr := d.errors[request.DeviceID]
	d.mu.Unlock()
	if blocked != nil {
		select {
		case <-blocked:
		case <-ctx.Done():
			return action.Result{}, ctx.Err()
		}
	}
	if hasErr {
		return action.Result{}, runErr
	}
	if hasResult {
		return result, nil
	}
	return action.Result{Attempt: action.Attempt{ID: "attempt-" + request.DeviceID}, Outcome: action.OutcomeVerified}, nil
}

func (d *fanoutDispatch) received(deviceID string) (InputRequest, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	request, ok := d.requests[deviceID]
	return request, ok
}

func (d *fanoutDispatch) receivedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.requests)
}

// inlineStarter is a run starter that executes each follower's run as it is
// accepted, so a case can assert the per-follower outcomes directly.
type inlineStarter struct {
	fanout   *FollowerFanout
	outcomes []FollowerInputOutcome
	jobs     []FollowerInputJob
}

func (s *inlineStarter) Start(ctx context.Context, job FollowerInputJob) error {
	s.jobs = append(s.jobs, job)
	s.outcomes = append(s.outcomes, s.fanout.Execute(ctx, job))
	return nil
}

func fanoutIDSource() FollowerFanoutIDSource { return fixedFanoutID{} }

type fixedFanoutID struct{}

func (fixedFanoutID) NewID() (string, error) { return "run-1", nil }

func testFanout(t *testing.T, fleet map[string]FleetDevice, control *fanoutControl, dispatch *fanoutDispatch, starter FollowerRunStarter) *FollowerFanout {
	t.Helper()
	fanout, err := NewFollowerFanout(fanoutFleet{devices: fleet}, control, dispatch, fanoutIDSource(), WithRunStarter(starter))
	if err != nil {
		t.Fatalf("the fan-out could not be constructed: %v", err)
	}
	return fanout
}

func tapRequest(deviceID string) FollowerFanoutRequest {
	return FollowerFanoutRequest{
		Workspace:         "workspace-1",
		SourceDeviceID:    "source",
		FollowerDeviceIDs: []string{deviceID},
		HolderID:          "operator-1",
		RequestID:         "request-1",
		ObservationToken:  "stream-source",
		Payload: InputPayload{Tap: &TapRequest{
			Point: Point{X: 100, Y: 200},
			Space: RenderSpace{Width: 1080, Height: 1920, ObservationToken: "stream-source"},
		}},
	}
}

func outcomeFor(t *testing.T, rows []FollowerInputOutcome, deviceID string) FollowerInputOutcome {
	t.Helper()
	for _, row := range rows {
		if row.DeviceID == deviceID {
			return row
		}
	}
	t.Fatalf("no outcome row names device %q; rows were %+v", deviceID, rows)
	return FollowerInputOutcome{}
}

// --- per-follower isolation -------------------------------------------------

// TestFollowerFanoutOneFollowerFailureLeavesEveryOtherFollowerResultUnchanged is
// the case this whole surface exists for: three followers, one of which refuses
// while the other two are dispatched, and each row reports its OWN outcome.
//
// It bites in both directions. A fan-out that swallowed the failing follower's
// failure - reporting it as delivered, or reporting it behind an aggregate
// sentence - fails here, and so does one that let the failure change another
// follower's result: the two healthy rows carry their own attempt identities and
// their own verified outcomes, and nothing about the refused follower appears in
// them.
func TestFollowerFanoutOneFollowerFailureLeavesEveryOtherFollowerResultUnchanged(t *testing.T) {
	fleet := map[string]FleetDevice{
		"device-a": {Online: true, Serial: "serial-a"},
		"device-b": {Online: true, Serial: "serial-b"},
		"device-c": {Online: true, Serial: "serial-c"},
	}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	// The middle follower is refused BY ITS OWN LEASE, which is the refusal a real
	// fleet produces most often: the device is under another operator's control.
	control.refuse = map[string]error{
		"device-b": refusalFor(RefusalLeaseNotHeld, nil),
	}
	starter := &inlineStarter{}
	fanout := testFanout(t, fleet, control, dispatch, starter)
	starter.fanout = fanout

	request := tapRequest("device-a")
	request.FollowerDeviceIDs = []string{"device-a", "device-b", "device-c"}
	report, err := fanout.FanOut(context.Background(), request)
	if err != nil {
		t.Fatalf("the fan-out could not be attempted: %v", err)
	}
	if report.TargetCount != 3 {
		t.Fatalf("the run targeted %d followers, want 3", report.TargetCount)
	}

	rows := starter.outcomes
	if len(rows) != 3 {
		t.Fatalf("the run produced %d outcome rows, want one per follower", len(rows))
	}
	refused := outcomeFor(t, rows, "device-b")
	if refused.Disposition != FollowerInputRefused {
		t.Fatalf("the refused follower reported %q, want %q: a swallowed failure must not read as a delivered one",
			refused.Disposition, FollowerInputRefused)
	}
	if refused.Reason != FollowerReasonLeaseRefused {
		t.Fatalf("the refused follower reported reason %q, want its own lease refusal", refused.Reason)
	}
	if refused.RefusalReason != RefusalLeaseNotHeld {
		t.Fatalf("the refused follower reported refusal reason %q, want the kernel's own %q", refused.RefusalReason, RefusalLeaseNotHeld)
	}

	for _, deviceID := range []string{"device-a", "device-c"} {
		row := outcomeFor(t, rows, deviceID)
		if row.Disposition != FollowerInputAccepted || row.Outcome != action.OutcomeVerified {
			t.Fatalf("follower %s reported %q/%q, want its own delivered, verified outcome", deviceID, row.Disposition, row.Outcome)
		}
		if row.AttemptID != "attempt-"+deviceID {
			t.Fatalf("follower %s reported attempt %q, want its own", deviceID, row.AttemptID)
		}
		if row.RefusalReason == RefusalLeaseNotHeld || row.Reason == FollowerReasonLeaseRefused {
			t.Fatalf("follower %s carries another follower's refusal: one follower's outcome changed another's", deviceID)
		}
	}
	// The refused follower reached no device at all, and the two healthy followers
	// each reached their own.
	if _, sent := dispatch.received("device-b"); sent {
		t.Fatal("a follower whose lease was refused still reached a device")
	}
	if dispatch.receivedCount() != 2 {
		t.Fatalf("%d devices were dispatched to, want the 2 followers whose leases were held", dispatch.receivedCount())
	}
}

// --- the candidate set ------------------------------------------------------

// TestFollowerFanoutNamesAnExcludedFollowerAndCountsOnlyTheSetItTargeted pins the
// two halves of the candidate set at once: a follower that is not online is NAMED
// with a reason of its own and counted as neither contacted nor failed, and the
// count the operator reads is what the run TARGETED rather than what was selected.
func TestFollowerFanoutNamesAnExcludedFollowerAndCountsOnlyTheSetItTargeted(t *testing.T) {
	fleet := map[string]FleetDevice{
		"device-a": {Online: true, Serial: "serial-a"},
		"device-b": {Online: true, Serial: "serial-b"},
		// Registered, and the plane does not read it as ONLINE.
		"device-away": {},
		// Never recorded at all: absent from the registry the reading is made from.
	}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	starter := &inlineStarter{}
	fanout := testFanout(t, fleet, control, dispatch, starter)
	starter.fanout = fanout

	request := tapRequest("device-a")
	request.FollowerDeviceIDs = []string{"device-a", "device-away", "device-ghost", "device-b"}
	report, err := fanout.FanOut(context.Background(), request)
	if err != nil {
		t.Fatalf("the fan-out could not be attempted: %v", err)
	}

	if report.TargetCount != 2 {
		t.Fatalf("the run reported %d targeted followers, want the 2 it actually targeted", report.TargetCount)
	}
	if report.Named() != 4 {
		t.Fatalf("the report named %d followers, want all 4 the operator selected: nothing may be dropped", report.Named())
	}
	if report.Delivered() != 2 {
		t.Fatalf("the acceptance reported %d delivered followers, want 2", report.Delivered())
	}

	away := outcomeFor(t, report.Followers, "device-away")
	if away.Disposition != FollowerInputExcluded || away.Reason != FollowerReasonNotOnline {
		t.Fatalf("the offline follower reported %q/%q, want its own exclusion", away.Disposition, away.Reason)
	}
	ghost := outcomeFor(t, report.Followers, "device-ghost")
	if ghost.Disposition != FollowerInputExcluded || ghost.Reason != FollowerReasonNotRegistered {
		t.Fatalf("the unrecorded follower reported %q/%q, want its own exclusion", ghost.Disposition, ghost.Reason)
	}
	if ghost.Reason == away.Reason {
		t.Fatal("an unrecorded device and an offline one reported one reason: they are fixed differently")
	}
	for _, row := range report.Followers {
		if row.Reason.Valid() == false || !row.Disposition.Valid() {
			t.Fatalf("row %+v is outside the plane's own vocabulary", row)
		}
		if row.DeviceID == "device-away" || row.DeviceID == "device-ghost" {
			if row.Disposition == FollowerInputAccepted {
				t.Fatal("a follower the run never contacted was counted as having received the gesture")
			}
		}
	}
	// Nothing was dispatched for either excluded follower, so neither was counted
	// against as a device that failed.
	if dispatch.receivedCount() != 2 {
		t.Fatalf("%d devices were dispatched to, want only the 2 online followers", dispatch.receivedCount())
	}
	// The run opens one control session per follower it actually contacts, so a
	// follower that was never a candidate spends nothing.
	if control.opened != 2 {
		t.Fatalf("%d control sessions were opened, want one per targeted follower", control.opened)
	}
}

// --- coordinates ------------------------------------------------------------

// TestFollowerFanoutRefusesAFollowerWhoseRenderSpaceDoesNotReconcile pins the
// coordinate decision: every follower is given the SOURCE's declared frame
// unchanged - no follower is handed a coordinate that was scaled into its own size
// - and a follower that does not present at that frame is refused and NAMED with
// both sizes rather than guessed at.
func TestFollowerFanoutRefusesAFollowerWhoseRenderSpaceDoesNotReconcile(t *testing.T) {
	fleet := map[string]FleetDevice{
		"device-same":  {Online: true, Serial: "serial-same"},
		"device-other": {Online: true, Serial: "serial-other"},
	}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	// The plane's own render-space gate refusing the second follower: it presents
	// at a different size, so the point cannot be carried to it.
	dispatch.errors["device-other"] = platformerrors.Wrap(platformerrors.CodePreconditionFailed,
		"the declared render space does not match the device render size",
		&RenderSpaceMismatchError{
			Declared: RenderSpace{Width: 1080, Height: 1920, ObservationToken: "stream-source"},
			Device:   DeviceRenderSize{Width: 720, Height: 1280, Provenance: RenderSizeFromOverride, ObservedAt: time.Now().UTC()},
		})
	starter := &inlineStarter{}
	fanout := testFanout(t, fleet, control, dispatch, starter)
	starter.fanout = fanout

	request := tapRequest("device-same")
	request.FollowerDeviceIDs = []string{"device-same", "device-other"}
	report, err := fanout.FanOut(context.Background(), request)
	if err != nil {
		t.Fatalf("the fan-out could not be attempted: %v", err)
	}
	if report.TargetCount != 2 {
		t.Fatalf("the run targeted %d followers, want both", report.TargetCount)
	}

	for _, deviceID := range []string{"device-same", "device-other"} {
		sent, ok := dispatch.received(deviceID)
		if !ok {
			t.Fatalf("follower %s was never dispatched to", deviceID)
		}
		if sent.Payload.Tap == nil {
			t.Fatalf("follower %s received no typed tap payload", deviceID)
		}
		// The frame each follower received is the OPERATOR's, unchanged. A
		// coordinate scaled into the follower's own frame would be a point nobody
		// pointed at, and this is the assertion that refuses it.
		if got := sent.Payload.Tap.Space; got.Width != 1080 || got.Height != 1920 {
			t.Fatalf("follower %s received render space %dx%d, want the operator's own 1080x1920",
				deviceID, got.Width, got.Height)
		}
		if sent.Payload.Tap.Point.X != 100 || sent.Payload.Tap.Point.Y != 200 {
			t.Fatalf("follower %s received point (%d,%d), want the operator's own (100,200)",
				deviceID, sent.Payload.Tap.Point.X, sent.Payload.Tap.Point.Y)
		}
		if !sent.Payload.Tap.Space.valid() {
			t.Fatalf("follower %s received a frame that is not a render space", deviceID)
		}
	}

	rows := starter.outcomes
	same := outcomeFor(t, rows, "device-same")
	if same.Disposition != FollowerInputAccepted {
		t.Fatalf("the follower whose frame reconciles reported %q, want delivered", same.Disposition)
	}
	other := outcomeFor(t, rows, "device-other")
	if other.Reason != FollowerReasonFrameNotReconciled {
		t.Fatalf("the follower whose frame does not reconcile reported reason %q, want %q", other.Reason, FollowerReasonFrameNotReconciled)
	}
	if other.Disposition != FollowerInputRefused {
		t.Fatalf("a follower whose frames cannot be reconciled reported %q, want refused", other.Disposition)
	}
	// The row names both sizes, which is what lets the operator tell which frame
	// is wrong, and it carries the source's frame as the one that was declared.
	for _, want := range []string{"1080x1920", "720x1280"} {
		if !strings.Contains(other.Detail, want) {
			t.Fatalf("the refusal %q does not name %q, so an operator cannot see which frame is wrong", other.Detail, want)
		}
	}
	if other.Frame.Width != 1080 || other.Frame.Height != 1920 {
		t.Fatalf("the refused follower's row names frame %dx%d, want the frame that was declared to it",
			other.Frame.Width, other.Frame.Height)
	}
}

// valid reports whether this render space carries a bounded size: a payload whose
// frame is missing would be refused before the kernel, so a case that asserts on
// the frame's contents also asserts that it is one.
func (s RenderSpace) valid() bool { return s.Width > 0 && s.Height > 0 && s.ObservationToken != "" }

// --- the gesture this plane will not carry ----------------------------------

// TestFollowerFanoutRefusesATypedTextGesturePerFollower says plainly what a
// gesture carrying operator content does at the followers: each one is refused
// with its own reason, and the content is never carried to N devices by a value
// that may be released exactly once.
func TestFollowerFanoutRefusesATypedTextGesturePerFollower(t *testing.T) {
	fleet := map[string]FleetDevice{
		"device-a": {Online: true, Serial: "serial-a"},
		"device-b": {Online: true, Serial: "serial-b"},
	}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	starter := &inlineStarter{}
	fanout := testFanout(t, fleet, control, dispatch, starter)
	starter.fanout = fanout

	request := tapRequest("device-a")
	request.FollowerDeviceIDs = []string{"device-a", "device-b"}
	request.Payload = InputPayload{Text: &TextReference{Handle: "text-handle-1", Length: 5}}
	report, err := fanout.FanOut(context.Background(), request)
	if err != nil {
		t.Fatalf("the fan-out could not be attempted: %v", err)
	}
	if report.TargetCount != 0 {
		t.Fatalf("a gesture this plane does not carry reported %d targeted followers, want none", report.TargetCount)
	}
	if report.Named() != 2 {
		t.Fatalf("the report named %d followers, want both the operator selected", report.Named())
	}
	for _, row := range report.Followers {
		if row.Disposition != FollowerInputRefused || row.Reason != FollowerReasonKindNotFannable {
			t.Fatalf("a typed-text follower reported %q/%q, want a named refusal", row.Disposition, row.Reason)
		}
	}
	if dispatch.receivedCount() != 0 || control.opened != 0 {
		t.Fatal("a gesture this plane does not carry reached a device or spent control")
	}
}

// --- idempotency ------------------------------------------------------------

// TestFollowerFanoutKeysArePerFollowerPerAction proves a retried fan-out cannot
// double-apply on a follower that already took the action, and that one
// follower's key can never replay another's.
func TestFollowerFanoutKeysArePerFollowerPerAction(t *testing.T) {
	if got, want := FollowerIdempotencyKey("request-1", "device-a"), "request-1:follower:device-a"; got != want {
		t.Fatalf("follower key is %q, want %q", got, want)
	}
	if FollowerIdempotencyKey("request-1", "device-a") == FollowerIdempotencyKey("request-1", "device-b") {
		t.Fatal("two followers of one gesture share an idempotency key, so one device's action could replay the other's")
	}
	if FollowerIdempotencyKey("request-1", "device-a") == "request-1" {
		t.Fatal("a follower's key is the source's own key, so the fan-out could replay the source's action")
	}
	if got, want := FollowerIdempotencyKey(" request-1 ", " device-a "), "request-1:follower:device-a"; got != want {
		t.Fatalf("a padded request identity produced key %q, want %q", got, want)
	}

	fleet := map[string]FleetDevice{"device-a": {Online: true, Serial: "serial-a"}}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	starter := &inlineStarter{}
	fanout := testFanout(t, fleet, control, dispatch, starter)
	starter.fanout = fanout

	for attempt := 0; attempt < 2; attempt++ {
		request := tapRequest("device-a")
		request.FollowerDeviceIDs = []string{"device-a"}
		if _, err := fanout.FanOut(context.Background(), request); err != nil {
			t.Fatalf("attempt %d could not be attempted: %v", attempt+1, err)
		}
	}
	if len(starter.jobs) != 2 {
		t.Fatalf("two attempts produced %d runs, want one run per attempt", len(starter.jobs))
	}
	if starter.jobs[0].IdempotencyKey != starter.jobs[1].IdempotencyKey {
		t.Fatalf("a retried fan-out changed the follower's key (%q then %q), so a retry would double-apply",
			starter.jobs[0].IdempotencyKey, starter.jobs[1].IdempotencyKey)
	}
}

// --- the ordering -----------------------------------------------------------

// TestFollowerFanoutAnswersTheSourceWithoutWaitingForTheSlowestFollower pins the
// ordering decision: the followers proceed independently, so a follower whose
// action NEVER settles cannot hold the operator's gesture.
//
// The fake is the whole point of the case. A dispatcher that answered at once
// cannot catch a fan-out that waits, so this one blocks until the test releases
// it - and the fan-out still returns, having accepted the follower's run, while
// that run is still in flight.
func TestFollowerFanoutAnswersTheSourceWithoutWaitingForTheSlowestFollower(t *testing.T) {
	fleet := map[string]FleetDevice{"device-slow": {Online: true, Serial: "serial-slow"}}
	control := &fanoutControl{}
	dispatch := newFanoutDispatch()
	release := make(chan struct{})
	dispatch.block["device-slow"] = release
	executor, err := NewFollowerFanoutExecutor(FollowerFanoutConfig{
		Runner: nil,
		Sink:   &recordingSink{},
	})
	if err == nil || executor != nil {
		t.Fatal("an executor without the fan-out it runs through was constructed")
	}

	// The fan-out is built WITHOUT a starter and the executor is bound to it
	// afterwards, which is the order the composition root uses: the executor runs
	// work through the fan-out, and the fan-out hands work to the executor.
	fanout, err := NewFollowerFanout(fanoutFleet{devices: fleet}, control, dispatch, fanoutIDSource())
	if err != nil {
		t.Fatalf("the fan-out could not be constructed: %v", err)
	}
	executor, err = NewFollowerFanoutExecutor(FollowerFanoutConfig{Runner: fanout, Sink: &recordingSink{}})
	if err != nil {
		t.Fatalf("the executor could not be constructed: %v", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan FollowerFanoutExecutorOutcome, 1)
	go func() { done <- executor.Run(runCtx) }()
	// The executor accepts nothing until its own lifetime starts, so the case
	// waits for it in the same terms the composition root does rather than
	// assuming a goroutine has been scheduled.
	deadline := time.Now().Add(5 * time.Second)
	for {
		executor.mu.Lock()
		started := executor.started
		executor.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the executor never began carrying work")
		}
		time.Sleep(time.Millisecond)
	}
	// The executor is installed as the fan-out's run starter, so the fan-out
	// accepts work onto the bounded queue exactly as the plane does.
	if err := fanout.BindRunStarter(executor); err != nil {
		t.Fatalf("the executor could not be bound as the fan-out's run starter: %v", err)
	}
	if err := fanout.BindRunStarter(executor); err == nil {
		t.Fatal("the fan-out accepted a second run starter beside the one that is running its work")
	}

	request := tapRequest("device-slow")
	request.FollowerDeviceIDs = []string{"device-slow"}
	returned := make(chan FollowerFanoutReport, 1)
	go func() {
		report, _ := fanout.FanOut(context.Background(), request)
		returned <- report
	}()

	select {
	case report := <-returned:
		if report.TargetCount != 1 {
			t.Fatalf("the run targeted %d followers, want 1", report.TargetCount)
		}
		if report.Followers[0].Disposition != FollowerInputAccepted {
			t.Fatalf("the follower's run was not accepted while its action was still in flight: %+v", report.Followers[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fan-out did not answer while the follower's action was still in flight: the operator's gesture is gated on the slowest follower")
	}
	// The follower's own action is still running, which is what makes the
	// assertion above mean something: the fan-out answered while the worker was
	// inside the follower's dispatch, and that dispatch does not return until this
	// case releases it.
	sentDeadline := time.Now().Add(5 * time.Second)
	for {
		if _, sent := dispatch.received("device-slow"); sent {
			break
		}
		if time.Now().After(sentDeadline) {
			t.Fatal("the follower's action never started, so the case proved nothing")
		}
		time.Sleep(time.Millisecond)
	}
	if sink, ok := executor.sink.(*recordingSink); ok {
		sink.mu.Lock()
		recorded := len(sink.outcomes)
		sink.mu.Unlock()
		if recorded != 0 {
			t.Fatal("a follower's outcome was recorded while its action was still in flight")
		}
	}

	close(release)
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the executor did not stop")
	}
}

// recordingSink keeps the rows the executor records, so a case can assert what an
// operator would be able to read.
type recordingSink struct {
	mu       sync.Mutex
	outcomes []FollowerInputOutcome
	err      error
}

func (s *recordingSink) RecordFollowerInputOutcome(_ context.Context, _ FollowerInputJob, outcome FollowerInputOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes = append(s.outcomes, outcome)
	return s.err
}

// unboundFanout refuses to accept a gesture it has nowhere to put: a fan-out with
// no run starter would take the operator's gesture and silently do nothing with it.
func TestFollowerFanoutRefusesToAcceptWorkWithNowhereToPutIt(t *testing.T) {
	fanout, err := NewFollowerFanout(fanoutFleet{devices: map[string]FleetDevice{
		"device-a": {Online: true, Serial: "serial-a"},
	}}, &fanoutControl{}, newFanoutDispatch(), fanoutIDSource())
	if err != nil {
		t.Fatalf("the fan-out could not be constructed: %v", err)
	}
	request := tapRequest("device-a")
	request.FollowerDeviceIDs = []string{"device-a"}
	if _, err := fanout.FanOut(context.Background(), request); err == nil {
		t.Fatal("a fan-out with no run starter accepted a gesture, so nothing would have been dispatched and nothing would have said so")
	}
}
