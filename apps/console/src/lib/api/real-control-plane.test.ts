// @vitest-environment jsdom

import { create } from "@bufbuild/protobuf"
import { afterEach, describe, expect, it, vi } from "vitest"
import { DeviceSchema, DeviceStatus, DeviceTransport } from "@/gen/drift/v1/device_pb"
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

  it("does not expose the registry's unknown model sentinel as a phone model", () => {
    const device = create(DeviceSchema, {
      id: "device-without-model",
      platformVersion: "unknown",
      status: DeviceStatus.OFFLINE,
    })

    expect(mapDevice(device)).toMatchObject({ platformVersion: "unknown" })
    expect(mapDevice(device)).not.toHaveProperty("phoneModel")
  })

  it("does not label an Android release as a phone model", () => {
    const device = create(DeviceSchema, {
      id: "device-with-platform-version",
      platformVersion: "Android 14",
      status: DeviceStatus.ONLINE,
    })

    expect(mapDevice(device)).toMatchObject({ platformVersion: "Android 14" })
    expect(mapDevice(device)).not.toHaveProperty("phoneModel")
  })

  it("fails closed on a device the control plane has not observed", () => {
    // The wire status carries no lifecycle reading. UNSPECIFIED is what the
    // control plane reports for a device nobody has observed, and it is also what
    // a device that is no longer observed reports when it holds no last positive
    // observation. Either way the console must not offer control: an absent device
    // is the one thing eligibilityFor must never call eligible.
    const neverObserved = create(DeviceSchema, {
      id: "device-never-observed",
      status: DeviceStatus.UNSPECIFIED,
      lastSeenAt: "",
    })
    expect(mapDevice(neverObserved)).toMatchObject({ status: "unobserved", controlEligibility: "offline", lastSeen: "" })

    // A device that was observed and has since left arrives as OFFLINE with the
    // last observation it did have, so the operator can still see when it was last
    // seen while control stays closed.
    const departed = create(DeviceSchema, {
      id: "device-departed",
      status: DeviceStatus.OFFLINE,
      lastSeenAt: "10:00:00",
    })
    expect(mapDevice(departed)).toMatchObject({ status: "offline", controlEligibility: "offline", lastSeen: "10:00:00" })

    // And the two stay TELLABLE APART: "nobody has observed this device" and
    // "this device was observed and left" are different facts, so they never
    // reach the console as one status. Both remain ineligible.
    expect(mapDevice(neverObserved).status).not.toBe(mapDevice(departed).status)
  })

  it("maps an attached-but-unauthorized device to its own status, transport and eligibility", () => {
    // The control plane reports what the transport said, and the console turns it
    // into a reading an operator acts on: the unit is USB, it is present, and it
    // is NOT eligible for control until it authorizes this host.
    const unauthorized = create(DeviceSchema, {
      id: "device-unauthorized",
      status: DeviceStatus.UNAUTHORIZED,
      endpointId: "endpoint-unauthorized",
      transport: DeviceTransport.USB,
      lastSeenAt: "10:00:00",
    })
    expect(mapDevice(unauthorized)).toMatchObject({
      status: "unauthorized",
      transport: "usb",
      controlEligibility: "incompatible",
      lifecycle: "active",
      endpointId: "endpoint-unauthorized",
      lastSeen: "10:00:00",
    })

    // A transport this host may not open is its own reading: the two need
    // different things done, and folding them into one hides which.
    const noPermissions = create(DeviceSchema, {
      id: "device-no-permissions",
      status: DeviceStatus.NO_PERMISSIONS,
      endpointId: "endpoint-no-permissions",
      transport: DeviceTransport.USB,
      lastSeenAt: "10:00:00",
    })
    expect(mapDevice(noPermissions)).toMatchObject({ status: "no_permissions", transport: "usb", controlEligibility: "incompatible" })
    expect(mapDevice(noPermissions).status).not.toBe(mapDevice(unauthorized).status)

    // Neither is absent: an operator drawing these as gone would hide the very
    // units that have to be authorized (ARC-196).
    expect(mapDevice(unauthorized).lifecycle).not.toBe("unavailable")
  })

  it("reads the transport the control plane recorded instead of deriving it from the endpoint", () => {
    const tcp = create(DeviceSchema, {
      id: "device-tcp",
      status: DeviceStatus.ONLINE,
      endpointId: "endpoint-tcp",
      transport: DeviceTransport.TCP,
    })
    expect(mapDevice(tcp)).toMatchObject({ endpointId: "endpoint-tcp", transport: "tcp" })

    const usb = create(DeviceSchema, {
      id: "device-usb",
      status: DeviceStatus.ONLINE,
      endpointId: "endpoint-usb",
      transport: DeviceTransport.USB,
    })
    expect(mapDevice(usb)).toMatchObject({ endpointId: "endpoint-usb", transport: "usb" })

    // A device whose transport the control plane never observed reads as
    // unspecified. It has no endpoint address to guess from, and it is not
    // reported as either transport.
    const unobserved = create(DeviceSchema, { id: "device-unobserved", status: DeviceStatus.ONLINE })
    expect(mapDevice(unobserved).transport).toBe("unspecified")
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

  it("retains the last successful device and endpoint projections when a refresh is partial", async () => {
    let deviceReads = 0
    let endpointReads = 0
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        deviceReads += 1
        if (deviceReads > 1) return new Response("device projection unavailable", { status: 503 })
        return new Response(JSON.stringify({
          devices: [{
            id: "device-pixel-1",
            displayName: "Pixel One",
            status: "DEVICE_STATUS_ONLINE",
            platformVersion: "SM-G9750",
            lastSeenAt: "2026-09-17T12:00:00Z",
            endpointId: "endpoint-1",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.EndpointService/ListDeviceEndpoints")) {
        endpointReads += 1
        if (endpointReads > 1) return new Response("endpoint projection unavailable", { status: 503 })
        return new Response(JSON.stringify({
          endpoints: [{
            id: "endpoint-1",
            deviceId: "device-pixel-1",
            endpointType: "adb_tcp",
            serial: "R5CT42GS94Z",
            host: "192.0.2.10",
            port: 5555,
            state: "ENDPOINT_STATE_CURRENT",
            observedAt: "2026-09-17T12:00:00Z",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const refreshed = await client.refresh()

    expect(refreshed.devices).toEqual([expect.objectContaining({ id: "device-pixel-1", phoneModel: "SM-G9750" })])
    expect(refreshed.endpoints).toEqual([expect.objectContaining({ id: "endpoint-1", state: "current" })])
    expect(refreshed.projectionWarnings).toEqual(expect.arrayContaining([
      expect.objectContaining({ source: "devices" }),
      expect.objectContaining({ source: "endpoints" }),
    ]))
  })

  it("follows endpoint pages so current rows beyond endpoint history remain visible", async () => {
    let endpointReads = 0
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-mekeni-40", status: "DEVICE_STATUS_ONLINE", endpointId: "endpoint-current" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.EndpointService/ListDeviceEndpoints")) {
        endpointReads += 1
        const body = JSON.parse(String(init?.body ?? "{}")) as { page?: { pageToken?: string } }
        if (endpointReads === 1) {
          expect(body.page?.pageToken ?? "").toBe("")
          return new Response(JSON.stringify({
            endpoints: [{ id: "endpoint-history", deviceId: "device-mekeni-40", serial: "192.168.1.119:5555", host: "192.168.1.119", port: 5555, state: "ENDPOINT_STATE_SUPERSEDED" }],
            page: { nextPageToken: "200" },
          }), { status: 200, headers: { "content-type": "application/json" } })
        }
        expect(body.page?.pageToken).toBe("200")
        return new Response(JSON.stringify({
          endpoints: [{ id: "endpoint-current", deviceId: "device-mekeni-40", serial: "192.168.1.124:5555", host: "192.168.1.124", port: 5555, state: "ENDPOINT_STATE_CURRENT" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(endpointReads).toBe(2)
    expect(snapshot.projectionWarnings).toEqual([])
    expect(snapshot.endpoints).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "endpoint-current", host: "192.168.1.124", port: 5555, state: "current" }),
    ]))
  })

  it("does not join a device to a different current endpoint", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-pixel-1", status: "DEVICE_STATUS_ONLINE", endpointId: "endpoint-1" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.EndpointService/ListDeviceEndpoints")) {
        return new Response(JSON.stringify({
          endpoints: [{
            id: "endpoint-2",
            deviceId: "device-pixel-1",
            endpointType: "adb_tcp",
            serial: "R5CT42GS94Z",
            host: "192.0.2.11",
            port: 5555,
            state: "ENDPOINT_STATE_CURRENT",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(snapshot.projectionWarnings).toEqual([expect.objectContaining({ source: "endpoints" })])
    expect(snapshot.devices[0]?.endpointId).toBe("endpoint-1")
    expect(snapshot.endpoints[0]?.id).toBe("endpoint-2")
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

  it("does not treat a failed runtime status as a connected spool", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.RuntimeService/GetRuntimeStatus")) {
        return new Response("runtime unavailable", { status: 500 })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(snapshot.runtimeConnection.state).toBe("disconnected")
    expect(snapshot.runtimeConnection.disconnectedReason).toBe("Runtime status unavailable.")
    expect(snapshot.spoolHealth.connectionState).toBe("disconnected")
    expect(snapshot.indeterminateActions).toEqual([])
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

  it("deletes a Network Profile through Connect JSON and refreshes the catalog", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      urls.push(url)
      if (url.includes("/drift.v1.NetworkProfileService/ListNetworkProfiles")) {
        return new Response(JSON.stringify({ profiles: [] }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "deleteNetworkProfile", profileId: "profile-1", confirmed: true })

    expect(result.ok).toBe(true)
    expect(result.message).toBe("Network profile deleted.")
    expect(urls).toContain("http://127.0.0.1:8080/drift.v1.NetworkProfileService/DeleteNetworkProfile")
    expect(urls.filter((url) => url.endsWith("/ListNetworkProfiles"))).toHaveLength(1)
    expect(client.getSnapshot().networkProfiles).toEqual([])
  })

  it("refuses an unconfirmed Network Profile delete without contacting the control plane", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "deleteNetworkProfile", profileId: "profile-1", confirmed: false })

    expect(result.ok).toBe(false)
    expect(urls).toEqual([])
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

  it("projects durable scan, lease, observation, and run-target lists from Connect JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DiscoveryService/ListScanRuns")) {
        return new Response(JSON.stringify({
          scanRuns: [{
            id: "scan-1",
            networkProfileId: "profile-1",
            state: "SCAN_RUN_STATE_COMPLETED",
            requestedAt: "2026-09-15T01:00:00Z",
            finishedAt: "2026-09-15T01:01:00Z",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) {
        return new Response(JSON.stringify({
          leases: [{
            id: "lease-1",
            deviceId: "device-pixel-1",
            controlSessionId: "session-1",
            holderId: "console-local-operator",
            fencingToken: "7",
            state: "LEASE_STATE_ACTIVE",
            expiresAt: "2026-09-15T02:00:00Z",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.ObservationService/ListObservationSnapshots")) {
        return new Response(JSON.stringify({
          observations: [{
            id: "obs-1",
            deviceId: "device-pixel-1",
            capturedAt: "2026-09-15T01:02:00Z",
            source: "adb",
            captureState: "OBSERVATION_CAPTURE_STATE_COMPLETE",
            packageName: "com.android.settings",
            activityName: ".Settings",
            coordinateSpace: "display",
            freshnessToken: "fresh-1",
            artifacts: [{ artifactId: "art-1" }],
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.RunService/ListRunTargets")) {
        return new Response(JSON.stringify({
          targets: [{
            id: "target-1",
            runId: "run-1",
            deviceId: "device-pixel-1",
            state: "RUN_TARGET_STATE_FAILED",
            leaseId: "lease-1",
            observationId: "obs-1",
            failure: { message: "postcondition_failed" },
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()

    expect(snapshot.scanRuns).toEqual([expect.objectContaining({ id: "scan-1", state: "completed", networkProfileId: "profile-1" })])
    expect(snapshot.leases).toEqual([expect.objectContaining({
      id: "lease-1",
      holder: "console-local-operator",
      fencingToken: 7,
      state: "active",
    })])
    expect(snapshot.observations).toEqual([expect.objectContaining({
      id: "obs-1",
      source: "device",
      captureStatus: "complete",
      artifactCount: 1,
    })])
    expect(snapshot.runTargets).toEqual([expect.objectContaining({
      id: "target-1",
      runId: "run-1",
      state: "failed",
      leaseId: "lease-1",
      observationId: "obs-1",
      failureClass: "postcondition_failed",
    })])
  })

  it("opens a control session and acquires a lease for an explicitly selected device", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.LeaseService/OpenControlSession")) {
        return new Response(JSON.stringify({
          session: { id: "session-1", holderId: "operator", state: "CONTROL_SESSION_STATE_ACTIVE" },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.LeaseService/AcquireDeviceLease")) {
        return new Response(JSON.stringify({
          lease: {
            id: "lease-1",
            deviceId: "device-pixel-1",
            controlSessionId: "session-1",
            state: "LEASE_STATE_ACTIVE",
            fencingToken: "1",
          },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "beginDeviceControl", deviceId: "device-pixel-1" })

    expect(result.ok).toBe(true)
    expect(result.message).toMatch(/lease acquired/i)
    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.LeaseService/OpenControlSession",
      "http://127.0.0.1:8080/drift.v1.LeaseService/AcquireDeviceLease",
    ]))
  })

  it("reuses the current operator's active lease without opening another session", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) {
        return new Response(JSON.stringify({
          leases: [{
            id: "lease-1",
            deviceId: "device-pixel-1",
            controlSessionId: "session-1",
            holderId: "console-local-operator",
            fencingToken: "7",
            state: "LEASE_STATE_ACTIVE",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "beginDeviceControl", deviceId: "device-pixel-1" })

    expect(result.ok).toBe(true)
    expect(result.message).toMatch(/already has an active lease/i)
    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).not.toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.LeaseService/OpenControlSession",
    ]))
  })

  it("refuses to begin control when another holder already has the lease", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) {
        return new Response(JSON.stringify({
          leases: [{
            id: "lease-1",
            deviceId: "device-pixel-1",
            controlSessionId: "session-other",
            holderId: "other-operator",
            fencingToken: "3",
            state: "LEASE_STATE_ACTIVE",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "beginDeviceControl", deviceId: "device-pixel-1" })

    expect(result.ok).toBe(false)
    expect(result.conflict).toBe(true)
    expect(result.errorCode).toBe("unauthorized")
    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).not.toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.LeaseService/OpenControlSession",
      "http://127.0.0.1:8080/drift.v1.LeaseService/AcquireDeviceLease",
    ]))
  })

  it("blocks unconfirmed capture and actions without an active lease", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
    )
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()

    const unconfirmed = await client.dispatch({ type: "submitDeviceAction", deviceId: "device-pixel-1", kind: "capture", confirmed: false })
    expect(unconfirmed.ok).toBe(false)
    expect(unconfirmed.errorCode).toBe("precondition_failed")

    const unleashed = await client.dispatch({ type: "submitDeviceAction", deviceId: "device-pixel-1", kind: "observe", confirmed: false })
    expect(unleashed.ok).toBe(false)
    expect(unleashed.message).toMatch(/active lease/i)
  })

  it("submits an observe action with the active lease fencing token", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) {
        return new Response(JSON.stringify({
          leases: [{
            id: "lease-1",
            deviceId: "device-pixel-1",
            controlSessionId: "session-1",
            holderId: "console-local-operator",
            fencingToken: "7",
            state: "LEASE_STATE_ACTIVE",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.ActionService/SubmitAction")) {
        return new Response(JSON.stringify({
          result: { actionId: "action-1", outcome: "ACTION_OUTCOME_PENDING" },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "submitDeviceAction", deviceId: "device-pixel-1", kind: "observe", confirmed: false })

    expect(result.ok).toBe(true)
    const submitCall = fetchSpy.mock.calls.find((call) => String(call[0]).includes("/drift.v1.ActionService/SubmitAction"))
    expect(submitCall).toBeDefined()
    const body = JSON.parse(String((submitCall?.[1] as RequestInit | undefined)?.body)) as {
      intent?: { leaseId?: string; fencingToken?: string | number }
    }
    expect(body.intent?.leaseId).toBe("lease-1")
    expect(String(body.intent?.fencingToken)).toBe("7")
  })

  it("creates then starts a recording for an explicit device", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.RecordingService/CreateRecordingSession")) {
        return new Response(JSON.stringify({
          session: { id: "recording-1", deviceId: "device-pixel-1", state: "RECORDING_STATE_REQUESTED" },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.RecordingService/StartRecordingSession")) {
        return new Response(JSON.stringify({
          session: { id: "recording-1", deviceId: "device-pixel-1", state: "RECORDING_STATE_RECORDING" },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "beginRecording", deviceId: "device-pixel-1" })

    expect(result.ok).toBe(true)
    expect(result.resourceId).toBe("recording-1")
    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.RecordingService/CreateRecordingSession",
      "http://127.0.0.1:8080/drift.v1.RecordingService/StartRecordingSession",
    ]))
  })

  it("blocks unconfirmed recording deletion and skill publish", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
    )
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()

    const deleted = await client.dispatch({ type: "deleteRecording", sessionId: "recording-1", confirmed: false })
    expect(deleted.ok).toBe(false)
    expect(deleted.errorCode).toBe("precondition_failed")

    const published = await client.dispatch({ type: "publishSkillVersion", versionId: "skill-v1", reason: "publish", confirmed: false })
    expect(published.ok).toBe(false)
    expect(published.errorCode).toBe("precondition_failed")
  })

  it("projects latest skill version trust onto the snapshot", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.SkillService/ListSkills")) {
        return new Response(JSON.stringify({
          skills: [{ id: "skill-1", displayName: "Inbox", state: "SKILL_STATE_VALIDATED" }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.SkillService/ListSkillVersions")) {
        return new Response(JSON.stringify({
          versions: [{
            id: "skill-1-v2",
            skillId: "skill-1",
            version: 2,
            state: "SKILL_STATE_VALIDATED",
            trustState: "TRUST_STATE_REVIEWED",
            capabilities: ["observe", "capture"],
            sourceRecordingSessionId: "recording-1",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const snapshot = await client.refresh()
    expect(snapshot.skills).toEqual([expect.objectContaining({
      id: "skill-1",
      versionId: "skill-1-v2",
      version: 2,
      trust: "reviewed",
      capabilities: ["observe", "capture"],
      sourceRecording: "recording-1",
    })])
  })

  it("creates groups and agents and starts runs only with confirmation and devices", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
      new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
    )
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()

    const group = await client.dispatch({ type: "createDeviceGroup", name: "Rack D" })
    const agent = await client.dispatch({ type: "createAutomationAgent", name: "Night steward" })
    const assigned = await client.dispatch({ type: "assignAutomationAgentDevice", agentId: "agent-1", deviceId: "device-pixel-1" })
    const unconfirmed = await client.dispatch({ type: "startWorkflowRun", workflowId: "wf-1", deviceIds: ["device-pixel-1"], confirmed: false })
    const noDevices = await client.dispatch({ type: "startWorkflowRun", workflowId: "wf-1", deviceIds: [], confirmed: true })
    const started = await client.dispatch({ type: "startWorkflowRun", workflowId: "wf-1", deviceIds: ["device-pixel-1"], confirmed: true })
    const urls = fetchSpy.mock.calls.map((call) => String(call[0]))

    expect(group.ok).toBe(true)
    expect(agent.ok).toBe(true)
    expect(assigned.ok).toBe(true)
    expect(unconfirmed.ok).toBe(false)
    expect(noDevices.ok).toBe(false)
    expect(started.ok).toBe(true)
    expect(urls).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.GroupService/CreateDeviceGroup",
      "http://127.0.0.1:8080/drift.v1.AutomationAgentService/CreateAutomationAgent",
      "http://127.0.0.1:8080/drift.v1.AutomationAgentService/AssignAutomationAgentDevice",
      "http://127.0.0.1:8080/drift.v1.RunService/StartWorkflowRun",
    ]))
    expect(urls.filter((url) => url.includes("StartWorkflowRun"))).toHaveLength(1)
  })

  it("creates and publishes a workflow version only after confirmation", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
      new Response("{}", { status: 200, headers: { "content-type": "application/json" } }),
    )
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const unconfirmed = await client.dispatch({ type: "publishWorkflowVersion", versionId: "version-1", confirmed: false })
    const created = await client.dispatch({ type: "createWorkflow", name: "Observe Device" })
    const published = await client.dispatch({ type: "publishWorkflowVersion", versionId: "version-1", confirmed: true })
    const urls = fetchSpy.mock.calls.map((call) => String(call[0]))

    expect(unconfirmed.ok).toBe(false)
    expect(created.ok).toBe(true)
    expect(published.ok).toBe(true)
    expect(urls).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.WorkflowService/CreateWorkflow",
      "http://127.0.0.1:8080/drift.v1.WorkflowService/PublishWorkflowVersion",
    ]))
  })

  it("starts and stops mirror preview with explicit followers and lists sessions", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.MirrorService/StartMirrorPreview")) {
        return new Response(JSON.stringify({
          session: {
            id: "mirror-1",
            sourceDeviceId: "device-source",
            controlSessionId: "session-1",
            state: "MIRROR_SESSION_STATE_ACTIVE",
            targets: [
              { id: "target-1", deviceId: "device-follower", state: "MIRROR_TARGET_STATE_PENDING", detail: "Preview admitted; no command sent." },
              { id: "target-2", deviceId: "device-offline", state: "MIRROR_TARGET_STATE_FAILED", failureClass: "device_offline", detail: "Preview withheld: follower is not an active connected device." },
            ],
          },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.MirrorService/ListMirrorSessions")) {
        return new Response(JSON.stringify({
          sessions: [{
            id: "mirror-1",
            sourceDeviceId: "device-source",
            state: "MIRROR_SESSION_STATE_ACTIVE",
            createdAt: "now",
            targets: [
              { id: "target-1", deviceId: "device-follower", state: "MIRROR_TARGET_STATE_PENDING", detail: "Preview admitted; no command sent." },
            ],
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const missingFollowers = await client.dispatch({ type: "startMirrorPreview", sourceDeviceId: "device-source", followerDeviceIds: ["device-source"] })
    const started = await client.dispatch({ type: "startMirrorPreview", sourceDeviceId: "device-source", followerDeviceIds: ["device-follower", "device-offline"] })
    const stopped = await client.dispatch({ type: "stopMirrorPreview", sessionId: "mirror-1" })
    const urls = fetchSpy.mock.calls.map((call) => String(call[0]))

    expect(missingFollowers.ok).toBe(false)
    expect(started.ok).toBe(true)
    expect(stopped.ok).toBe(true)
    expect(client.getSnapshot().mirrorSessions).toEqual([expect.objectContaining({
      id: "mirror-1",
      sourceDeviceId: "device-source",
      state: "active",
      followerResults: [expect.objectContaining({ deviceId: "device-follower", outcome: "preview_admitted" })],
    })])
    expect(urls).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.MirrorService/StartMirrorPreview",
      "http://127.0.0.1:8080/drift.v1.MirrorService/StopMirrorPreview",
      "http://127.0.0.1:8080/drift.v1.MirrorService/ListMirrorSessions",
    ]))
  })

  it("disconnects and reconnects runtime without replaying blocked spool items", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.RuntimeService/GetRuntimeStatus")) {
        return new Response(JSON.stringify({
          connection: { state: "RUNTIME_CONNECTION_STATE_CONNECTED", transportId: "usb-a", protocol: "adb" },
          spool: { pending: 0, blocked: 0, fenceToken: 1, connectionState: "RUNTIME_CONNECTION_STATE_CONNECTED", blockedSequences: [] },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })
    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const disconnected = await client.dispatch({ type: "simulateRuntimeDisconnect", reason: "cable removed" })
    const begun = await client.dispatch({ type: "beginRuntimeReconnect" })
    const missingTransport = await client.dispatch({ type: "completeRuntimeReconnect", transportId: "", protocol: "adb" })
    const completed = await client.dispatch({ type: "completeRuntimeReconnect", transportId: "usb-b", protocol: "adb" })
    const urls = fetchSpy.mock.calls.map((call) => String(call[0]))

    expect(disconnected.ok).toBe(true)
    expect(begun.ok).toBe(true)
    expect(missingTransport.ok).toBe(false)
    expect(completed.ok).toBe(true)
    expect(urls).toEqual(expect.arrayContaining([
      "http://127.0.0.1:8080/drift.v1.RuntimeService/GetRuntimeStatus",
      "http://127.0.0.1:8080/drift.v1.RuntimeService/DisconnectRuntime",
      "http://127.0.0.1:8080/drift.v1.RuntimeService/BeginRuntimeReconnect",
      "http://127.0.0.1:8080/drift.v1.RuntimeService/CompleteRuntimeReconnect",
    ]))
  })

  it("projects the devices a scan observed with their link state and identity", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DiscoveryService/StartScan")) {
        return new Response(JSON.stringify({
          scanRun: {
            id: "scan-9",
            networkProfileId: "profile-1",
            state: "SCAN_RUN_STATE_COMPLETED",
            requestedAt: "2026-09-16T01:00:00Z",
            finishedAt: "2026-09-16T01:00:05Z",
          },
          devices: [
            {
              host: "192.0.2.5", port: 5555, serial: "mock-serial-1", model: "Mock Five",
              state: "DEVICE_LINK_STATE_ONLINE", known: true, deviceId: "device-1", endpointId: "endpoint-1",
            },
            {
              host: "192.0.2.6", port: 5555, serial: "mock-serial-2", model: "Mock Six",
              state: "DEVICE_LINK_STATE_UNAUTHORIZED", known: false,
            },
            {
              host: "192.0.2.7", port: 5555, serial: "mock-serial-3", model: "Mock Seven",
              state: "DEVICE_LINK_STATE_NO_PERMISSIONS", known: false,
            },
          ],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "startScan", profileId: "profile-1" })

    expect(result.ok).toBe(true)
    // The response carries the observation; the run alone would leave the
    // console unable to render what the scan saw.
    expect(client.getSnapshot().scanObservations).toEqual([
      expect.objectContaining({
        scanRunId: "scan-9", host: "192.0.2.5", port: 5555, serial: "mock-serial-1",
        state: "online", known: true, deviceId: "device-1", endpointId: "endpoint-1",
      }),
      expect.objectContaining({
        scanRunId: "scan-9", host: "192.0.2.6", serial: "mock-serial-2",
        state: "unauthorized", known: false, deviceId: "", endpointId: "",
      }),
      // A transport this host may not open at all. The wire carries it as its
      // own value now (it used to arrive as UNSPECIFIED); this surface has one
      // reading for "attached and not usable here", so it renders as
      // unauthorized exactly as it did before, and the fact is not lost on the
      // wire for other consumers.
      expect.objectContaining({
        scanRunId: "scan-9", host: "192.0.2.7", serial: "mock-serial-3",
        state: "unauthorized", known: false, deviceId: "", endpointId: "",
      }),
    ])
  })
})

describe("RealControlPlaneClient entered-range scan", () => {
  const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })

  it("names the entered range as the scan's target and carries no saved profile with it", async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (url.includes("/drift.v1.DiscoveryService/StartRangeScan")) {
        bodies.push(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>)
        return jsonResponse({
          scanRun: { id: "scan-11", state: "SCAN_RUN_STATE_COMPLETED", requestedAt: "2026-09-16T01:00:00Z", finishedAt: "2026-09-16T01:00:05Z" },
          devices: [{
            host: "192.168.1.20", port: 5555, serial: "mock-serial-9", model: "Mock Nine",
            state: "DEVICE_LINK_STATE_ONLINE", known: false,
          }],
        })
      }
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "scanRange", startIp: "192.168.1.1", endIp: "192.168.1.254", port: 5555 })

    expect(result.ok).toBe(true)
    // The entered range is the target, spelled once: no profile id travels with
    // it, because the run this opens records no profile reference.
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toMatchObject({ addressPolicy: "192.168.1.1-192.168.1.254", port: 5555 })
    expect(bodies[0]).not.toHaveProperty("networkProfileId")
    expect(client.getSnapshot().scanObservations).toEqual([
      expect.objectContaining({ scanRunId: "scan-11", host: "192.168.1.20", port: 5555, serial: "mock-serial-9", state: "online", known: false }),
    ])
  })

  it("refuses a malformed entered range before any scan is dispatched", async () => {
    const called: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      called.push(String(input))
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const inverted = await client.dispatch({ type: "scanRange", startIp: "192.168.1.254", endIp: "192.168.1.1", port: 5555 })
    const outOfRange = await client.dispatch({ type: "scanRange", startIp: "192.168.1.1", endIp: "192.168.1.999", port: 5555 })

    expect(inverted.ok).toBe(false)
    expect(inverted.errorCode).toBe("invalid_input")
    expect(inverted.message).toContain("Nothing was written")
    expect(outOfRange.ok).toBe(false)
    expect(outOfRange.message).toContain("four octets of 0 through 255")
    expect(called.filter((url) => url.includes("StartRangeScan"))).toEqual([])
  })
})

describe("RealControlPlaneClient reload", () => {
  it("reports each known device's own observation and restarts nothing", async () => {
    const called: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      called.push(url)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) {
        return new Response(JSON.stringify({
          devices: [{
            id: "device-1",
            displayName: "Atlas 04",
            agentId: "agent-1",
            status: "DEVICE_STATUS_ONLINE",
            platformVersion: "14",
            batteryPercent: 86,
            latencyMs: 12,
            lastSeenAt: "10:00:00",
            endpointId: "endpoint-1",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      if (url.includes("/drift.v1.EndpointService/ListDeviceEndpoints")) {
        return new Response(JSON.stringify({
          endpoints: [{
            id: "endpoint-1", deviceId: "device-1", endpointType: "adb_tcp", serial: "R5CT42GS94Z",
            host: "192.0.2.10", port: 5555, state: "ENDPOINT_STATE_CURRENT", observedAt: "2026-09-16T01:00:00Z",
          }],
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "reloadDevices" })

    expect(result.ok).toBe(true)
    // The transient result is a concise summary; detailed device and endpoint
    // state remains in the refreshed projection rather than the toast.
    expect(result.message).toContain("1 known device re-read")
    expect(result.message).not.toContain("R5CT42GS94Z")
    expect(result.message).not.toContain("192.0.2.10:5555")
    expect(result.message).toContain("No adb server was restarted")
    expect(called.filter((url) => url.includes("/drift.v1.DeviceService/ListDevices"))).toHaveLength(1)
    // It is not the host-wide operation: no restart was even attempted.
    expect(called.filter((url) => url.includes("ConnectionService"))).toEqual([])
  })
})

describe("RealControlPlaneClient transport surface", () => {
  const connectionCalls = (url: string, method: string) => url.endsWith(`/drift.v1.ConnectionService/${method}`)
  const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  const emptyResponse = () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })

  it("opens a transport for the named device and reports the adapter's own output", async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (connectionCalls(url, "ConnectEndpoint")) {
        bodies.push(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>)
        return jsonResponse({ endpoint: "192.168.1.106:5556", port: 5556, exitCode: 0, output: "already connected to 192.168.1.106:5556" })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "connectEndpoint", serial: "R5CT42GS94Z", endpoint: "192.168.1.106:5556" })

    expect(result.ok).toBe(true)
    expect(result.message).toBe("192.168.1.106:5556 for R5CT42GS94Z on port 5556: already connected to 192.168.1.106:5556")
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toMatchObject({ serial: "R5CT42GS94Z", endpoint: "192.168.1.106:5556" })
  })

  it("reports an off-port refusal at connect time without claiming a transport was opened", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (connectionCalls(url, "ConnectEndpoint")) {
        // The refusal the transport actually returns: Connect carries the
        // service's own sentence, which names the port and the accepted set.
        return new Response(JSON.stringify({
          code: "permission_denied",
          message: "refusing to open a transport to 192.168.1.106:5556: port 5556 is not in the profile's accepted ports [5555], so no device was contacted",
        }), { status: 403, headers: { "content-type": "application/json" } })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "connectEndpoint", serial: "R5CT42GS94Z", endpoint: "192.168.1.106:5556" })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("5556 is not in the profile's accepted ports")
    expect(result.message).toContain("no device was contacted")
    expect(result.errorCode).toBe("unauthorized")
  })

  it("reports a restart per endpoint and names the one that did not come back", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (connectionCalls(url, "RestartServer")) {
        return jsonResponse({
          transportsBefore: 2,
          transportsAfterStartKnown: true,
          transportsAfterStart: 0,
          killExitCode: 0,
          killFailed: false,
          startExitCode: 0,
          startFailed: false,
          reestablished: 1,
          failed: 1,
          endpoints: [
            { endpoint: "192.168.1.106:5556", port: 5556, reestablished: true, exitCode: 0, reason: "" },
            { endpoint: "192.168.1.111:5555", port: 5555, reestablished: false, exitCode: 1, reason: "failed to connect to 192.168.1.111:5555" },
          ],
        })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "restartTransportServer", endpoints: ["192.168.1.106:5556", "192.168.1.111:5555"] })

    expect(result.ok).toBe(true)
    expect(result.message).toContain("2 transport(s) before the restart")
    expect(result.message).toContain("1 of 2 endpoint(s) re-established")
    expect(result.message).toContain("not restored: 192.168.1.111:5555 (failed to connect to 192.168.1.111:5555)")
    expect(result.message).not.toContain("not restored: 192.168.1.106:5556")
  })

  it("says a restart could not read the server's transports instead of reporting zero", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (connectionCalls(url, "RestartServer")) {
        return jsonResponse({
          transportsBefore: 2,
          transportsAfterStartKnown: false,
          transportsAfterStart: 0,
          killExitCode: 0,
          startExitCode: 0,
          reestablished: 2,
          failed: 0,
          endpoints: [],
        })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "restartTransportServer", endpoints: ["192.168.1.106:5556"] })

    expect(result.message).toContain("the transports held after start-server could not be read")
    expect(result.message).not.toContain("0 transport(s) after start-server")
  })

  it("refuses a restart with nothing to re-establish without contacting the control plane", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "restartTransportServer", endpoints: [] })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("at least one observed endpoint")
    expect(urls).toEqual([])
  })

  it("carries every serial's own answer for a fleet activation, and never only a count", async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (connectionCalls(url, "ActivateFleet")) {
        bodies.push(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>)
        return jsonResponse({
          port: 5555,
          activated: 1,
          needsOperatorAuthorization: 1,
          refused: 1,
          failed: 0,
          alreadyOnPort: 1,
          devices: [
            { serial: "R5CT42GS94Z", port: 5555, activated: true, message: "R5CT42GS94Z is listening on port 5555 and is device" },
            { serial: "ZY223UNAUTH", port: 5555, needsOperatorAuthorization: true, message: "ZY223UNAUTH is now listening on port 5555, but it is UNAUTHORIZED for this host: accept the prompt on its screen." },
            { serial: "192.168.1.9:5556", port: 5555, refusal: "not_usb", message: "refusing to change the transport mode of 192.168.1.9:5556: its transport is not USB" },
            { serial: "192.168.1.7:5555", port: 5555, alreadyOnPort: true, message: "192.168.1.7:5555 was already answering on port 5555, so nothing was sent for it." },
          ],
        })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "activateFleet", port: 5555 })

    expect(result.ok).toBe(true)
    // The port and NOTHING else: an operator action cannot assert which devices
    // are attached, so it cannot name them.
    expect(bodies[0]).toMatchObject({ port: 5555 })
    expect(Object.keys(bodies[0])).not.toEqual(expect.arrayContaining(["serial", "devices", "endpoints"]))
    for (const serial of ["R5CT42GS94Z", "ZY223UNAUTH", "192.168.1.9:5556", "192.168.1.7:5555"]) {
      expect(result.message).toContain(serial)
    }
    expect(result.message).toContain("UNAUTHORIZED for this host")
    expect(result.message).toContain("already answering on port 5555")
    expect(result.message).toContain("1 activated, 1 needing the operator's prompt, 1 refused, 0 failed, 1 already on port 5555")
  })

  it("says so when a fleet activation had no device to report", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (connectionCalls(url, "ActivateFleet")) {
        return jsonResponse({ port: 5555, devices: [] })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "activateFleet", port: 5555 })

    expect(result.ok).toBe(true)
    expect(result.message).toContain("found no device to report")
    expect(result.message).not.toContain("0 activated")
  })

  it("refuses a fleet activation port outside 1-65535 without contacting the control plane", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "activateFleet", port: 70000 })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("70000")
    expect(urls).toEqual([])
  })

  it("creates a discovery range only when no saved profile holds an equivalent one", async () => {
    const created: Array<Record<string, unknown>> = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (url.endsWith("/drift.v1.NetworkProfileService/ListNetworkProfiles")) {
        return jsonResponse({
          profiles: [{ id: "profile-lab", displayName: "Lab A", addressPolicy: "192.0.2.0-192.0.2.255", allowedPorts: [5555], isDefault: true }],
        })
      }
      if (url.endsWith("/drift.v1.NetworkProfileService/CreateNetworkProfile")) {
        created.push(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>)
        return jsonResponse({ profile: { id: "profile-new" } })
      }
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()

    // The same bounded range, written the other way: an equivalent range.
    const equivalent = await client.dispatch({ type: "addDiscoveryRange", startIp: "192.0.2.0", endIp: "192.0.2.255", port: 5555 })
    expect(equivalent.ok).toBe(true)
    expect(equivalent.message).toContain("already exists")
    expect(created).toHaveLength(0)

    const fresh = await client.dispatch({ type: "addDiscoveryRange", startIp: "192.168.1.1", endIp: "192.168.1.255", port: 5555 })
    expect(fresh.ok).toBe(true)
    expect(fresh.message).toContain("created as a saved Network Profile")
    expect(created).toHaveLength(1)
    expect(created[0]).toMatchObject({
      profile: expect.objectContaining({ addressPolicy: "192.168.1.1-192.168.1.255", allowedPorts: [5555] }),
    })
  })

  it("refuses an inverted range and a bad port without contacting the control plane", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const inverted = await client.dispatch({ type: "addDiscoveryRange", startIp: "192.168.1.20", endIp: "192.168.1.10", port: 5555 })
    const badPort = await client.dispatch({ type: "addDiscoveryRange", startIp: "192.168.1.1", endIp: "192.168.1.255", port: 70000 })

    expect(inverted.ok).toBe(false)
    expect(inverted.message).toContain("after its end address")
    expect(badPort.ok).toBe(false)
    expect(badPort.message).toContain("70000")
    expect(urls).toEqual([])
  })
})

/**
 * The fleet device-settings apply.
 *
 * This surface reaches real devices and changes their settings, so the boundary
 * it draws with the control plane is asserted rather than assumed: the request
 * names the reviewed settings and no device list, a high-risk change is refused
 * without the operator's explicit approval, an unnameable row is withheld rather
 * than shown in part, and an empty fleet is reported as an empty fleet.
 */
describe("real control plane fleet device settings", () => {
  const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  const settingsCalls = (url: string) => url.endsWith("/drift.v1.DeviceSettingsService/ApplyDeviceSettings")

  it("applies the reviewed settings and carries every device's own row back", async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input)
      if (settingsCalls(url)) {
        bodies.push(JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>)
        return jsonResponse({
          totalDevices: 2,
          appliedDevices: 1,
          failedDevices: 1,
          results: [
            { deviceId: "device-alpha", setting: "DEVICE_SETTING_ROTATION_LOCK", applied: true, verified: true, message: "the device reported the setting holding after the change" },
            { deviceId: "device-alpha", setting: "DEVICE_SETTING_AUTOFILL_OFF", applied: true, verified: true, message: "the device reported the setting holding after the change" },
            {
              deviceId: "device-beta",
              setting: "DEVICE_SETTING_AUTOFILL_OFF",
              applied: false,
              verified: true,
              refusal: "DEVICE_SETTING_REFUSAL_REASON_POSTCONDITION_FAILED",
              failureClass: "postcondition",
              message: "the device answered and does not report the setting holding the required value",
            },
          ],
        })
      }
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "applyFleetDeviceSettings", settings: ["rotation_lock", "autofill_off"], confirmed: true })

    expect(result.ok).toBe(true)
    // The settings are named, and NO device list is: the fleet is read from the
    // registry by the control plane, so a console cannot assert which devices are
    // attached.
    expect(bodies[0]).toMatchObject({ approvalGranted: true })
    expect(bodies[0].settings).toEqual(["DEVICE_SETTING_ROTATION_LOCK", "DEVICE_SETTING_AUTOFILL_OFF"])
    expect(Object.keys(bodies[0])).not.toEqual(expect.arrayContaining(["devices", "deviceIds", "serials", "endpoints"]))

    // Every row is carried, and the one that did not apply is named with the
    // control plane's own sentence.
    expect(result.deviceSettingsApply?.outcomes).toHaveLength(3)
    expect(result.deviceSettingsApply?.appliedDevices).toBe(1)
    expect(result.deviceSettingsApply?.failedDevices).toBe(1)
    expect(result.message).toContain("device-beta")
    expect(result.message).toContain("does not report the setting holding")
  })

  it("refuses an unconfirmed apply without contacting the control plane", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "applyFleetDeviceSettings", settings: ["autofill_off"], confirmed: false })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("explicit confirmation")
    expect(urls).toEqual([])
  })

  it("refuses an empty setting set and a name it cannot turn into a setting", async () => {
    const urls: string[] = []
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      urls.push(String(input))
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const empty = await client.dispatch({ type: "applyFleetDeviceSettings", settings: [], confirmed: true })
    const unknown = await client.dispatch({ type: "applyFleetDeviceSettings", settings: [{ toString: () => "unreviewed" } as unknown as "rotation_lock"], confirmed: true })

    expect(empty.ok).toBe(false)
    expect(unknown.ok).toBe(false)
    expect(urls).toEqual([])
  })

  it("withholds the per-device outcomes rather than showing a row it cannot name", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (settingsCalls(url)) {
        return jsonResponse({ totalDevices: 1, results: [{ deviceId: "device-alpha", setting: 99, applied: true }] })
      }
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "applyFleetDeviceSettings", settings: ["rotation_lock"], confirmed: true })

    expect(result.ok).toBe(false)
    expect(result.message).toContain("withheld")
    expect(result.deviceSettingsApply).toBeUndefined()
  })

  it("reports an apply with no device as an empty fleet, never as success", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (settingsCalls(url)) {
        return jsonResponse({ totalDevices: 0, appliedDevices: 0, failedDevices: 0, results: [] })
      }
      return jsonResponse({})
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    const result = await client.dispatch({ type: "applyFleetDeviceSettings", settings: ["rotation_lock"], confirmed: true })

    expect(result.ok).toBe(true)
    expect(result.message).toContain("No device")
    expect(result.deviceSettingsApply?.totalDevices).toBe(0)
  })
})

describe("RealControlPlaneClient typed text", () => {
  const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  const emptyResponse = () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
  const onlineDevice = () => jsonResponse({ devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }] })
  const activeLease = () =>
    jsonResponse({
      leases: [{
        id: "lease-1",
        deviceId: "device-pixel-1",
        controlSessionId: "session-1",
        holderId: "console-local-operator",
        fencingToken: "7",
        state: "LEASE_STATE_ACTIVE",
      }],
    })

  /**
   * The value an operator types is not a message field, in either direction: it
   * leaves the console as the BODY of a registration on the control plane's own
   * content surface, and the RPC that types it names the opaque handle the
   * registration returned. Both halves are asserted here, because either one
   * alone would leave the other free to carry content.
   */
  it("registers the typed text as a request body and types it by reference", async () => {
    const value = "correct horse battery staple"
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) return onlineDevice()
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) return activeLease()
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "submitDeviceText", deviceId: "device-pixel-1", text: value, confirmed: true })

    expect(result.ok).toBe(true)
    const registration = fetchSpy.mock.calls.find((call) => String(call[0]).startsWith("http://127.0.0.1:8080/local/text-references/"))
    expect(registration).toBeDefined()
    const [registrationUrl, registrationInit] = registration ?? []
    expect(registrationInit).toMatchObject({ method: "POST", body: value })
    expect(registrationInit?.headers).toMatchObject({ "X-Drift-Workspace": "workspace-lab-local", "X-Drift-Lab-Token": "lab-token" })

    const typed = fetchSpy.mock.calls.find((call) => String(call[0]).includes("/drift.v1.DeviceInputService/TypeText"))
    expect(typed).toBeDefined()
    const typedBody = JSON.parse(String(typed?.[1]?.body)) as { text?: { handle?: string; valueLength?: number }; deviceId?: string; leaseId?: string; fencingToken?: string }
    const handle = String(registrationUrl).replace("http://127.0.0.1:8080/local/text-references/", "")
    expect(typedBody.text).toEqual({ handle, valueLength: value.length })
    expect(typedBody.deviceId).toBe("device-pixel-1")
    expect(typedBody.leaseId).toBe("lease-1")
    expect(typedBody.fencingToken).toBe("7")

    // The value is in exactly one place: the registration body. It is not in the
    // request that types it, and not in what the console reports back.
    expect(String(typed?.[1]?.body)).not.toContain(value)
    expect(JSON.stringify(result)).not.toContain(value)
  })

  /**
   * A value the control plane did not register is never dispatched: the handle
   * would resolve to nothing, and the kernel's own refusal would be reported for
   * an input that could never have reached a device.
   */
  it("does not dispatch a typed text the control plane refused to register", async () => {
    const value = "correct horse battery staple"
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/local/text-references/")) return new Response("the handle is already held", { status: 409 })
      if (url.includes("/drift.v1.DeviceService/ListDevices")) return onlineDevice()
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) return activeLease()
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "submitDeviceText", deviceId: "device-pixel-1", text: value, confirmed: true })

    expect(result.ok).toBe(false)
    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).not.toEqual(expect.arrayContaining([
      expect.stringContaining("/drift.v1.DeviceInputService/TypeText"),
    ]))
    expect(JSON.stringify(result)).not.toContain(value)
  })
})

describe("RealControlPlaneClient key events", () => {
  const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  const emptyResponse = () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })
  const onlineDevice = () => jsonResponse({ devices: [{ id: "device-pixel-1", displayName: "Pixel One", status: "DEVICE_STATUS_ONLINE" }] })
  const activeLease = () =>
    jsonResponse({
      leases: [{
        id: "lease-1",
        deviceId: "device-pixel-1",
        controlSessionId: "session-1",
        holderId: "console-local-operator",
        fencingToken: "7",
        state: "LEASE_STATE_ACTIVE",
      }],
    })

  /**
   * The measured defect, pinned at the line that carried it: the key event's
   * request was built with `observationToken: ""`, hard-coded, while the tap and
   * swipe requests beside it sent the observation the gesture was measured
   * against.
   *
   * The kernel's own action catalog declares a key event as requiring a fresh
   * observation token, so an intent naming none is refused as malformed before
   * it can be authorized - and what an operator read was the refusal's client
   * message, "device input intent is invalid", for every keystroke and every
   * press of the device's own navigation keys. A hard-coded empty token is the
   * whole of the cause: it is asserted against the request body rather than
   * against the console's own object, so the two cannot drift apart.
   */
  it("carries the observation the keystroke is measured against, never an empty token", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes("/drift.v1.DeviceService/ListDevices")) return onlineDevice()
      if (url.includes("/drift.v1.LeaseService/ListDeviceLeases")) return activeLease()
      return emptyResponse()
    })

    const client = createRealControlPlaneClient({ baseUrl: "http://127.0.0.1:8080", token: "lab-token" })
    await client.refresh()
    const result = await client.dispatch({ type: "submitDeviceKeyEvent", deviceId: "device-pixel-1", keyCode: 4, observationToken: "stream-1", confirmed: true })

    expect(result.ok).toBe(true)
    const sent = fetchSpy.mock.calls.find((call) => String(call[0]).includes("/drift.v1.DeviceInputService/KeyEvent"))
    expect(sent).toBeDefined()
    const body = JSON.parse(String(sent?.[1]?.body)) as { observationToken?: string; keyEvent?: { keyCode?: number }; deviceId?: string; leaseId?: string; fencingToken?: string }
    expect(body.observationToken).toBe("stream-1")
    expect(body.keyEvent).toEqual({ keyCode: 4 })
    expect(body.deviceId).toBe("device-pixel-1")
    expect(body.leaseId).toBe("lease-1")
    expect(body.fencingToken).toBe("7")
  })
})
