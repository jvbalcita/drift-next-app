import { create } from "@bufbuild/protobuf"
import {
  type AssistanceUseCase,
  ProposeRequestSchema,
  type SanitizedEvidenceReference,
} from "@/gen/drift/v1/assistance_pb"
import { WorkspaceRefSchema } from "@/gen/drift/v1/common_pb"

export type AssistanceEvidenceInput = Pick<
  SanitizedEvidenceReference,
  "artifactId" | "contentHash" | "mediaType" | "schemaVersion"
>

export type AssistanceRequestInput = {
  workspaceId: string
  requestId: string
  useCase: AssistanceUseCase
  evidence: AssistanceEvidenceInput[]
  maxResponseBytes?: number
}

// createAssistanceRequest is a generated-type seam only. It serializes no
// provider choice, raw evidence bytes, device command, token, or lease value.
export function createAssistanceRequest(input: AssistanceRequestInput) {
  return create(ProposeRequestSchema, {
    workspace: create(WorkspaceRefSchema, { workspaceId: input.workspaceId }),
    requestId: input.requestId,
    useCase: input.useCase,
    evidence: input.evidence,
    maxResponseBytes: input.maxResponseBytes ?? 0,
  })
}
