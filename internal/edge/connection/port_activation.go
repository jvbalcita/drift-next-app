package connection

import (
	"sync"
	"time"
)

// PortActivation records an operator's decision to open a transport to ONE device on
// ONE port, and the moment they made it.
//
// The identity rule is the repository's own: the activation is keyed by the device's
// stable serial, and the endpoint and port it carries are mutable transport facts. That
// is why the endpoint is compared as well as the port — an activation for
// 192.168.1.106:5556 is a statement about that address, not a statement that port 5556
// is now generally acceptable, and it is certainly not a statement about any other
// device that happens to be discovered on that port later.
type PortActivation struct {
	Serial      string
	Endpoint    string
	Port        uint16
	ActivatedAt time.Time
}

// Covers reports whether this activation is a statement about exactly this endpoint and
// port for this device.
func (a PortActivation) Covers(serial, endpoint string, port uint16) bool {
	return a.Serial == serial && a.Endpoint == endpoint && a.Port == port
}

// PortActivations is the set of activations in force. It is safe for concurrent use
// because the console's Activate control and a connect can arrive at the same time.
type PortActivations struct {
	mu       sync.Mutex
	bySerial map[string]PortActivation
}

func NewPortActivations() *PortActivations {
	return &PortActivations{bySerial: make(map[string]PortActivation)}
}

// Activate records the decision and returns what is now in force for that device. A
// second activation for the same device replaces the first: a device has one transport
// identity at a time, and keeping a list of stale ones would let a device be connected
// at an address it no longer has.
func (a *PortActivations) Activate(serial, endpoint string, port uint16, at time.Time) PortActivation {
	activation := PortActivation{Serial: serial, Endpoint: endpoint, Port: port, ActivatedAt: at.UTC()}
	if a == nil {
		return activation
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bySerial == nil {
		a.bySerial = make(map[string]PortActivation)
	}
	a.bySerial[serial] = activation
	return activation
}

// For returns the activation in force for a device, if any.
func (a *PortActivations) For(serial string) (PortActivation, bool) {
	if a == nil {
		return PortActivation{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	activation, ok := a.bySerial[serial]
	return activation, ok
}

// Covers reports whether THIS device has an activation for exactly this endpoint and
// port. A nil set covers nothing, so a connector built without activations refuses every
// off-port endpoint rather than accepting them.
func (a *PortActivations) Covers(serial, endpoint string, port uint16) bool {
	activation, ok := a.For(serial)
	if !ok {
		return false
	}
	return activation.Covers(serial, endpoint, port)
}
