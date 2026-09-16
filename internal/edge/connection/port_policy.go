package connection

import (
	"fmt"
	"sort"

	"drift.local/drift-next/internal/edge/adb"
)

// A scan OBSERVES every ADB-enabled device in the profile's host range; the port is
// a discovered fact, never a filter. This is the single place where that fact
// becomes a decision, and it is deliberately the connect path rather than the list:
// refusing an off-port device at list time hides the very device port activation
// exists to enable, and Activate becomes unreachable for exactly those devices.
type PortPolicy struct {
	accepted []uint16
}

// NewPortPolicy records the ports a transport may be opened to. An empty set is
// legal and refuses every port, which is the fail-closed reading of a profile that
// declares none.
func NewPortPolicy(accepted []uint16) PortPolicy {
	unique := make(map[uint16]struct{}, len(accepted))
	for _, port := range accepted {
		unique[port] = struct{}{}
	}
	ports := make([]uint16, 0, len(unique))
	for port := range unique {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return PortPolicy{accepted: ports}
}

// AcceptedPorts returns the accepted ports in ascending order.
func (p PortPolicy) AcceptedPorts() []uint16 {
	out := make([]uint16, len(p.accepted))
	copy(out, p.accepted)
	return out
}

// Accepts reports whether a transport may be opened to a port.
func (p PortPolicy) Accepts(port uint16) bool {
	for _, candidate := range p.accepted {
		if candidate == port {
			return true
		}
	}
	return false
}

// PortNotAcceptedError is the refusal a connect makes when an endpoint's port is
// outside the profile's accepted set. It names the port and the reason, and it is
// distinguishable from every transport failure because no device was asked
// anything: the refusal costs no device call.
type PortNotAcceptedError struct {
	Endpoint string
	Port     uint16
	Accepted []uint16
}

func (e *PortNotAcceptedError) Error() string {
	return fmt.Sprintf("refusing to open a transport to %s: port %d is not in the profile's accepted ports %v, so no device was contacted",
		e.Endpoint, e.Port, e.Accepted)
}

// CheckEndpoint validates the endpoint's shape before reading anything out of it,
// then reads its port and decides. Validating first is deliberate: a policy that
// parsed an unvalidated token would be reading a number out of arbitrary text.
func (p PortPolicy) CheckEndpoint(endpoint string) (uint16, error) {
	if err := adb.ValidateEndpoint(endpoint); err != nil {
		return 0, err
	}
	port, err := adb.EndpointPort(endpoint)
	if err != nil {
		return 0, err
	}
	if !p.Accepts(port) {
		return port, &PortNotAcceptedError{Endpoint: endpoint, Port: port, Accepted: p.AcceptedPorts()}
	}
	return port, nil
}
