// Package transportconnect contains safe Connect adapters and contract validation.
package transportconnect

import (
	"strings"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	maxAssistanceEvidence     = 16
	maxAssistanceResponseSize = 65536
	maxEvidenceFieldBytes     = 512
)

// ValidateAssistanceRequest rejects malformed or unbounded assistance input
// before it reaches an optional provider boundary. The request contains only
// sanitized evidence metadata, never bytes, paths, credentials, or actions.
func ValidateAssistanceRequest(request *driftv1.ProposeRequest) error {
	if request == nil {
		return invalidArgument("assistance request is required")
	}
	if request.GetWorkspace() == nil || strings.TrimSpace(request.GetWorkspace().GetWorkspaceId()) == "" {
		return invalidArgument("workspace ID is required")
	}
	if strings.TrimSpace(request.GetRequestId()) == "" {
		return invalidArgument("request ID is required")
	}
	if request.GetUseCase() == driftv1.AssistanceUseCase_ASSISTANCE_USE_CASE_UNSPECIFIED {
		return invalidArgument("assistance use case is required")
	}
	if request.GetMaxResponseBytes() > maxAssistanceResponseSize {
		return invalidArgument("assistance response budget exceeds the safe limit")
	}
	return validateEvidence(request.GetEvidence())
}

// ValidateAssistanceResponse blocks oversized or credential-bearing provider
// output. It deliberately rejects rather than silently mutates a proposal so
// review provenance remains truthful.
func ValidateAssistanceResponse(request *driftv1.ProposeRequest, response *driftv1.ProposeResponse) error {
	if response == nil {
		return invalidArgument("assistance response is required")
	}
	suggestion := response.GetSuggestion()
	if suggestion == nil {
		return nil
	}
	budget := uint32(maxAssistanceResponseSize)
	if request != nil && request.GetMaxResponseBytes() != 0 {
		budget = request.GetMaxResponseBytes()
	}
	if len(suggestion.GetProposalJson()) > int(budget) {
		return invalidArgument("assistance proposal exceeds the requested response budget")
	}
	if redaction.RedactString(suggestion.GetProposalJson()) != suggestion.GetProposalJson() {
		return invalidArgument("assistance proposal contains sensitive material")
	}
	return validateEvidence(suggestion.GetEvidence())
}

func validateEvidence(evidence []*driftv1.SanitizedEvidenceReference) error {
	if len(evidence) > maxAssistanceEvidence {
		return invalidArgument("assistance evidence count exceeds the safe limit")
	}
	for _, reference := range evidence {
		if reference == nil || strings.TrimSpace(reference.GetArtifactId()) == "" || strings.TrimSpace(reference.GetContentHash()) == "" || strings.TrimSpace(reference.GetMediaType()) == "" || reference.GetSchemaVersion() == 0 {
			return invalidArgument("each assistance evidence reference must be bounded and sanitized")
		}
		if len(reference.GetArtifactId()) > maxEvidenceFieldBytes || len(reference.GetContentHash()) > maxEvidenceFieldBytes || len(reference.GetMediaType()) > maxEvidenceFieldBytes {
			return invalidArgument("assistance evidence reference exceeds the safe limit")
		}
	}
	return nil
}

func invalidArgument(message string) error {
	return connectrpc.NewError(connectrpc.CodeInvalidArgument, &safeError{message: message})
}

type safeError struct{ message string }

func (e *safeError) Error() string { return e.message }
