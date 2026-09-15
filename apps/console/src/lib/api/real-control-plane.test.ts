// @vitest-environment jsdom

import { create } from "@bufbuild/protobuf"
import { afterEach, describe, expect, it, vi } from "vitest"
import { DeviceSchema, DeviceStatus } from "@/gen/drift/v1/device_pb"
import { createRealControlPlaneClient, mapDevice } from "./real-control-plane"

afterEach(() => {
  vi.restoreAllMocks()
})

describe("mapDevice", () => {
  it("maps a list device onto the operator view without inventing fields", () => {
    const device = create(DeviceSchema, {
      id: "device-pixel-1",
      displayName: "Pixel One",
      agentId: "agent-1",
      status: DeviceStatus.ONLINE,
      platformVersion: "14",
      batteryPercent: 91,
      latencyMs: 18,
      lastSeenAt: "10:00:00",
      endpointId: "endpoint-1",
    })

    expect(mapDevice(device)).toMatchObject({
      id: "device-pixel-1",
      displayName: "Pixel One",
      status: "online",
      agentId: "agent-1",
      endpointId: "endpoint-1",
      location: "",
      workflow: "",
    })
  })
})

describe("RealControlPlaneClient", () => {
  it("loads list devices through Connect JSON and caches the snapshot", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{
            id: "device-pixel-1",
            displayName: "Pixel One",
            agentId: "agent-1",
            status: "DEVICE_STATUS_ONLINE",
            platformVersion: "14",
            batteryPercent: 91,
            latencyMs: 18,
            lastSeenAt: "10:00:00",
            endpointId: "endpoint-1",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(snapshot.devices).toEqual([expect.objectContaining({ id: "device-pixel-1", displayName: "Pixel One", status: "online" })])
    expect(snapshot.runtimeConnection.state).toBe("connected")
    expect(snapshot.accountSources).toEqual([])
    expect(client.getSnapshot().devices[0]?.id).toBe("device-pixel-1")
    expect(fetchSpy).toHaveBeenCalledWith(
      "http://127.0.0.1:8080/drift.v1.DeviceService/ListDevices",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-Drift-Lab-Token": "lab-token" }),
      }),
    )
  })

  it("surfaces a disconnected empty snapshot when the control plane is unreachable", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"))

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080" })
    const snapshot = await client.refresh()

    expect(snapshot.devices).toEqual([])
    expect(snapshot.accountSources).toEqual([])
    expect(snapshot.runtimeConnection.state).toBe("disconnected")
    expect(snapshot.labAdapter.readiness).toBe("unavailable")
    expect(snapshot.workspaceName).toBe("")
  })

  it("does not invent account connector success when sources are empty", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
    )

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080" })
    const snapshot = await client.refresh()

    expect(snapshot.accountSources).toEqual([])
    expect(snapshot.accounts).toEqual([])
    expect(snapshot.accountSyncEvents).toEqual([])
  })

  it("marks disconnected when every list call is unauthorized", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("lab adapter requires a valid local lab token", { status: 401 }),
    )

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080" })
    const snapshot = await client.refresh()

    expect(snapshot.devices).toEqual([])
    expect(snapshot.runtimeConnection.state).toBe("disconnected")
    expect(snapshot.runtimeConnection.disconnectedReason).toBe("Control plane authorization failed.")
  })
})
