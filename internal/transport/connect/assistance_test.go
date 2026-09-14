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
	response := &driftv1.ProposeResponse{Suggestion: validSuggestion("\"0123456789abcdefg\"")}

	err := transportconnect.ValidateAssistanceResponse(request, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("oversized response code = %v, want invalid argument", connectrpc.CodeOf(err))
	}

	request.MaxResponseBytes = 65536
	response.Suggestion.ProposalJson = `{"token":"example-value"}`
	err = transportconnect.ValidateAssistanceResponse(request, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("sensitive response code = %v, want invalid argument", connectrpc.CodeOf(err))
	}
}

func TestValidateAssistanceResponseRejectsUnsafeSuggestionMetadata(t *testing.T) {
	response := &driftv1.ProposeResponse{Suggestion: validSuggestion(`{"summary":"ok"}`)}
	response.Suggestion.Provider = "token=example-value"

	err := transportconnect.ValidateAssistanceResponse(nil, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ValidateAssistanceResponse() code = %v, want invalid argument", connectrpc.CodeOf(err))
	}
}

func TestValidateAssistanceResponseRejectsUnsafeTypedFailure(t *testing.T) {
	response := &driftv1.ProposeResponse{Failure: &driftv1.Failure{
		Code:    driftv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
		Message: "token=example-value",
	}}

	err := transportconnect.ValidateAssistanceResponse(nil, response)
	if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
		t.Fatalf("ValidateAssistanceResponse() code = %v, want invalid argument", connectrpc.CodeOf(err))
	}
}

func validSuggestion(proposalJSON string) *driftv1.AssistanceSuggestion {
	return &driftv1.AssistanceSuggestion{
		SuggestionId:          "suggestion-1",
		Kind:                  driftv1.SuggestionKind_SUGGESTION_KIND_SUMMARY,
		ProposalJson:          proposalJSON,
		Provider:              "optional-provider",
		Model:                 "optional-model",
		ModelVersion:          "v1",
		PromptTemplateVersion: "v1",
		Confidence:            0.8,
		Uncertainty:           "low",
		ExpiresAt:             "2026-09-14T00:00:00Z",
		Disposition:           driftv1.SuggestionDisposition_SUGGESTION_DISPOSITION_PENDING_REVIEW,
	}
}
