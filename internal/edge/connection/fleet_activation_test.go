package connection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
)

// serialRunner answers the device half per serial, so a run can fail for ONE
// device and succeed for the next. That difference is the whole point of a fleet
// report: one device's failure is not the fleet's verdict.
type serialRunner struct {
	failures map[string]error
	serials  []string
	argvs    []string
	log      *[]string
}

func (r *serialRunner) RunAllowlisted(_ context.Context, serial string, args []string) (adb.Result, error) {
	joined := strings.Join(args, " ")
	r.serials = append(r.serials, serial)
	r.argvs = append(r.argvs, joined)
	if r.log != nil {
		*r.log = append(*r.log, "run:"+joined)
	}
	return adb.Result{ExitCode: 0}, r.failures[serial]
}

// fleetFixture is one scripted fleet activation run: the activator under test
// and the collaborators that recorded what it did.
type fleetFixture struct {
	activator  *Activator
	runner     *serialRunner
	enumerator *scriptedFleet
	wait       *recordedWait
}

// newFleetFixture scripts the readings a fleet activation performs, in the order
// it performs them: one read of the fleet, then — per device that gets as far as
// the change — a read before, the change, the settle, and a read after.
//
// The readings are scripted PER CALL rather than derived, because what this path
// has to prove is that the state it acts on is READ, not assumed: a device whose
// second reading differs from its first must be reported on its second.
func newFleetFixture(t *testing.T, readings [][]adb.DiscoveredDevice, failing map[string]error) fleetFixture {
	t.Helper()
	log := &[]string{}
	runner := &serialRunner{failures: failing, log: log}
	enumerator := &scriptedFleet{fleets: readings, log: log}
	wait := &recordedWait{log: log}
	activator, err := NewActivator(ActivatorConfig{Runner: runner, Enumerator: enumerator, Wait: wait.wait})
	if err != nil {
		t.Fatalf("NewActivator: %v", err)
	}
	return fleetFixture{activator: activator, runner: runner, enumerator: enumerator, wait: wait}
}

func fleetDevices() (usbOther, usbUnauth, tcpOnOther, tcpOnTarget adb.DiscoveredDevice) {
	return transport("R5CT42GS94Z", "device", "usb"),
		transport("ZY223UNAUTH", "unauthorized", "usb"),
		transport("192.168.1.9:5556", "device", "tcp"),
		transport("192.168.1.7:5555", "device", "tcp")
}

// THE FLEET REPORT. Every discovered device that is not already answering on the
// port is acted on, the gate is applied per serial, and each one is reported on
// its own — including the two that are refused, whose refusals are different
// instructions to an operator and therefore different answers.
func TestAFleetActivationReportsEverySerialOnItsOwn(t *testing.T) {
	usbOther, usbUnauth, tcpOnOther, tcpOnTarget := fleetDevices()
	// The fourth reading is the one taken AFTER the first device's adbd
	// restarted, and it says the device came back unauthorized. The outcome must
	// follow THIS reading: a change reported from the state before it would
	// claim a device is usable when it is not.
	readings := [][]adb.DiscoveredDevice{
		{usbOther, usbUnauth, tcpOnOther, tcpOnTarget},
		{usbOther, usbUnauth, tcpOnOther, tcpOnTarget},
		{transport("R5CT42GS94Z", "unauthorized", "usb"), usbUnauth, tcpOnOther, tcpOnTarget},
		{usbOther, usbUnauth, tcpOnOther, tcpOnTarget},
		{usbOther, usbUnauth, tcpOnOther, tcpOnTarget},
	}
	fixture := newFleetFixture(t, readings, nil)

	report, err := fixture.activator.ActivateFleet(context.Background(), 5555)
	if err != nil {
		t.Fatalf("ActivateFleet = %v", err)
	}
	if report.Port != 5555 {
		t.Fatalf("report port = %d, want the port that was asked for", report.Port)
	}
	got := make([]string, 0, len(report.Devices))
	for _, device := range report.Devices {
		kind := "activated"
		switch {
		case device.AlreadyOnPort:
			kind = "already_on_port"
		case device.Refusal != "":
			kind = "refused:" + string(device.Refusal)
		case device.NeedsOperatorAuthorization:
			kind = "needs_authorization"
		case device.Err != nil:
			kind = "failed"
		}
		got = append(got, device.Serial+"="+kind)
	}
	want := []string{
		"R5CT42GS94Z=needs_authorization",
		"ZY223UNAUTH=refused:not_authorized",
		"192.168.1.9:5556=refused:not_usb",
		"192.168.1.7:5555=already_on_port",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the fleet report was\n  %v\nwant\n  %v", got, want)
	}

	// Only the device that could be changed was changed, and only once: a
	// refused device reaches no device at all, and a device already on the port
	// is not restarted to prove it.
	if len(fixture.runner.serials) != 1 || fixture.runner.serials[0] != "R5CT42GS94Z" {
		t.Fatalf("runner serials = %v, want exactly the one device that needed the change", fixture.runner.serials)
	}
	if len(fixture.runner.argvs) != 1 || fixture.runner.argvs[0] != "tcpip 5555" {
		t.Fatalf("runner argvs = %v, want exactly one tcpip 5555", fixture.runner.argvs)
	}
	// The settle is observed once, for the one device whose adbd restarted.
	if len(fixture.wait.waits) != 1 || fixture.wait.waits[0] != SettleWindow {
		t.Fatalf("settles = %v, want exactly one %v", fixture.wait.waits, SettleWindow)
	}
	log := strings.Join(*fixture.runner.log, ",")
	wantLog := "enumerate," + // the fleet read
		"enumerate,run:tcpip 5555,wait,enumerate," + // the device that was moved
		"enumerate," + // the unauthorized device's precondition
		"enumerate" // the tcp device's precondition
	if log != wantLog {
		t.Fatalf("the sequence was\n  %s\nwant\n  %s", log, wantLog)
	}

	counts := fmt.Sprintf("%d/%d/%d/%d/%d", report.Activated(), report.NeedsOperatorAuthorization(), report.Refused(), report.Failed(), report.AlreadyOnPort())
	if counts != "0/1/2/0/1" {
		t.Fatalf("activated/needs-authorization/refused/failed/already-on-port = %s, want 0/1/2/0/1", counts)
	}

	// Every serial gets its own sentence, naming itself: an aggregate verdict is
	// what this replaces.
	seen := map[string]bool{}
	for _, device := range report.Devices {
		message := device.Message()
		if !strings.Contains(message, device.Serial) {
			t.Fatalf("the sentence for %s does not name it: %q", device.Serial, message)
		}
		if seen[message] {
			t.Fatalf("two devices share one sentence: %q", message)
		}
		seen[message] = true
	}
}

// A device already answering on the port is REPORTED and never touched: moving a
// device that is already there would restart adbd for nothing and drop the
// transport the operator just established.
func TestAFleetActivationDoesNotTouchADeviceOnThePortAlready(t *testing.T) {
	_, _, _, tcpOnTarget := fleetDevices()
	fixture := newFleetFixture(t, [][]adb.DiscoveredDevice{{tcpOnTarget}}, nil)

	report, err := fixture.activator.ActivateFleet(context.Background(), 5555)
	if err != nil {
		t.Fatalf("ActivateFleet = %v", err)
	}
	if len(report.Devices) != 1 || !report.Devices[0].AlreadyOnPort {
		t.Fatalf("report = %+v, want one already-on-port outcome", report.Devices)
	}
	if report.Activated() != 0 {
		t.Fatalf("activated = %d, want 0: nothing was moved", report.Activated())
	}
	if len(fixture.runner.serials) != 0 || len(fixture.wait.waits) != 0 {
		t.Fatalf("a device already on the port was acted on: %v, %v", fixture.runner.serials, fixture.wait.waits)
	}
	// One enumeration, and no more: the fleet is read once, and a device that is
	// skipped has no state to read before or after.
	if fixture.enumerator.calls != 1 {
		t.Fatalf("enumerations = %d, want exactly one", fixture.enumerator.calls)
	}
}

// A port that is not a port is refused before the fleet is read, so an invalid
// request cannot reach a device through this path at all.
func TestAFleetActivationRefusesPortZeroBeforeReadingAnything(t *testing.T) {
	usbOther, _, _, _ := fleetDevices()
	fixture := newFleetFixture(t, [][]adb.DiscoveredDevice{{usbOther}}, nil)

	if _, err := fixture.activator.ActivateFleet(context.Background(), 0); err == nil {
		t.Fatal("ActivateFleet(port 0) succeeded")
	}
	if fixture.enumerator.calls != 0 || len(fixture.runner.argvs) != 0 {
		t.Fatalf("port 0 reached a device: %d enumerations, %v", fixture.enumerator.calls, fixture.runner.argvs)
	}
}

// A device whose change fails is reported against its own serial and the run
// CONTINUES. Stopping at the first failure would leave every device after it
// untouched for no stated reason, and would hide that work with an aggregate
// error.
func TestAFleetActivationCarriesOneDevicesFailureAndKeepsGoing(t *testing.T) {
	usbOther, _, _, _ := fleetDevices()
	second := transport("R5CT42GS95Z", "device", "usb")
	readings := [][]adb.DiscoveredDevice{
		{usbOther, second}, // the fleet read
		{usbOther, second}, // the first device's precondition
		// The first device's change fails here, so there is no settle and no
		// second reading for it.
		{usbOther, second}, // the second device's precondition
		{usbOther, second}, // and it comes back authorized
	}
	fixture := newFleetFixture(t, readings, map[string]error{"R5CT42GS94Z": errors.New("adb tcpip refused by the device")})

	report, err := fixture.activator.ActivateFleet(context.Background(), 5555)
	if err != nil {
		t.Fatalf("ActivateFleet = %v: one device's failure must not end the run", err)
	}
	if len(report.Devices) != 2 {
		t.Fatalf("report = %+v, want both devices reported", report.Devices)
	}
	failed, activated := report.Devices[0], report.Devices[1]
	if failed.Err == nil || failed.Serial != "R5CT42GS94Z" {
		t.Fatalf("the failure was not reported against its own serial: %+v", failed)
	}
	if !strings.Contains(failed.Message(), "UNKNOWN") {
		t.Fatalf("a failed change reported %q, want it to say the device's state is unknown", failed.Message())
	}
	if !activated.Activated || activated.Serial != "R5CT42GS95Z" {
		t.Fatalf("the device after the failure was not activated: %+v", activated)
	}
	if report.Failed() != 1 || report.Activated() != 1 {
		t.Fatalf("failed = %d activated = %d, want one of each", report.Failed(), report.Activated())
	}
	if len(fixture.runner.serials) != 2 {
		t.Fatalf("runner serials = %v, want both devices attempted", fixture.runner.serials)
	}
}

// A cancelled context ends the run rather than being swallowed, and it is
// reported as an error because the fleet was not attempted at all.
func TestAFleetActivationStopsOnACancelledContext(t *testing.T) {
	usbOther, _, _, _ := fleetDevices()
	fixture := newFleetFixture(t, [][]adb.DiscoveredDevice{{usbOther}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fixture.activator.ActivateFleet(ctx, 5555); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateFleet on a cancelled context = %v, want context.Canceled", err)
	}
	if fixture.enumerator.calls != 0 {
		t.Fatalf("a cancelled run read the fleet %d time(s)", fixture.enumerator.calls)
	}
}

// A fleet whose transports cannot be read is refused as a whole: which devices
// still need the port is the one thing this operation cannot guess, and acting
// on the ones it happened to see would be a claim about a fleet nobody measured.
func TestAFleetActivationRefusesWhenTheFleetCannotBeRead(t *testing.T) {
	activator, err := NewActivator(ActivatorConfig{Runner: &serialRunner{}, Enumerator: failingFleet{}})
	if err != nil {
		t.Fatalf("NewActivator: %v", err)
	}
	if _, err := activator.ActivateFleet(context.Background(), 5555); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("ActivateFleet on an unreadable fleet = %v, want a refusal that says the fleet could not be read", err)
	}
}

type failingFleet struct{}

func (failingFleet) Enumerate(context.Context) ([]adb.DiscoveredDevice, error) {
	return nil, errors.New("adb server is not running")
}
