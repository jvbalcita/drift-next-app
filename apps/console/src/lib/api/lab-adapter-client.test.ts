import { create, toJson } from "@bufbuild/protobuf"
import { describe, expect, it, vi } from "vitest"
import { LabMode, LabObservationBundleSchema, LabReadiness, LabStatusSchema } from "@/gen/drift/v1/lab_adapter_pb"
import { LabAdapterRequestError, createLabAdapterClient, toLabAdapterView } from "./lab-adapter-client"
import { adapterServiceBaseUrl } from "./connect-json"

describe("lab adapter client", () => {
  it("stays on the mock path when no adapter endpoint is configured in tests", () => {
    expect(adapterServiceBaseUrl("")).toBe("")
    expect(adapterServiceBaseUrl("http://127.0.0.1:8787/")).toBe("http://127.0.0.1:8787")
    expect(createLabAdapterClient(undefined)).toBeUndefined()
    expect(createLabAdapterClient("")).toBeUndefined()
    expect(createLabAdapterClient("http://127.0.0.1:8787")).toBeDefined()
  })

  it("projects a lab status without inventing a preview or a target", () => {
    const status = create(LabStatusSchema, {
      mode: LabMode.LAB,
      readiness: LabReadiness.READY,
      adapterVersion: "lab-adapter 0.13.0",
      connectionState: "device",
      connectionType: "usb",
      observationLatencyMs: 412n,
      lastScreenshotHash: "sha256:abc",
      discovered: [{ serial: "SERIAL1", connectionState: "device", connectionType: "usb", transportId: "7", model: "Pixel 7a" }],
    })

    const view = toLabAdapterView(status)

    expect(view).toMatchObject({ mode: "lab", readiness: "ready", observationLatencyMs: 412, lastObservedSerial: "" })
    expect(view.lastScreenshotPreviewDataUrl).toBeUndefined()
    expect(view.discovered).toEqual([{ serial: "SERIAL1", state: "device", model: "Pixel 7a", transportId: "7", connectionType: "usb" }])
  })

  it("drops a truncated or empty preview and keeps a complete one", () => {
    const status = create(LabStatusSchema, { mode: LabMode.LAB, readiness: LabReadiness.READY })
    const truncated = create(LabObservationBundleSchema, { serial: "SERIAL1", previewBase64: "aGk=", previewTruncated: true })
    const empty = create(LabObservationBundleSchema, { serial: "SERIAL1" })
    const complete = create(LabObservationBundleSchema, { serial: "SERIAL1", previewBase64: "aGk=" })

    expect(toLabAdapterView(status, truncated).lastScreenshotPreviewDataUrl).toBeUndefined()
    expect(toLabAdapterView(status, empty).lastScreenshotPreviewDataUrl).toBeUndefined()
    expect(toLabAdapterView(status, complete).lastScreenshotPreviewDataUrl).toBe("data:image/png;base64,aGk=")
    expect(toLabAdapterView(status, complete).lastObservedSerial).toBe("SERIAL1")
  })

  it("sends the local lab token only when one is configured", async () => {
    const status = create(LabStatusSchema, { mode: LabMode.LAB, readiness: LabReadiness.BLOCKED })
    const requests: Request[] = []
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
      requests.push(new Request(input as RequestInfo, init))
      return Promise.resolve(new Response(JSON.stringify({ observation: {}, status: toJson(LabStatusSchema, status) }), { status: 200, headers: { "content-type": "application/json" } }))
    })

    const options = { workspaceId: "workspace-1", requestId: "request-1", operatorId: "operator-1" }
    await createLabAdapterClient("http://127.0.0.1:8787", "lab-token-value")?.captureLabObservation(options, "SERIAL1")
    await createLabAdapterClient("http://127.0.0.1:8787", "")?.captureLabObservation(options, "SERIAL1")

    expect(requests).toHaveLength(2)
    expect(requests[0]?.headers.get("X-Drift-Lab-Token")).toBe("lab-token-value")
    expect(requests[1]?.headers.get("X-Drift-Lab-Token")).toBeNull()
    fetchSpy.mockRestore()
  })

  it("reports a refused lab call as a typed error instead of a silent fallback", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ code: "unauthenticated", message: "lab adapter requires a valid local lab token" }), { status: 401, headers: { "content-type": "application/json" } }),
    )

    const client = createLabAdapterClient("http://127.0.0.1:8787", "wrong")
    await expect(client?.captureLabObservation({ workspaceId: "workspace-1", requestId: "request-1", operatorId: "operator-1" }, "SERIAL1")).rejects.toBeInstanceOf(LabAdapterRequestError)
    fetchSpy.mockRestore()
  })
})
