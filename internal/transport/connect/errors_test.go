package transportconnect_test

import (
	"errors"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestMapErrorUsesSafeClientMessageWithoutDiagnosticCause(t *testing.T) {
	mapped := transportconnect.MapError(platformerrors.Wrap(
		platformerrors.CodeInternal,
		"request could not be completed",
		errors.New("diagnostic: [REDACTED] private subprocess output"),
	))

	if connectrpc.CodeOf(mapped) != connectrpc.CodeInternal {
		t.Fatalf("MapError() code = %v, want %v", connectrpc.CodeOf(mapped), connectrpc.CodeInternal)
	}
	if strings.Contains(mapped.Error(), "private subprocess") {
		t.Fatalf("MapError() leaked diagnostic cause: %q", mapped.Error())
	}
}
