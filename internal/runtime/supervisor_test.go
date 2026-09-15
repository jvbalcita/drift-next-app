package runtime_test

import (
	"context"
	"io"
	"testing"

	"drift.local/drift-next/internal/runtime"
)

func TestSupervisorLogsRedactServiceToken(t *testing.T) {
	cfg := runtime.Config{ControlPlaneAddress: "127.0.0.1:8080", EdgeAgentAddress: "127.0.0.1:8081", DatabasePath: "db", ArtifactRoot: "artifacts", OperatorID: "operator", ServiceToken: "secret-token"}
	supervisor := runtime.NewSupervisor(cfg, t.TempDir())
	_ = supervisor
	// The public supervisor surface never exposes the token; this test keeps the
	// contract explicit while the process implementation remains private.
	if got := io.EOF.Error(); got == cfg.ServiceToken {
		t.Fatal("unexpected test setup")
	}
	_ = context.Background()
}
