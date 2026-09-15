package uiautomator_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	"drift.local/drift-next/internal/platform/redaction"
)

// The ADB adapter is the only production command boundary for this package.
var _ uiautomator.CommandRunner = (*adb.Adapter)(nil)

const (
	testExecutable   = "/opt/android/platform-tools/adb"
	testSerial       = "R5CT30ABCD"
	testCorrelation  = "cap-1"
	testDevicePath   = "/sdcard/drift-cap-1.xml"
	dumpProgressText = "UI hierarchy dumped to: /dev/tty"
)

var testCapturedAt = time.Date(2026, time.September, 15, 4, 30, 0, 0, time.UTC)

const smallHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" bounds="[0,0][1080,2340]">
    <node index="0" text="Sign in" resource-id="com.example:id/title" class="android.widget.TextView" package="com.example" content-desc="Sign in heading" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" bounds="[40,120][1040,200]" />
    <node index="1" text="TEST_ONLY_password_value" resource-id="com.example:id/password" class="android.widget.EditText" package="com.example" content-desc="Password" checkable="false" enabled="true" clickable="true" scrollable="false" long-clickable="true" password="true" bounds="[40,240][1040,320]" />
    <node index="2" text="api_key=TEST_ONLY_sk_live_9f8e7d6c" resource-id="com.example:id/debug" class="android.widget.TextView" package="com.example" content-desc="" checkable="false" enabled="false" clickable="false" scrollable="false" long-clickable="false" password="false" bounds="[40,360][1040,400]" />
  </node>
</hierarchy>
`

func serialArgs(args ...string) []string {
	return append([]string{"-s", testSerial}, args...)
}

func newHarness(t *testing.T, opts ...uiautomator.Option) (*uiautomator.Adapter, *adb.FakeRunner) {
	t.Helper()
	runner := adb.NewFakeRunner()
	commands, err := adb.NewAdapter(testExecutable, runner)
	if err != nil {
		t.Fatalf("adb.NewAdapter() = %v", err)
	}
	options := append([]uiautomator.Option{
		uiautomator.WithClock(clock.NewFixed(testCapturedAt)),
		uiautomator.WithIDGenerator(ids.NewSequence(testCorrelation, "cap-2")),
	}, opts...)
	adapter, err := uiautomator.New(commands, options...)
	if err != nil {
		t.Fatalf("uiautomator.New() = %v", err)
	}
	return adapter, runner
}

// scriptFileStrategy makes the streaming dump yield no hierarchy so the
// temporary-file fallback runs.
func scriptFileStrategy(t *testing.T, runner *adb.FakeRunner, hierarchy string, dumpResponse *adb.FakeResponse) {
	t.Helper()
	dumpArgs, err := adb.UIAutomatorDumpFileArgv(testDevicePath)
	if err != nil {
		t.Fatalf("UIAutomatorDumpFileArgv() = %v", err)
	}
	catArgs, err := adb.CatArgv(testDevicePath)
	if err != nil {
		t.Fatalf("CatArgv() = %v", err)
	}
	removeArgs, err := adb.RemoveArgv(testDevicePath)
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}

	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), "ERROR: could not get idle state.")
	if dumpResponse != nil {
		runner.Respond(serialArgs(dumpArgs...), *dumpResponse)
	} else {
		runner.RespondStdout(serialArgs(dumpArgs...), "UI hierarchy dumped to: "+testDevicePath)
	}
	runner.RespondStdout(serialArgs(catArgs...), hierarchy)
	runner.RespondStdout(serialArgs(removeArgs...), "")
}

func invokedCommands(runner *adb.FakeRunner) []string {
	commands := make([]string, 0, len(runner.Invocations()))
	for _, invocation := range runner.Invocations() {
		commands = append(commands, strings.Join(invocation.Args, " "))
	}
	return commands
}

func TestCaptureStreamsTheHierarchyWithoutATemporaryFile(t *testing.T) {
	adapter, runner := newHarness(t)
	runner.Respond(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), adb.FakeResponse{
		Result: adb.Result{Stdout: []byte(smallHierarchy + dumpProgressText), Duration: 30 * time.Millisecond},
	})

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if !capture.Complete() {
		t.Fatalf("capture = %+v, want a complete observation", capture)
	}
	if capture.NodeCount != 4 || capture.MaxDepth != 2 {
		t.Fatalf("NodeCount = %d MaxDepth = %d, want 4 and 2", capture.NodeCount, capture.MaxDepth)
	}
	if capture.Source != uiautomator.Source || capture.ProtocolVersion != uiautomator.ProtocolVersion {
		t.Fatalf("capture = %+v, want the typed observation provenance", capture)
	}
	if capture.AdapterVersion != uiautomator.AdapterVersion {
		t.Fatalf("AdapterVersion = %q, want %q", capture.AdapterVersion, uiautomator.AdapterVersion)
	}
	if capture.CorrelationID != testCorrelation || !capture.CapturedAt.Equal(testCapturedAt) {
		t.Fatalf("capture = %+v, want the seeded correlation and clock", capture)
	}
	if !strings.HasPrefix(capture.FreshnessToken, "sha256:") {
		t.Fatalf("FreshnessToken = %q, want a sha256 value", capture.FreshnessToken)
	}
	if capture.RawSize == 0 || capture.Latency != 30*time.Millisecond {
		t.Fatalf("capture = %+v, want a recorded raw size and latency", capture)
	}
	if !capture.Sanitized || capture.CleanupFailed {
		t.Fatalf("capture = %+v, want a sanitized capture with clean teardown", capture)
	}

	commands := invokedCommands(runner)
	if len(commands) != 1 {
		t.Fatalf("commands = %q, want only the streaming dump", commands)
	}
	for _, command := range commands {
		if strings.Contains(command, "rm") || strings.Contains(command, "cat") {
			t.Fatalf("streaming path touched the filesystem: %q", command)
		}
	}
}

func TestCaptureMapsNodesAndSanitizesObservedText(t *testing.T) {
	adapter, runner := newHarness(t)
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), smallHierarchy)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}

	title := capture.Nodes[1]
	if title.ResourceID != "com.example:id/title" || title.StableText != "Sign in" {
		t.Fatalf("title node = %+v", title)
	}
	if title.AccessibilityLabel != "Sign in heading" || title.ClassName != "android.widget.TextView" {
		t.Fatalf("title node = %+v", title)
	}
	if title.Bounds != [4]int{40, 120, 1040, 200} {
		t.Fatalf("title bounds = %v, want [40 120 1040 200]", title.Bounds)
	}
	if title.Actionable || !title.Enabled || title.Editable {
		t.Fatalf("title node flags = %+v", title)
	}

	password := capture.Nodes[2]
	if password.StableText != redaction.Replacement {
		t.Fatalf("password text = %q, want %q", password.StableText, redaction.Replacement)
	}
	if !password.Editable || !password.Actionable {
		t.Fatalf("password node = %+v, want an editable actionable field", password)
	}

	debug := capture.Nodes[3]
	if strings.Contains(debug.StableText, "TEST_ONLY_sk_live_9f8e7d6c") {
		t.Fatalf("credential material survived sanitization: %q", debug.StableText)
	}
	if debug.Enabled {
		t.Fatalf("disabled node reported as enabled: %+v", debug)
	}

	fingerprints := make(map[string]struct{}, len(capture.Nodes))
	for _, node := range capture.Nodes {
		if node.ContextFingerprint == "" {
			t.Fatalf("node %+v has no context fingerprint", node)
		}
		if strings.Contains(node.ContextFingerprint, "Sign in") {
			t.Fatalf("fingerprint embeds observed text: %q", node.ContextFingerprint)
		}
		fingerprints[node.ContextFingerprint] = struct{}{}
	}
	if len(fingerprints) != len(capture.Nodes) {
		t.Fatalf("fingerprints collided: %d unique for %d nodes", len(fingerprints), len(capture.Nodes))
	}
}

func TestCaptureFallsBackToATemporaryFileAndAlwaysRemovesIt(t *testing.T) {
	adapter, runner := newHarness(t)
	scriptFileStrategy(t, runner, smallHierarchy, nil)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if !capture.Complete() || capture.CleanupFailed {
		t.Fatalf("capture = %+v, want a complete capture with clean teardown", capture)
	}

	commands := invokedCommands(runner)
	want := []string{
		"-s " + testSerial + " exec-out uiautomator dump --compressed /dev/tty",
		"-s " + testSerial + " shell uiautomator dump --compressed " + testDevicePath,
		"-s " + testSerial + " shell rm -f " + testDevicePath,
		"-s " + testSerial + " exec-out cat " + testDevicePath,
	}
	if len(commands) != len(want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
	for index, expected := range want {
		if commands[index] != expected {
			t.Fatalf("commands[%d] = %q, want %q", index, commands[index], expected)
		}
	}
}

func TestCaptureRemovesTheTemporaryFileAfterAFailedDump(t *testing.T) {
	adapter, runner := newHarness(t)
	failure := adb.FakeResponse{Result: adb.Result{ExitCode: 1, Stderr: []byte("error: device offline")}}
	scriptFileStrategy(t, runner, smallHierarchy, &failure)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err == nil {
		t.Fatal("Capture() = nil, want a classified failure")
	}
	if capture.Complete() || !capture.Partial {
		t.Fatalf("capture = %+v, want a partial observation", capture)
	}
	if capture.FailureClass != domain.FailureDeviceOffline {
		t.Fatalf("FailureClass = %q, want device_offline", capture.FailureClass)
	}

	removed := false
	for _, command := range invokedCommands(runner) {
		if strings.Contains(command, "shell rm -f "+testDevicePath) {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("temporary file was not removed: %q", invokedCommands(runner))
	}
}

func TestCaptureRemovesTheTemporaryFileAfterCancellation(t *testing.T) {
	adapter, runner := newHarness(t)
	dumpArgs, err := adb.UIAutomatorDumpFileArgv(testDevicePath)
	if err != nil {
		t.Fatalf("UIAutomatorDumpFileArgv() = %v", err)
	}
	removeArgs, err := adb.RemoveArgv(testDevicePath)
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), "ERROR: could not get idle state.")
	runner.Respond(serialArgs(dumpArgs...), adb.FakeResponse{Delay: 5 * time.Second})
	runner.RespondStdout(serialArgs(removeArgs...), "")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	defer cancel()

	capture, captureErr := adapter.Capture(ctx, testSerial)
	if captureErr == nil {
		t.Fatal("Capture() = nil, want a cancellation failure")
	}
	if capture.FailureClass != domain.FailureOperatorCancelled {
		t.Fatalf("FailureClass = %q, want operator_cancelled", capture.FailureClass)
	}

	commands := invokedCommands(runner)
	if !strings.Contains(commands[len(commands)-1], "shell rm -f "+testDevicePath) {
		t.Fatalf("cleanup did not run after cancellation: %q", commands)
	}
}

func TestCaptureReportsCleanupFailureWithoutClaimingCleanTeardown(t *testing.T) {
	adapter, runner := newHarness(t)
	scriptFileStrategy(t, runner, smallHierarchy, nil)
	removeArgs, err := adb.RemoveArgv(testDevicePath)
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}
	runner.Respond(serialArgs(removeArgs...), adb.FakeResponse{
		Result: adb.Result{ExitCode: 1, Stderr: []byte("rm: read-only file system")},
	})

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if !capture.CleanupFailed || capture.FailureClass != domain.FailureCleanupFailed {
		t.Fatalf("capture = %+v, want a recorded cleanup failure", capture)
	}
	if capture.Complete() {
		t.Fatal("Complete() = true despite a cleanup failure")
	}
}

func TestCaptureRejectsMalformedHierarchies(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "unclosed element", output: `<hierarchy rotation="0"><node index="0" class="a"></hierarchy>`},
		{name: "broken attribute", output: `<hierarchy rotation="0"><node index=0 class="a" /></hierarchy>`},
		{name: "truncated document", output: `<hierarchy rotation="0"><node index="0" class="a"><node index="0"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, runner := newHarness(t)
			runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), test.output)

			capture, err := adapter.Capture(context.Background(), testSerial)
			if err == nil {
				t.Fatal("Capture() = nil, want a malformed-dump failure")
			}
			if capture.Complete() || !capture.Partial {
				t.Fatalf("capture = %+v, want a partial observation", capture)
			}
			if capture.FailureClass != domain.FailureObservation {
				t.Fatalf("FailureClass = %q, want observation_error", capture.FailureClass)
			}
		})
	}
}

func TestCaptureReportsAMissingHierarchyRatherThanAnEmptyOne(t *testing.T) {
	adapter, runner := newHarness(t)
	scriptFileStrategy(t, runner, "ERROR: could not get idle state.", nil)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if !errors.Is(err, uiautomator.ErrDumpEmpty) {
		t.Fatalf("Capture() = %v, want ErrDumpEmpty", err)
	}
	if capture.Complete() || !capture.Partial || capture.FailureClass != domain.FailureObservation {
		t.Fatalf("capture = %+v, want a partial observation", capture)
	}
	if capture.NodeCount != 0 {
		t.Fatalf("NodeCount = %d, want 0 for a missing hierarchy", capture.NodeCount)
	}
}

func TestCaptureTruncatesAtTheNodeBound(t *testing.T) {
	adapter, runner := newHarness(t, uiautomator.WithMaxNodes(2))
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), smallHierarchy)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v, want a bounded partial capture rather than an error", err)
	}
	if capture.NodeCount != 2 {
		t.Fatalf("NodeCount = %d, want 2", capture.NodeCount)
	}
	if !capture.Truncated || !capture.Partial || capture.Complete() {
		t.Fatalf("capture = %+v, want a truncated partial capture", capture)
	}
	if capture.FailureClass != domain.FailureObservation {
		t.Fatalf("FailureClass = %q, want observation_error", capture.FailureClass)
	}
}

func TestCaptureTruncatesAtTheDepthBound(t *testing.T) {
	adapter, runner := newHarness(t, uiautomator.WithMaxDepth(1))
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), smallHierarchy)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if capture.NodeCount != 1 || capture.MaxDepth != 1 {
		t.Fatalf("NodeCount = %d MaxDepth = %d, want 1 and 1", capture.NodeCount, capture.MaxDepth)
	}
	if !capture.Truncated || capture.Complete() {
		t.Fatalf("capture = %+v, want a depth-truncated capture", capture)
	}
}

func TestCaptureTruncatesAtThePayloadBound(t *testing.T) {
	adapter, runner := newHarness(t, uiautomator.WithMaxXMLBytes(200))
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), smallHierarchy)

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v, want a bounded partial capture rather than an error", err)
	}
	if !capture.Truncated || !capture.Partial || capture.Complete() {
		t.Fatalf("capture = %+v, want a payload-truncated capture", capture)
	}
	if capture.RawSize <= 200 {
		t.Fatalf("RawSize = %d, want the pre-bound size", capture.RawSize)
	}
	if capture.FailureClass != domain.FailureObservation {
		t.Fatalf("FailureClass = %q, want observation_error", capture.FailureClass)
	}
}

func TestCaptureMarksRunnerTruncatedOutputAsPartial(t *testing.T) {
	adapter, runner := newHarness(t)
	runner.Respond(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), adb.FakeResponse{
		Result: adb.Result{Stdout: []byte(smallHierarchy), StdoutTruncated: true},
	})

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if !capture.Truncated || capture.Complete() {
		t.Fatalf("capture = %+v, want a truncated capture when the runner hit its bound", capture)
	}
}

func TestCaptureValidatesTheSerialBeforeAnyCommand(t *testing.T) {
	adapter, runner := newHarness(t)

	for _, serial := range []string{"", "   ", "a;id", "a|id", "a$(id)", "a`id`", "a\nb", "-s"} {
		capture, err := adapter.Capture(context.Background(), serial)
		if err == nil {
			t.Fatalf("Capture(%q) = nil, want a serial validation failure", serial)
		}
		if !errors.Is(err, adb.ErrSerialInvalid) && !errors.Is(err, adb.ErrSerialRequired) {
			t.Fatalf("Capture(%q) = %v, want a serial validation failure", serial, err)
		}
		if capture.NodeCount != 0 {
			t.Fatalf("Capture(%q) returned nodes for a rejected serial", serial)
		}
	}
	if count := len(runner.Invocations()); count != 0 {
		t.Fatalf("runner was invoked %d times for rejected serials, want 0", count)
	}
}

func TestCaptureOnlyIssuesAllowlistedCommands(t *testing.T) {
	adapter, runner := newHarness(t)
	scriptFileStrategy(t, runner, smallHierarchy, nil)

	if _, err := adapter.Capture(context.Background(), testSerial); err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	for _, invocation := range runner.Invocations() {
		if len(invocation.Args) < 3 || invocation.Args[0] != "-s" || invocation.Args[1] != testSerial {
			t.Fatalf("args = %q, want an explicit -s SERIAL prefix", invocation.Args)
		}
		for _, arg := range invocation.Args {
			if strings.ContainsAny(arg, " ;|&$`\"'<>\n") {
				t.Fatalf("argv token %q carries shell syntax", arg)
			}
		}
		joined := strings.Join(invocation.Args[2:], " ")
		switch joined {
		case "exec-out uiautomator dump --compressed /dev/tty",
			"shell uiautomator dump --compressed " + testDevicePath,
			"exec-out cat " + testDevicePath,
			"shell rm -f " + testDevicePath:
		default:
			t.Fatalf("unexpected command %q", joined)
		}
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := uiautomator.New(nil); !errors.Is(err, uiautomator.ErrCommandsRequired) {
		t.Fatalf("New(nil) = %v, want ErrCommandsRequired", err)
	}
	commands, err := adb.NewAdapter(testExecutable, adb.NewFakeRunner())
	if err != nil {
		t.Fatalf("adb.NewAdapter() = %v", err)
	}
	for name, option := range map[string]uiautomator.Option{
		"nil option":  nil,
		"zero nodes":  uiautomator.WithMaxNodes(0),
		"zero depth":  uiautomator.WithMaxDepth(0),
		"zero bytes":  uiautomator.WithMaxXMLBytes(0),
		"nil clock":   uiautomator.WithClock(nil),
		"nil id gen":  uiautomator.WithIDGenerator(nil),
		"negative nc": uiautomator.WithMaxNodes(-1),
	} {
		if _, err := uiautomator.New(commands, option); err == nil {
			t.Fatalf("New(%s) = nil, want an error", name)
		}
	}
}

func TestCaptureScalesToALargeHierarchyWithinItsBounds(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`<hierarchy rotation="0">`)
	for index := 0; index < 500; index++ {
		fmt.Fprintf(&builder,
			`<node index="%d" text="row %d" resource-id="com.example:id/row%d" class="android.widget.TextView" enabled="true" clickable="true" bounds="[0,%d][1080,%d]" />`,
			index, index, index, index*10, index*10+10)
	}
	builder.WriteString(`</hierarchy>`)

	adapter, runner := newHarness(t)
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), builder.String())

	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	if capture.NodeCount != 500 || !capture.Complete() {
		t.Fatalf("capture = %+v, want 500 complete nodes", capture)
	}
	if capture.Nodes[499].StableText != "row 499" || !capture.Nodes[499].Actionable {
		t.Fatalf("last node = %+v", capture.Nodes[499])
	}
}
