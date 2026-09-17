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
