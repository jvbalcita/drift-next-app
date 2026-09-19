package connection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// scriptedFleet answers each enumeration with a whole fleet, so the precondition and
// the re-verification can return DIFFERENT states. That difference is the entire point
// of this path: what comes back after adbd restarts is not what went in.
type scriptedFleet struct {
	fleets [][]adb.DiscoveredDevice
	calls  int
	log    *[]string
}

func (f *scriptedFleet) Enumerate(_ context.Context) ([]adb.DiscoveredDevice, error) {
	fleet := []adb.DiscoveredDevice{}
	if f.calls < len(f.fleets) {
		fleet = f.fleets[f.calls]
	}
	f.calls++
	if f.log != nil {
		*f.log = append(*f.log, "enumerate")
	}
	return fleet, nil
}

func transport(serial, state, connection string) adb.DiscoveredDevice {
	return adb.DiscoveredDevice{Serial: serial, State: adb.DeviceAuthState(state), ConnectionType: connection}
}

// deviceRunner is the serial-bound half. A tcpip array reaching this with a serial is
// correct; reaching the serial-free host runner would not be, which the adb package
// asserts separately.
type deviceRunner struct {
	serials []string
	argvs   []string
	result  adb.Result
	err     error
	log     *[]string
}

func (r *deviceRunner) RunAllowlisted(_ context.Context, serial string, args []string) (adb.Result, error) {
	joined := strings.Join(args, " ")
	r.serials = append(r.serials, serial)
	r.argvs = append(r.argvs, joined)
	if r.log != nil {
		*r.log = append(*r.log, "run:"+joined)
	}
	return r.result, r.err
}

type recordedWait struct {
	waits []time.Duration
	err   error
	log   *[]string
}

func (w *recordedWait) wait(_ context.Context, d time.Duration) error {
	w.waits = append(w.waits, d)
	if w.log != nil {
		*w.log = append(*w.log, "wait")
	}
	return w.err
}

const labSerial = "R5CT42GS94Z"

func newActivator(t *testing.T, fleets [][]adb.DiscoveredDevice) (*Activator, *deviceRunner, *scriptedFleet, *recordedWait) {
	t.Helper()
	log := &[]string{}
	runner := &deviceRunner{log: log}
	enumerator := &scriptedFleet{fleets: fleets, log: log}
	wait := &recordedWait{log: log}
	activator, err := NewActivator(ActivatorConfig{Runner: runner, Enumerator: enumerator, Wait: wait.wait})
	if err != nil {
		t.Fatalf("NewActivator: %v", err)
	}
	return activator, runner, enumerator, wait
}

// THE SAFETY GATE. A device that is not on USB is refused before anything runs: the
// USB requirement guarantees the device is physically attached, so a change that
// strands it is recoverable by an action the operator can take at the machine.
func TestActivationRefusesADeviceThatIsNotOnUSB(t *testing.T) {
	activator, runner, enumerator, wait := newActivator(t, [][]adb.DiscoveredDevice{{transport(labSerial, "device", "tcp")}})

	_, err := activator.Activate(context.Background(), labSerial, 5556)
	var refusal *ActivationRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate = %v, want an *ActivationRefusalError", err)
	}
	if refusal.Reason != ActivationRefusalNotUSB {
		t.Fatalf("refusal reason = %q, want %q", refusal.Reason, ActivationRefusalNotUSB)
	}
	if len(runner.argvs) != 0 {
		t.Fatalf("a refused activation ran %v", runner.argvs)
	}
	if wait.waits != nil {
		t.Fatalf("a refused activation settled for %v", wait.waits)
	}
	if enumerator.calls != 1 {
		t.Fatalf("the precondition was read %d times, want exactly 1", enumerator.calls)
	}
}

// An unauthorized device is its own refusal, and the message is an instruction —
// but it must be an instruction the operator of a SCREENSLESS unit can act on.
// "Accept the prompt on the device's screen" is not: half this fleet is mainboard
// units with no display attached, and a refusal that names an action nobody can
// take is not an instruction at all. What is true is that no host can accept that
// prompt for the device — that is the device-side boundary, and the product does
// not bypass it — so the sentence says so and names the two things that DO work.
func TestActivationRefusesAnUnauthorizedDeviceAndNamesTheDeviceSideBoundary(t *testing.T) {
	activator, runner, _, _ := newActivator(t, [][]adb.DiscoveredDevice{{transport(labSerial, "unauthorized", "usb")}})

	_, err := activator.Activate(context.Background(), labSerial, 5556)
	var refusal *ActivationRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate = %v, want an *ActivationRefusalError", err)
	}
	if refusal.Reason != ActivationRefusalNotAuthorized {
		t.Fatalf("refusal reason = %q, want %q", refusal.Reason, ActivationRefusalNotAuthorized)
	}
	if !strings.Contains(err.Error(), "no host can accept that prompt for it") {
		t.Fatalf("refusal %q must say no host can accept the prompt for this device", err.Error())
	}
	if !strings.Contains(err.Error(), "authorized once on its own display") {
		t.Fatalf("refusal %q must name the one thing that does authorize the device", err.Error())
	}
	if strings.Contains(err.Error(), "device's screen") {
		t.Fatalf("refusal %q tells the operator to use a screen the device does not have", err.Error())
	}
	if len(runner.argvs) != 0 {
		t.Fatalf("a refused activation ran %v", runner.argvs)
	}
}

func TestActivationRefusesASerialThatIsNotAttachedAtAll(t *testing.T) {
	activator, runner, _, _ := newActivator(t, [][]adb.DiscoveredDevice{{transport("SOMEOTHER", "device", "usb")}})

	_, err := activator.Activate(context.Background(), labSerial, 5556)
	var refusal *ActivationRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Activate = %v, want an *ActivationRefusalError", err)
	}
	if refusal.Reason != ActivationRefusalNotAttached {
		t.Fatalf("refusal reason = %q, want %q", refusal.Reason, ActivationRefusalNotAttached)
	}
	if len(runner.argvs) != 0 {
		t.Fatalf("a refused activation ran %v", runner.argvs)
	}
}

// THE CORRECTION THIS PATH EXISTS FOR. The change happens, and the device comes back
// UNAUTHORIZED because restarting adbd did not preserve the host's authorization
// session. That is reported as an outcome naming what to do next, never as success —
// and it is not an error either, because the change did happen and the next step is a
// person, not a retry.
func TestActivationReportsUnauthorizedAfterTheChangeRatherThanSuccess(t *testing.T) {
	activator, runner, enumerator, wait := newActivator(t, [][]adb.DiscoveredDevice{
		{transport(labSerial, "device", "usb")},
		{transport(labSerial, "unauthorized", "usb")},
	})

	outcome, err := activator.Activate(context.Background(), labSerial, 5556)
	if err != nil {
		t.Fatalf("Activate = %v; a change that happened and needs a person is an outcome, not a failure", err)
	}
	if !outcome.NeedsOperatorAuthorization {
		t.Fatalf("outcome = %+v; a device that came back unauthorized must not be reported as activated", outcome)
	}
	if outcome.StateAfter != "unauthorized" {
		t.Fatalf("StateAfter = %q, want unauthorized", outcome.StateAfter)
	}
	message := outcome.Message()
	if !strings.Contains(message, "UNAUTHORIZED") || !strings.Contains(message, "no host can accept that prompt for it") {
		t.Fatalf("outcome message %q must say the device is unauthorized and that no host can accept the prompt for it", message)
	}
	if strings.Contains(message, "device's screen") {
		t.Fatalf("outcome message %q names a screen the device has not got", message)
	}
	if len(runner.argvs) != 1 || runner.argvs[0] != "tcpip 5556" {
		t.Fatalf("the change ran %v, want exactly [tcpip 5556]", runner.argvs)
	}
	if len(runner.serials) != 1 || runner.serials[0] != labSerial {
		t.Fatalf("the change was aimed at %v, want the serial %q", runner.serials, labSerial)
	}
	if len(wait.waits) != 1 || wait.waits[0] != SettleWindow {
		t.Fatalf("settled for %v, want exactly one %v", wait.waits, SettleWindow)
	}
	if enumerator.calls != 2 {
		t.Fatalf("the state was read %d times, want 2 (before and after)", enumerator.calls)
	}
}

// The order is the safety property, so it is asserted as an order rather than as a set:
// read the precondition, run the change, settle, then read the result.
func TestActivationReadsBeforeItChangesAndReadsAgainAfter(t *testing.T) {
	log := &[]string{}
	runner := &deviceRunner{log: log}
	enumerator := &scriptedFleet{fleets: [][]adb.DiscoveredDevice{
		{transport(labSerial, "device", "usb")},
		{transport(labSerial, "device", "tcp")},
	}, log: log}
	wait := &recordedWait{log: log}
	activator, err := NewActivator(ActivatorConfig{Runner: runner, Enumerator: enumerator, Wait: wait.wait})
	if err != nil {
		t.Fatalf("NewActivator: %v", err)
	}

	outcome, err := activator.Activate(context.Background(), labSerial, 5556)
	if err != nil {
		t.Fatalf("Activate = %v", err)
	}
	if outcome.NeedsOperatorAuthorization {
		t.Fatalf("outcome = %+v; an authorized device must not be reported as needing a tap", outcome)
	}
	if outcome.StateAfter != "device" || !strings.Contains(outcome.Message(), "listening on port 5556") {
		t.Fatalf("outcome = %+v (message %q)", outcome, outcome.Message())
	}
	want := []string{"enumerate", "run:tcpip 5556", "wait", "enumerate"}
	if strings.Join(*log, ",") != strings.Join(want, ",") {
		t.Fatalf("the sequence was %v, want %v (read, change, settle, re-read)", *log, want)
	}
}

// A port that is not a port is refused by the builder before the device is even read,
// so an invalid request cannot reach a device through this path at all.
func TestActivationRefusesPortZeroBeforeReadingAnything(t *testing.T) {
	activator, runner, enumerator, _ := newActivator(t, [][]adb.DiscoveredDevice{{transport(labSerial, "device", "usb")}})

	if _, err := activator.Activate(context.Background(), labSerial, 0); err == nil {
		t.Fatal("Activate(port 0) succeeded")
	}
	if len(runner.argvs) != 0 || enumerator.calls != 0 {
		t.Fatalf("port 0 reached the device: %v, %d enumerations", runner.argvs, enumerator.calls)
	}
}

func TestANewActivatorRequiresARunnerAndAnEnumerator(t *testing.T) {
	if _, err := NewActivator(ActivatorConfig{Enumerator: &scriptedFleet{}}); err == nil {
		t.Fatal("NewActivator accepted a nil runner")
	}
	if _, err := NewActivator(ActivatorConfig{Runner: &deviceRunner{}}); err == nil {
		t.Fatal("NewActivator accepted a nil enumerator")
	}
}
