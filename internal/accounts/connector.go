package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrConnectorsDisabled is returned until a separately approved connector
// boundary exists. Account references remain useful without external access.
var ErrConnectorsDisabled = errors.New("external account connectors are disabled")

// SyncRequest is the future connector contract. It carries only a typed
// account-source reference and operation metadata; it never carries a
// credential, session, workbook, or provider client.
type SyncRequest struct {
	Workspace      string
	SourceID       AccountSourceID
	IdempotencyKey string
	CorrelationID  string
	DryRun         bool
}

func (r SyncRequest) Validate() error {
	if strings.TrimSpace(r.Workspace) == "" || strings.TrimSpace(string(r.SourceID)) == "" || strings.TrimSpace(r.IdempotencyKey) == "" || strings.TrimSpace(r.CorrelationID) == "" {
		return fmt.Errorf("connector sync request requires workspace, source, idempotency, and correlation metadata")
	}
	return nil
}

type SyncResult struct {
	Outcome   SyncOutcome
	EventID   AccountSyncEventID
	Attempted bool
}

// Connector is intentionally narrow. Implementations must be separately
// approved and must preserve dry-run, idempotency, timeout, and audit rules.
type Connector interface {
	Sync(context.Context, SyncRequest) (SyncResult, error)
}

type DisabledConnector struct{}

func (DisabledConnector) Sync(ctx context.Context, request SyncRequest) (SyncResult, error) {
	if ctx == nil {
		return SyncResult{Outcome: SyncDisabled}, fmt.Errorf("context is required")
	}
	if err := request.Validate(); err != nil {
		return SyncResult{Outcome: SyncDisabled}, err
	}
	return SyncResult{Outcome: SyncDisabled}, ErrConnectorsDisabled
}
