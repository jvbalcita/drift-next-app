package connection

import (
	"context"
	"errors"
	"fmt"

	"drift.local/drift-next/internal/edge/adb"
)

// FleetActivation is ONE device's result inside a fleet activation.
//
// It carries its own outcome rather than a position in an aggregate verdict:
// when fourteen devices are moved and one is refused, an operator has to be able
// to read WHICH one was left without a transport, and why. That is why the
// refusal reason travels with the serial it happened to instead of being
// summarised into a count.
type FleetActivation struct {
	Serial string
	Port   uint16
	// Activated is true only when this device's transport mode was changed AND it
	// came back able to accept this host. A device that came back unauthorized
	// was changed but is not activated, and it is reported as its own outcome
	// rather than folded into either answer.
	Activated bool
	// AlreadyOnPort is true when the device was already answering on the target
	// port, so nothing at all was sent for it. "Moved" and "was already there"
	// are different things to tell an operator.
	AlreadyOnPort bool
	// Refusal names the precondition that failed, and is empty when none did. It
	// is the classified reason rather than prose, so a caller branches on a
	// stable value and reads the sentence from Sentence.
	Refusal ActivationRefusalReason
	// StateBefore and StateAfter are the device's own readings, taken by the
	// activation that ran for this serial. StateAfter is empty for a device whose
	// transport mode was never changed.
	StateBefore string
	StateAfter  string
	// NeedsOperatorAuthorization is true when the device came back unauthorized:
	// the change happened, and what it needs next is a person accepting the
	// debugging prompt on its screen rather than a retry.
	NeedsOperatorAuthorization bool
	ExitCode                   int
	// Err is the failure that kept this device from being activated. A fleet run
	// records it against this serial and carries on: one device's failure is not
	// the fleet's verdict, and a run that stopped at the first failure would
	// leave every device after it untouched for no stated reason.
	Err error
}

// Message is the sentence an operator reads for THIS serial.
func (f FleetActivation) Message() string {
	switch {
	case f.AlreadyOnPort:
		return fmt.Sprintf("%s was already answering on port %d, so nothing was sent for it.", f.Serial, f.Port)
	case f.Refusal != "":
		return f.Refusal.Sentence(f.Serial)
	case f.Err != nil:
		return fmt.Sprintf("the transport mode of %s could not be changed, so whether it is usable is UNKNOWN", f.Serial)
	default:
		return activationMessage(f.Serial, f.Port, f.NeedsOperatorAuthorization, f.StateAfter)
	}
}

// FleetActivationReport is what a fleet activation did, per device, in the order
// the device reported its own transports.
type FleetActivationReport struct {
	Port    uint16
	Devices []FleetActivation
}

// Activated counts the devices this run moved onto the port and left usable.
func (r FleetActivationReport) Activated() int {
	return r.count(func(d FleetActivation) bool { return d.Activated })
}

// NeedsOperatorAuthorization counts the devices that were moved onto the port
// and now need a person to accept the debugging prompt on their screen.
func (r FleetActivationReport) NeedsOperatorAuthorization() int {
	return r.count(func(d FleetActivation) bool { return d.NeedsOperatorAuthorization })
}

// Refused counts the devices the transport-mode gate refused.
func (r FleetActivationReport) Refused() int {
	return r.count(func(d FleetActivation) bool { return d.Refusal != "" })
}

// Failed counts the devices whose change ran and failed.
func (r FleetActivationReport) Failed() int {
	return r.count(func(d FleetActivation) bool { return d.Err != nil })
}

// AlreadyOnPort counts the devices that were already answering on the port and
// were therefore not touched.
func (r FleetActivationReport) AlreadyOnPort() int {
	return r.count(func(d FleetActivation) bool { return d.AlreadyOnPort })
}

func (r FleetActivationReport) count(matches func(FleetActivation) bool) int {
	count := 0
	for _, device := range r.Devices {
		if matches(device) {
			count++
		}
	}
	return count
}

// ActivateFleet moves every discovered device that is not already answering on
// port onto it: `adb -s <serial> tcpip <port>` for each one, with the same
// precondition, settle and re-verification a single activation performs.
//
// The fleet is read from the DEVICE, once, before anything runs — never from a
// list a caller handed in. A caller-supplied list would be a claim about the
// fleet that nobody verified, and the one thing worse than a device command is a
// device command whose subject is whoever happens to be attached.
//
// The devices run ONE AT A TIME and in the order the device reported them. Each
// activation restarts adbd, so the settle window and the re-read that follow it
// have to finish before the next device is touched; a fleet run that overlapped
// them would be reading one device's state while another's adbd was coming back,
// and the per-serial result would be a reading of the wrong moment.
//
// A refusal or a failure is recorded against its own serial and the run
// continues. An error is returned only when the run as a whole could not be
// attempted: a port that is not a port, an unreadable fleet, or a cancelled
// context. Every device's outcome is in the report either way.
func (a *Activator) ActivateFleet(ctx context.Context, port uint16) (FleetActivationReport, error) {
	if err := ctx.Err(); err != nil {
		return FleetActivationReport{}, err
	}
	// The port is validated by the adapter's own builder BEFORE the fleet is
	// read, so a request that cannot be run costs no device call at all.
	if _, err := adb.TcpipArgv(port); err != nil {
		return FleetActivationReport{}, err
	}
	fleet, err := a.enumerator.Enumerate(ctx)
	if err != nil {
		return FleetActivationReport{}, fmt.Errorf("refusing to activate the fleet: the attached transports could not be read, so which devices still need the port is unknown: %w", err)
	}
	report := FleetActivationReport{Port: port, Devices: make([]FleetActivation, 0, len(fleet))}
	for _, device := range fleet {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if answeringOn(device.Serial, port) {
			report.Devices = append(report.Devices, FleetActivation{
				Serial:        device.Serial,
				Port:          port,
				AlreadyOnPort: true,
				StateBefore:   string(device.State),
				StateAfter:    string(device.State),
			})
			continue
		}
		report.Devices = append(report.Devices, a.activateOne(ctx, device.Serial, port))
	}
	return report, nil
}

// activateOne runs one serial's activation and reduces its result to the
// per-serial record, so a refusal or a failure stays with the device it happened
// to instead of ending the run.
func (a *Activator) activateOne(ctx context.Context, serial string, port uint16) FleetActivation {
	outcome, err := a.Activate(ctx, serial, port)
	entry := FleetActivation{
		Serial:                     serial,
		Port:                       port,
		Activated:                  err == nil && !outcome.NeedsOperatorAuthorization,
		StateBefore:                outcome.StateBefore,
		StateAfter:                 outcome.StateAfter,
		NeedsOperatorAuthorization: outcome.NeedsOperatorAuthorization,
		ExitCode:                   outcome.ExitCode,
	}
	if err == nil {
		return entry
	}
	var refusal *ActivationRefusalError
	if errors.As(err, &refusal) {
		entry.Refusal = refusal.Reason
		// The refused device's state is the reading that produced the refusal,
		// which is the one fact an operator needs to act on it.
		entry.StateBefore = refusal.State
		entry.StateAfter = ""
		return entry
	}
	entry.Err = err
	return entry
}

// answeringOn reports whether a serial is a transport already served on this
// port. A device reached over TCP reports its own address as its serial, so this
// is a fact read from the device rather than a guess about one, and a serial that
// is not an address (a USB serial) can never be "already answering" on a port.
func answeringOn(serial string, port uint16) bool {
	served, err := adb.EndpointPort(serial)
	return err == nil && served == port
}
