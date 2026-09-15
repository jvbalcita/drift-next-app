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
      if (url.includes("/drift.v1.DiscoveryService/ListScanCandidates")) {
        return new Response(JSON.stringify({
          candidates: [{
            id: "candidate-1",
            scanRunId: "scan-1",
            candidateKey: "192.0.2.10:5555",
            host: "192.0.2.10",
            port: 5555,
            serial: "SERIAL1",
            fingerprint: "fp-1",
            state: "SCAN_CANDIDATE_STATE_PENDING_APPROVAL",
            discoveredAt: "2026-09-15T01:00:30Z",
            evidenceSummary: "Sanitized candidate evidence",
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
    expect(snapshot.scanCandidates).toEqual([expect.objectContaining({
      id: "candidate-1",
      serial: "SERIAL1",
      state: "pending_approval",
      evidenceSummary: "Sanitized candidate evidence",
    })])
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
})
