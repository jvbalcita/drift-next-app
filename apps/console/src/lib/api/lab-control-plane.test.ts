import { create } from "@bufbuild/protobuf"
import { describe, expect, it } from "vitest"
import { LabMode, LabObservationBundleSchema, LabReadiness, LabStatusSchema } from "@/gen/drift/v1/lab_adapter_pb"
import { LabAdapterRequestError, type LabAdapterClient } from "./lab-adapter-client"
import { applyLabIntent, isLabControlPlaneIntent, labIntentFailure } from "./lab-control-plane"

const status = create(LabStatusSchema, {
  mode: LabMode.LAB,
  readiness: LabReadiness.READY,
  adapterVersion: "lab-adapter 0.13.0",
})

// stubClient records the calls the hybrid path makes without opening a socket.
function stubClient(overrides: Partial<LabAdapterClient> = {}) {
  const calls: string[] = []
  const client = {
    captureLabObservation: () => {
      calls.push("capture")
      return Promise.resolve({ status, observation: create(LabObservationBundleSchema, { serial: "SERIAL1", previewBase64: "aGk=" }) })
    },
    ...overrides,
  } as unknown as LabAdapterClient
  return { client, calls }
}

const context = { workspaceId: "workspace-1", operatorId: "operator-1" }

describe("lab control plane routing", () => {
  it("routes the device-scoped capture to the adapter and leaves every other intent alone", () => {
    expect(isLabControlPlaneIntent({ type: "captureLabObservation", serial: "SERIAL1" })).toBe(true)
    expect(isLabControlPlaneIntent({ type: "refresh" })).toBe(false)
    expect(isLabControlPlaneIntent({ type: "cancelRun", runId: "run-1" })).toBe(false)
    expect(isLabControlPlaneIntent({ type: "startScan", profileId: "profile-1" })).toBe(false)
    // The mock-only QA affordance must never reach a lab adapter.
    expect(isLabControlPlaneIntent({ type: "simulateLabCaptureFailure" })).toBe(false)
  })

  it("projects the capture onto the adapter view without inventing a target", async () => {
    const { client, calls } = stubClient()

    const captured = await applyLabIntent(client, { type: "captureLabObservation", serial: "SERIAL1" }, context)

    expect(calls).toEqual(["capture"])
    expect(captured).toMatchObject({ mode: "lab", readiness: "ready", lastObservedSerial: "SERIAL1" })
    expect(captured.lastScreenshotPreviewDataUrl).toBe("data:image/png;base64,aGk=")
  })

  it("refuses to project a response with no status", async () => {
    const { client } = stubClient({ captureLabObservation: () => Promise.resolve({}) })

    await expect(applyLabIntent(client, { type: "captureLabObservation", serial: "SERIAL1" }, context)).rejects.toBeInstanceOf(LabAdapterRequestError)
  })

  it("distinguishes a refusal from an unreachable adapter", () => {
    const refused = labIntentFailure({ type: "captureLabObservation", serial: "SERIAL1" }, new LabAdapterRequestError("policy_denied", "operator is not permitted"))
    const unreachable = labIntentFailure({ type: "captureLabObservation", serial: "SERIAL1" }, new TypeError("Failed to fetch"))

    expect(refused.ok).toBe(false)
    expect(refused.message).toContain("policy_denied")
    expect(unreachable.message).toContain("could not be reached")
  })
})
