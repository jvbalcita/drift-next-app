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

  it("projects group memberships from ListDeviceGroups", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.GroupService/ListDeviceGroups")) {
        return new Response(JSON.stringify({
          groups: [{ id: "group-1", displayName: "Rack A", state: "GROUP_STATE_ACTIVE", rowVersion: "1" }],
          memberships: [{
            id: "mem-1",
            groupId: "group-1",
            deviceId: "device-pixel-1",
            position: 1,
            state: "active",
            startedAt: "2026-09-15",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(snapshot.groups).toEqual([expect.objectContaining({ id: "group-1", name: "Rack A" })])
    expect(snapshot.memberships).toEqual([expect.objectContaining({
      id: "mem-1",
      groupId: "group-1",
      deviceId: "device-pixel-1",
      position: 1,
      state: "active",
    })])
  })

  it("classifies mutation 401 as unauthorized", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("lab adapter requires a valid local lab token", { status: 401 }),
    )

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080" })
    const result = await client.dispatch({
      type: "createNetworkProfile",
      name: "Rack policy",
      addressPolicy: "192.0.2.0/24",
      ports: [5555],
      isDefault: false,
    })

    expect(result.ok).toBe(false)
    expect(result.errorCode).toBe("unauthorized")
  })
})
