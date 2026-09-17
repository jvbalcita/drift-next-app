package transportconnect_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/connection"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

// recordingConnections is the application boundary these tests drive. It records
// every call so a refusal can be proven to have reached no transport, and answers
// with the real outcome and refusal shapes the connection package produces.
type recordingConnections struct {
	connectOutcome connection.ConnectOutcome
	connectErr     error

	modeOutcome connection.ActivationOutcome
	modeErr     error

	activation     connection.PortActivation
	activationNew  bool
	activationErr  error
	activationTime time.Time

	restartOutcome connection.RestartOutcome
	restartErr     error

	fleetOutcome connection.FleetActivationReport
	fleetErr     error

	connects  []string
	forwards  []string
	modes     []string
	activates []string
	fleets    []uint16
	restarts  [][]string
}

func (c *recordingConnections) Connect(_ context.Context, endpoint string) (connection.ConnectOutcome, error) {
	c.connects = append(c.connects, endpoint)
	return c.connectOutcome, c.connectErr
}

func (c *recordingConnections) ConnectFor(_ context.Context, serial, endpoint string) (connection.ConnectOutcome, error) {
	c.forwards = append(c.forwards, serial+"|"+endpoint)
	return c.connectOutcome, c.connectErr
}

func (c *recordingConnections) ChangeTransportMode(_ context.Context, serial string, port uint16) (connection.ActivationOutcome, error) {
	c.modes = append(c.modes, serial+"|"+strconv.FormatUint(uint64(port), 10))
	return c.modeOutcome, c.modeErr
}

func (c *recordingConnections) ActivatePort(_ context.Context, serial, endpoint string, at time.Time) (connection.PortActivation, bool, error) {
	c.activates = append(c.activates, serial+"|"+endpoint)
	c.activationTime = at
	return c.activation, c.activationNew, c.activationErr
}

func (c *recordingConnections) ActivateFleet(_ context.Context, port uint16) (connection.FleetActivationReport, error) {
	c.fleets = append(c.fleets, port)
	return c.fleetOutcome, c.fleetErr
}

func (c *recordingConnections) Restart(_ context.Context, endpoints []string) (connection.RestartOutcome, error) {
	c.restarts = append(c.restarts, append([]string(nil), endpoints...))
	return c.restartOutcome, c.restartErr
}

func (c *recordingConnections) calls() int {
	return len(c.connects) + len(c.forwards) + len(c.modes) + len(c.activates) + len(c.fleets) + len(c.restarts)
}

func connectionCode(t *testing.T, err error) connectrpc.Code {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a refusal")
	}
	return connectrpc.CodeOf(err)
}

func connectionMessage(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a refusal")
	}
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a *connect.Error", err)
	}
	return connectErr.Message()
}

// A nil boundary is what lets the route be skipped entirely. The typed-nil case
// matters as much as the plain one: a *recordingConnections that was never
// assigned is an interface that is non-nil and must not be mounted.
func TestAConnectionHandlerIsNotBuiltWithoutATransportBoundary(t *testing.T) {
	if handler := transportconnect.NewConnectionHandler(nil); handler != nil {
		t.Fatalf("NewConnectionHandler(nil) = %#v, want nil", handler)
	}
	var typedNil *recordingConnections
	if handler := transportconnect.NewConnectionHandler(typedNil); handler != nil {
		t.Fatalf("NewConnectionHandler(typed nil) = %#v, want nil", handler)
	}
}

// A request with no request id is refused before any transport work: the id is
// the correlation identity of the call, and the refusal must cost no device call.
func TestARequestWithoutARequestIdIsRefusedBeforeAnyTransportWork(t *testing.T) {
	service := &recordingConnections{}
	handler := transportconnect.NewConnectionHandler(service)

	_, err := handler.ConnectEndpoint(context.Background(), connectrpc.NewRequest(&driftv1.ConnectEndpointRequest{
		Context:  &driftv1.RequestContext{},
		Endpoint: "192.168.1.106:5556",
	}))
	if code := connectionCode(t, err); code != connectrpc.CodeInvalidArgument {
		t.Fatalf("connect without a request id = %v, want invalid_argument", code)
	}
	if service.calls() != 0 {
		t.Fatalf("a refused request reached the transport %d time(s)", service.calls())
	}
}

// Done bar 4, through the route: an off-port endpoint is refused with its OWN
// code and a message that names the port, not with the generic internal answer.
// The refusal is what the console renders, so its identity on the wire is the
// property that matters — not just the error type inside the process.
func TestAnOffPortConnectKeepsItsOwnCodeAndNamesThePort(t *testing.T) {
	service := &recordingConnections{connectErr: &connection.PortNotAcceptedError{
		Endpoint: "192.168.1.106:5556",
		Port:     5556,
		Accepted: []uint16{5555},
	}}
	handler := transportconnect.NewConnectionHandler(service)

	_, err := handler.ConnectEndpoint(context.Background(), connectrpc.NewRequest(&driftv1.ConnectEndpointRequest{
		Context:  requestContext("connect-off-port"),
		Serial:   "R5CT42GS94Z",
		Endpoint: "192.168.1.106:5556",
	}))
	if code := connectionCode(t, err); code != connectrpc.CodePermissionDenied {
		t.Fatalf("an off-port connect = %v, want permission_denied", code)
	}
	message := connectionMessage(t, err)
	if !strings.Contains(message, "5556") || !strings.Contains(message, "5555") {
		t.Fatalf("the refusal message = %q, want it to name the refused port and the accepted set", message)
	}
	if strings.Contains(message, "request could not be completed") {
		t.Fatalf("the refusal answered with the generic internal message: %q", message)
	}
	// The named device travelled with the call: that is what lets the decision
	// honour an activation made for this device and no other.
	if len(service.forwards) != 1 || service.forwards[0] != "R5CT42GS94Z|192.168.1.106:5556" {
		t.Fatalf("connect calls = %v, want the serial-carrying entry point", service.forwards)
	}
}

// A connect with no serial is the endpoint-only path: the profile's accepted
// ports decide alone and no activation is consulted. The two paths stay
// distinguishable because the serial is what an activation is keyed on.
func TestAConnectWithoutASerialUsesTheEndpointOnlyPath(t *testing.T) {
	service := &recordingConnections{connectOutcome: connection.ConnectOutcome{
		Endpoint: "192.168.1.109:5555", Port: 5555, Operation: "connect",
		Result: adb.Result{ExitCode: 0, Stdout: []byte("connected to 192.168.1.109:5555")},
	}}
	handler := transportconnect.NewConnectionHandler(service)

	response, err := handler.ConnectEndpoint(context.Background(), connectrpc.NewRequest(&driftv1.ConnectEndpointRequest{
		Context:  requestContext("connect-endpoint-only"),
		Endpoint: "192.168.1.109:5555",
	}))
	if err != nil {
		t.Fatalf("connect = %v", err)
	}
	if len(service.connects) != 1 || len(service.forwards) != 0 {
		t.Fatalf("connect calls = %v forwards = %v, want the endpoint-only entry point", service.connects, service.forwards)
	}
	if response.Msg.GetPort() != 5555 || response.Msg.GetExitCode() != 0 {
		t.Fatalf("response = %#v, want port 5555 and the adapter's own exit code", response.Msg)
	}
	if !strings.Contains(response.Msg.GetOutput(), "connected to") {
		t.Fatalf("response output = %q, want the adapter's own report of what it did", response.Msg.GetOutput())
	}
}

// A device that comes back unauthorized after a transport-mode change is an
// OUTCOME, not an error: the change happened, and what it needs next is a person
// accepting the prompt on the device. Reporting it as a failure would invite a
// retry that cannot succeed.
func TestATransportModeChangeThatNeedsTheOperatorIsAnOutcomeAndNotAnError(t *testing.T) {
	service := &recordingConnections{modeOutcome: connection.ActivationOutcome{
		Serial: "R5CT42GS94Z", Port: 5556, StateBefore: "device", ConnectionBefore: "usb",
		StateAfter: "unauthorized", NeedsOperatorAuthorization: true, Settle: connection.SettleWindow,
	}}
	handler := transportconnect.NewConnectionHandler(service)

	response, err := handler.ChangeTransportMode(context.Background(), connectrpc.NewRequest(&driftv1.ChangeTransportModeRequest{
		Context: requestContext("tcpip-needs-operator"),
		Serial:  "R5CT42GS94Z",
		Port:    5556,
	}))
	if err != nil {
		t.Fatalf("a change that came back unauthorized returned an error: %v", err)
	}
	if !response.Msg.GetNeedsOperatorAuthorization() {
		t.Fatalf("response = %#v, want needs_operator_authorization", response.Msg)
	}
	if response.Msg.GetStateAfter() != "unauthorized" || response.Msg.GetStateBefore() != "device" {
		t.Fatalf("response = %#v, want both readings reported", response.Msg)
	}
	if !strings.Contains(response.Msg.GetMessage(), "USB debugging") {
		t.Fatalf("message = %q, want it to name the prompt the operator has to accept", response.Msg.GetMessage())
	}
	if len(service.modes) != 1 {
		t.Fatalf("transport-mode calls = %v, want exactly one", service.modes)
	}
}

// The three preconditions are three different instructions to an operator, so a
// failed one reaches the wire as its own sentence and its own code — and costs
// no device call, because the refusal happens before the change is attempted.
func TestARefusedTransportModeChangeCarriesItsOwnPrecondition(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason connection.ActivationRefusalReason
		want   string
	}{
		{"not attached", connection.ActivationRefusalNotAttached, "no such transport is attached"},
		{"not authorized", connection.ActivationRefusalNotAuthorized, "USB debugging prompt"},
		{"not usb", connection.ActivationRefusalNotUSB, "not physically attached"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &recordingConnections{modeErr: &connection.ActivationRefusalError{
				Serial: "R5CT42GS94Z", Reason: test.reason,
			}}
			handler := transportconnect.NewConnectionHandler(service)

			_, err := handler.ChangeTransportMode(context.Background(), connectrpc.NewRequest(&driftv1.ChangeTransportModeRequest{
				Context: requestContext("tcpip-refused"),
				Serial:  "R5CT42GS94Z",
				Port:    5556,
			}))
			if code := connectionCode(t, err); code != connectrpc.CodeFailedPrecondition {
				t.Fatalf("a refused change = %v, want failed_precondition", code)
			}
			if message := connectionMessage(t, err); !strings.Contains(message, test.want) {
				t.Fatalf("refusal message = %q, want it to contain %q", message, test.want)
			}
		})
	}
}

// A port that is not a port is refused at the boundary rather than narrowed into
// one: 70000 must not become 4464. The domain takes a uint16, so the bound is
// enforced here, and a refused port costs no device call.
func TestAnUnrepresentableTransportPortIsRefusedRatherThanNarrowed(t *testing.T) {
	for _, port := range []uint32{0, 65536, 70000} {
		service := &recordingConnections{}
		handler := transportconnect.NewConnectionHandler(service)
		_, err := handler.ChangeTransportMode(context.Background(), connectrpc.NewRequest(&driftv1.ChangeTransportModeRequest{
			Context: requestContext("tcpip-bad-port"),
			Serial:  "R5CT42GS94Z",
			Port:    port,
		}))
		if code := connectionCode(t, err); code != connectrpc.CodeInvalidArgument {
			t.Fatalf("port %d = %v, want invalid_argument", port, code)
		}
		if service.calls() != 0 {
			t.Fatalf("port %d reached the transport", port)
		}
	}
}

// A transport-mode change must name its device. A device command whose subject
// is whoever happens to be attached is worse than no command at all.
func TestATransportModeChangeWithoutASerialIsRefused(t *testing.T) {
	service := &recordingConnections{}
	handler := transportconnect.NewConnectionHandler(service)

	_, err := handler.ChangeTransportMode(context.Background(), connectrpc.NewRequest(&driftv1.ChangeTransportModeRequest{
		Context: requestContext("tcpip-no-serial"),
		Port:    5556,
	}))
	if code := connectionCode(t, err); code != connectrpc.CodeInvalidArgument {
		t.Fatalf("a change without a serial = %v, want invalid_argument", code)
	}
	if service.calls() != 0 {
		t.Fatal("a change without a serial reached the transport")
	}
}

// "Activated" and "already accepted" are different answers and stay different on
// the wire: reporting the second as the first claims credit for a change that
// did not happen.
func TestPortActivationKeepsActivatedAndAlreadyAcceptedApart(t *testing.T) {
	service := &recordingConnections{activation: connection.PortActivation{
		Serial: "R5CT42GS94Z", Endpoint: "192.168.1.106:5556", Port: 5556,
		ActivatedAt: time.Date(2026, time.September, 17, 1, 2, 3, 0, time.UTC),
	}}
	handler := transportconnect.NewConnectionHandler(service)

	activate := func(changed bool) *driftv1.ActivatePortResponse {
		t.Helper()
		service.activationNew = changed
		response, err := handler.ActivatePort(context.Background(), connectrpc.NewRequest(&driftv1.ActivatePortRequest{
			Context:  requestContext("activate-port"),
			Serial:   "R5CT42GS94Z",
			Endpoint: "192.168.1.106:5556",
		}))
		if err != nil {
			t.Fatalf("activate = %v", err)
		}
		return response.Msg
	}

	changed := activate(true)
	if !changed.GetChanged() || changed.GetPort() != 5556 {
		t.Fatalf("activation = %#v, want changed with the observed port", changed)
	}
	if changed.GetActivatedAt() == "" {
		t.Fatal("activation reported no timestamp")
	}
	if already := activate(false); already.GetChanged() {
		t.Fatalf("an already-accepted port = %#v, want changed=false", already)
	}
	if service.activationTime.IsZero() {
		t.Fatal("the activation was recorded with no time")
	}
	if len(service.activates) != 2 {
		t.Fatalf("activation calls = %v, want two", service.activates)
	}
}

// A restart reports per endpoint, names the one that did not come back with its
// reason, and never reports a count it could not read as zero.
func TestARestartReportsPerEndpointAndNeverInventsACount(t *testing.T) {
	service := &recordingConnections{restartOutcome: connection.RestartOutcome{
		TransportsBefore:     3,
		TransportsAfterStart: -1,
		KillExitCode:         0,
		StartExitCode:        0,
		Reestablished:        1,
		Failed:               1,
		Endpoints: []connection.EndpointOutcome{
			{Endpoint: "192.168.1.109:5555", Port: 5555, Reestablished: true, ExitCode: 0},
			{Endpoint: "192.168.1.111:5556", Port: 5556, ExitCode: 1, Err: platformerrors.New(platformerrors.CodePolicyDenied, "port 5556 is not in the profile's accepted ports")},
		},
	}}
	handler := transportconnect.NewConnectionHandler(service)

	response, err := handler.RestartServer(context.Background(), connectrpc.NewRequest(&driftv1.RestartServerRequest{
		Context:   requestContext("restart"),
		Endpoints: []string{"192.168.1.109:5555", "192.168.1.111:5556"},
	}))
	if err != nil {
		t.Fatalf("restart = %v", err)
	}
	if response.Msg.GetTransportsBefore() != 3 {
		t.Fatalf("transports_before = %d, want 3", response.Msg.GetTransportsBefore())
	}
	if response.Msg.GetTransportsAfterStartKnown() {
		t.Fatalf("an unreadable count was reported as known: %#v", response.Msg)
	}
	if response.Msg.GetReestablished() != 1 || response.Msg.GetFailed() != 1 {
		t.Fatalf("report = %#v, want 1 re-established and 1 failed", response.Msg)
	}
	if len(response.Msg.GetEndpoints()) != 2 {
		t.Fatalf("per-endpoint outcomes = %#v, want two", response.Msg.GetEndpoints())
	}
	failed := response.Msg.GetEndpoints()[1]
	if failed.GetEndpoint() != "192.168.1.111:5556" || failed.GetReestablished() {
		t.Fatalf("the endpoint that did not come back was not named: %#v", failed)
	}
	if !strings.Contains(failed.GetReason(), "5556") {
		t.Fatalf("the failed endpoint's reason = %q, want it to name why", failed.GetReason())
	}
	if len(service.restarts) != 1 || len(service.restarts[0]) != 2 ||
		service.restarts[0][0] != "192.168.1.109:5555" || service.restarts[0][1] != "192.168.1.111:5556" {
		t.Fatalf("restart endpoints = %v, want operator order carried verbatim", service.restarts)
	}
}

// A restart with nothing to restore, or with an empty entry, is refused before
// the server is touched: dropping the adb server with nothing to put back
// strands every device it holds.
func TestARestartWithoutRestorableEndpointsIsRefused(t *testing.T) {
	for name, endpoints := range map[string][]string{
		"none":         nil,
		"an empty one": {"192.168.1.109:5555", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			service := &recordingConnections{}
			handler := transportconnect.NewConnectionHandler(service)
			_, err := handler.RestartServer(context.Background(), connectrpc.NewRequest(&driftv1.RestartServerRequest{
				Context:   requestContext("restart-refused"),
				Endpoints: endpoints,
			}))
			if code := connectionCode(t, err); code != connectrpc.CodeInvalidArgument {
				t.Fatalf("restart with %s = %v, want invalid_argument", name, code)
			}
			if service.calls() != 0 {
				t.Fatalf("a refused restart touched the server: %v", service.restarts)
			}
		})
	}
}

// A collaborator that was not constructed answers "not available here" rather
// than delegating to a nil receiver, and it answers with a stable code rather
// than a generic internal failure.
func TestAnUnconstructedCollaboratorRefusesWithItsOwnCode(t *testing.T) {
	handler := transportconnect.NewConnectionHandler(transportconnect.ConnectionOperations{})
	for name, call := range map[string]func() error{
		"connect": func() error {
			_, err := handler.ConnectEndpoint(context.Background(), connectrpc.NewRequest(&driftv1.ConnectEndpointRequest{
				Context: requestContext("connect-unwired"), Endpoint: "192.168.1.109:5555",
			}))
			return err
		},
		"change transport mode": func() error {
			_, err := handler.ChangeTransportMode(context.Background(), connectrpc.NewRequest(&driftv1.ChangeTransportModeRequest{
				Context: requestContext("tcpip-unwired"), Serial: "R5CT42GS94Z", Port: 5556,
			}))
			return err
		},
		"activate port": func() error {
			_, err := handler.ActivatePort(context.Background(), connectrpc.NewRequest(&driftv1.ActivatePortRequest{
				Context: requestContext("activate-unwired"), Serial: "R5CT42GS94Z", Endpoint: "192.168.1.106:5556",
			}))
			return err
		},
		"activate fleet": func() error {
			_, err := handler.ActivateFleet(context.Background(), connectrpc.NewRequest(&driftv1.ActivateFleetRequest{
				Context: requestContext("activate-fleet-unwired"), Port: 5555,
			}))
			return err
		},
		"restart": func() error {
			_, err := handler.RestartServer(context.Background(), connectrpc.NewRequest(&driftv1.RestartServerRequest{
				Context: requestContext("restart-unwired"), Endpoints: []string{"192.168.1.109:5555"},
			}))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if code := connectionCode(t, call()); code != connectrpc.CodeUnavailable {
				t.Fatalf("%s on an unconstructed boundary = %v, want unavailable", name, code)
			}
		})
	}
}

// ONE OPERATOR ACTION, EVERY SERIAL'S OWN ANSWER. The counts make a bare "ok"
// impossible, and the per-serial entries are what the operator acts on: a device
// that came back unauthorized is an outcome rather than an error, a refusal
// names its own precondition, and a device already on the port is neither.
func TestAFleetActivationReportsEverySerialAndNeverOnlyACount(t *testing.T) {
	service := &recordingConnections{fleetOutcome: connection.FleetActivationReport{
		Port: 5555,
		Devices: []connection.FleetActivation{
			{Serial: "R5CT42GS94Z", Port: 5555, Activated: true, StateBefore: "device", StateAfter: "device"},
			{Serial: "ZY223UNAUTH", Port: 5555, NeedsOperatorAuthorization: true, StateBefore: "device", StateAfter: "unauthorized"},
			{Serial: "192.168.1.9:5556", Port: 5555, Refusal: connection.ActivationRefusalNotUSB, StateBefore: "device"},
			{Serial: "192.168.1.7:5555", Port: 5555, AlreadyOnPort: true, StateBefore: "device", StateAfter: "device"},
		},
	}}
	handler := transportconnect.NewConnectionHandler(service)

	response, err := handler.ActivateFleet(context.Background(), connectrpc.NewRequest(&driftv1.ActivateFleetRequest{
		Context: requestContext("activate-fleet"),
		Port:    5555,
	}))
	if err != nil {
		t.Fatalf("ActivateFleet = %v", err)
	}
	if len(service.fleets) != 1 || service.fleets[0] != 5555 {
		t.Fatalf("fleet calls = %v, want exactly one for port 5555", service.fleets)
	}
	body := response.Msg
	if body.GetPort() != 5555 {
		t.Fatalf("port = %d, want the port that was asked for", body.GetPort())
	}
	if body.GetActivated() != 1 || body.GetNeedsOperatorAuthorization() != 1 || body.GetRefused() != 1 || body.GetAlreadyOnPort() != 1 || body.GetFailed() != 0 {
		t.Fatalf("counts = activated %d, needs-authorization %d, refused %d, already-on-port %d, failed %d; want 1/1/1/1/0",
			body.GetActivated(), body.GetNeedsOperatorAuthorization(), body.GetRefused(), body.GetAlreadyOnPort(), body.GetFailed())
	}
	if len(body.GetDevices()) != 4 {
		t.Fatalf("devices = %d, want every serial the run reported", len(body.GetDevices()))
	}
	for _, device := range body.GetDevices() {
		if !strings.Contains(device.GetMessage(), device.GetSerial()) {
			t.Fatalf("the sentence for %s does not name it: %q", device.GetSerial(), device.GetMessage())
		}
	}
	if refusal := body.GetDevices()[2].GetRefusal(); refusal != "not_usb" {
		t.Fatalf("refusal = %q, want the classified reason rather than prose", refusal)
	}
	if body.GetDevices()[0].GetRefusal() != "" {
		t.Fatalf("an activated device carried a refusal: %q", body.GetDevices()[0].GetRefusal())
	}
	if !body.GetDevices()[1].GetNeedsOperatorAuthorization() {
		t.Fatal("a device that came back unauthorized was not reported as needing the operator")
	}
	if !body.GetDevices()[3].GetAlreadyOnPort() {
		t.Fatal("a device already on the port was not reported as such")
	}
}

// A change that ran and failed is reported with a BOUNDED, redacted diagnostic
// beside its own sentence, and it is reported as a failure rather than as a
// refusal: the two are different facts about a device, and conflating them would
// tell an operator to fix a precondition that was never the problem.
func TestAFleetActivationReportsAFailureWithABoundedDiagnostic(t *testing.T) {
	service := &recordingConnections{fleetOutcome: connection.FleetActivationReport{
		Port: 5555,
		Devices: []connection.FleetActivation{{
			Serial: "R5CT42GS94Z",
			Port:   5555,
			Err:    errors.New("adb tcpip exited 1: " + strings.Repeat("x", 4000)),
		}},
	}}
	handler := transportconnect.NewConnectionHandler(service)

	response, err := handler.ActivateFleet(context.Background(), connectrpc.NewRequest(&driftv1.ActivateFleetRequest{
		Context: requestContext("activate-fleet-failure"),
		Port:    5555,
	}))
	if err != nil {
		t.Fatalf("ActivateFleet = %v", err)
	}
	device := response.Msg.GetDevices()[0]
	if !device.GetFailed() {
		t.Fatal("a failed change was not reported as failed against its own serial")
	}
	if response.Msg.GetFailed() != 1 || response.Msg.GetActivated() != 0 {
		t.Fatalf("counts = failed %d, activated %d; want 1 failed and none activated", response.Msg.GetFailed(), response.Msg.GetActivated())
	}
	if device.GetRefusal() != "" {
		t.Fatalf("a failed change carried a refusal: %q", device.GetRefusal())
	}
	if !strings.Contains(device.GetMessage(), "UNKNOWN") {
		t.Fatalf("the sentence = %q, want it to say the device's state is unknown", device.GetMessage())
	}
	if len(device.GetMessage()) > 2000 {
		t.Fatalf("the sentence is %d bytes; a diagnostic has to be bounded", len(device.GetMessage()))
	}
	if !strings.Contains(device.GetMessage(), "adb tcpip exited 1") {
		t.Fatalf("the sentence = %q, want the bounded diagnostic beside it", device.GetMessage())
	}
}

// A port that is not a port is refused at the boundary, before the fleet is read
// at all: the caller cannot make the control plane read the fleet for a request
// it will never run.
func TestAFleetActivationRefusesAPortThatIsNotAPort(t *testing.T) {
	for name, port := range map[string]uint32{"zero": 0, "above the range": 70000} {
		t.Run(name, func(t *testing.T) {
			service := &recordingConnections{}
			handler := transportconnect.NewConnectionHandler(service)

			_, err := handler.ActivateFleet(context.Background(), connectrpc.NewRequest(&driftv1.ActivateFleetRequest{
				Context: requestContext("activate-fleet-invalid"),
				Port:    port,
			}))
			if code := connectionCode(t, err); code != connectrpc.CodeInvalidArgument {
				t.Fatalf("a fleet activation on port %d = %v, want invalid_argument", port, code)
			}
			if service.calls() != 0 {
				t.Fatalf("a refused fleet activation reached the fleet: %v", service.fleets)
			}
		})
	}
}
