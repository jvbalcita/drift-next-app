package execution_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
)

// The catalogued device settings are exercised here against a fake transport
// that answers each argument array from a table. The arrays are spelled out as
// literals rather than imported from the builders, so a builder that drifted away
// from the admitted shape would fail this test instead of silently agreeing with
// itself.

// The arrays the two settings issue, as literals.
const (
	rotationAutoOffArgs = "shell settings put system accelerometer_rotation 0"
	rotationZeroArgs    = "shell settings put system user_rotation 0"
	rotationAutoRead    = "shell settings get system accelerometer_rotation"
	rotationZeroRead    = "shell settings get system user_rotation"
	autofillDeleteArgs  = "shell settings delete secure autofill_service"
	autofillSetArgs     = "shell cmd autofill set default-augmented-service-enabled 0 false"
	autofillResetArgs   = "shell cmd autofill reset"
	autofillReadArgs    = "shell settings get secure autofill_service"
	autofillGetArgs     = "shell cmd autofill get default-augmented-service-enabled"
)

// settingTransport answers each argument array from a table and records every
// call in order, so a case can assert both what the device was asked and what it
// answered.
type settingTransport struct {
	mu       sync.Mutex
	answers  map[string]adb.Result
	failures map[string]error
	calls    []string
}

func newSettingTransport() *settingTransport {
	return &settingTransport{answers: map[string]adb.Result{}, failures: map[string]error{}}
}

// answersWith states what the device replies to one argument array. An array with
// no stated answer exits zero with no output, which is what a `settings put`
// prints.
func (t *settingTransport) answersWith(argv string, stdout string, exitCode int) *settingTransport {
	t.answers[argv] = adb.Result{ExitCode: exitCode, Stdout: []byte(stdout)}
	return t
}

// failsWith states that one argument array never reaches the device.
func (t *settingTransport) failsWith(argv string, err error) *settingTransport {
	t.failures[argv] = err
	return t
}

func (t *settingTransport) RunDeviceCommand(_ context.Context, _ string, args []string) (adb.Result, error) {
	argv := strings.Join(args, " ")
	t.mu.Lock()
	t.calls = append(t.calls, argv)
	answer, ok := t.answers[argv]
	failure := t.failures[argv]
	t.mu.Unlock()
	if failure != nil {
		return adb.Result{}, failure
	}
	if !ok {
		return adb.Result{ExitCode: 0}, nil
	}
	return answer, nil
}

func (t *settingTransport) issued() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

func newSettingInputs(t *testing.T, transport *settingTransport) *execution.Inputs {
	t.Helper()
	inputs, err := execution.NewInputs(transport, nil, testSerial)
	if err != nil {
		t.Fatalf("new device inputs: %v", err)
	}
	return inputs
}

// matchIssued asserts the device was asked exactly these arrays, in this order.
func matchIssued(t *testing.T, transport *settingTransport, want ...string) {
	t.Helper()
	got := transport.issued()
	if len(got) != len(want) {
		t.Fatalf("device calls = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("device call %d = %q, want %q (all calls: %q)", index, got[index], want[index], got)
		}
	}
}

func TestRotationLockWritesBothSettingsAndReadsThemBack(t *testing.T) {
	transport := newSettingTransport().
		answersWith(rotationAutoRead, "0\n", 0).
		answersWith(rotationZeroRead, "0\n", 0)
	inputs := newSettingInputs(t, transport)

	readback, err := inputs.ApplyRotationLock(context.Background())
	if err != nil {
		t.Fatalf("apply rotation lock: %v", err)
	}
	if !readback.RotationLocked() {
		t.Fatalf("readback = %#v, want the rotation lock holding", readback)
	}
	// Both writes are issued, in the order the device expects them, and both
	// settings are read back. A change is not reported applied on the strength of
	// the write alone.
	matchIssued(t, transport, rotationAutoOffArgs, rotationZeroArgs, rotationAutoRead, rotationZeroRead)
}

func TestRotationLockIsNotHeldWhenEitherSettingReadsBackOtherwise(t *testing.T) {
	for _, answer := range []struct {
		name             string
		autoRotation     string
		userRotation     string
		wantRotationHeld bool
	}{
		{"both zero", "0", "0", true},
		{"auto rotate still on", "1", "0", false},
		{"rotation not zero", "0", "1", false},
		{"auto rotation unreadable", "", "0", false},
		{"rotation unreadable", "0", "", false},
		{"a value that is not the setting", "null", "null", false},
	} {
		t.Run(answer.name, func(t *testing.T) {
			transport := newSettingTransport().
				answersWith(rotationAutoRead, answer.autoRotation, 0).
				answersWith(rotationZeroRead, answer.userRotation, 0)
			inputs := newSettingInputs(t, transport)

			readback, err := inputs.ApplyRotationLock(context.Background())
			if err != nil {
				t.Fatalf("apply rotation lock: %v", err)
			}
			if readback.RotationLocked() != answer.wantRotationHeld {
				t.Fatalf("rotation locked = %t for readback %#v, want %t", readback.RotationLocked(), readback, answer.wantRotationHeld)
			}
		})
	}
}

func TestSettingsReadBackThatTheDeviceRefusedDoesNotConfirmTheSetting(t *testing.T) {
	// The device answered and refused the read. That is not a value, so the
	// setting is unconfirmed rather than assumed to hold.
	transport := newSettingTransport().
		answersWith(rotationAutoRead, "", 1).
		answersWith(rotationZeroRead, "0", 0)
	inputs := newSettingInputs(t, transport)

	readback, err := inputs.ApplyRotationLock(context.Background())
	if err != nil {
		t.Fatalf("apply rotation lock: %v", err)
	}
	if readback.RotationLocked() {
		t.Fatalf("readback = %#v, want an unread setting to leave the lock unconfirmed", readback)
	}
}

func TestSettingsReadBackThatCouldNotBeAttemptedIsAnError(t *testing.T) {
	// A transport failure is a different fact from a setting that did not change:
	// the device was unreachable, so whether the write landed is UNKNOWN.
	transport := newSettingTransport().failsWith(rotationAutoRead, errors.New("adb: device offline"))
	inputs := newSettingInputs(t, transport)

	if _, err := inputs.ApplyRotationLock(context.Background()); err == nil {
		t.Fatal("an unreachable device during the read-back reported success")
	}
}

func TestRotationLockStopsAtTheFirstWriteThatFails(t *testing.T) {
	transport := newSettingTransport().answersWith(rotationAutoOffArgs, "error: no such setting", 1)
	inputs := newSettingInputs(t, transport)

	if _, err := inputs.ApplyRotationLock(context.Background()); err == nil {
		t.Fatal("a refused write reported success")
	}
	matchIssued(t, transport, rotationAutoOffArgs)
}

func TestAutofillOffIssuesItsThreeCommandsInOrderAndReadsBothBack(t *testing.T) {
	transport := newSettingTransport().
		answersWith(autofillReadArgs, "null\n", 0).
		answersWith(autofillGetArgs, "false\n", 0)
	inputs := newSettingInputs(t, transport)

	readback, err := inputs.ApplyAutofillOff(context.Background())
	if err != nil {
		t.Fatalf("apply autofill off: %v", err)
	}
	if !readback.AutofillDisabled() {
		t.Fatalf("readback = %#v, want autofill disabled", readback)
	}
	matchIssued(t, transport, autofillDeleteArgs, autofillSetArgs, autofillResetArgs, autofillReadArgs, autofillGetArgs)
}

func TestAutofillOffReportsStillEnabledFromTheDevicesOwnReadBack(t *testing.T) {
	for _, answer := range []struct {
		name      string
		service   string
		augmented string
		want      bool
	}{
		{"no service and augmented off", "null\n", "false\n", true},
		{"an empty service and augmented off", "\n", "false\n", true},
		{"an empty service and a labelled augmented off", "", "default-augmented-service-enabled: false\n", true},
		// The ported behaviour failed when autofill was still enabled, and both
		// halves are what "enabled" means.
		{"the service is still selected", "com.example/.AutofillService\n", "false\n", false},
		{"the augmented service is still enabled", "null\n", "true\n", false},
		{"both are still enabled", "com.example/.AutofillService\n", "true\n", false},
		// An answer that is not the device's own boolean is not a confirmation.
		{"the augmented answer is not a boolean", "null\n", "maybe\n", false},
		{"the augmented answer is missing", "null\n", "", false},
		// A read the device refused leaves the setting unconfirmed.
		{"the service read was refused", "", "false\n", true},
	} {
		t.Run(answer.name, func(t *testing.T) {
			transport := newSettingTransport().
				answersWith(autofillReadArgs, answer.service, 0).
				answersWith(autofillGetArgs, answer.augmented, 0)
			inputs := newSettingInputs(t, transport)

			readback, err := inputs.ApplyAutofillOff(context.Background())
			if err != nil {
				t.Fatalf("apply autofill off: %v", err)
			}
			if readback.AutofillDisabled() != answer.want {
				t.Fatalf("autofill disabled = %t for readback %#v, want %t", readback.AutofillDisabled(), readback, answer.want)
			}
		})
	}
}

func TestAutofillOffReadBackRefusedLeavesTheSettingUnconfirmed(t *testing.T) {
	transport := newSettingTransport().
		answersWith(autofillReadArgs, "com.example/.AutofillService\n", 0).
		answersWith(autofillGetArgs, "", 1)
	inputs := newSettingInputs(t, transport)

	readback, err := inputs.ApplyAutofillOff(context.Background())
	if err != nil {
		t.Fatalf("apply autofill off: %v", err)
	}
	if readback.AutofillDisabled() {
		t.Fatalf("readback = %#v, want a refused read to leave autofill unconfirmed", readback)
	}
}

func TestAutofillOffStopsAtTheFirstWriteThatFails(t *testing.T) {
	transport := newSettingTransport().answersWith(autofillSetArgs, "error: unknown command", 1)
	inputs := newSettingInputs(t, transport)

	if _, err := inputs.ApplyAutofillOff(context.Background()); err == nil {
		t.Fatal("a refused write reported success")
	}
	// The reset never ran, because the change it would make effective was not
	// made. The read-backs never ran either.
	matchIssued(t, transport, autofillDeleteArgs, autofillSetArgs)
}

func TestSettingsPrimitivesHonourContextCancellation(t *testing.T) {
	transport := newSettingTransport()
	inputs := newSettingInputs(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := inputs.ApplyRotationLock(ctx); err == nil {
		t.Fatal("a cancelled rotation lock reported success")
	}
	if _, err := inputs.ApplyAutofillOff(ctx); err == nil {
		t.Fatal("a cancelled autofill off reported success")
	}
	if calls := transport.issued(); len(calls) != 0 {
		t.Fatalf("device calls = %q, want none: a cancelled settings operation sends nothing", calls)
	}
}

// Every argument array is issued by the operation that owns it and by nothing
// else, and the exported table the safety test reads is exactly those arrays.
func TestDeviceSettingArgvsListsEveryIssuedArray(t *testing.T) {
	rotations := map[string]bool{}
	autofills := map[string]bool{}
	{
		transport := newSettingTransport()
		inputs := newSettingInputs(t, transport)
		if _, err := inputs.ApplyRotationLock(context.Background()); err != nil {
			t.Fatalf("apply rotation lock: %v", err)
		}
		for _, argv := range transport.issued() {
			rotations[argv] = true
		}
	}
	{
		transport := newSettingTransport()
		inputs := newSettingInputs(t, transport)
		if _, err := inputs.ApplyAutofillOff(context.Background()); err != nil {
			t.Fatalf("apply autofill off: %v", err)
		}
		for _, argv := range transport.issued() {
			autofills[argv] = true
		}
	}

	published := map[string]bool{}
	for _, args := range execution.DeviceSettingArgvs() {
		published[strings.Join(args, " ")] = true
	}
	for argv := range rotations {
		if !published[argv] {
			t.Fatalf("the rotation lock issues %q, which the published admission table does not list", argv)
		}
	}
	for argv := range autofills {
		if !published[argv] {
			t.Fatalf("autofill off issues %q, which the published admission table does not list", argv)
		}
	}
	if len(published) != len(rotations)+len(autofills) {
		t.Fatalf("the published admission table lists %d arrays, but the operations issue %d distinct ones", len(published), len(rotations)+len(autofills))
	}
	if len(published) != 9 {
		t.Fatalf("the published admission table lists %d arrays, want the 9 the two operations issue", len(published))
	}
}
