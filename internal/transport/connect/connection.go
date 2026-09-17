package transportconnect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/connection"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// maxTransportPort is the largest port a transport operation may name. The
// domain takes a uint16, so a caller-supplied uint32 is refused ABOVE this
// bound rather than narrowed into it: a silent wrap would let 70000 become a
// port nobody asked for.
const maxTransportPort = 65535

// Connections is the application-service boundary this handler adapts: the four
// transport operations the OTG Setup tab performs. It is satisfied by
// ConnectionOperations, which delegates to the connector, the restarter and the
// activator this repository already has. The handler owns no connection
// behaviour of its own — it validates input, calls one of these, and maps a
// known refusal or failure to a stable code.
type Connections interface {
	// Connect opens one transport decided by the profile's port policy alone.
	Connect(ctx context.Context, endpoint string) (connection.ConnectOutcome, error)
	// ConnectFor opens one transport on behalf of a named device, so the port
	// decision can honour a port activation made for THAT device and no other.
	ConnectFor(ctx context.Context, serial, endpoint string) (connection.ConnectOutcome, error)
	// ChangeTransportMode restarts one device's adbd in TCP mode.
	ChangeTransportMode(ctx context.Context, serial string, port uint16) (connection.ActivationOutcome, error)
	// ActivatePort records the operator's decision to accept this device's
	// observed endpoint on a port the profile does not accept. It reports
	// whether anything changed, so "activated" and "already accepted" stay
	// different answers.
	ActivatePort(ctx context.Context, serial, endpoint string, at time.Time) (connection.PortActivation, bool, error)
	// ActivateFleet moves every discovered device that is not already answering
	// on the port onto it, one at a time, and reports each serial on its own.
	// The fleet is read from the devices, so nothing about it comes from the
	// caller.
	ActivateFleet(ctx context.Context, port uint16) (connection.FleetActivationReport, error)
	// Restart drops the adb server's transports and re-establishes the endpoints
	// it was given, in the order it was given them.
	Restart(ctx context.Context, endpoints []string) (connection.RestartOutcome, error)
}

// ConnectionOperations adapts the three collaborators the connection package
// exposes to the one boundary the handler depends on.
//
// Each method refuses when its collaborator was not constructed, rather than
// panicking or delegating to a nil receiver: a deployment that built a connector
// but no activator must answer "this operation is not available here" instead of
// taking the process down, and the failure has to be indistinguishable from a
// missing boundary rather than looking like a device answer.
type ConnectionOperations struct {
	Connector *connection.Connector
	Restarter *connection.Restarter
	Activator *connection.Activator
}

func (o ConnectionOperations) Connect(ctx context.Context, endpoint string) (connection.ConnectOutcome, error) {
	if o.Connector == nil {
		return connection.ConnectOutcome{}, errNotWired("connect")
	}
	return o.Connector.Connect(ctx, endpoint)
}

func (o ConnectionOperations) ConnectFor(ctx context.Context, serial, endpoint string) (connection.ConnectOutcome, error) {
	if o.Connector == nil {
		return connection.ConnectOutcome{}, errNotWired("connect")
	}
	return o.Connector.ConnectFor(ctx, serial, endpoint)
}

func (o ConnectionOperations) ChangeTransportMode(ctx context.Context, serial string, port uint16) (connection.ActivationOutcome, error) {
	if o.Activator == nil {
		return connection.ActivationOutcome{}, errNotWired("change transport mode")
	}
	return o.Activator.Activate(ctx, serial, port)
}

func (o ConnectionOperations) ActivatePort(ctx context.Context, serial, endpoint string, at time.Time) (connection.PortActivation, bool, error) {
	if o.Connector == nil {
		return connection.PortActivation{}, false, errNotWired("activate port")
	}
	return o.Connector.ActivatePort(ctx, serial, endpoint, at)
}

// ActivateFleet refuses when the activator was not constructed, for the same
// reason the other methods do: a deployment without one must answer that the
// operation is not available here rather than panic or appear to succeed.
func (o ConnectionOperations) ActivateFleet(ctx context.Context, port uint16) (connection.FleetActivationReport, error) {
	if o.Activator == nil {
		return connection.FleetActivationReport{}, errNotWired("activate fleet")
	}
	return o.Activator.ActivateFleet(ctx, port)
}

func (o ConnectionOperations) Restart(ctx context.Context, endpoints []string) (connection.RestartOutcome, error) {
	if o.Restarter == nil {
		return connection.RestartOutcome{}, errNotWired("restart")
	}
	return o.Restarter.Restart(ctx, endpoints)
}

func errNotWired(operation string) error {
	return platformerrors.New(platformerrors.CodeUnavailable, "the "+operation+" operation is not available in this deployment")
}

// ConnectionHandler adapts the transport boundary to the generated Connect
// service. It validates input and maps known refusals to stable codes; it
// performs no transport work and holds no device state.
type ConnectionHandler struct {
	service Connections
	clock   clock.Clock
}

// NewConnectionHandler returns a handler, or nil when no transport boundary was
// constructed. A nil handler is what lets the route be skipped entirely, so a
// deployment without one exposes no transport control rather than a control that
// can only refuse — the mounting rule this repository already applies to the
// device input and text reference surfaces.
func NewConnectionHandler(service Connections) *ConnectionHandler {
	if isAbsentConnections(service) {
		return nil
	}
	return &ConnectionHandler{service: service, clock: clock.System{}}
}

// isAbsentConnections reports a transport boundary this handler has nothing to
// call.
//
// The plain nil check is not enough: an interface can hold a typed nil, and
// `var c *ConnectionOperations; NewConnectionHandler(c)` compiles. Without this,
// that caller would get a mounted route whose first request fails at the call
// site instead of a route that is never mounted at all — a control an operator
// surface renders and finds dead, which is the outcome this gate exists to
// prevent. It is the same reasoning, and the same shape, as the device input
// boundary's own gate.
func isAbsentConnections(service Connections) bool {
	if service == nil {
		return true
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// ConnectEndpoint opens one transport. The serial is optional and its absence is
// meaningful: without one the profile's accepted ports decide alone, and with
// one an activation the operator made for that device is also admissible. That
// is why the two domain entry points are kept apart here rather than merged.
func (h *ConnectionHandler) ConnectEndpoint(ctx context.Context, request *connectrpc.Request[driftv1.ConnectEndpointRequest]) (*connectrpc.Response[driftv1.ConnectEndpointResponse], error) {
	if request == nil {
		return nil, invalidArgument("connect request is required")
	}
	// These calls change host and device transport state, so the boundary requires
	// an identified caller — a request id, or an actor id — before anything runs.
	// The actor is validated here and NOT persisted: the connection package's
	// operations take no actor, and recording them as append-only evidence is not
	// part of this slice. That gap is named rather than papered over by passing an
	// actor to something that does not consume one.
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	service, err := h.connections()
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(request.Msg.GetEndpoint())
	if endpoint == "" {
		return nil, invalidArgument("an endpoint is required")
	}
	serial := strings.TrimSpace(request.Msg.GetSerial())

	var outcome connection.ConnectOutcome
	if serial == "" {
		outcome, err = service.Connect(ctx, endpoint)
	} else {
		outcome, err = service.ConnectFor(ctx, serial, endpoint)
	}
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connectrpc.NewResponse(&driftv1.ConnectEndpointResponse{
		Endpoint: outcome.Endpoint,
		Port:     uint32(outcome.Port),
		ExitCode: int32(outcome.Result.ExitCode),
		Output:   adb.RedactOutput(outcome.Result.Stdout),
	}), nil
}

// ChangeTransportMode restarts one device's adbd in TCP mode and reports the
// state read AFTER the change.
//
// A device that comes back unauthorized is returned as a successful response
// carrying needs_operator_authorization: the change happened, and what it needs
// next is a person accepting the debugging prompt on the device's screen rather
// than a retry. Reporting it as an error would invite exactly the wrong action.
func (h *ConnectionHandler) ChangeTransportMode(ctx context.Context, request *connectrpc.Request[driftv1.ChangeTransportModeRequest]) (*connectrpc.Response[driftv1.ChangeTransportModeResponse], error) {
	if request == nil {
		return nil, invalidArgument("transport mode request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	service, err := h.connections()
	if err != nil {
		return nil, err
	}
	serial := strings.TrimSpace(request.Msg.GetSerial())
	if serial == "" {
		return nil, invalidArgument("a serial is required: the change restarts that device's adbd and must name it")
	}
	port, err := transportPort(request.Msg.GetPort())
	if err != nil {
		return nil, err
	}

	outcome, err := service.ChangeTransportMode(ctx, serial, port)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connectrpc.NewResponse(&driftv1.ChangeTransportModeResponse{
		Serial:                     outcome.Serial,
		Port:                       uint32(outcome.Port),
		StateBefore:                outcome.StateBefore,
		ConnectionBefore:           outcome.ConnectionBefore,
		StateAfter:                 outcome.StateAfter,
		NeedsOperatorAuthorization: outcome.NeedsOperatorAuthorization,
		Message:                    outcome.Message(),
		ExitCode:                   int32(outcome.ExitCode),
	}), nil
}

// ActivatePort enables one device's OBSERVED endpoint on a port the profile does
// not accept. It reports whether anything changed: an endpoint whose port the
// profile already accepts is answered as no change rather than as an activation.
func (h *ConnectionHandler) ActivatePort(ctx context.Context, request *connectrpc.Request[driftv1.ActivatePortRequest]) (*connectrpc.Response[driftv1.ActivatePortResponse], error) {
	if request == nil {
		return nil, invalidArgument("port activation request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	service, err := h.connections()
	if err != nil {
		return nil, err
	}
	serial := strings.TrimSpace(request.Msg.GetSerial())
	if serial == "" {
		return nil, invalidArgument("a serial is required: an activation is a statement about one device")
	}
	endpoint := strings.TrimSpace(request.Msg.GetEndpoint())
	if endpoint == "" {
		return nil, invalidArgument("the device's observed endpoint is required")
	}

	activation, changed, err := service.ActivatePort(ctx, serial, endpoint, h.clock.Now())
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return connectrpc.NewResponse(&driftv1.ActivatePortResponse{
		Serial:      activation.Serial,
		Endpoint:    activation.Endpoint,
		Port:        uint32(activation.Port),
		Changed:     changed,
		ActivatedAt: formatTime(activation.ActivatedAt),
	}), nil
}

// RestartServer drops the adb server and re-establishes the endpoints it was
// given, reporting per endpoint what came back.
//
// The endpoint list is carried verbatim, in operator order, and an empty entry
// is refused rather than dropped: silently removing one would change which
// devices the operator asked to restore.
func (h *ConnectionHandler) RestartServer(ctx context.Context, request *connectrpc.Request[driftv1.RestartServerRequest]) (*connectrpc.Response[driftv1.RestartServerResponse], error) {
	if request == nil {
		return nil, invalidArgument("restart request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	service, err := h.connections()
	if err != nil {
		return nil, err
	}
	given := request.Msg.GetEndpoints()
	if len(given) == 0 {
		return nil, invalidArgument("at least one endpoint is required: dropping the adb server with nothing to restore would strand every device it holds")
	}
	endpoints := make([]string, 0, len(given))
	for _, endpoint := range given {
		trimmed := strings.TrimSpace(endpoint)
		if trimmed == "" {
			return nil, invalidArgument("every endpoint in a restart must be named: an empty entry would restore nothing for a device the operator asked for")
		}
		endpoints = append(endpoints, trimmed)
	}

	outcome, err := service.Restart(ctx, endpoints)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	observed := make([]*driftv1.EndpointOutcome, 0, len(outcome.Endpoints))
	for _, entry := range outcome.Endpoints {
		observed = append(observed, endpointOutcomeProto(entry))
	}
	response := &driftv1.RestartServerResponse{
		TransportsBefore: uint32(outcome.TransportsBefore),
		KillExitCode:     int32(outcome.KillExitCode),
		KillFailed:       outcome.KillFailed,
		StartExitCode:    int32(outcome.StartExitCode),
		StartFailed:      outcome.StartFailed,
		Reestablished:    uint32(outcome.Reestablished),
		Failed:           uint32(outcome.Failed),
		Endpoints:        observed,
	}
	// A negative count is the domain's "the transports could not be read". It is
	// reported as unknown rather than as zero, because zero is a claim about the
	// fleet and this is a claim about the reading.
	if outcome.TransportsAfterStart >= 0 {
		response.TransportsAfterStartKnown = true
		response.TransportsAfterStart = uint32(outcome.TransportsAfterStart)
	}
	return connectrpc.NewResponse(response), nil
}

// ActivateFleet moves every discovered device that is not already answering on
// the port onto it, and reports each serial on its own.
//
// The request names a port and nothing else, because the fleet is read from the
// devices rather than taken from the caller: an operator action cannot assert
// which devices are attached, and a refusal or a failure is reported against the
// serial it happened to instead of ending the run.
func (h *ConnectionHandler) ActivateFleet(ctx context.Context, request *connectrpc.Request[driftv1.ActivateFleetRequest]) (*connectrpc.Response[driftv1.ActivateFleetResponse], error) {
	if request == nil {
		return nil, invalidArgument("fleet activation request is required")
	}
	if _, _, err := requireActor(request.Msg.GetContext()); err != nil {
		return nil, err
	}
	service, err := h.connections()
	if err != nil {
		return nil, err
	}
	port, err := transportPort(request.Msg.GetPort())
	if err != nil {
		return nil, err
	}

	report, err := service.ActivateFleet(ctx, port)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	devices := make([]*driftv1.FleetActivationOutcome, 0, len(report.Devices))
	for _, entry := range report.Devices {
		devices = append(devices, fleetActivationOutcomeProto(entry))
	}
	return connectrpc.NewResponse(&driftv1.ActivateFleetResponse{
		Port:                       uint32(report.Port),
		Activated:                  uint32(report.Activated()),
		NeedsOperatorAuthorization: uint32(report.NeedsOperatorAuthorization()),
		Refused:                    uint32(report.Refused()),
		Failed:                     uint32(report.Failed()),
		AlreadyOnPort:              uint32(report.AlreadyOnPort()),
		Devices:                    devices,
	}), nil
}

// fleetActivationOutcomeProto maps one per-serial result.
//
// The sentence and the failure detail are kept apart on purpose. The sentence is
// the domain's, and states what happened to that device; the detail of a failed
// change is redacted and bounded here, so a device's own error text cannot carry
// raw command output into the response field an operator reads.
func fleetActivationOutcomeProto(entry connection.FleetActivation) *driftv1.FleetActivationOutcome {
	message := entry.Message()
	if entry.Err != nil {
		message = message + ": " + boundedDiagnostic(entry.Err)
	}
	return &driftv1.FleetActivationOutcome{
		Serial:                     entry.Serial,
		Port:                       uint32(entry.Port),
		Activated:                  entry.Activated,
		AlreadyOnPort:              entry.AlreadyOnPort,
		Refusal:                    string(entry.Refusal),
		Message:                    message,
		NeedsOperatorAuthorization: entry.NeedsOperatorAuthorization,
		StateBefore:                entry.StateBefore,
		StateAfter:                 entry.StateAfter,
		ExitCode:                   int32(entry.ExitCode),
		Failed:                     entry.Err != nil,
	}
}

func endpointOutcomeProto(entry connection.EndpointOutcome) *driftv1.EndpointOutcome {
	reason := ""
	if entry.Err != nil {
		reason = boundedDiagnostic(entry.Err)
	}
	return &driftv1.EndpointOutcome{
		Endpoint:      entry.Endpoint,
		Port:          uint32(entry.Port),
		Reestablished: entry.Reestablished,
		ExitCode:      int32(entry.ExitCode),
		Reason:        reason,
	}
}

func (h *ConnectionHandler) connections() (Connections, error) {
	if h == nil || h.service == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the transport boundary is not constructed")
	}
	return h.service, nil
}

// transportPort refuses a uint32 that is not a port instead of narrowing it. The
// zero value is refused too: adbd listening on port 0 is not something an
// operator means.
func transportPort(requested uint32) (uint16, error) {
	if requested == 0 || requested > maxTransportPort {
		return 0, invalidArgument("a transport port must be between 1 and 65535")
	}
	return uint16(requested), nil
}

// mapConnectionError resolves a connection refusal to its own stable code and
// message before falling back to the shared mapper.
//
// The two refusals below are deliberately NOT platform errors: each carries its
// own reason and the sentence an operator has to act on, and letting the shared
// mapper see them would downgrade both to the generic internal answer. A refused
// port reached no device and is an authorization-shaped answer; a failed
// precondition names which of the three preconditions failed and what to do
// about it. They stay distinguishable from each other and from every transport
// failure, which is the property the card asks for.
func mapConnectionError(err error) error {
	if err == nil {
		return nil
	}
	var portRefusal *connection.PortNotAcceptedError
	if errors.As(err, &portRefusal) {
		return connectrpc.NewError(connectrpc.CodePermissionDenied, &safeError{message: portRefusal.Error()})
	}
	var preconditionRefusal *connection.ActivationRefusalError
	if errors.As(err, &preconditionRefusal) {
		return connectrpc.NewError(connectrpc.CodeFailedPrecondition, &safeError{message: preconditionRefusal.Error()})
	}
	return MapError(err)
}
