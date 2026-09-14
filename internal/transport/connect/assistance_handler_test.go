package transportconnect_test

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

type fakeProposer struct{ called bool }

func (f *fakeProposer) Propose(context.Context, *driftv1.ProposeRequest) (*driftv1.ProposeResponse, error) {
	f.called = true
	return &driftv1.ProposeResponse{Failure: &driftv1.Failure{
		Code:    driftv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
		Message: "assistance is temporarily unavailable",
	}}, nil
}

func TestAssistanceHandlerRejectsInvalidInputBeforeOptionalProposer(t *testing.T) {
	proposer := &fakeProposer{}
	handler := transportconnect.NewAssistanceHandler(proposer)

	_, err := handler.Propose(context.Background(), connectrpc.NewRequest(&driftv1.ProposeRequest{}))
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("Propose() code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
	}
	if proposer.called {
		t.Fatal("Propose() called optional proposer for invalid input")
	}
}
