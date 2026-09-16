package adb

import (
	"context"
	"errors"
	"sort"
	"testing"
)

// The render-size read admission (card ARC-75).
//
// `shell wm size` is the declaration read the render-space cross-check obtains
// its device reading from (ADR-0011), and it is the one device call ADR-0010
// deliberately left refused: the six admitted input arrays do not include it, so
// the real transport answered `ErrArgvNotAllowlisted` and every
// coordinate-bearing dispatch failed closed at the render-space gate.
//
// The admission is one argument array with zero variable positions: arity three,
// three fixed literals. There is no caller value, no decimal, no name, no flag,
// no path and no second command, so there is nothing to parameterise and no
// bound to derive — the array cannot express command text at all.
//
// These tests are the admission's own record: the array is admitted, it executes
// through the real adapter with the serial as its own token, every near miss is
// refused as its own case, and the whole allow-list surface is swept to prove
// that nothing else newly widened.

// renderSizeReadOperation is the operation name the allow-list reports for the
// render-size read. It is a read-only declaration read, not a device input, so
// it carries its own name rather than one of the six input names.
const renderSizeReadOperation = "wm-size"

// renderSizeReadArgv is spelled here rather than imported from the builder in
// internal/edge/execution. The allow-list is an independent second gate, so it
// must not validate itself against the code it guards (AGENTS.md §3); the two
// gates are allowed to disagree, and a disagreement is a refusal.
func renderSizeReadArgv() []string { return []string{"shell", "wm", "size"} }

// renderSizeReadNearMisses is every shape that is close to the admitted array
// and must stay refused. It is a package-level table because the sweep below
// reuses it, so a near miss cannot be dropped from one test without the other
// noticing.
var renderSizeReadNearMisses = []struct {
	name string
	args []string
}{
	{"wm size reset", []string{"shell", "wm", "size", "reset"}},
	{"wm density", []string{"shell", "wm", "density"}},
	{"wm without a subcommand", []string{"shell", "wm"}},
	{"size without shell", []string{"wm", "size"}},
	{"shell wm size with an extra token", []string{"shell", "wm", "size", "1"}},
	{"shell wm size with an empty extra token", []string{"shell", "wm", "size", ""}},
	{"a variable position carrying a size", []string{"shell", "wm", "size", "1080x2280"}},
	{"a variable position carrying a number", []string{"shell", "wm", "size", "-1"}},
	{"a variable position carrying a substitution", []string{"shell", "wm", "size", "$(id)"}},
	{"a variable position carrying a separator", []string{"shell", "wm", "size", "; id"}},
	{"a second command after the read", []string{"shell", "wm", "size", "&&", "id"}},
	{"the read through a shell", []string{"shell", "sh", "-c", "wm size"}},
	{"a case variant of the subcommand", []string{"shell", "WM", "size"}},
	{"a case variant of the size token", []string{"shell", "wm", "SIZE"}},
	{"the binary named by path", []string{"shell", "/system/bin/wm", "size"}},
	{"a flag instead of a subcommand", []string{"shell", "wm", "-s"}},
	{"the read without the remote shell", []string{"exec-out", "wm", "size"}},
	{"an over-long argument array", []string{"shell", "wm", "size", "reset", "1080x2280"}},
}

// TestAllowlistAdmitsTheRenderSizeRead is the admission itself. Before this
// change the allow-list returned false for this exact array.
func TestAllowlistAdmitsTheRenderSizeRead(t *testing.T) {
	name, ok := matchesAllowlist(renderSizeReadArgv())
	if !ok {
		t.Fatalf("matchesAllowlist(%q) = %q, false; want %q, true: the render-size read is refused, so the render-space cross-check cannot obtain a device reading and every coordinate-bearing dispatch fails closed",
			renderSizeReadArgv(), name, renderSizeReadOperation)
	}
	if name != renderSizeReadOperation {
		t.Fatalf("matchesAllowlist(%q) = %q, true; want the read's own operation name %q rather than an input name", renderSizeReadArgv(), name, renderSizeReadOperation)
	}
}

// TestTheRenderSizeReadHasNoVariablePosition proves the structural claim the
// admission rests on: every position is a fixed literal, so no position can
// carry a caller value.
func TestTheRenderSizeReadHasNoVariablePosition(t *testing.T) {
	args := renderSizeReadArgv()
	if len(args) != 3 {
		t.Fatalf("the render-size read has %d positions, want exactly 3 fixed tokens (%q)", len(args), args)
	}
	for index := range args {
		mutated := append([]string(nil), args...)
		mutated[index] = "x"
		if name, ok := matchesAllowlist(mutated); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; the render-size read admitted a caller value at position %d", mutated, name, index)
		}
	}
}

// TestAllowlistRefusesEveryNearMissOfTheRenderSizeRead is the other half of the
// admission, each near miss as its own case.
func TestAllowlistRefusesEveryNearMissOfTheRenderSizeRead(t *testing.T) {
	for _, test := range renderSizeReadNearMisses {
		t.Run(test.name, func(t *testing.T) {
			if name, ok := matchesAllowlist(test.args); ok {
				t.Fatalf("matchesAllowlist(%q) = %q, true; want false: this array is a near miss of the render-size read and must stay refused", test.args, name)
			}
		})
	}
}

// TestRunAllowlistedExecutesTheRenderSizeRead closes the loop between the two
// gates: the exact array the reader builds runs through the real adapter, with
// the serial as its own token, instead of being refused with
// ErrArgvNotAllowlisted.
func TestRunAllowlistedExecutesTheRenderSizeRead(t *testing.T) {
	stdout := "Physical size: 1080x2280\nOverride size: 720x1520\n"
	runner := NewFakeRunner().Respond(deviceArgs(renderSizeReadArgv()...), FakeResponse{Result: Result{Stdout: []byte(stdout)}})
	adapter := newTestAdapter(t, runner)

	result, err := adapter.RunAllowlisted(context.Background(), testSerial, renderSizeReadArgv())
	if errors.Is(err, ErrArgvNotAllowlisted) {
		t.Fatalf("RunAllowlisted(%q) = %v, want the read to execute: the real transport still refuses the render-size read", renderSizeReadArgv(), err)
	}
	if err != nil {
		t.Fatalf("RunAllowlisted(%q) = %v, want the read to execute", renderSizeReadArgv(), err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("the render-size read exited %d, want 0", result.ExitCode)
	}
	if got := string(result.Stdout); got != stdout {
		t.Fatalf("the render-size read returned %q, want the device's declaration %q", got, stdout)
	}

	invocations := runner.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("invocations = %d, want exactly 1", len(invocations))
	}
	want := deviceArgs(renderSizeReadArgv()...)
	if got := invocations[0].Args; len(got) != len(want) {
		t.Fatalf("invoked %q, want %q", got, want)
	} else {
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("invoked %q, want %q: the serial must be its own token before the fixed array", got, want)
			}
		}
	}
}

// TestTheRenderSizeAdmissionDoesNotWidenTheAllowlistSurface is the sweep. It
// proves the surface is exactly the previously recorded one plus this one
// addition: every admitted array still resolves to its own operation name, the
// set of operation names is exactly the recorded set, and a corpus of arrays
// that were refused before stays refused.
func TestTheRenderSizeAdmissionDoesNotWidenTheAllowlistSurface(t *testing.T) {
	getProp, err := getPropArgv("ro.build.version.sdk")
	if err != nil {
		t.Fatalf("getPropArgv() = %v", err)
	}
	dumpFile, err := UIAutomatorDumpFileArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("UIAutomatorDumpFileArgv() = %v", err)
	}
	cat, err := CatArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("CatArgv() = %v", err)
	}
	remove, err := RemoveArgv("/sdcard/drift-abc.xml")
	if err != nil {
		t.Fatalf("RemoveArgv() = %v", err)
	}

	// Every array the adapter admits, with the operation name it must resolve
	// to: the read-only builders (ADR-0004), the six typed device input arrays
	// (ADR-0010) and the render-size read (ARC-75).
	admitted := []struct {
		operation string
		args      []string
	}{
		{renderSizeReadOperation, renderSizeReadArgv()},
		{"get-state", getStateArgv()},
		{"screencap", screencapArgv()},
		{"uiautomator-dump-stdout", UIAutomatorDumpStdoutArgv()},
		{"getprop", getProp},
		{"uiautomator-dump-file", dumpFile},
		{"cat", cat},
		{"rm", remove},
		{"input-tap", []string{"shell", "input", "tap", "540", "960"}},
		{"input-swipe", []string{"shell", "input", "swipe", "1", "2", "3", "4", "300"}},
		{"input-keyevent", []string{"shell", "input", "keyevent", "4"}},
		{"input-text", []string{"shell", "input", "text", "hello%sworld"}},
		{"launch-app-package", []string{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "1"}},
		{"launch-app-activity", []string{"shell", "am", "start", "-n", "com.example.app/.MainActivity"}},
	}

	operations := make(map[string]struct{}, len(admitted))
	for _, test := range admitted {
		name, ok := matchesAllowlist(test.args)
		if !ok {
			t.Fatalf("matchesAllowlist(%q) = %q, false; want %q, true: an array admitted before this change is no longer admitted", test.args, name, test.operation)
		}
		if name != test.operation {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want %q: the operation name of an existing admission changed", test.args, name, test.operation)
		}
		operations[name] = struct{}{}
	}

	// The recorded surface is the operation-name set itself, so an admission
	// that is not recorded here fails this test rather than passing quietly.
	want := []string{
		"cat",
		"get-state",
		"getprop",
		"input-keyevent",
		"input-swipe",
		"input-tap",
		"input-text",
		"launch-app-activity",
		"launch-app-package",
		"rm",
		"screencap",
		"uiautomator-dump-file",
		"uiautomator-dump-stdout",
		renderSizeReadOperation,
	}
	got := make([]string, 0, len(operations))
	for name := range operations {
		got = append(got, name)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("the allow-list admits %d operations (%q), want %d (%q): the admitted surface changed and this sweep was not updated", len(got), got, len(want), want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("the allow-list admits %q, want %q: the admitted surface changed and this sweep was not updated", got, want)
		}
	}

	// Nothing else is newly admitted. Every case here was refused before the
	// change and must stay refused after it.
	refused := [][]string{
		// Near misses of the array this change admits.
		{"shell", "input", "tap", "540"},
		{"shell", "input", "text", "a;id"},
		{"shell", "input", "text", "100%"},
		{"shell", "input", "keyevent", "10001"},
		{"shell", "monkey", "-p", "com.example.app", "-c", "android.intent.category.LAUNCHER", "2"},
		{"shell", "am", "start", "-n", "com.example/../../data/data"},
		{"shell", "am", "start", "-a", "android.intent.action.VIEW"},
		// Arbitrary shell, a second command, and a host-scoped command.
		{"shell", "sh", "-c", "input tap 1 2"},
		{"shell", "rm", "-rf", "/sdcard"},
		{"shell", "rm", "/sdcard/drift-abc.xml"},
		{"shell", "getprop", "ro.serialno"},
		{"shell", "pm", "uninstall", "com.example"},
		{"shell", "uiautomator", "dump", "--compressed", "/sdcard/window_dump.xml"},
		{"exec-out", "cat", "/data/misc/adb/adb_keys"},
		{"exec-out", "cat", "/sdcard/drift-abc.xml", "extra"},
		{"exec-out", "sh"},
		{"sh", "-c", "ls"},
		{"push", "/tmp/payload", "/data/local/tmp/payload"},
		{"root"},
		{"devices", "-l"},
		{"version"},
		{"reconnect"},
	}
	for _, test := range renderSizeReadNearMisses {
		refused = append(refused, test.args)
	}
	for _, args := range refused {
		if name, ok := matchesAllowlist(args); ok {
			t.Fatalf("matchesAllowlist(%q) = %q, true; want false: this array was refused before the render-size admission and must stay refused", args, name)
		}
	}
}
