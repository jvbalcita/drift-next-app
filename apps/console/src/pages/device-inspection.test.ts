// @vitest-environment jsdom

import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneSnapshot, DeviceView, EndpointView } from "@/lib/domain/control-plane"
import {
  buildInspection,
  deviceName,
  endpointAddress,
  humanTimestamp,
  joinParts,
  reportedDeviceName,
  stableIdentityLabel,
  type InspectionTab,
} from "./device-inspection"

const snapshot = new MockControlPlaneClient().getSnapshot()

function deviceById(id: string): DeviceView {
  const device = snapshot.devices.find((candidate) => candidate.id === id)
  if (!device) throw new Error(`fixture device ${id} is missing`)
  return device
}

function inspectionTab(tabs: readonly InspectionTab[], label: string): InspectionTab {
  const tab = tabs.find((candidate) => candidate.label === label)
  if (!tab) throw new Error(`tab ${label} is not rendered; rendered: ${tabs.map((candidate) => candidate.label).join(", ")}`)
  return tab
}

// A scanned device is registered from a transport observation: the registry
// stores the observation serial (host:port for a network target) as the display
// name because no device-supplied name exists yet.
const scannedDevice: DeviceView = {
  ...deviceById("atlas-04"),
  id: "device-777",
  stableIdentity: "device-777",
  displayName: "192.168.1.123:5555",
  endpointId: "endpoint-777",
  location: "",
  platformVersion: "",
}

const scannedEndpoints: EndpointView[] = [
  {
    id: "endpoint-777",
    deviceId: "device-777",
    endpointType: "network transport",
    serial: "192.168.1.123:5555",
    host: "192.168.1.123",
    port: 5555,
    state: "current",
    observedAt: "2026-09-16T04:12:00Z",
  },
]

const bareDevice: DeviceView = {
  ...deviceById("orion-03"),
  status: "offline",
  batteryPercent: 0,
  latencyMs: 0,
  lastSeen: "",
  platformVersion: "",
  location: "",
  capabilities: [],
  agentId: "",
  endpointId: "",
}

const bareSnapshot: ControlPlaneSnapshot = {
  ...snapshot,
  devices: [bareDevice],
  endpoints: [],
  leases: [],
  observations: [],
  runTargets: [],
  memberships: [],
  accountDeviceAssignments: [],
  events: [],
}

describe("device inspection identity", () => {
  it("keeps stable identity available as a separate record", () => {
    expect(stableIdentityLabel(deviceById("atlas-04"))).toBe("device-101")
  })

  it("never presents a transport address as the device name", () => {
    expect(reportedDeviceName(scannedDevice, scannedEndpoints)).toBeUndefined()

    const name = deviceName(scannedDevice, scannedEndpoints)
    expect(name.primary).toBe("SM-G9750")
    expect(name.secondary).toBe("stable · device-777")
  })

  it("keeps a reported device name that is not a transport address", () => {
    expect(reportedDeviceName(deviceById("atlas-04"), snapshot.endpoints)).toBe("Atlas 04")

    const name = deviceName(deviceById("atlas-04"), snapshot.endpoints)
    expect(name.primary).toBe("Atlas 04")
    expect(name.secondary).toBe("stable · device-101")
  })

  it("does not treat a device identifier as a reported name", () => {
    const identifierNamed: DeviceView = { ...deviceById("atlas-04"), displayName: "device-101" }
    expect(reportedDeviceName(identifierNamed, snapshot.endpoints)).toBeUndefined()
  })

  it("renders a transport address only as an endpoint attribute", () => {
    expect(endpointAddress(scannedEndpoints[0])).toBe("192.168.1.123:5555")
    expect(endpointAddress({ ...scannedEndpoints[0], host: "", port: 0 })).toBe("192.168.1.123:5555")
  })

  it("formats registry timestamps for operators and keeps an exact value for inspection", () => {
    const now = new Date("2026-09-17T12:00:45Z")
    expect(humanTimestamp("2026-09-17T12:00:00Z", now)).toEqual({ label: "just now", exact: "2026-09-17T12:00:00.000Z" })
    expect(humanTimestamp("2026-09-17T11:58:00Z", now).label).toBe("2 min ago")
    expect(humanTimestamp("2026-09-16T12:00:00Z", now).label).toBe("1 day ago")
    expect(humanTimestamp("18 sec ago", now)).toEqual({ label: "18 sec ago" })
  })

  it("joins projections without producing an unknown placeholder", () => {
    expect(joinParts(["Rack A · Bay 04", "Android 14"])).toBe("Rack A · Bay 04 · Android 14")
    expect(joinParts(["Rack A · Bay 04", ""])).toBe("Rack A · Bay 04")
    expect(joinParts(["", undefined])).toBe("")
  })
})

describe("device inspection tabs", () => {
  it("carries real endpoint, health, activity and access evidence", () => {
    const tabs = buildInspection(deviceById("atlas-04"), snapshot)
    expect(tabs.map((tab) => tab.label)).toEqual(["Identity", "Endpoint", "Health", "Activity", "Access"])

    const rendered = JSON.stringify(tabs)
    expect(rendered).toContain("device-101")
    expect(rendered).toContain("192.0.2.10:5555")
    // superseded endpoint history is real projection data, not a placeholder
    expect(rendered).toContain("192.0.2.9:5555")
    expect(rendered).toContain("86%")
    expect(rendered).toContain("run-1042")
    expect(rendered).toContain("observation-atlas-04")
    expect(rendered).toContain("operator-1")
    expect(rendered).toContain("First Seen")
  })

  it("reads the device transport from the record rather than from an endpoint address", () => {
    const transportRow = (deviceID: string) => {
      const identity = inspectionTab(buildInspection(deviceById(deviceID), snapshot), "Identity")
      return identity.sections.flatMap((section) => section.rows ?? []).find((row) => row.label === "Transport")?.value
    }

    expect(transportRow("atlas-04")).toBe("USB")
    expect(transportRow("atlas-07")).toBe("TCP")
    // A device whose current endpoint recorded no transport states that about the
    // record. It is never named as one of the transports from its endpoint
    // address, and it is never a placeholder.
    expect(transportRow("orion-03")).toBe("Not recorded")
  })

  it("renders no tab without a backing read path", () => {
    expect(buildInspection(bareDevice, bareSnapshot).map((tab) => tab.id)).toEqual(["identity", "health"])
  })

  it("does not render a lifecycle tab: this registry exposes no lifecycle history", () => {
    const labels = buildInspection(deviceById("nova-02"), snapshot).map((tab) => tab.label)
    expect(labels).not.toContain("Lifecycle")
    expect(JSON.stringify(buildInspection(deviceById("nova-02"), snapshot))).not.toMatch(/lifecycle history/i)
  })

  it("uses a captured Android name before the phone model", () => {
    const captured: DeviceView = { ...scannedDevice, displayName: "ALTA 1", phoneModel: "SM-G9750" }
    expect(reportedDeviceName(captured, scannedEndpoints)).toBe("ALTA 1")
    expect(deviceName(captured, scannedEndpoints).primary).toBe("ALTA 1")
  })

  it("does not label a model fallback as a captured name", () => {
    const modelNamed: DeviceView = { ...scannedDevice, displayName: "SM-G9750", phoneModel: "SM-G9750" }
    expect(reportedDeviceName(modelNamed, scannedEndpoints)).toBeUndefined()
    expect(deviceName(modelNamed, scannedEndpoints).primary).toBe("SM-G9750")
  })

  it("uses one observation status instead of a duplicate lifecycle signal", () => {
    const identity = inspectionTab(buildInspection(deviceById("atlas-04"), snapshot), "Identity")
    const labels = identity.sections.flatMap((section) => section.rows ?? []).map((row) => row.label)
    expect(labels).toContain("Status")
    expect(labels).not.toContain("Lifecycle")
  })

  it("never renders an empty section", () => {
    for (const device of snapshot.devices) {
      for (const tab of buildInspection(device, snapshot)) {
        expect(tab.sections.length).toBeGreaterThan(0)
        for (const section of tab.sections) {
          expect(section.rows ?? section.items ?? []).not.toHaveLength(0)
        }
      }
    }
  })

  it("never renders a placeholder value", () => {
    for (const device of snapshot.devices) {
      const rendered = JSON.stringify(buildInspection(device, snapshot))
      expect(rendered).not.toMatch(/unknown/i)
      expect(rendered).not.toContain("undefined")
      expect(rendered).not.toContain("null")
      expect(rendered).not.toContain("—")
    }
  })

  it("drops a projection row that has no value instead of concatenating blanks", () => {
    const identity = inspectionTab(buildInspection(scannedDevice, { ...bareSnapshot, devices: [scannedDevice], endpoints: scannedEndpoints }), "Identity")
    const labels = (identity.sections.flatMap((section) => section.rows ?? [])).map((row) => row.label)
    expect(labels).toContain("Stable Identity")
    expect(labels).not.toContain("Location")
    expect(labels).not.toContain("Platform Version")
  })
})
