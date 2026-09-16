// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent, ControlPlaneSnapshot, DeviceView, EndpointView, MutationResult } from "@/lib/domain/control-plane"
import { DevicesPage } from "./DevicesPage"

function renderDevicesPage(snapshot?: ControlPlaneSnapshot) {
  const client = new MockControlPlaneClient()
  const intents: ControlPlaneIntent[] = []
  const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    intents.push(intent)
    return await client.dispatch(intent)
  }
  const active = snapshot ?? client.getSnapshot()
  const view = () => <DevicesPage snapshot={active} dispatch={dispatch} view="all" onViewChange={() => undefined} />
  return { client, intents, ...render(view()) }
}

function snapshotWithDevices(deviceIds: readonly string[], overrides: Partial<ControlPlaneSnapshot> = {}): ControlPlaneSnapshot {
  const base = new MockControlPlaneClient().getSnapshot()
  return { ...base, ...overrides, devices: overrides.devices ?? base.devices.filter((device) => deviceIds.includes(device.id)) }
}

// A scanned device is registered from its transport observation, so the registry
// stores the observation serial (host:port) as the display name.
const scannedDevice: DeviceView = {
  id: "device-777",
  displayName: "192.168.1.123:5555",
  stableIdentity: "device-777",
  lifecycle: "registered",
  status: "online",
  platformVersion: "",
  batteryPercent: 71,
  latencyMs: 24,
  lastSeen: "12 sec ago",
  agentId: "",
  endpointId: "endpoint-777",
  location: "",
  packageName: "",
  activityName: "",
  workflow: "",
  workflowStatus: "",
  taskProgress: 0,
  controlEligibility: "eligible",
  capabilities: [],
}

const scannedEndpoint: EndpointView = {
  id: "endpoint-777",
  deviceId: "device-777",
  endpointType: "network transport",
  serial: "192.168.1.123:5555",
  host: "192.168.1.123",
  port: 5555,
  state: "current",
  observedAt: "2026-09-16T04:12:00Z",
}

function scannedSnapshot(): ControlPlaneSnapshot {
  return snapshotWithDevices([], {
    devices: [scannedDevice],
    endpoints: [scannedEndpoint],
    observations: [],
    leases: [],
    runTargets: [],
    memberships: [],
    accountDeviceAssignments: [],
    events: [],
  })
}

async function openInspect(user: ReturnType<typeof userEvent.setup>, index = 0) {
  await user.click(screen.getAllByRole("button", { name: /^Inspect / })[index])
  return await screen.findByRole("dialog")
}

describe("DevicesPage registry table", () => {
  it("sizes the registry container to its content instead of a fixed height", () => {
    renderDevicesPage()

    const region = screen.getByRole("region", { name: "Device Registry" })
    expect(region).toHaveClass("max-h-[min(62vh,680px)]")
    expect(region).toHaveClass("overflow-auto")
    expect(region.className).not.toMatch(/(^|\s)h-\[/)
    expect(within(region).getByRole("table", { name: "Device Registry" })).toBeInTheDocument()
  })

  it("titles a scanned device on its stable identity and keeps the address in the endpoint column", () => {
    renderDevicesPage(scannedSnapshot())

    const row = screen.getByRole("row", { name: /device-777/ })
    expect(within(row).getByText("device-777")).toBeInTheDocument()
    expect(within(row).getByText("192.168.1.123:5555")).toBeInTheDocument()
    // the identity cell leads the row and holds no transport address
    expect(within(row).getAllByRole("cell")[0]).toHaveTextContent("device-777")
    expect(screen.getByRole("columnheader", { name: "Endpoint" })).toBeInTheDocument()
  })
})

describe("DevicesPage adapter surface", () => {
  it("keeps adapter detail behind on-demand disclosure instead of a permanent header panel", async () => {
    const user = userEvent.setup()
    renderDevicesPage()

    expect(screen.queryByRole("heading", { name: "Device Adapter Status" })).not.toBeInTheDocument()
    expect(screen.queryByText(/Read-only observation boundary/i)).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /Adapter Diagnostics/ }))

    const sheet = await screen.findByRole("dialog")
    expect(within(sheet).getByRole("heading", { name: "Device Adapter Diagnostics" })).toBeInTheDocument()
    expect(within(sheet).getByText(/Observation is not registration/i)).toBeInTheDocument()
  })
})

describe("DevicesPage bulk controls", () => {
  it("renders no control for an operation this registry cannot perform", () => {
    const page = renderDevicesPage()

    expect(screen.queryByRole("button", { name: "Bulk Move" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Remove Terminal Records/ })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Confirm Removal/ })).not.toBeInTheDocument()
    expect(screen.queryByText(/Remove Terminal Device Records/)).not.toBeInTheDocument()
    expect(screen.queryByText(/no registry data changed/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Bulk move is unavailable/i)).not.toBeInTheDocument()
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
    expect(page.intents).toEqual([])
  })
})

describe("DevicesPage inspection surface", () => {
  it("titles the inspection surface on stable identity, never on the transport address", async () => {
    const user = userEvent.setup()
    renderDevicesPage(scannedSnapshot())

    const sheet = await openInspect(user)

    expect(within(sheet).getByRole("heading", { name: "device-777" })).toBeInTheDocument()
    expect(within(sheet).queryByRole("heading", { name: "192.168.1.123:5555" })).not.toBeInTheDocument()

    await user.click(within(sheet).getByRole("tab", { name: "Endpoint" }))
    const endpointPanel = within(sheet).getByRole("tabpanel", { name: "Endpoint" })
    expect(within(endpointPanel).getByText("Address")).toBeInTheDocument()
    // the address is an endpoint attribute; for a network target the serial is the same string
    expect(within(endpointPanel).getAllByText("192.168.1.123:5555").length).toBeGreaterThan(0)
    expect(within(endpointPanel).getByText(/Transport endpoints are mutable/i)).toBeInTheDocument()
  })

  it("submits a typed tap and renders the kernel outcome", async () => {
    const user = userEvent.setup(); const page = renderDevicesPage(); const sheet = await openInspect(user, 0)
    await user.click(within(sheet).getByRole("button", { name: "Submit to kernel" }))
    expect(page.intents).toContainEqual(expect.objectContaining({ type: "submitDeviceTap", deviceId: "atlas-04", observationToken: "fresh-atlas-04" }))
    expect(within(sheet).getByRole("status")).toHaveTextContent(/Kernel outcome/i)
  })

  it("renders a kernel refusal without replacing it with an internal error", async () => {
    const user = userEvent.setup(); const client = new MockControlPlaneClient()
    const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => intent.type === "submitDeviceTap" ? { ok: false, kind: intent.type, message: "Lease has expired.", errorCode: "precondition_failed" } : await client.dispatch(intent)
    render(<DevicesPage snapshot={client.getSnapshot()} dispatch={dispatch} view="all" />)
    const sheet = await openInspect(user, 0); await user.click(within(sheet).getByRole("button", { name: "Submit to kernel" }))
    expect(within(sheet).getByRole("status")).toHaveTextContent("Kernel refusal: Lease has expired.")
  })
  it("renders real projection data in every tab that survives", async () => {
    const user = userEvent.setup()
    renderDevicesPage()

    const sheet = await openInspect(user, 0)

    // the title is the stable identity, and the identity section carries it as a record too
    expect(within(sheet).getByRole("heading", { name: "device-101" })).toBeInTheDocument()
    expect(within(sheet).getAllByText("device-101").length).toBeGreaterThan(1)
    expect(within(sheet).getByText("Atlas 04")).toBeInTheDocument()
    expect(within(sheet).getByText(/Rack A · Bay 04/)).toBeInTheDocument()

    await user.click(within(sheet).getByRole("tab", { name: "Endpoint" }))
    const endpointPanel = within(sheet).getByRole("tabpanel", { name: "Endpoint" })
    expect(within(endpointPanel).getByText("192.0.2.10:5555")).toBeInTheDocument()
    // superseded endpoint history is a real projection, not a placeholder
    expect(within(endpointPanel).getByText("192.0.2.9:5555")).toBeInTheDocument()

    await user.click(within(sheet).getByRole("tab", { name: "Health" }))
    const healthPanel = within(sheet).getByRole("tabpanel", { name: "Health" })
    expect(within(healthPanel).getByText("86%")).toBeInTheDocument()
    expect(within(healthPanel).getByText("42 ms")).toBeInTheDocument()
    expect(within(healthPanel).getByText("Edge Alpha")).toBeInTheDocument()

    await user.click(within(sheet).getByRole("tab", { name: "Activity" }))
    const activityPanel = within(sheet).getByRole("tabpanel", { name: "Activity" })
    // the observation appears as its own record and as the run target's evidence
    expect(within(activityPanel).getAllByText("observation-atlas-04").length).toBeGreaterThan(1)
    expect(within(activityPanel).getByText("run-1042 · Content validation")).toBeInTheDocument()
    expect(within(activityPanel).getByText("lease.renewed")).toBeInTheDocument()

    await user.click(within(sheet).getByRole("tab", { name: "Access" }))
    const accessPanel = within(sheet).getByRole("tabpanel", { name: "Access" })
    expect(within(accessPanel).getByText("operator-1")).toBeInTheDocument()
    expect(within(accessPanel).getByText("18")).toBeInTheDocument()
    expect(within(accessPanel).getByText("Operations demo")).toBeInTheDocument()
  })

  it("hides an inspection tab the device has no data source for", async () => {
    const user = userEvent.setup()
    renderDevicesPage(snapshotWithDevices(["orion-03"]))

    const sheet = await openInspect(user)

    expect(within(sheet).getByRole("tab", { name: "Identity" })).toBeInTheDocument()
    expect(within(sheet).getByRole("tab", { name: "Activity" })).toBeInTheDocument()
    expect(within(sheet).queryByRole("tab", { name: "Access" })).not.toBeInTheDocument()
    expect(within(sheet).queryByRole("tab", { name: "Lifecycle" })).not.toBeInTheDocument()
  })

  it("shows the access tab when the device does have lease and account data", async () => {
    const user = userEvent.setup()
    renderDevicesPage(snapshotWithDevices(["atlas-04"]))

    const sheet = await openInspect(user)

    expect(within(sheet).getByRole("tab", { name: "Access" })).toBeInTheDocument()
  })
})
