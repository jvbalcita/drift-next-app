import { describe, expect, it } from "vitest"
import { AssistanceUseCase } from "@/gen/drift/v1/assistance_pb"
import { createAssistanceRequest } from "./contracts"

describe("createAssistanceRequest", () => {
  it("creates a bounded metadata-only request without execution authority", () => {
    const request = createAssistanceRequest({
      workspaceId: "workspace-1",
      requestId: "request-1",
      useCase: AssistanceUseCase.RUN_EXPLANATION,
      evidence: [
        {
          artifactId: "artifact-1",
          contentHash: "sha256:abc",
          mediaType: "application/json",
          schemaVersion: 1,
        },
      ],
    })

    expect(request.workspace?.workspaceId).toBe("workspace-1")
    expect(request.evidence).toHaveLength(1)
    expect(Object.keys(request)).not.toEqual(expect.arrayContaining(["action", "command", "token", "lease", "fencingToken"]))
  })
})
