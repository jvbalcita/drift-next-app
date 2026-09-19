// Package endpoints owns mutable transport observations and endpoint history,
// including the transport an observation was made over.
package endpoints

import (
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
)

type EndpointID string

type State string

const (
	Observed   State = "observed"
	Current    State = "current"
	Superseded State = "superseded"
	Retired    State = "retired"
)

// Transport is how a device's endpoint was reached when it was observed: over
// USB, or over TCP at a host:port.
//
// It is recorded with the observation and read back from the record. It is
// deliberately not re-derived by a consumer from the shape of an endpoint
// address: a consumer that reconstructs it can disagree with the observation it
// is reporting on, and a console that reconstructs it is guessing at a fact the
// control plane already holds.
//
// It is a property of the transport, not a device lifecycle. A device has no
// lifecycle beyond its identity and its observation history, so nothing here
// describes a state an operator advances.
type Transport string

const (
	// TransportUnspecified is a transport that was never observed, such as an
	// endpoint record with no observation behind it. It is the zero value, and
	// it is reported as unspecified rather than guessed into a transport.
	TransportUnspecified Transport = ""
	TransportUSB         Transport = "usb"
	TransportTCP         Transport = "tcp"
)

// Valid reports whether the transport is one an observation can produce.
func (t Transport) Valid() bool {
	return t == TransportUSB || t == TransportTCP
}

// TransportOf reports the transport one observation was made over, read from
// the transport address the observation itself carries.
//
// This is the single place the classification is decided. The endpoint record
// stores its result, and every reader of that record — the registry, the
// transport boundary, the console — reads the stored value rather than
// rebuilding it from an address, so no reader can reach a different answer.
//
// A device the observation reached at a TCP address was observed over TCP. A
// USB transport carries no address at all: it is named by its hardware serial,
// which is not a host:port.
func TransportOf(serial, host string, port uint16) Transport {
	if host != "" || port > 0 {
		return TransportTCP
	}
	if _, _, ok := SplitAddress(serial); ok {
		return TransportTCP
	}
	return TransportUSB
}

// SplitAddress reads the host and port a TCP transport is named by. An
// enumeration names a device reached over TCP by the address it answers on,
// which is where the address comes from; ok is false for a USB serial, which is
// a hardware identifier carrying no address.
func SplitAddress(serial string) (host string, port uint16, ok bool) {
	host, portText, found := strings.Cut(serial, ":")
	if !found || host == "" || portText == "" {
		return "", 0, false
	}
	if strings.IndexFunc(portText, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", 0, false
	}
	value, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return "", 0, false
	}
	return host, uint16(value), true
}

type Endpoint struct {
	ID         EndpointID
	Workspace  organizations.WorkspaceID
	DeviceID   devices.DeviceID
	Transport  Transport
	Serial     string
	Host       string
	Port       uint16
	State      State
	ObservedAt time.Time
	// LinkState is what the transport reported about the device when this
	// endpoint was observed: usable, present-but-unauthorized,
	// present-but-not-openable-by-this-host, or listed-but-not-answering. It is
	// a property of the OBSERVATION, and it is deliberately separate from
	// State: State says whether this is the transport the device is at, and
	// LinkState says what that transport reported. Conflating them is what made
	// an attached-but-unauthorized device read as a device with no transport at
	// all (ARC-196).
	//
	// The zero value means the observation did not record one - history written
	// before the column existed - and never means "usable".
	LinkState LinkState
	// SupersededAt is when this endpoint stopped being the device's current
	// one, and it is nil for an endpoint that is still current. The endpoint
	// record is not deleted when the device moves: it stays as the history of
	// the transport the device was observed at, and this is the date that makes
	// the history readable rather than only present.
	SupersededAt *time.Time
}

// LinkState is what an endpoint's transport reported about the device when it
// was observed. Its vocabulary is the discovery model's own spelling of an
// observed link, so the stored fact and the observation that produced it are
// one vocabulary rather than two.
type LinkState string

const (
	// LinkStateUnrecorded is the zero value: no link state was recorded for
	// this endpoint. It is the state of history written before the column
	// existed and it settles nothing on its own.
	LinkStateUnrecorded LinkState = ""
	// LinkStateOnline is a transport the adapter can use the device at.
	LinkStateOnline LinkState = "online"
	// LinkStateOffline is a transport that is listed but not answering.
	LinkStateOffline LinkState = "offline"
	// LinkStateUnauthorized is a device that is present and has not authorized
	// this host: only the device's own operator can accept its debugging prompt,
	// and no host can accept it for the device.
	LinkStateUnauthorized LinkState = "unauthorized"
	// LinkStateNoPermissions is a transport this host may not open at all. It is
	// a host-side condition an operator can fix, and it is not the device
	// refusing this host.
	LinkStateNoPermissions LinkState = "no_permissions"
)

// Valid reports whether the link state is one an observation can record. The
// zero value is valid: it is the absence of the fact rather than a third state.
func (s LinkState) Valid() bool {
	switch s {
	case LinkStateUnrecorded, LinkStateOnline, LinkStateOffline, LinkStateUnauthorized, LinkStateNoPermissions:
		return true
	default:
		return false
	}
}

// Usable reports whether an action may be dispatched over a transport observed
// in this state. It fails closed on every recorded state except `online`, and
// it admits the zero value because a current endpoint written before this
// column existed was only ever made current when the device was usable.
//
// This is the ONE place the action path asks whether a transport may be used,
// so an observation the adapter could not act on is refused with the fact the
// transport reported rather than by a reader re-deriving it.
func (s LinkState) Usable() bool {
	return s == LinkStateOnline || s == LinkStateUnrecorded
}

// Condition is the clause that names what an unusable transport reported, for a
// refusal sentence an operator reads. It is one function so a single dispatch, a
// fleet-wide run and an error message cannot describe one condition three ways.
func (s LinkState) Condition() string {
	switch s {
	case LinkStateUnauthorized:
		return "present but unauthorized for this host"
	case LinkStateNoPermissions:
		return "present but not openable by this host"
	case LinkStateOffline:
		return "present but not answering"
	default:
		return "not usable"
	}
}

func (s State) Valid() bool {
	switch s {
	case Observed, Current, Superseded, Retired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case Observed:
		return to == Current || to == Superseded || to == Retired
	case Current:
		return to == Superseded || to == Retired
	case Superseded:
		return to == Retired
	case Retired:
		return false
	default:
		return false
	}
}

func Transition(from, to State) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("endpoint", string(from), string(to))
	}
	return nil
}
