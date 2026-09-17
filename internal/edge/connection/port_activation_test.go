package connection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The off-port device from the supervised run: a real address on a port the profile does
// not accept.
const offPortEndpoint = "192.168.1.106:5556"

func newActivatableConnector(t *testing.T, accepted []uint16) (*Connector, *recordingRunner) {
	t.Helper()
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{
		Runner:      runner,
		Policy:      NewPortPolicy(accepted),
		Activations: NewPortActivations(),
	})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	return connector, runner
}

// DONE BAR 2. A device on a port other than the accepted one is enabled by port
// activation: refused before the operator acts, and connectable by that device
// afterwards — with the refusal costing no device call in the meantime.
func TestAnOffPortEndpointIsRefusedUntilTheOperatorActivatesIt(t *testing.T) {
	connector, runner := newActivatableConnector(t, []uint16{5555})

	if _, err := connector.ConnectFor(context.Background(), labSerial, offPortEndpoint); err == nil {
		t.Fatal("an off-port endpoint connected without activation")
	} else {
		var refusal *PortNotAcceptedError
		if !errors.As(err, &refusal) {
			t.Fatalf("error = %v, want a *PortNotAcceptedError", err)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused off-port connect reached the device: %v", runner.calls)
	}

	activation, changed, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}
	if !changed {
		t.Fatal("ActivatePort reported no change; it recorded the activation")
	}
	if activation.Port != 5556 || activation.Endpoint != offPortEndpoint || activation.Serial != labSerial {
		t.Fatalf("activation = %+v, want the serial, endpoint and port that were activated", activation)
	}

	outcome, err := connector.ConnectFor(context.Background(), labSerial, offPortEndpoint)
	if err != nil {
		t.Fatalf("ConnectFor after activation = %v", err)
	}
	if outcome.Port != 5556 {
		t.Fatalf("outcome port = %d, want 5556", outcome.Port)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("the device was contacted %d times, want exactly 1", len(runner.calls))
	}
	if got := strings.Join(runner.calls[0], " "); got != "connect "+offPortEndpoint {
		t.Fatalf("the array was %q, want the adapter's own connect builder output", got)
	}
}

// An activation is a statement about ONE device. It must not become a statement that a
// port is acceptable for whichever device is discovered on it next.
func TestAnActivationAppliesToItsOwnDeviceAndNoOther(t *testing.T) {
	connector, runner := newActivatableConnector(t, []uint16{5555})

	if _, _, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0)); err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}

	_, err := connector.ConnectFor(context.Background(), "A-DIFFERENT-DEVICE", offPortEndpoint)
	var refusal *PortNotAcceptedError
	if !errors.As(err, &refusal) {
		t.Fatalf("ConnectFor for another device = %v, want a *PortNotAcceptedError", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("another device inherited an activation that was not made for it: %v", runner.calls)
	}
}

// An activation is also a statement about ONE endpoint: the same device at a different
// address is a different transport, and it has not been activated.
func TestAnActivationAppliesToItsOwnEndpointAndNoOther(t *testing.T) {
	connector, runner := newActivatableConnector(t, []uint16{5555})

	if _, _, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0)); err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}

	for _, other := range []string{"192.168.1.106:5557", "192.168.1.107:5556"} {
		if _, err := connector.ConnectFor(context.Background(), labSerial, other); err == nil {
			t.Fatalf("ConnectFor(%q) succeeded on an activation made for %q", other, offPortEndpoint)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("an activation widened past its endpoint: %v", runner.calls)
	}
}

// Activating a port the profile already accepts changes nothing, and says so: telling an
// operator they enabled something that was already permitted claims credit for work that
// did not happen.
func TestActivatingAnAlreadyAcceptedPortReportsNoChange(t *testing.T) {
	connector, _ := newActivatableConnector(t, []uint16{5555})

	_, changed, err := connector.ActivatePort(context.Background(), labSerial, acceptedEndpoint, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}
	if changed {
		t.Fatal("ActivatePort reported a change for a port the profile already accepts")
	}

	_, changedAgain, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}
	if !changedAgain {
		t.Fatal("the first activation of an off-port endpoint reported no change")
	}
	_, repeat, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}
	if repeat {
		t.Fatal("re-activating the same endpoint reported a change; it is already in force")
	}
}

// The endpoint-only path is the recovery path, and an activation must not widen it: a
// decision made about a device cannot become permission for a serial-free connect.
func TestAnEndpointOnlyConnectDoesNotUseAnActivation(t *testing.T) {
	connector, runner := newActivatableConnector(t, []uint16{5555})

	if _, _, err := connector.ActivatePort(context.Background(), labSerial, offPortEndpoint, time.Unix(0, 0)); err != nil {
		t.Fatalf("ActivatePort = %v", err)
	}
	if _, err := connector.Connect(context.Background(), offPortEndpoint); err == nil {
		t.Fatal("an endpoint-only connect used a device's activation; permission granted for a device must not become permission without one")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("an endpoint-only connect reached the device: %v", runner.calls)
	}
}

func TestActivationRefusesAMalformedEndpointAndRecordsNothing(t *testing.T) {
	connector, _ := newActivatableConnector(t, []uint16{5555})

	for _, bad := range []string{"lab.local:5556", "192.168.1.106", "192.168.1.106:70000", " 192.168.1.106:5556"} {
		if _, _, err := connector.ActivatePort(context.Background(), labSerial, bad, time.Unix(0, 0)); err == nil {
			t.Fatalf("ActivatePort(%q) succeeded; the endpoint is not one", bad)
		}
	}
	if _, ok := connector.activations.For(labSerial); ok {
		t.Fatal("a malformed endpoint was recorded as an activation")
	}
}

// A connector built without activations refuses every off-port endpoint: the fail-closed
// default, so a forgotten field cannot open a port.
func TestAConnectorWithoutActivationsRefusesEveryOffPortEndpoint(t *testing.T) {
	runner := &recordingRunner{}
	connector, err := NewConnector(ConnectorConfig{Runner: runner, Policy: NewPortPolicy([]uint16{5555})})
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}

	if _, err := connector.ConnectFor(context.Background(), labSerial, offPortEndpoint); err == nil {
		t.Fatal("a connector without activations connected to an off-port endpoint")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused off-port connect reached the device: %v", runner.calls)
	}
}
