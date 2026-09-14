package transportconnect_test

import (
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

func TestValidateAssistanceRequestRejectsOversizedResponseBudget(t *testing.T) {
	request := &driftv1.ProposeRequest{
		Workspace:        &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		RequestId:        "request-1",
		UseCase:          driftv1.AssistanceUseCase_ASSISTANCE_USE_CASE_RUN_EXPLANATION,
		MaxResponseBytes: 65537,
	}

	err := transportconnect.ValidateAssistanceRequest(request)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ValidateAssistanceRequest() code = %v, want %v (err: %v)", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument, err)
	}
}

func TestValidateAssistanceRequestRejectsEvidenceWithoutBoundedSanitizedReference(t *testing.T) {
	request := &driftv1.ProposeRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		RequestId: "request-1",
		UseCase:   driftv1.AssistanceUseCase_ASSISTANCE_USE_CASE_UNKNOWN_SCREEN_SUMMARY,
		Evidence: []*driftv1.SanitizedEvidenceReference{
			{ArtifactId: "artifact-1", ContentHash: "", MediaType: "image/png", SchemaVersion: 1},
		},
	}

	err := transportconnect.ValidateAssistanceRequest(request)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ValidateAssistanceRequest() code = %v, want %v (err: %v)", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument, err)
	}
}

func TestValidateAssistanceRequestRejectsSensitiveEvidenceMetadata(t *testing.T) {
	request := &driftv1.ProposeRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: "workspace-1"},
		RequestId: "request-1",
		UseCase:   driftv1.AssistanceUseCase_ASSISTANCE_USE_CASE_UNKNOWN_SCREEN_SUMMARY,
		Evidence: []*driftv1.SanitizedEvidenceReference{
			{ArtifactId: "artifact-1", ContentHash: "sha256:abc", MediaType: "token=example-value", SchemaVersion: 1},
		},
	}

	err := transportconnect.ValidateAssistanceRequest(request)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ValidateAssistanceRequest() code = %v, want %v (err: %v)", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument, err)
	}
}

func TestValidateAssistanceResponseRejectsOversizedOrSensitiveProposal(t *testing.T) {
	request := &driftv1.ProposeRequest{MaxResponseBytes: 16}
	response := &driftv1.ProposeResponse{Suggestion: &driftv1.AssistanceSuggestion{ProposalJson: "0123456789abcdefg"}}

	err := transportconnect.ValidateAssistanceResponse(request, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("oversized response code = %v, want invalid argument", connectrpc.CodeOf(err))
	}

	response.Suggestion.ProposalJson = `{"token":"[REDACTED]"}`
	err = transportconnect.ValidateAssistanceResponse(request, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("sensitive response code = %v, want invalid argument", connectrpc.CodeOf(err))
	}
}
