import { create } from "@bufbuild/protobuf"
import { describe, expect, it } from "vitest"
import { LabMode, LabObservationBundleSchema, LabReadiness, LabStatusSchema } from "@/gen/drift/v1/lab_adapter_pb"
import { LabAdapterRequestError, type LabAdapterClient } from "./lab-adapter-client"
import { applyLabIntent, isLabControlPlaneIntent, labIntentFailure } from "./lab-control-plane"

const status = create(LabStatusSchema, {
  mode: LabMode.LAB,
  readiness: LabReadiness.READY,
  confirmedSerial: "SERIAL1",
  confirmedDisplayName: "Lab bench",
  adapterVersion: "lab-adapter 0.13.0",
})

// stubClient records the calls the hybrid path makes without opening a socket.
function stubClient(overrides: Partial<LabAdapterClient> = {}) {
  const calls: string[] = []
  const client = {
    discoverLabDevices: () => { calls.push("discover"); return Promise.resolve(status) },
    confirmLabTarget: () => { calls.push("confirm"); return Promise.resolve(status) },
    clearLabTarget: () => { calls.push("clear"); return Promise.resolve(status) },
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
  it("routes lab intents to the adapter and leaves every other intent alone", () => {
    expect(isLabControlPlaneIntent({ type: "discoverLabDevices" })).toBe(true)
    expect(isLabControlPlaneIntent({ type: "clearLabTarget" })).toBe(true)
    expect(isLabControlPlaneIntent({ type: "captureLabObservation", serial: "SERIAL1" })).toBe(true)
    expect(isLabControlPlaneIntent({ type: "refresh" })).toBe(false)
    expect(isLabControlPlaneIntent({ type: "cancelRun", runId: "run-1" })).toBe(false)
    // The mock-only QA affordance must never reach a lab adapter.
    expect(isLabControlPlaneIntent({ type: "simulateLabCaptureFailure" })).toBe(false)
  })

  it("projects each lab intent onto the adapter view", async () => {
    const { client, calls } = stubClient()

    const discovered = await applyLabIntent(client, { type: "discoverLabDevices" }, context)
    await applyLabIntent(client, { type: "confirmLabTarget", serial: "SERIAL1", displayName: "Lab bench", confirmationText: "SERIAL1", reason: "Bring-up" }, context)
    await applyLabIntent(client, { type: "clearLabTarget" }, context)
    const captured = await applyLabIntent(client, { type: "captureLabObservation", serial: "SERIAL1" }, context)

    expect(calls).toEqual(["discover", "confirm", "clear", "capture"])
    expect(discovered).toMatchObject({ mode: "lab", readiness: "ready", confirmedSerial: "SERIAL1" })
    expect(captured.lastScreenshotPreviewDataUrl).toBe("data:image/png;base64,aGk=")
  })

  it("refuses to project a response with no status", async () => {
    const { client } = stubClient({ discoverLabDevices: () => Promise.resolve(undefined) })

    await expect(applyLabIntent(client, { type: "discoverLabDevices" }, context)).rejects.toBeInstanceOf(LabAdapterRequestError)
  })

  it("distinguishes a refusal from an unreachable adapter", () => {
    const refused = labIntentFailure({ type: "discoverLabDevices" }, new LabAdapterRequestError("policy_denied", "operator is not permitted"))
    const unreachable = labIntentFailure({ type: "discoverLabDevices" }, new TypeError("Failed to fetch"))

    expect(refused.ok).toBe(false)
    expect(refused.message).toContain("policy_denied")
    expect(unreachable.message).toContain("could not be reached")
  })
})
