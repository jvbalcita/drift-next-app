package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
)

// The render-size read over the real allow-list (card ARC-75).
//
// The reader reaches a device through the same narrow transport as every other
// device call, so before this admission the real transport refused its array
// with `adb.ErrArgvNotAllowlisted`, the reader reported the device render size as
// unavailable, and every coordinate-bearing dispatch was refused at the
// render-space gate. That refusal was correct and fail-closed; ARC-75 removes the
// blocker rather than fixing a bug.
//
// Both halves are asserted here against the real adapter and its real allow-list:
// the exact array is no longer refused, and the real reader yields a reading
// instead of reporting the device render size as unavailable. No process is
// spawned, no socket is opened and no device is touched: the runner is the ADB
// fake.

// realRenderSizeArgs is the argv the adapter asks the host for: the serial as its
// own token, then the fixed three-token render-size read.
func realRenderSizeArgs() []string {
	return []string{"-s", testSerial, "shell", "wm", "size"}
}

func newRealAllowListReader(t *testing.T) *execution.WmSizeReader {
	t.Helper()
	runner := adb.NewFakeRunner().Respond(realRenderSizeArgs(), adb.FakeResponse{Result: adb.Result{Stdout: []byte(overrideAndPanelOutput())}})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", runner, adb.WithOperationTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	return newWmSizeReader(t, execution.NewADBInputTransport(adapter))
}

// TestTheRealTransportNoLongerRefusesTheRenderSizeRead is the refusal-is-gone
// assertion for this exact argv.
func TestTheRealTransportNoLongerRefusesTheRenderSizeRead(t *testing.T) {
	args := []string{"shell", "wm", "size"}
	runner := adb.NewFakeRunner().Respond(realRenderSizeArgs(), adb.FakeResponse{Result: adb.Result{Stdout: []byte(overrideAndPanelOutput())}})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", runner, adb.WithOperationTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	result, err := execution.NewADBInputTransport(adapter).RunDeviceCommand(context.Background(), testSerial, args)
	if errors.Is(err, adb.ErrArgvNotAllowlisted) {
		t.Fatalf("RunDeviceCommand(%q) = %v, want the read to be admitted: the real transport still refuses the render-size read", args, err)
	}
	if err != nil {
		t.Fatalf("RunDeviceCommand(%q) = %v, want the read to execute", args, err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("the render-size read exited %d, want 0", result.ExitCode)
	}

	invocations := runner.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("process invocations = %d, want exactly 1", len(invocations))
	}
	want := realRenderSizeArgs()
	got := invocations[0].Args
	if len(got) != len(want) {
		t.Fatalf("invoked %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("invoked %q, want %q: the read must be exactly the fixed three-token array behind the serial", got, want)
		}
	}
}

// TestTheRealReaderYieldsAReadingThroughTheRealAllowList is the reason the
// admission exists: the render-space cross-check can now obtain a device reading
// through the real transport.
func TestTheRealReaderYieldsAReadingThroughTheRealAllowList(t *testing.T) {
	reader := newRealAllowListReader(t)

	size, err := reader.RenderSize(context.Background())
	if err != nil {
		t.Fatalf("RenderSize() = %v, want the device's declaration: the render-size read does not reach a device through the real allow-list, so every coordinate-bearing dispatch is still refused", err)
	}
	if size.Width != fakeDeviceOverrideWidth || size.Height != fakeDeviceOverrideHeight {
		t.Fatalf("RenderSize() = %dx%d, want the device's declaration %dx%d", size.Width, size.Height, fakeDeviceOverrideWidth, fakeDeviceOverrideHeight)
	}
	if size.Provenance != execution.RenderSizeFromOverride {
		t.Fatalf("RenderSize() provenance = %q, want %q: the device declared an override, so the override is the frame", size.Provenance, execution.RenderSizeFromOverride)
	}
	if size.ObservedAt.IsZero() {
		t.Fatal("RenderSize() carries no observation time, so the cross-check cannot tell a fresh reading from a stale one")
	}
}

// TestNoTypedDeviceInputEmitsTheRenderSizeRead is the producing side of the
// separation the read-only admission has to keep. The read is reachable through
// the same allow-list an operator-authored input reaches, so no typed device
// input may put it on the wire: each of the five inputs is dispatched through the
// real adapter and the array it emits is compared against the read. The
// classification side - that the read is not a typed input kind, and that no
// array is admitted by both recognisers - is asserted in
// internal/edge/adb/allowlist_classification_test.go.
func TestNoTypedDeviceInputEmitsTheRenderSizeRead(t *testing.T) {
	read := []string{"shell", "wm", "size"}
	runner := adb.NewFakeRunner().RespondDefault(adb.FakeResponse{Result: adb.Result{ExitCode: 0}})
	adapter, err := adb.NewAdapter("/opt/android/platform-tools/adb", runner, adb.WithOperationTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	cases := []struct {
		name  string
		build func(*execution.Inputs) error
	}{
		{"tap", func(inputs *execution.Inputs) error {
			return inputs.Tap(context.Background(), execution.TapRequest{Point: execution.Point{X: 540, Y: 960}, Space: testRenderSpace()})
		}},
		{"swipe", func(inputs *execution.Inputs) error {
			return inputs.Swipe(context.Background(), execution.SwipeRequest{Start: execution.Point{X: 1, Y: 2}, End: execution.Point{X: 3, Y: 4}, DurationMS: 300, Space: testRenderSpace()})
		}},
		{"key event", func(inputs *execution.Inputs) error {
			return inputs.KeyEvent(context.Background(), execution.KeyEventRequest{KeyCode: 4, Repeat: 1})
		}},
		{"typed text by reference", func(inputs *execution.Inputs) error {
			return inputs.TypeText(context.Background(), execution.TypeTextRequest{Text: execution.TextReference{Handle: "clipboard-1", Length: uint32(len(typedValueFixture))}})
		}},
		{"app launch by package", func(inputs *execution.Inputs) error {
			return inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app"})
		}},
		{"app launch by component", func(inputs *execution.Inputs) error {
			return inputs.LaunchApp(context.Background(), execution.LaunchAppRequest{PackageName: "com.example.app", ActivityName: ".MainActivity"})
		}},
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
				t.Fatalf("process invocations = %d, want %d: the input is the only call", len(invocations), before+1)
			}
			argv := invocations[len(invocations)-1].Args
			if len(argv) < 3 || argv[0] != "-s" || argv[1] != testSerial {
				t.Fatalf("argv = %q, want the serial as its own token", argv)
			}
			emitted := argv[2:]
			if len(emitted) != len(read) {
				return
			}
			for index := range read {
				if emitted[index] != read[index] {
					return
				}
			}
			t.Fatalf("the %s input emitted the render-size read %q: a device input must not be able to issue the read-only admission", test.name, read)
		})
	}
}
