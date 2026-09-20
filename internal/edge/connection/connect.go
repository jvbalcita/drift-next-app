package connection

import (
	"context"
	"errors"
	"time"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// HostCommandRunner is the narrow half of the device adapter that a connection
// needs: the host-level allow-list. It is an interface so a test can count calls
// and prove that a refusal reached nothing at all.
type HostCommandRunner interface {
	RunHostAllowlisted(ctx context.Context, args []string) (adb.Result, error)
}

type ConnectorConfig struct {
	Runner HostCommandRunner
	Policy PortPolicy
	// Activations carries the operator's per-device port decisions. A connector built
	// without one refuses every off-port endpoint, which is the fail-closed default:
	// activation is something an operator does, not something that happens because a
	// field was left empty.
	Activations *PortActivations
	// Current is the registry read every connect is bounded by: an address is only
	// dialled when this plane currently observes it there. A connector built without
	// one refuses EVERY connect, which is the fail-closed default for the same
	// reason an absent activation is - the alternative is an adapter that dials
	// addresses from a list it cannot check, which is how a retired range keeps
	// being re-dialled (ARC-231).
	Current CurrentEndpointSource
}

type Connector struct {
	runner      HostCommandRunner
	policy      PortPolicy
	activations *PortActivations
	current     CurrentEndpointSource
}

func NewConnector(cfg ConnectorConfig) (*Connector, error) {
	if cfg.Runner == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a connection runner is required")
	}
	activations := cfg.Activations
	if activations == nil {
		activations = NewPortActivations()
	}
	return &Connector{runner: cfg.Runner, policy: cfg.Policy, activations: activations, current: cfg.Current}, nil
}

// ConnectOutcome is what a connect actually did for one endpoint: the operation
// that ran, the port it was opened to, and the adapter's own result, so that a
// caller reports what happened rather than that something did.
type ConnectOutcome struct {
	Endpoint  string
	Port      uint16
	Operation string
	Result    adb.Result
}

// Connect opens one transport. The port decision is made here, before the runner is
// consulted, so a refused port costs no device call at all. The array handed to the
// runner is the adapter's own builder, so the caller cannot express a command the
// allow-list would have to filter.
func (c *Connector) Connect(ctx context.Context, endpoint string) (ConnectOutcome, error) {
	port, err := c.admittedPort(ctx, endpoint, "", false)
	if err != nil {
		return ConnectOutcome{}, err
	}
	return c.connectTo(ctx, endpoint, port)
}

// ConnectFor opens a transport on behalf of a NAMED device, so the port decision can
// honour an activation the operator made for that device and for no other. An endpoint
// on its own cannot carry that meaning, which is why this is a separate entry point
// rather than an extra argument on Connect: the serial is the identity, and the endpoint
// is the mutable fact being decided about.
func (c *Connector) ConnectFor(ctx context.Context, serial, endpoint string) (ConnectOutcome, error) {
	port, err := c.admittedPort(ctx, endpoint, serial, true)
	if err != nil {
		return ConnectOutcome{}, err
	}
	return c.connectTo(ctx, endpoint, port)
}

// ActivatePort records the operator's decision to accept this device's endpoint on a
// port the profile does not accept. It reports whether anything CHANGED, because
// "activated" and "already accepted" are different things to tell an operator and
// reporting the second as the first claims credit for something that did not happen.
func (c *Connector) ActivatePort(ctx context.Context, serial, endpoint string, at time.Time) (PortActivation, bool, error) {
	if err := ctx.Err(); err != nil {
		return PortActivation{}, false, err
	}
	port, err := adb.EndpointPort(endpoint)
	if err != nil {
		return PortActivation{}, false, err
	}
	if c.policy.Accepts(port) {
		return PortActivation{Serial: serial, Endpoint: endpoint, Port: port, ActivatedAt: at.UTC()}, false, nil
	}
	if existing, ok := c.activations.For(serial); ok && existing.Covers(serial, endpoint, port) {
		return existing, false, nil
	}
	return c.activations.Activate(serial, endpoint, port, at), true, nil
}

// admittedPort validates the endpoint's shape and decides its port: the profile's
// accepted set always, and — only for a named device, only when the operator has
// activated exactly this endpoint and port for it — the activation.
func (c *Connector) admittedPort(ctx context.Context, endpoint, serial string, allowActivation bool) (uint16, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	port, err := c.policy.CheckEndpoint(endpoint)
	if err == nil {
		return port, nil
	}
	if allowActivation {
		var refusal *PortNotAcceptedError
		if errors.As(err, &refusal) && c.activations.Covers(serial, endpoint, refusal.Port) {
			return refusal.Port, nil
		}
	}
	return 0, err
}

// connectTo is the single path both entries share, so a policy decision, the
// currency check and the array that follows them cannot drift between them.
//
// The currency check is first and it is the one that makes a retired range
// undialable: an address this plane does not currently observe costs no device
// call at all, and the refusal says which fact is missing rather than only that
// the connect failed. Every connect this adapter makes goes through here -
// including the recovery path's, which re-establishes the endpoints it was handed
// - so a reconnect cannot reach an address that nothing has observed, whichever
// surface asked for it (ARC-231).
func (c *Connector) connectTo(ctx context.Context, endpoint string, port uint16) (ConnectOutcome, error) {
	current, err := currentEndpointSet(ctx, c.current)
	if err != nil {
		return ConnectOutcome{}, err
	}
	canonical, spellable := CanonicalEndpoint(endpoint)
	if !spellable {
		// An address this adapter's own rule cannot spell is refused by the
		// builder below with its own reason; the currency check simply cannot
		// admit it, and it must not read as "current" by accident.
		return ConnectOutcome{}, platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint must be IPv4:port")
	}
	if _, observed := current[canonical]; !observed {
		return ConnectOutcome{}, &EndpointNotCurrentError{Endpoint: canonical, Current: len(current)}
	}
	argv, err := adb.ConnectArgv(endpoint)
	if err != nil {
		return ConnectOutcome{}, err
	}
	result, err := c.runner.RunHostAllowlisted(ctx, argv)
	outcome := ConnectOutcome{Endpoint: endpoint, Port: port, Operation: "connect", Result: result}
	if err != nil {
		return outcome, err
	}
	return outcome, nil
}
