package connection

import (
	"context"
	"fmt"

	"drift.local/drift-next/internal/edge/adb"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// TransportEnumerator is the host-level read a restart needs: how many transports
// the adb server holds. It is asked BEFORE anything is killed, because a restart
// that cannot measure what it is about to drop must not drop it. It is an interface
// so a test can supply the before and after counts deterministically.
type TransportEnumerator interface {
	Enumerate(ctx context.Context) ([]adb.DiscoveredDevice, error)
}

type RestarterConfig struct {
	Runner     HostCommandRunner
	Enumerator TransportEnumerator
	Policy     PortPolicy
	// Current is the registry read this restart is bounded by. A restart that
	// cannot read it must not drop the adb server: the kill has already happened
	// by the time an endpoint is dialled, so a restart that discovered there that
	// nothing is current would have stranded every device it held. The set is
	// therefore read BEFORE the kill, and only endpoints in it are re-established.
	Current CurrentEndpointSource
}

type Restarter struct {
	connector  *Connector
	runner     HostCommandRunner
	enumerator TransportEnumerator
	current    CurrentEndpointSource
}

func NewRestarter(cfg RestarterConfig) (*Restarter, error) {
	if cfg.Runner == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a restart runner is required")
	}
	if cfg.Enumerator == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "a transport enumerator is required, so that a restart can measure what it drops")
	}
	// The recovery re-establishes through the SAME connect path an operator uses, so
	// the policy, the currency bound, the refusal and the array cannot drift between
	// the two.
	connector, err := NewConnector(ConnectorConfig{Runner: cfg.Runner, Policy: cfg.Policy, Current: cfg.Current})
	if err != nil {
		return nil, err
	}
	return &Restarter{connector: connector, runner: cfg.Runner, enumerator: cfg.Enumerator, current: cfg.Current}, nil
}

// EndpointOutcome is one endpoint's result. It is reported PER ENDPOINT because an
// aggregate verdict hides which device was left unreachable: if fourteen devices
// recover and one does not, the one has to be named, with its reason.
type EndpointOutcome struct {
	Endpoint      string
	Port          uint16
	Reestablished bool
	ExitCode      int
	Err           error
}

// RestartOutcome is what the restart actually did, including the counts that make a
// bare "ok" impossible: how many transports existed before, how many existed once
// the server was restarted, and which endpoints came back. A restart that reports
// only success hides what it dropped.
type RestartOutcome struct {
	TransportsBefore     int
	TransportsAfterStart int
	KillExitCode         int
	KillFailed           bool
	StartExitCode        int
	StartFailed          bool
	Endpoints            []EndpointOutcome
	Reestablished        int
	Failed               int
}

// Restart drops the adb server's transports and re-establishes the endpoints it was
// given, in the order it was given them — operator order, carried rather than left
// to timing.
//
// An error is returned only for a REFUSAL: a cancelled context, an empty endpoint
// list, or a measurement that could not be taken. A per-endpoint failure is reported
// in the outcome instead, because a partial recovery is a result the operator needs
// to read, not an exception that discards it.
func (r *Restarter) Restart(ctx context.Context, endpoints []string) (RestartOutcome, error) {
	if err := ctx.Err(); err != nil {
		return RestartOutcome{}, err
	}
	if len(endpoints) == 0 {
		return RestartOutcome{}, platformerrors.New(platformerrors.CodeInvalidInput,
			"a restart needs at least one endpoint to re-establish: dropping the adb server with nothing to restore would strand every device it holds")
	}

	before, err := r.enumerator.Enumerate(ctx)
	if err != nil {
		return RestartOutcome{}, fmt.Errorf("refusing to restart the adb server: its current transports could not be read, so what would be dropped is unknown: %w", err)
	}
	outcome := RestartOutcome{TransportsBefore: len(before)}

	// The endpoints this plane currently observes are read BEFORE the kill, for the
	// same reason the transport count is: once the server is down, an endpoint that
	// turns out to be un-dialable is a device this restart has already stranded. An
	// address the registry does not hold as current is not re-dialled at all - it is
	// reported against its own entry - so a retired range handed to this path
	// re-establishes nothing and costs no device call.
	current, err := currentEndpointSet(ctx, r.current)
	if err != nil {
		return RestartOutcome{}, fmt.Errorf("refusing to restart the adb server: %w", err)
	}

	killResult, killErr := r.runner.RunHostAllowlisted(ctx, adb.KillServerArgv())
	outcome.KillExitCode, outcome.KillFailed = killResult.ExitCode, killErr != nil

	// The start is attempted even when the kill reported a failure. A server that was
	// never killed is unharmed by a start, whereas a server that WAS killed and left
	// down would leave every device unreachable — the failure mode this whole path
	// exists to avoid.
	startResult, startErr := r.runner.RunHostAllowlisted(ctx, adb.StartServerArgv())
	outcome.StartExitCode, outcome.StartFailed = startResult.ExitCode, startErr != nil

	// The count after the restart is the measurement that makes the state honest: the
	// restarted server holds nothing, which is the real not-connected state rather
	// than one the product fabricated.
	if after, afterErr := r.enumerator.Enumerate(ctx); afterErr == nil {
		outcome.TransportsAfterStart = len(after)
	} else {
		outcome.TransportsAfterStart = -1
	}

	for _, endpoint := range endpoints {
		canonical, spellable := CanonicalEndpoint(endpoint)
		if !spellable {
			outcome.Failed++
			outcome.Endpoints = append(outcome.Endpoints, EndpointOutcome{
				Endpoint: endpoint,
				Err:      platformerrors.New(platformerrors.CodeInvalidInput, "an endpoint must be IPv4:port"),
			})
			continue
		}
		if _, observed := current[canonical]; !observed {
			// Nothing this plane observes is at that address, so there is nothing
			// to re-establish and no dial is attempted: the refusal is the fact.
			outcome.Failed++
			outcome.Endpoints = append(outcome.Endpoints, EndpointOutcome{
				Endpoint: canonical,
				Err:      &EndpointNotCurrentError{Endpoint: canonical, Current: len(current)},
			})
			continue
		}
		connected, connectErr := r.connector.Connect(ctx, canonical)
		entry := EndpointOutcome{
			Endpoint: canonical,
			Port:     connected.Port,
			ExitCode: connected.Result.ExitCode,
			Err:      connectErr,
		}
		if connectErr == nil {
			entry.Reestablished = true
			outcome.Reestablished++
		} else {
			outcome.Failed++
		}
		outcome.Endpoints = append(outcome.Endpoints, entry)
	}
	return outcome, nil
}
