package connection

import (
	"context"

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
}

type Connector struct {
	runner HostCommandRunner
	policy PortPolicy
}

func NewConnector(cfg ConnectorConfig) (*Connector, error) {
	if cfg.Runner == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a connection runner is required")
	}
	return &Connector{runner: cfg.Runner, policy: cfg.Policy}, nil
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
	if err := ctx.Err(); err != nil {
		return ConnectOutcome{}, err
	}
	port, err := c.policy.CheckEndpoint(endpoint)
	if err != nil {
		return ConnectOutcome{}, err
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
