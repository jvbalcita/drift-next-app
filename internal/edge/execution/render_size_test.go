package execution_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// --- cross-checking a declared render space against the device ---------------
//
// A coordinate is only meaningful against the render size the device actually
// presents at. The frame a caller declares is cross-checked against the device
// before a coordinate-bearing input executes: a frame the device does not
// present at is refused naming both sizes, a frame that cannot be checked at
// all is refused, and nothing is ever scaled from one frame into another.
//
// Every case below runs against fakes: no process is spawned, no socket is
// opened, and no device is touched.

const (
	// fakeDeviceOverrideWidth/fakeDeviceOverrideHeight are the sizes a scripted
	// device declares in its `wm size` output.
	fakeDeviceOverrideWidth  = 720
	fakeDeviceOverrideHeight = 1520
	fakeDevicePanelWidth     = 1080
	fakeDevicePanelHeight    = 2280
)

// wmSizeArgs is the read-only declaration read the render size comes from.
func wmSizeArgs() []string { return []string{"shell", "wm", "size"} }

func overrideAndPanelOutput() string {
	return fmt.Sprintf("Physical size: %dx%d\nOverride size: %dx%d\n",
		fakeDevicePanelWidth, fakeDevicePanelHeight, fakeDeviceOverrideWidth, fakeDeviceOverrideHeight)
}

func panelOnlyOutput() string {
	return fmt.Sprintf("Physical size: %dx%d\n", fakeDevicePanelWidth, fakeDevicePanelHeight)
}

// --- fakes ------------------------------------------------------------------

// movableClock is a deterministic clock whose instant a staleness case steps.
type movableClock struct {
	mu sync.Mutex
	at time.Time
}

func newMovableClock(at time.Time) *movableClock { return &movableClock{at: at.UTC()} }

func (c *movableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *movableClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(by)
}

// scriptedTransport answers an exact argument array and refuses anything it was
// not scripted for, so a case cannot pass because an answer was reused.
type scriptedTransport struct {
	mu       sync.Mutex
	answers  map[string]adb.Result
	failures map[string]error
	calls    [][]string
}

func newScriptedTransport() *scriptedTransport {
	return &scriptedTransport{answers: map[string]adb.Result{}, failures: map[string]error{}}
}

func scriptKey(args []string) string { return strings.Join(args, " ") }

func (s *scriptedTransport) answer(args []string, stdout string) *scriptedTransport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[scriptKey(args)] = adb.Result{Stdout: []byte(stdout)}
	delete(s.failures, scriptKey(args))
	return s
}

func (s *scriptedTransport) refuse(args []string, err error) *scriptedTransport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[scriptKey(args)] = err
	delete(s.answers, scriptKey(args))
	return s
}

func (s *scriptedTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	s.mu.Lock()
	key := scriptKey(args)
	s.calls = append(s.calls, append([]string(nil), args...))
	result, answered := s.answers[key]
	failure, refused := s.failures[key]
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return adb.Result{ExitCode: -1, Canceled: true}, err
	}
	switch {
	case refused:
		return adb.Result{ExitCode: -1}, failure
	case answered:
		return result, nil
	default:
		return adb.Result{ExitCode: -1}, fmt.Errorf("scripted transport has no answer for %q", key)
	}
}

func (s *scriptedTransport) invocations() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([][]string, len(s.calls))
	for index, call := range s.calls {
		copied[index] = append([]string(nil), call...)
	}
	return copied
}

func (s *scriptedTransport) countOf(args []string) int {
	count := 0
	for _, call := range s.invocations() {
		if scriptKey(call) == scriptKey(args) {
			count++
		}
	}
	return count
}

// newInputsReportingDeviceSize binds the primitives to a device whose render
// size the caller states explicitly.
func newInputsReportingDeviceSize(t *testing.T, transport execution.InputTransport, size execution.DeviceRenderSize) *execution.Inputs {
	t.Helper()
	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial,
		execution.WithRenderSizeSource(&fakeRenderSizes{size: size}))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}
	return inputs
}

func overrideRenderSize(width, height uint32) execution.DeviceRenderSize {
	return execution.DeviceRenderSize{
		Width: width, Height: height,
		Provenance: execution.RenderSizeFromOverride,
		ObservedAt: testDeviceRenderSize().ObservedAt,
	}
}

func newWmSizeReader(t *testing.T, transport execution.InputTransport, options ...execution.RenderSizeOption) *execution.WmSizeReader {
	t.Helper()
	reader, err := execution.NewWmSizeReader(transport, testSerial, options...)
	if err != nil {
		t.Fatalf("new render-size reader: %v", err)
	}
	return reader
}

// --- a frame that cannot be checked is never dispatched ----------------------

// The device's render size has never been resolved here, so the declared frame
// cannot be checked against the device at all. That is a refusal, not a licence
// to dispatch.
func TestCoordinateWithoutADeviceRenderSizeReadingFailsClosed(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial)
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}

	for name, run := range map[string]func() error{
		"tap": func() error {
			return inputs.Tap(context.Background(), execution.TapRequest{
				Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace(),
			})
		},
		"swipe": func() error {
			return inputs.Swipe(context.Background(), execution.SwipeRequest{
				Start: execution.Point{X: 5, Y: 5}, End: execution.Point{X: 6, Y: 6},
				DurationMS: 100, Space: testRenderSpace(),
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("a coordinate was dispatched with no device render-size reading")
			}
		})
	}

	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: an unverifiable coordinate frame was dispatched", got)
	}

	// A nil source is refused at construction: an input boundary must not be
	// buildable with a "no reading, never mind" option.
	if _, err := execution.NewInputs(newFakeDeviceTransport(), nil, testSerial, execution.WithRenderSizeSource(nil)); err == nil {
		t.Fatal("a nil device render-size source was accepted")
	}
}

// A device whose render size cannot be read fails closed: the frame is
// unverifiable, so nothing is dispatched on an assumption.
func TestAnUnreadableDeviceRenderSizeFailsClosed(t *testing.T) {
	cases := map[string]execution.DeviceRenderSize{
		"the source reports no size": {Provenance: execution.RenderSizeFromOverride, ObservedAt: testDeviceRenderSize().ObservedAt},
		"the source reports no provenance": {
			Width: testRenderWidth, Height: testRenderHeight, ObservedAt: testDeviceRenderSize().ObservedAt,
		},
		"the source reports no observation": {Width: testRenderWidth, Height: testRenderHeight, Provenance: execution.RenderSizeFromOverride},
		"the source reports a partial reading": {
			Width: testRenderWidth, Provenance: execution.RenderSizeFromOverride, ObservedAt: testDeviceRenderSize().ObservedAt,
		},
	}

	for name, size := range cases {
		t.Run(name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputsReportingDeviceSize(t, transport, size)

			err := inputs.Tap(context.Background(), execution.TapRequest{
				Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace(),
			})
			if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
				t.Fatalf("code = %v err = %v, want unavailable", platformerrors.CodeOf(err), err)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0: a coordinate with an unestablished device frame was dispatched", got)
			}
		})
	}
}

// A source that is asked and fails (an unreadable device) is not a licence to
// dispatch either.
func TestAFailingRenderSizeSourceFailsClosed(t *testing.T) {
	unreadable := &fakeRenderSizes{
		size: testDeviceRenderSize(),
		err:  platformerrors.New(platformerrors.CodeUnavailable, "the device did not report a render size"),
	}

	transport := newFakeDeviceTransport()
	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial,
		execution.WithRenderSizeSource(unreadable))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}

	err = inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace(),
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("code = %v err = %v, want unavailable", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: an unverifiable coordinate frame was dispatched", got)
	}
}

// --- a frame the device does not present at is refused -----------------------

// The legacy failure mode: a coordinate that is valid inside its declared frame
// and wrong on the device, because the frame is a downscaled screenshot or
// vision frame rather than the size the device presents at.
func TestCoordinateValidInItsDeclaredFrameButWrongOnTheDeviceIsRefused(t *testing.T) {
	cases := []struct {
		name            string
		device          execution.DeviceRenderSize
		declared        execution.RenderSpace
		point           execution.Point
		wantDeclaredIn  string
		wantDeviceInErr string
	}{
		{
			name:            "a downscaled vision frame",
			device:          overrideRenderSize(fakeDevicePanelWidth, fakeDevicePanelHeight),
			declared:        execution.RenderSpace{Width: 540, Height: 1140, ObservationToken: "observation-1"},
			point:           execution.Point{X: 539, Y: 1139},
			wantDeclaredIn:  "540x1140",
			wantDeviceInErr: "1080x2280",
		},
		{
			name:            "a coordinate outside the device but inside its frame",
			device:          overrideRenderSize(fakeDeviceOverrideWidth, fakeDeviceOverrideHeight),
			declared:        execution.RenderSpace{Width: 1080, Height: 2280, ObservationToken: "observation-1"},
			point:           execution.Point{X: 1000, Y: 2200},
			wantDeclaredIn:  "1080x2280",
			wantDeviceInErr: "720x1520",
		},
		{
			name:            "one dimension right and one wrong",
			device:          overrideRenderSize(1080, 2280),
			declared:        execution.RenderSpace{Width: 1080, Height: 1140, ObservationToken: "observation-1"},
			point:           execution.Point{X: 1000, Y: 1100},
			wantDeclaredIn:  "1080x1140",
			wantDeviceInErr: "1080x2280",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputsReportingDeviceSize(t, transport, testCase.device)

			err := inputs.Tap(context.Background(), execution.TapRequest{
				Point: testCase.point, Space: testCase.declared,
			})

			if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
				t.Fatalf("code = %v err = %v, want precondition_failed", platformerrors.CodeOf(err), err)
			}
			// The error names both sizes, so an operator can tell which is wrong.
			var mismatch *execution.RenderSpaceMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("the refusal is not a render-space mismatch: %v", err)
			}
			if !strings.Contains(err.Error(), testCase.wantDeclaredIn) {
				t.Fatalf("the refusal does not name the declared size %q: %v", testCase.wantDeclaredIn, err)
			}
			if !strings.Contains(err.Error(), testCase.wantDeviceInErr) {
				t.Fatalf("the refusal does not name the device size %q: %v", testCase.wantDeviceInErr, err)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0: a coordinate in a frame the device does not present at was dispatched", got)
			}
		})
	}
}

// A swipe is refused on the same terms as a tap: the frame belongs to the
// gesture, not to one of its endpoints.
func TestSwipeInAFrameTheDeviceDoesNotPresentAtIsRefused(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs := newInputsReportingDeviceSize(t, transport, overrideRenderSize(fakeDeviceOverrideWidth, fakeDeviceOverrideHeight))

	err := inputs.Swipe(context.Background(), execution.SwipeRequest{
		Start: execution.Point{X: 10, Y: 20}, End: execution.Point{X: 30, Y: 40},
		DurationMS: 250,
		Space:      execution.RenderSpace{Width: 1080, Height: 2280, ObservationToken: "observation-1"},
	})

	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("code = %v err = %v, want precondition_failed", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: a swipe in a frame the device does not present at was dispatched", got)
	}
}

// Inputs that carry no coordinate are unaffected: the cross-check is about the
// frame a coordinate travels with, and nothing else.
func TestInputsWithoutACoordinateDoNotNeedARenderSize(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial)
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}

	if err := inputs.KeyEvent(context.Background(), execution.KeyEventRequest{KeyCode: 4, Repeat: 1}); err != nil {
		t.Fatalf("key event: %v", err)
	}
	if err := inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app"}); err != nil {
		t.Fatalf("launch app: %v", err)
	}
	if err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-1", Length: uint32(len(typedValueFixture))},
		Workspace: textWorkspace,
	}); err != nil {
		t.Fatalf("type text: %v", err)
	}
	if got := transport.invocationCount(); got != 3 {
		t.Fatalf("transport invocations = %d, want 3", got)
	}
}

// --- the physical panel size is never mistaken for the override --------------

// A device that reports both sizes is checked against its OVERRIDE: the size it
// presents at, which is the only frame a coordinate can be valid in. The
// physical panel size is not a second, equally acceptable frame.
func TestTheDeviceOverrideWinsOverThePhysicalPanelSize(t *testing.T) {
	transport := newScriptedTransport().answer(wmSizeArgs(), overrideAndPanelOutput())

	// Bind the primitives to a real reader over the scripted device, so the
	// whole path is exercised: read, resolve, cross-check.
	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial,
		execution.WithRenderSizeSource(newWmSizeReader(t, transport)))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}
	transport.answer([]string{"shell", "input", "tap", "10", "20"}, "")

	// The override is the frame that dispatches at all.
	if err := inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 10, Y: 20},
		Space: execution.RenderSpace{Width: fakeDeviceOverrideWidth, Height: fakeDeviceOverrideHeight, ObservationToken: "observation-1"},
	}); err != nil {
		t.Fatalf("a tap in the device's override frame was refused: %v", err)
	}
	if got := transport.countOf(wmSizeArgs()); got != 1 {
		t.Fatalf("device render-size reads = %d, want 1", got)
	}

	// The physical panel size is not a frame the device presents at.
	err = inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 10, Y: 20},
		Space: execution.RenderSpace{Width: fakeDevicePanelWidth, Height: fakeDevicePanelHeight, ObservationToken: "observation-1"},
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("code = %v err = %v, want precondition_failed", platformerrors.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%dx%d", fakeDevicePanelWidth, fakeDevicePanelHeight)) ||
		!strings.Contains(err.Error(), fmt.Sprintf("%dx%d", fakeDeviceOverrideWidth, fakeDeviceOverrideHeight)) {
		t.Fatalf("the refusal does not name both sizes: %v", err)
	}
}

func TestWmSizeOutputIsResolvedToTheOverride(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantWidth  uint32
		wantHeight uint32
		want       execution.RenderSizeProvenance
		wantErr    bool
	}{
		{
			name: "override and physical", output: overrideAndPanelOutput(),
			wantWidth: fakeDeviceOverrideWidth, wantHeight: fakeDeviceOverrideHeight,
			want: execution.RenderSizeFromOverride,
		},
		{
			name: "physical only names itself", output: panelOnlyOutput(),
			wantWidth: fakeDevicePanelWidth, wantHeight: fakeDevicePanelHeight,
			want: execution.RenderSizeFromPhysical,
		},
		{name: "no size at all", output: "", wantErr: true},
		{name: "an unparsable size", output: "Physical size: large\n", wantErr: true},
		{name: "a zero size", output: "Override size: 0x1520\n", wantErr: true},
		{name: "a negative size", output: "Override size: -720x1520\n", wantErr: true},
		{name: "an unbounded size", output: "Override size: 100000x1520\n", wantErr: true},
		{name: "two different overrides", output: "Override size: 720x1520\nOverride size: 1080x2280\n", wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			size, err := execution.ParseWmSizeOutput([]byte(testCase.output))
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("an unreadable device render size was resolved to %dx%d", size.Width, size.Height)
				}
				if size.Width != 0 || size.Height != 0 {
					t.Fatalf("a refused reading still carried %dx%d", size.Width, size.Height)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", testCase.output, err)
			}
			if size.Width != testCase.wantWidth || size.Height != testCase.wantHeight {
				t.Fatalf("size = %dx%d, want %dx%d", size.Width, size.Height, testCase.wantWidth, testCase.wantHeight)
			}
			if size.Provenance != testCase.want {
				t.Fatalf("provenance = %q, want %q", size.Provenance, testCase.want)
			}
		})
	}
}

// The physical panel size must never come back labelled as an override: the
// provenance is what keeps the two apart, and a device with no override says so.
func TestAReadSizeIsNeverLabelledAsAnOverrideItDidNotDeclare(t *testing.T) {
	size, err := execution.ParseWmSizeOutput([]byte(panelOnlyOutput()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if size.Provenance == execution.RenderSizeFromOverride {
		t.Fatal("a device that declared no override reported the physical panel size as an override")
	}
	if size.Provenance != execution.RenderSizeFromPhysical {
		t.Fatalf("provenance = %q, want %q", size.Provenance, execution.RenderSizeFromPhysical)
	}
}

// --- staleness --------------------------------------------------------------

// The device's render size is resolved from the device and cached with an
// explicit freshness bound. A reading inside the bound is reused; a reading past
// it is re-taken, and a re-take that fails refuses rather than serving the old
// value, because a stale override is exactly the value that makes a coordinate
// wrong.
func TestAStaleRenderSizeReadingIsNeverServed(t *testing.T) {
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	now := newMovableClock(at)
	transport := newScriptedTransport().answer(wmSizeArgs(), overrideAndPanelOutput())
	reader := newWmSizeReader(t, transport,
		execution.WithRenderSizeClock(now),
		execution.WithRenderSizeMaxAge(time.Minute))

	first, err := reader.RenderSize(context.Background())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if first.Width != fakeDeviceOverrideWidth || first.Height != fakeDeviceOverrideHeight {
		t.Fatalf("first read = %dx%d, want the override %dx%d", first.Width, first.Height, fakeDeviceOverrideWidth, fakeDeviceOverrideHeight)
	}
	if first.Provenance != execution.RenderSizeFromOverride {
		t.Fatalf("provenance = %q, want %q", first.Provenance, execution.RenderSizeFromOverride)
	}
	if !first.ObservedAt.Equal(at) {
		t.Fatalf("observed at = %v, want %v", first.ObservedAt, at)
	}

	// Inside the freshness bound the reading is reused rather than re-taken.
	now.advance(30 * time.Second)
	reused, err := reader.RenderSize(context.Background())
	if err != nil {
		t.Fatalf("reused read: %v", err)
	}
	if reused.Width != fakeDeviceOverrideWidth || reused.Height != fakeDeviceOverrideHeight {
		t.Fatalf("reused read = %dx%d, want the override", reused.Width, reused.Height)
	}
	if got := transport.countOf(wmSizeArgs()); got != 1 {
		t.Fatalf("device reads within the freshness bound = %d, want 1", got)
	}

	// Past the bound the reader re-takes the reading, so a device whose override
	// moved is not checked against a size it no longer presents at.
	transport.answer(wmSizeArgs(), fmt.Sprintf("Physical size: %dx%d\nOverride size: %dx%d\n",
		fakeDevicePanelWidth, fakeDevicePanelHeight, fakeDevicePanelWidth, fakeDevicePanelHeight))
	now.advance(31 * time.Second)
	refreshed, err := reader.RenderSize(context.Background())
	if err != nil {
		t.Fatalf("refreshed read: %v", err)
	}
	if refreshed.Width != fakeDevicePanelWidth || refreshed.Height != fakeDevicePanelHeight {
		t.Fatalf("refreshed read = %dx%d, want %dx%d", refreshed.Width, refreshed.Height, fakeDevicePanelWidth, fakeDevicePanelHeight)
	}
	if got := transport.countOf(wmSizeArgs()); got != 2 {
		t.Fatalf("device reads after the freshness bound = %d, want 2", got)
	}

	// A device that stops answering must not have its last reading served: the
	// stale value is refused, and the refusal says how old it is.
	transport.refuse(wmSizeArgs(), errors.New("adb transport failed"))
	now.advance(time.Hour)
	_, err = reader.RenderSize(context.Background())
	if platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
		t.Fatalf("code = %v err = %v, want stale_observation", platformerrors.CodeOf(err), err)
	}
	if got := transport.countOf(wmSizeArgs()); got != 3 {
		t.Fatalf("device reads after a failed refresh = %d, want 3: the stale reading was served instead of re-taken", got)
	}

	// The refusal is not a one-off: the stale reading stays refused until a
	// fresh one succeeds.
	now.advance(time.Hour)
	if _, err := reader.RenderSize(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
		t.Fatalf("code = %v err = %v, want stale_observation on the second attempt", platformerrors.CodeOf(err), err)
	}
	transport.answer(wmSizeArgs(), overrideAndPanelOutput())
	fresh, err := reader.RenderSize(context.Background())
	if err != nil {
		t.Fatalf("read after the device recovered: %v", err)
	}
	if fresh.Width != fakeDeviceOverrideWidth || fresh.Height != fakeDeviceOverrideHeight {
		t.Fatalf("recovered read = %dx%d, want the override", fresh.Width, fresh.Height)
	}
}

// A stale reading reaches the caller as a refusal, not as a frame that happens
// to still be there.
func TestAStaleReadingDoesNotDispatchACoordinate(t *testing.T) {
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	now := newMovableClock(at)
	transport := newScriptedTransport().answer(wmSizeArgs(), overrideAndPanelOutput())
	reader := newWmSizeReader(t, transport,
		execution.WithRenderSizeClock(now),
		execution.WithRenderSizeMaxAge(time.Minute))

	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial,
		execution.WithRenderSizeSource(reader))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}

	// Establish a reading, then let it age past the bound while the device goes
	// quiet: the size is no longer trustworthy.
	if _, err := reader.RenderSize(context.Background()); err != nil {
		t.Fatalf("first read: %v", err)
	}
	transport.refuse(wmSizeArgs(), errors.New("adb transport failed"))
	now.advance(2 * time.Minute)

	err = inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 5, Y: 5},
		Space: execution.RenderSpace{Width: fakeDeviceOverrideWidth, Height: fakeDeviceOverrideHeight, ObservationToken: "observation-1"},
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeStaleObservation {
		t.Fatalf("code = %v err = %v, want stale_observation", platformerrors.CodeOf(err), err)
	}
	if got := transport.countOf([]string{"shell", "input", "tap", "5", "5"}); got != 0 {
		t.Fatalf("input invocations = %d, want 0: a coordinate was dispatched against a stale render size", got)
	}
}

// An unreadable device is reported, never resolved.
func TestAnUnreadableWmSizeOutputFailsClosed(t *testing.T) {
	cases := map[string]string{
		"no output":            "",
		"unparsable output":    "Override size: huge\n",
		"an unrelated command": "Error: no such service\n",
	}
	for name, stdout := range cases {
		t.Run(name, func(t *testing.T) {
			transport := newScriptedTransport().answer(wmSizeArgs(), stdout)
			reader := newWmSizeReader(t, transport)

			if _, err := reader.RenderSize(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
				t.Fatalf("code = %v err = %v, want unavailable", platformerrors.CodeOf(err), err)
			}
		})
	}

	// A device that refuses the read command, or that cannot be reached, is
	// unavailable rather than resolved to nothing.
	refusing := newScriptedTransport().refuse(wmSizeArgs(), errors.New("adb: device offline"))
	if _, err := newWmSizeReader(t, refusing).RenderSize(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeUnavailable {
		t.Fatalf("code = %v err = %v, want unavailable", platformerrors.CodeOf(err), err)
	}
}

// --- nothing is ever scaled --------------------------------------------------

// A coordinate is dispatched exactly as declared, or not at all: no frame is
// ever reconciled by scaling a point into it. A downscaled vision frame is the
// legacy failure mode, and the answer to it is refusal.
func TestNoCoordinateIsEverRescaledToTheDeviceSize(t *testing.T) {
	cases := []struct {
		name           string
		declaredWidth  uint32
		declaredHeight uint32
		deviceWidth    uint32
		deviceHeight   uint32
		wantDispatch   bool
	}{
		{name: "the declared frame is the device frame", declaredWidth: 1080, declaredHeight: 2280, deviceWidth: 1080, deviceHeight: 2280, wantDispatch: true},
		{name: "a downscaled vision frame", declaredWidth: 540, declaredHeight: 1140, deviceWidth: 1080, deviceHeight: 2280},
		{name: "an upscaled frame", declaredWidth: 2160, declaredHeight: 4560, deviceWidth: 1080, deviceHeight: 2280},
		{name: "one axis half the device", declaredWidth: 540, declaredHeight: 2280, deviceWidth: 1080, deviceHeight: 2280},
		{name: "a taller frame than the device", declaredWidth: 1080, declaredHeight: 4560, deviceWidth: 1080, deviceHeight: 2280},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputsReportingDeviceSize(t, transport, overrideRenderSize(testCase.deviceWidth, testCase.deviceHeight))

			point := execution.Point{X: 100, Y: 200}
			err := inputs.Tap(context.Background(), execution.TapRequest{
				Point: point,
				Space: execution.RenderSpace{
					Width: testCase.declaredWidth, Height: testCase.declaredHeight,
					ObservationToken: "observation-1",
				},
			})

			if !testCase.wantDispatch {
				if err == nil {
					t.Fatal("a coordinate in a frame the device does not present at was dispatched")
				}
				if got := transport.invocationCount(); got != 0 {
					t.Fatalf("transport invocations = %d, want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("a coordinate in the device's own frame was refused: %v", err)
			}
			if got := transport.invocationCount(); got != 1 {
				t.Fatalf("transport invocations = %d, want 1", got)
			}
			// The declared coordinate, verbatim: no multiple of it, no rounding
			// into another frame.
			matchArgs(t, transport.invocation(0).args, "shell", "input", "tap", "100", "200")
		})
	}
}

// The package builds argument arrays and comparisons, never a conversion: a
// function or type whose name promises to scale or convert a coordinate into
// another frame is exactly the code path that must not exist here.
func TestExecutionPackageHasNoCoordinateScalingPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	forbidden := []string{"scale", "rescale", "convert"}
	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.FuncDecl:
				reportScalingName(t, name, declaration.Name.Name, forbidden)
			case *ast.TypeSpec:
				reportScalingName(t, name, declaration.Name.Name, forbidden)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no package source was scanned")
	}
}

func reportScalingName(t *testing.T, file, name string, forbidden []string) {
	t.Helper()
	lowered := strings.ToLower(name)
	for _, needle := range forbidden {
		if strings.Contains(lowered, needle) {
			t.Fatalf("%s declares %q: a coordinate is refused in a frame it does not match, never scaled into it", file, name)
		}
	}
}
