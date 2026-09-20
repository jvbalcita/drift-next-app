package execution_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// Every case below runs against fakes: no process is spawned, no socket is
// opened, and no device is touched.

const (
	// testSerial is the transport serial the primitives are bound to.
	testSerial = "mock-device-alpha"

	// typedValueFixture stands in for the value behind a typed-text reference.
	// It is never printed: the assertions that use it compare, and report only
	// which surface leaked, never the value.
	typedValueFixture = "typed-value-fixture-1"

	// textWorkspace is the scope a typed-text reference belongs to: a value is
	// only ever released into the workspace that registered it.
	textWorkspace = "workspace-1"

	// testRenderWidth/testRenderHeight are the `wm size` OVERRIDE the cases
	// declare, never a physical panel size.
	testRenderWidth  = 1080
	testRenderHeight = 2280
)

// testObservedAt is when the fake device's render size was resolved.
var testObservedAt = time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)

// testDeviceRenderSize is the size the fake device presents at: the OVERRIDE it
// declares, not a physical panel size.
func testDeviceRenderSize() execution.DeviceRenderSize {
	return execution.DeviceRenderSize{
		Width:      testRenderWidth,
		Height:     testRenderHeight,
		Provenance: execution.RenderSizeFromOverride,
		ObservedAt: testObservedAt,
	}
}

func testRenderSpace() execution.RenderSpace {
	return execution.RenderSpace{
		Width:            testRenderWidth,
		Height:           testRenderHeight,
		ObservationToken: "observation-1",
	}
}

// --- fakes -----------------------------------------------------------------

type fakeCall struct {
	serial string
	args   []string
}

// fakeDeviceTransport is a deterministic stand-in for the allow-listed device
// transport. It records the exact call it was given and answers with a scripted
// outcome, honouring the caller's context.
type fakeDeviceTransport struct {
	mu          sync.Mutex
	calls       []fakeCall
	result      adb.Result
	err         error
	blockOn     <-chan struct{}
	entered     chan struct{}
	onCall      func(callNumber int, cancel func())
	cancel      context.CancelFunc
	cancelAfter int
}

func newFakeDeviceTransport() *fakeDeviceTransport {
	return &fakeDeviceTransport{entered: make(chan struct{}, 32)}
}

func (f *fakeDeviceTransport) RunDeviceCommand(ctx context.Context, serial string, args []string) (adb.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{serial: serial, args: append([]string(nil), args...)})
	callNumber := len(f.calls)
	result, err, blockOn, cancelAfter := f.result, f.err, f.blockOn, f.cancelAfter
	f.mu.Unlock()

	select {
	case f.entered <- struct{}{}:
	default:
	}

	if f.onCall != nil {
		f.onCall(callNumber, f.cancel)
	}
	if cancelAfter > 0 && callNumber >= cancelAfter && f.cancel != nil {
		f.cancel()
	}

	if blockOn != nil {
		select {
		case <-blockOn:
		case <-ctx.Done():
			return adb.Result{ExitCode: -1, Canceled: true}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return adb.Result{ExitCode: -1, Canceled: true}, err
	}
	return result, err
}

func (f *fakeDeviceTransport) invocationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDeviceTransport) invocation(index int) fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.calls) {
		return fakeCall{}
	}
	return fakeCall{serial: f.calls[index].serial, args: append([]string(nil), f.calls[index].args...)}
}

// fakeResolver releases a scripted value for a handle. It records the handles it
// was asked for so a test can prove resolution happens only at dispatch.
type fakeResolver struct {
	mu      sync.Mutex
	value   string
	err     error
	handles []string
}

func (r *fakeResolver) Resolve(ctx context.Context, workspace string, reference execution.TextReference) (string, error) {
	r.mu.Lock()
	r.handles = append(r.handles, reference.Handle)
	value, err := r.value, r.err
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return value, nil
}

func (r *fakeResolver) resolvedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.handles)
}

func newInputs(t *testing.T, transport execution.InputTransport, resolver execution.TextResolver) *execution.Inputs {
	t.Helper()
	inputs, err := execution.NewInputs(transport, resolver, testSerial,
		execution.WithRenderSizeSource(&fakeRenderSizes{size: testDeviceRenderSize()}))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}
	return inputs
}

// fakeRenderSizes is a deterministic stand-in for the device's render size. It
// records how often it was asked, so a case can prove a reading was reused
// rather than re-taken.
type fakeRenderSizes struct {
	mu    sync.Mutex
	size  execution.DeviceRenderSize
	err   error
	reads int
}

func (f *fakeRenderSizes) RenderSize(ctx context.Context) (execution.DeviceRenderSize, error) {
	f.mu.Lock()
	f.reads++
	size, err := f.size, f.err
	f.mu.Unlock()
	if err != nil {
		return execution.DeviceRenderSize{}, err
	}
	if err := ctx.Err(); err != nil {
		return execution.DeviceRenderSize{}, err
	}
	return size, nil
}

func (f *fakeRenderSizes) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func matchArgs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argument array length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("argument %d = %q, want %q", index, got[index], want[index])
		}
	}
}

// --- each primitive issues the expected narrow call -------------------------

func TestEachInputPrimitiveIssuesTheExpectedNarrowCall(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, inputs *execution.Inputs)
		want [][]string
	}{
		{
			name: "tap",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.Tap(context.Background(), execution.TapRequest{
					Point: execution.Point{X: 10, Y: 20}, Space: testRenderSpace(),
				}); err != nil {
					t.Fatalf("tap: %v", err)
				}
			},
			want: [][]string{{"shell", "input", "tap", "10", "20"}},
		},
		{
			name: "swipe",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.Swipe(context.Background(), execution.SwipeRequest{
					Start: execution.Point{X: 10, Y: 20}, End: execution.Point{X: 30, Y: 40},
					DurationMS: 250, Space: testRenderSpace(),
				}); err != nil {
					t.Fatalf("swipe: %v", err)
				}
			},
			want: [][]string{{"shell", "input", "swipe", "10", "20", "30", "40", "250"}},
		},
		{
			name: "key event",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.KeyEvent(context.Background(), execution.KeyEventRequest{KeyCode: 4, Repeat: 1}); err != nil {
					t.Fatalf("key event: %v", err)
				}
			},
			want: [][]string{{"shell", "input", "keyevent", "4"}},
		},
		{
			name: "key event repeat",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.KeyEvent(context.Background(), execution.KeyEventRequest{KeyCode: 67, Repeat: 3}); err != nil {
					t.Fatalf("key event repeat: %v", err)
				}
			},
			want: [][]string{
				{"shell", "input", "keyevent", "67"},
				{"shell", "input", "keyevent", "67"},
				{"shell", "input", "keyevent", "67"},
			},
		},
		{
			name: "launch app with an activity",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{
					PackageName: "com.example.app", ActivityName: ".MainActivity",
				}); err != nil {
					t.Fatalf("launch app: %v", err)
				}
			},
			want: [][]string{{"shell", "am", "start", "-n", "com.example.app/.MainActivity"}},
		},
		{
			name: "launch app by package",
			run: func(t *testing.T, inputs *execution.Inputs) {
				if err := inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app"}); err != nil {
					t.Fatalf("launch app: %v", err)
				}
			},
			want: [][]string{{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"}},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

			testCase.run(t, inputs)

			if got := transport.invocationCount(); got != len(testCase.want) {
				t.Fatalf("transport invocations = %d, want %d", got, len(testCase.want))
			}
			for index, want := range testCase.want {
				call := transport.invocation(index)
				if call.serial != testSerial {
					t.Fatalf("call %d serial = %q, want %q", index, call.serial, testSerial)
				}
				matchArgs(t, call.args, want...)
			}
		})
	}
}

// Typed text is the one primitive whose payload is not expressible as an
// argument, so it is asserted separately: the value reaches the device once,
// and nowhere else.
func TestTypedTextIssuesTheExpectedNarrowCall(t *testing.T) {
	transport := newFakeDeviceTransport()
	resolver := &fakeResolver{value: typedValueFixture}
	inputs := newInputs(t, transport, resolver)

	if err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-1", Length: uint32(len(typedValueFixture))},
		Workspace: textWorkspace,
	}); err != nil {
		t.Fatalf("type text: %v", err)
	}
	if got := transport.invocationCount(); got != 1 {
		t.Fatalf("transport invocations = %d, want 1", got)
	}
	call := transport.invocation(0)
	if call.serial != testSerial {
		t.Fatalf("call serial = %q, want %q", call.serial, testSerial)
	}
	if len(call.args) != 4 {
		t.Fatalf("argument array length = %d, want 4", len(call.args))
	}
	matchArgs(t, call.args[:3], "shell", "input", "text")
	// The value is compared, never rendered: a failing assertion here reports
	// the position, not the content.
	if call.args[3] != typedValueFixture {
		t.Fatal("the released value did not reach the transport as the single text token")
	}
	if resolver.resolvedCount() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.resolvedCount())
	}
}

// --- render-space coordinates ----------------------------------------------

func TestCoordinateOutsideItsRenderSpaceIsRefused(t *testing.T) {
	space := testRenderSpace()
	cases := []struct {
		name string
		run  func(inputs *execution.Inputs) error
	}{
		{
			name: "tap at the render width",
			run: func(inputs *execution.Inputs) error {
				return inputs.Tap(context.Background(), execution.TapRequest{
					Point: execution.Point{X: testRenderWidth, Y: 10}, Space: space,
				})
			},
		},
		{
			name: "tap at the render height",
			run: func(inputs *execution.Inputs) error {
				return inputs.Tap(context.Background(), execution.TapRequest{
					Point: execution.Point{X: 10, Y: testRenderHeight}, Space: space,
				})
			},
		},
		{
			name: "swipe whose start leaves the render space",
			run: func(inputs *execution.Inputs) error {
				return inputs.Swipe(context.Background(), execution.SwipeRequest{
					Start: execution.Point{X: testRenderWidth, Y: 10}, End: execution.Point{X: 10, Y: 10},
					DurationMS: 200, Space: space,
				})
			},
		},
		{
			name: "swipe whose end leaves the render space",
			run: func(inputs *execution.Inputs) error {
				return inputs.Swipe(context.Background(), execution.SwipeRequest{
					Start: execution.Point{X: 10, Y: 10}, End: execution.Point{X: 10, Y: testRenderHeight},
					DurationMS: 200, Space: space,
				})
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

			err := testCase.run(inputs)

			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0: a coordinate outside its frame was dispatched", got)
			}
		})
	}
}

func TestCoordinateWithoutARenderSpaceIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		space execution.RenderSpace
	}{
		{name: "no render space at all", space: execution.RenderSpace{}},
		{
			name:  "a render space with no width",
			space: execution.RenderSpace{Height: testRenderHeight, ObservationToken: "observation-1"},
		},
		{
			name:  "a render space with no height",
			space: execution.RenderSpace{Width: testRenderWidth, ObservationToken: "observation-1"},
		},
		{
			name:  "a render space naming no observation",
			space: execution.RenderSpace{Width: testRenderWidth, Height: testRenderHeight},
		},
		{
			name:  "a render space larger than any panel",
			space: execution.RenderSpace{Width: 100000, Height: 100000, ObservationToken: "observation-1"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

			err := inputs.Tap(context.Background(), execution.TapRequest{
				Point: execution.Point{X: 1, Y: 1}, Space: testCase.space,
			})

			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0: a coordinate with no frame was dispatched", got)
			}
		})
	}
}

// A coordinate is never scaled from one frame into another. A frame that is
// half the device's render size is a different frame — the downscaled vision
// frame that bit the legacy product — so a coordinate measured in it is refused
// outright rather than doubled into the device's size.
func TestCoordinateIsNeverScaledFromAnotherFrame(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	// The device presents at testRenderSpace(); a downscaled 540x1140 frame is
	// not that frame, and a point measured in it is refused.
	downscaled := execution.RenderSpace{Width: 540, Height: 1140, ObservationToken: "observation-1"}
	err := inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 539, Y: 1139}, Space: downscaled,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodePreconditionFailed {
		t.Fatalf("code = %v err = %v, want precondition_failed", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: a coordinate was rescaled into the device's frame", got)
	}

	// A point outside the declared frame is refused without a device call to
	// make it look plausible.
	if err := inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 1080, Y: 10}, Space: testRenderSpace(),
	}); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: an out-of-frame point was dispatched", got)
	}
}

// --- typed text by reference ------------------------------------------------

// The value behind a typed-text reference must never appear in a request, an
// error, or any formatted output: those are logged, rendered and persisted.
func TestTypedTextNeverRendersItsValueAnywhere(t *testing.T) {
	leaks := func(t *testing.T, surface string, rendered string) {
		t.Helper()
		if strings.Contains(rendered, typedValueFixture) {
			t.Fatalf("%s rendered the typed value", surface)
		}
	}

	request := execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-1", Length: uint32(len(typedValueFixture))},
		Workspace: textWorkspace,
	}
	leaks(t, "the value form of a typed-text request", fmt.Sprintf("%v", request))
	leaks(t, "the debug form of a typed-text request", fmt.Sprintf("%+v", request))
	leaks(t, "the go-syntax form of a typed-text request", fmt.Sprintf("%#v", request))
	leaks(t, "the pointer form of a typed-text request", fmt.Sprintf("%v", &request))

	// A failing transport whose own diagnostics quote the value must not leak
	// it: typed text suppresses device detail entirely.
	failing := newFakeDeviceTransport()
	failing.result = adb.Result{ExitCode: 1, Stderr: []byte("input text " + typedValueFixture + " failed")}
	failing.err = fmt.Errorf("adb invocation failed: %s", typedValueFixture)
	inputs := newInputs(t, failing, &fakeResolver{value: typedValueFixture})

	err := inputs.TypeText(context.Background(), request)
	if err == nil {
		t.Fatal("a failing device text call reported success")
	}
	leaks(t, "the error from a failing typed-text call", err.Error())
	leaks(t, "the formatted error from a failing typed-text call", fmt.Sprintf("%v", err))
	leaks(t, "the deep-formatted error from a failing typed-text call", fmt.Sprintf("%+v", err))

	// A device that refuses the command and quotes the value in its own
	// diagnostics must not leak it either: typed text suppresses device detail
	// entirely, so a value is never echoed back through an error.
	refused := newFakeDeviceTransport()
	refused.result = adb.Result{ExitCode: 1, Stderr: []byte("Error: input text " + typedValueFixture + " is not allowed")}
	refusedInputs := newInputs(t, refused, &fakeResolver{value: typedValueFixture})
	refusedErr := refusedInputs.TypeText(context.Background(), request)
	if refusedErr == nil {
		t.Fatal("a device that refused the text reported success")
	}
	leaks(t, "the error from a device that refused the text", refusedErr.Error())
	leaks(t, "the deep-formatted error from a device that refused the text", fmt.Sprintf("%+v", refusedErr))

	// A resolver that refuses must not have its message echoed either: it may
	// quote the value it was asked to release.
	refusing := &fakeResolver{err: fmt.Errorf("cannot release %s", typedValueFixture)}
	refusingInputs := newInputs(t, newFakeDeviceTransport(), refusing)
	resolveErr := refusingInputs.TypeText(context.Background(), request)
	if resolveErr == nil {
		t.Fatal("an unresolvable text reference was accepted")
	}
	leaks(t, "the error from an unresolvable reference", resolveErr.Error())
	leaks(t, "the deep-formatted error from an unresolvable reference", fmt.Sprintf("%+v", resolveErr))

	// The reference handle is the only thing that may be named.
	if !strings.Contains(resolveErr.Error(), "value-ref-1") {
		t.Fatalf("the unresolvable reference error does not name the handle: %v", resolveErr)
	}
}

// The value is released only at dispatch: validation happens first, and a
// non-text primitive never touches the resolver.
func TestTypedTextIsResolvedOnlyAtDispatch(t *testing.T) {
	transport := newFakeDeviceTransport()
	resolver := &fakeResolver{value: typedValueFixture}
	inputs := newInputs(t, transport, resolver)

	if err := inputs.Tap(context.Background(), execution.TapRequest{
		Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace(),
	}); err != nil {
		t.Fatalf("tap: %v", err)
	}
	if got := resolver.resolvedCount(); got != 0 {
		t.Fatalf("resolver calls after a tap = %d, want 0", got)
	}

	// An invalid reference is refused before the resolver is asked for the value.
	if err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "not a handle", Length: 4},
		Workspace: textWorkspace,
	}); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
	}
	if got := resolver.resolvedCount(); got != 0 {
		t.Fatalf("resolver calls after an invalid reference = %d, want 0", got)
	}

	// A cancellation before dispatch means the value is never released.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := inputs.TypeText(ctx, execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-2", Length: uint32(len(typedValueFixture))},
		Workspace: textWorkspace,
	}); err == nil {
		t.Fatal("a cancelled typed-text call reported success")
	}
	if got := resolver.resolvedCount(); got != 0 {
		t.Fatalf("resolver calls after a cancelled call = %d, want 0", got)
	}
}

func TestTypedTextRefusesAValueItCannotCarryWithoutShellMeaning(t *testing.T) {
	values := map[string]string{
		"a semicolon":         "abc;rm -rf /",
		"a command separator": "abc && id",
		"a shell variable":    "abc$HOME",
		"a backtick":          "abc`id`",
		"a quote":             "abc'def",
		"a percent escape":    "abc%ss",
		"a newline":           "abc\ndef",
		"a non-ascii rune":    "abc\u00e9",
	}

	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: value})

			err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
				Text:      execution.TextReference{Handle: "value-ref-1", Length: uint32(len(value))},
				Workspace: textWorkspace,
			})

			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
			}
			if err != nil && strings.Contains(err.Error(), value) {
				t.Fatal("the refusal rendered the value it refused")
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0: an unrepresentable value was dispatched", got)
			}
		})
	}
}

func TestTypedTextRefusesAReferenceWhoseValueLengthDoesNotMatch(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-1", Length: uint32(len(typedValueFixture)) + 5},
		Workspace: textWorkspace,
	})
	if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0", got)
	}
}

// --- key events -------------------------------------------------------------

func TestKeyEventIsBounded(t *testing.T) {
	cases := []struct {
		name     string
		request  execution.KeyEventRequest
		wantCode platformerrors.Code
	}{
		{name: "no key code", request: execution.KeyEventRequest{Repeat: 1}, wantCode: platformerrors.CodeInvalidInput},
		{name: "an unbounded key code", request: execution.KeyEventRequest{KeyCode: 10001, Repeat: 1}, wantCode: platformerrors.CodeInvalidInput},
		{name: "no repeat count", request: execution.KeyEventRequest{KeyCode: 4}, wantCode: platformerrors.CodeInvalidInput},
		{name: "an unbounded repeat count", request: execution.KeyEventRequest{KeyCode: 4, Repeat: 1000}, wantCode: platformerrors.CodeInvalidInput},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

			err := inputs.KeyEvent(context.Background(), testCase.request)

			if platformerrors.CodeOf(err) != testCase.wantCode {
				t.Fatalf("code = %v err = %v, want %v", platformerrors.CodeOf(err), err, testCase.wantCode)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0", got)
			}
		})
	}
}

// --- app launch -------------------------------------------------------------

func TestLaunchAppRefusesCommandText(t *testing.T) {
	cases := []struct {
		name    string
		request execution.LaunchAppRequest
	}{
		{name: "a package with a command separator", request: execution.LaunchAppRequest{PackageName: "com.example.app; id"}},
		{name: "a package that is a shell invocation", request: execution.LaunchAppRequest{PackageName: "sh -c id"}},
		{name: "a package with a path traversal", request: execution.LaunchAppRequest{PackageName: "com.example.app/../evil"}},
		{name: "no package", request: execution.LaunchAppRequest{PackageName: ""}},
		{name: "an activity with a command separator", request: execution.LaunchAppRequest{PackageName: "com.example.app", ActivityName: "Main; id"}},
		{name: "an activity with a traversal", request: execution.LaunchAppRequest{PackageName: "com.example.app", ActivityName: "com.example.app..Main"}},
		{name: "an activity that is a shell invocation", request: execution.LaunchAppRequest{PackageName: "com.example.app", ActivityName: "sh -c id"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := newFakeDeviceTransport()
			inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

			err := inputs.LaunchApp(context.Background(), testCase.request)

			if platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
				t.Fatalf("code = %v err = %v, want invalid_input", platformerrors.CodeOf(err), err)
			}
			if got := transport.invocationCount(); got != 0 {
				t.Fatalf("transport invocations = %d, want 0", got)
			}
		})
	}
}

// --- cancellation and deadlines ---------------------------------------------

func TestCancellationIsHonouredMidCall(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	transport := newFakeDeviceTransport()
	transport.blockOn = blocked

	ctx, cancel := context.WithCancel(context.Background())
	transport.cancel = cancel
	transport.cancelAfter = 1
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	err := inputs.Tap(ctx, execution.TapRequest{Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace()})

	if platformerrors.CodeOf(err) != platformerrors.CodeCanceled {
		t.Fatalf("code = %v err = %v, want canceled", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 1 {
		t.Fatalf("transport invocations = %d, want 1", got)
	}
}

func TestCancellationBeforeDispatchIssuesNoCall(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := inputs.Tap(ctx, execution.TapRequest{Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace()})

	if platformerrors.CodeOf(err) != platformerrors.CodeCanceled {
		t.Fatalf("code = %v err = %v, want canceled", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0: a cancelled input was dispatched", got)
	}
}

// A hung transport call must not hang the caller: the primitive bounds the call
// with its own deadline when the caller supplies none.
func TestAHungInputCallDoesNotHangTheCaller(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	transport := newFakeDeviceTransport()
	transport.blockOn = blocked

	inputs, err := execution.NewInputs(transport, &fakeResolver{value: typedValueFixture}, testSerial,
		execution.WithRenderSizeSource(&fakeRenderSizes{size: testDeviceRenderSize()}),
		execution.WithInputTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- inputs.Tap(context.Background(), execution.TapRequest{Point: execution.Point{X: 5, Y: 5}, Space: testRenderSpace()})
	}()

	select {
	case callErr := <-done:
		if platformerrors.CodeOf(callErr) != platformerrors.CodeDeadlineExceeded {
			t.Fatalf("code = %v err = %v, want deadline_exceeded", platformerrors.CodeOf(callErr), callErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a hung device input call did not return")
	}
}

func TestCancellationStopsTheRemainingKeyEventRepeats(t *testing.T) {
	transport := newFakeDeviceTransport()
	ctx, cancel := context.WithCancel(context.Background())
	transport.cancel = cancel
	transport.cancelAfter = 2
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	err := inputs.KeyEvent(ctx, execution.KeyEventRequest{KeyCode: 67, Repeat: 5})

	if platformerrors.CodeOf(err) != platformerrors.CodeCanceled {
		t.Fatalf("code = %v err = %v, want canceled", platformerrors.CodeOf(err), err)
	}
	if got := transport.invocationCount(); got != 2 {
		t.Fatalf("transport invocations = %d, want 2: the repeats did not stop at cancellation", got)
	}
}

// --- the boundary itself ----------------------------------------------------

func TestInputsConstructorRefusesAnUnusableBoundary(t *testing.T) {
	cases := []struct {
		name      string
		transport execution.InputTransport
		serial    string
	}{
		{name: "no transport", transport: nil, serial: testSerial},
		{name: "no serial", transport: newFakeDeviceTransport(), serial: ""},
		{name: "an unsafe serial", transport: newFakeDeviceTransport(), serial: "serial; id"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := execution.NewInputs(testCase.transport, &fakeResolver{}, testCase.serial); err == nil {
				t.Fatal("an unusable device input boundary was constructed")
			}
		})
	}
}

// Typed text fails closed with no resolver: the other four primitives are
// unaffected.
func TestTypedTextFailsClosedWithoutAResolver(t *testing.T) {
	transport := newFakeDeviceTransport()
	inputs := newInputs(t, transport, nil)

	err := inputs.TypeText(context.Background(), execution.TypeTextRequest{
		Text:      execution.TextReference{Handle: "value-ref-1", Length: 4},
		Workspace: textWorkspace,
	})
	if err == nil {
		t.Fatal("typed text executed with no resolver")
	}
	if got := transport.invocationCount(); got != 0 {
		t.Fatalf("transport invocations = %d, want 0", got)
	}
}

func TestTransportFailureIsClassifiedAndCarriesNoCommandText(t *testing.T) {
	transport := newFakeDeviceTransport()
	transport.result = adb.Result{ExitCode: 1, Stderr: []byte("error: no such activity")}
	inputs := newInputs(t, transport, &fakeResolver{value: typedValueFixture})

	err := inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app"})

	if err == nil {
		t.Fatal("a failing launch reported success")
	}
	if platformerrors.CodeOf(err) == platformerrors.CodeInternal {
		t.Fatalf("a device failure came back unclassified: %v", err)
	}
}

// --- no path to arbitrary command text --------------------------------------

// The only exported entry points on the primitives are the typed device inputs,
// the two catalogued settings operations, and the panel's catalogued device
// operations. No exported method accepts a string, a string slice, or an untyped
// value, so no caller can hand this boundary command text positionally.
//
// Three of those are the strongest form of the rule rather than exceptions to
// it: the settings operations and the reboot and keyboard switch take no
// argument at all beyond the context, so there is no further position in which a
// caller could put anything at all.
//
// `AdvancedAnswer` is the ADVANCED form AGENTS.md section 3 admits, and it is
// the one entry point that carries an operator's own argument array. It is
// asserted here rather than waived: the array arrives inside a typed request
// struct, never as a bare slice, and the boundary refuses an array it will not
// dispatch before any device call. The separation that makes it safe is that no
// catalogued kind reaches it - asserted in `TestOnlyTheAdvancedEntryPointCarriesAnArgumentArray`
// below, and at the applier by `RunAdvancedCommand` having no catalogued caller.
func TestInputPrimitivesExposeNoCommandTextParameter(t *testing.T) {
	inputsType := reflect.TypeOf(&execution.Inputs{})
	readback := reflect.TypeOf(execution.SettingReadback{})
	operationReadback := reflect.TypeOf(execution.OperationReadback{})
	want := map[string]bool{
		"Tap": true, "Swipe": true, "TypeText": true, "KeyEvent": true, "LaunchApp": true,
		"ApplyRotationLock": true, "ApplyAutofillOff": true,
		"Reboot": true, "SwitchKeyboard": true, "ImportFile": true, "InstallPackage": true,
		"ExportFile": true, "AdvancedAnswer": true,
	}

	seen := make(map[string]bool)
	for index := 0; index < inputsType.NumMethod(); index++ {
		method := inputsType.Method(index)
		seen[method.Name] = true
		if !want[method.Name] {
			t.Fatalf("the device input boundary exports %q; only the reviewed primitives may be reachable", method.Name)
		}
		function := method.Func.Type()
		// The typed inputs take a context and one typed request; the two
		// catalogued settings operations and two of the panel's operations take
		// a context and nothing else, which is the strongest form of this rule:
		// there is no further position at all.
		if function.NumIn() < 2 || function.NumIn() > 3 {
			t.Fatalf("%s takes %d arguments, want a context and at most one typed request", method.Name, function.NumIn()-1)
		}
		if !function.In(1).Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
			t.Fatalf("%s does not take a context: cancellation could not be honoured", method.Name)
		}
		for argument := 2; argument < function.NumIn(); argument++ {
			kind := function.In(argument).Kind()
			if kind == reflect.String || kind == reflect.Slice || kind == reflect.Interface {
				t.Fatalf("%s accepts %s, which can carry command text", method.Name, function.In(argument))
			}
		}
		// Every primitive reports an error last, and a primitive that reads its
		// own postcondition back reports that reading before it.
		if function.NumOut() < 1 || function.NumOut() > 3 || function.Out(function.NumOut()-1) != reflect.TypeOf((*error)(nil)).Elem() {
			t.Fatalf("%s does not return an error last", method.Name)
		}
		// A one-reading primitive is a settings operation; ExportFile answers
		// the bytes it pulled beside the reading that describes them.
		if function.NumOut() > 2 && function.Out(0) != operationReadback {
			t.Fatalf("%s returns %s before its other answers, which is not an operation read-back", method.Name, function.Out(0))
		}
		if function.NumOut() > 2 && function.Out(1).Kind() != reflect.Slice {
			t.Fatalf("%s returns %s for the bytes it pulled, which is not bytes", method.Name, function.Out(1))
		}
		if function.NumOut() == 2 && function.Out(0) != readback && function.Out(0) != operationReadback {
			t.Fatalf("%s returns %s before its error, which is not a read-back", method.Name, function.Out(0))
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("the reviewed primitive %q is missing", name)
		}
	}
}

// TestOnlyTheAdvancedEntryPointCarriesAnArgumentArray is the separation the
// amended command rule requires, asserted directly rather than intended.
//
// An argument array reaches this boundary through exactly ONE type, and exactly
// one exported primitive accepts that type. Every catalogued operation's request
// carries bounded typed fields only - a file NAME, an artifact identity, a media
// type, a package name - so there is no catalogued request in which command text
// could be named, and no catalogued caller that could reach the array's
// recogniser.
func TestOnlyTheAdvancedEntryPointCarriesAnArgumentArray(t *testing.T) {
	advanced := reflect.TypeOf(execution.AdvancedCommandRequest{})
	arrayBearer := func(typ reflect.Type) bool {
		for index := 0; index < typ.NumField(); index++ {
			if typ.Field(index).Type == reflect.TypeOf([]string{}) {
				return true
			}
		}
		return false
	}
	catalogue := []reflect.Type{
		reflect.TypeOf(execution.RebootRequest{}),
		reflect.TypeOf(execution.KeyboardSwitchRequest{}),
		reflect.TypeOf(execution.FileImportRequest{}),
		reflect.TypeOf(execution.FileExportRequest{}),
		reflect.TypeOf(execution.PackageInstallRequest{}),
	}
	for _, request := range catalogue {
		if arrayBearer(request) {
			t.Fatalf("catalogued request %s carries an argument array", request.Name())
		}
	}
	if !arrayBearer(advanced) {
		t.Fatalf("the advanced form's request carries no argument array, so it is not the form the rule admits")
	}
	inputsType := reflect.TypeOf(&execution.Inputs{})
	accepting := []reflect.Type{advanced}
	for index := 0; index < inputsType.NumMethod(); index++ {
		method := inputsType.Method(index)
		function := method.Func.Type()
		for argument := 2; argument < function.NumIn(); argument++ {
			matched := false
			for _, candidate := range accepting {
				if function.In(argument) == candidate {
					matched = true
				}
			}
			if matched && method.Name != "AdvancedAnswer" {
				t.Fatalf("%s accepts the advanced form's request; only the advanced entry point may", method.Name)
			}
		}
	}
}

// The primitives build argument arrays and nothing else. This is checked
// structurally against the source: no shell, no exec, and every []string the
// file produces comes out of a fixed *Argv builder.
func TestDeviceInputSourceBuildsArgumentArraysOnly(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "device_input.go", nil, 0)
	if err != nil {
		t.Fatalf("parse device_input.go: %v", err)
	}

	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		for _, forbidden := range []string{"os/exec", "shell", "os/exec/"} {
			if path == forbidden {
				t.Fatalf("device_input.go imports %q: the input boundary must not be able to run a process", path)
			}
		}
	}

	argvBodies := make([][2]token.Pos, 0, 8)
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if !returnsStringSlice(declaration) {
			return true
		}
		if !strings.HasSuffix(declaration.Name.Name, "Argv") {
			t.Fatalf("device_input.go returns an argument array from %q; only a fixed *Argv builder may", declaration.Name.Name)
		}
		if declaration.Body == nil {
			t.Fatalf("%q is an argument-array builder with no body", declaration.Name.Name)
		}
		argvBodies = append(argvBodies, [2]token.Pos{declaration.Body.Pos(), declaration.Body.End()})
		return true
	})

	if len(argvBodies) != 5 {
		t.Fatalf("argument-array builders = %d, want one per primitive", len(argvBodies))
	}

	inArgvBuilder := func(position token.Pos) bool {
		for _, bounds := range argvBodies {
			if position >= bounds[0] && position <= bounds[1] {
				return true
			}
		}
		return false
	}

	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		array, ok := literal.Type.(*ast.ArrayType)
		if !ok {
			return true
		}
		element, ok := array.Elt.(*ast.Ident)
		if !ok || element.Name != "string" {
			return true
		}
		if !inArgvBuilder(literal.Pos()) {
			t.Fatalf("device_input.go builds an argument array outside a fixed *Argv builder at line %d", fset.Position(literal.Pos()).Line)
		}
		return true
	})
}

func returnsStringSlice(declaration *ast.FuncDecl) bool {
	if declaration.Type.Results == nil {
		return false
	}
	for _, result := range declaration.Type.Results.List {
		array, ok := result.Type.(*ast.ArrayType)
		if !ok {
			continue
		}
		if element, ok := array.Elt.(*ast.Ident); ok && element.Name == "string" {
			return true
		}
	}
	return false
}
