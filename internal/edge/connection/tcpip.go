package connection

import (
	"context"
	"fmt"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// DeviceCommandRunner is the serial-bound half of the device adapter: the entry point
// that validates the serial it is given. tcpip is reached ONLY through here, never
// through the serial-free host path, because it changes a device's transport mode.
type DeviceCommandRunner interface {
	RunAllowlisted(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// SettleWindow is how long adbd is given after tcpip restarts it. It is necessary and
// it is NOT sufficient: see the re-verification below.
const SettleWindow = 2500 * time.Millisecond

type ActivatorConfig struct {
	Runner     DeviceCommandRunner
	Enumerator TransportEnumerator
	// Wait bounds the settle. It is injected so a test does not sleep, and the
	// production default honours the context rather than sleeping through a cancel.
	Wait func(ctx context.Context, d time.Duration) error
}

type Activator struct {
	runner     DeviceCommandRunner
	enumerator TransportEnumerator
	wait       func(ctx context.Context, d time.Duration) error
}

func NewActivator(cfg ActivatorConfig) (*Activator, error) {
	if cfg.Runner == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an activation runner is required")
	}
	if cfg.Enumerator == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "an enumerator is required, so that the precondition and the result can both be read from the device rather than assumed")
	}
	wait := cfg.Wait
	if wait == nil {
		wait = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &Activator{runner: cfg.Runner, enumerator: cfg.Enumerator, wait: wait}, nil
}

// ActivationRefusalReason names which precondition failed. "This device is not
// attached", "this device needs the debugging prompt accepted", and "this device is not
// on USB" are three different instructions to an operator, so they are three different
// refusals rather than one.
type ActivationRefusalReason string

const (
	// ActivationRefusalNotAttached: the serial is not among the transports the adb
	// server holds at all.
	ActivationRefusalNotAttached ActivationRefusalReason = "not_attached"
	// ActivationRefusalNotAuthorized: the device is present but will not accept this
	// host — the operator has to accept the debugging prompt on its screen.
	ActivationRefusalNotAuthorized ActivationRefusalReason = "not_authorized"
	// ActivationRefusalNotUSB: the device is present and authorized but not over USB.
	// This is the safety gate, not an optimisation: it guarantees the device is
	// physically attached, so a device that tcpip strands is recoverable by an action
	// the operator can take at the machine.
	ActivationRefusalNotUSB ActivationRefusalReason = "not_usb"
)

type ActivationRefusalError struct {
	Serial string
	Reason ActivationRefusalReason
	State  string
}

func (e *ActivationRefusalError) Error() string { return e.Reason.Sentence(e.Serial) }

// Sentence is the instruction an operator reads for this refusal. It is one
// function so that a single activation, a fleet activation and the refusal error
// itself cannot describe the same condition in three different ways: the reason
// is classified, and the words that go with it live here.
func (r ActivationRefusalReason) Sentence(serial string) string {
	switch r {
	case ActivationRefusalNotAttached:
		return fmt.Sprintf("refusing to change the transport mode of %s: no such transport is attached to this host", serial)
	case ActivationRefusalNotAuthorized:
		return fmt.Sprintf("refusing to change the transport mode of %s: it is present but unauthorized, so accept the USB debugging prompt on the device's screen and try again", serial)
	default:
		return fmt.Sprintf("refusing to change the transport mode of %s: it is present and authorized but its transport is not USB, and a device that is not physically attached cannot be recovered if the change strands it", serial)
	}
}

// ActivationOutcome is what the change actually did, including the state read AFTER it.
// That second read is the point: restarting adbd can leave the device unauthorized, and
// a device that came back unauthorized has not been activated.
type ActivationOutcome struct {
	Serial           string
	Port             uint16
	StateBefore      string
	ConnectionBefore string
	StateAfter       string
	// NeedsOperatorAuthorization is true when the device came back unauthorized: the
	// host's authorization session did not survive the restart, and the device will
	// refuse every dispatch until someone accepts the prompt on its screen. It is
	// reported as an outcome rather than as a failure because the change DID happen —
	// what it needs next is a person, not a retry.
	NeedsOperatorAuthorization bool
	Settle                     time.Duration
	ExitCode                   int
}

// Message is the sentence an operator is meant to read. It names the device and what it
// needs, because "activation failed" is not an instruction.
func (o ActivationOutcome) Message() string {
	return activationMessage(o.Serial, o.Port, o.NeedsOperatorAuthorization, o.StateAfter)
}

// activationMessage is the single place that says what one device's transport-mode
// change did, so a single activation and a fleet activation cannot describe the same
// outcome differently.
func activationMessage(serial string, port uint16, needsOperatorAuthorization bool, stateAfter string) string {
	if needsOperatorAuthorization {
		return fmt.Sprintf("%s is now listening on port %d, but it is UNAUTHORIZED for this host: accept the \"Allow USB debugging?\" prompt on the device's screen. It cannot be used until that is done, and no action will be dispatched to it.", serial, port)
	}
	return fmt.Sprintf("%s is listening on port %d and is %s", serial, port, stateAfter)
}

// Activate changes one device's transport mode to TCP on a port.
//
// The order is the safety property. The precondition is read from the device and the
// refusal happens before anything is run; the change is only then attempted; the settle
// window is observed; and the state is read AGAIN, because what comes back after adbd
// restarts is not what went in.
func (a *Activator) Activate(ctx context.Context, serial string, port uint16) (ActivationOutcome, error) {
	if err := ctx.Err(); err != nil {
		return ActivationOutcome{}, err
	}
	argv, err := adb.TcpipArgv(port)
	if err != nil {
		return ActivationOutcome{}, err
	}

	fleet, err := a.enumerator.Enumerate(ctx)
	if err != nil {
		return ActivationOutcome{}, fmt.Errorf("refusing to change the transport mode of %s: the attached transports could not be read, so the state it is in is unknown: %w", serial, err)
	}
	before, found := fleetState(fleet, serial)
	if !found {
		return ActivationOutcome{}, &ActivationRefusalError{Serial: serial, Reason: ActivationRefusalNotAttached}
	}
	if !before.authorized() {
		return ActivationOutcome{}, &ActivationRefusalError{Serial: serial, Reason: ActivationRefusalNotAuthorized, State: before.state}
	}
	if !before.usb() {
		return ActivationOutcome{}, &ActivationRefusalError{Serial: serial, Reason: ActivationRefusalNotUSB, State: before.state}
	}

	result, err := a.runner.RunAllowlisted(ctx, serial, argv)
	outcome := ActivationOutcome{
		Serial:           serial,
		Port:             port,
		StateBefore:      before.state,
		ConnectionBefore: before.connection,
		ExitCode:         result.ExitCode,
		Settle:           SettleWindow,
	}
	if err != nil {
		return outcome, fmt.Errorf("changing the transport mode of %s: %w", serial, err)
	}

	if err := a.wait(ctx, SettleWindow); err != nil {
		return outcome, err
	}

	// The re-verification. adbd restarted, so the state after is a new reading and not
	// the one taken before: a device that returns unauthorized has not been activated.
	afterFleet, err := a.enumerator.Enumerate(ctx)
	if err != nil {
		return outcome, fmt.Errorf("the transport mode of %s was changed but its state could not be read afterwards, so whether it is usable is UNKNOWN: %w", serial, err)
	}
	after, found := fleetState(afterFleet, serial)
	if !found {
		outcome.StateAfter = "absent"
		outcome.NeedsOperatorAuthorization = false
		return outcome, nil
	}
	outcome.StateAfter = after.state
	outcome.NeedsOperatorAuthorization = !after.authorized()
	return outcome, nil
}

// observedTransport is one transport as the enumerator reported it, reduced to the two
// facts this path decides on.
type observedTransport struct {
	serial     string
	state      string
	connection string
}

func (o observedTransport) authorized() bool { return o.state == "device" }
func (o observedTransport) usb() bool        { return o.connection == "usb" }

// fleetState finds a device in the fleet by serial. A serial is the device's stable
// identity, which is why the lookup is by serial and not by endpoint: the endpoint is
// exactly what this operation is about to change.
func fleetState(fleet []adb.DiscoveredDevice, serial string) (observedTransport, bool) {
	for _, device := range fleet {
		if device.Serial == serial {
			return observedTransport{serial: device.Serial, state: string(device.State), connection: device.ConnectionType}, true
		}
	}
	return observedTransport{}, false
}
