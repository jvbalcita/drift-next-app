// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { Toaster } from "@/components/ui/sonner"
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
  const view = () => <><Toaster /><DevicesPage snapshot={active} dispatch={dispatch} view="all" onViewChange={() => undefined} /></>
  return { client, intents, ...render(view()) }
}

function snapshotWithDevices(deviceIds: readonly string[], overrides: Partial<ControlPlaneSnapshot> = {}): ControlPlaneSnapshot {
  const base = new MockControlPlaneClient().getSnapshot()
  return { ...base, ...overrides, devices: overrides.devices ?? base.devices.filter((device) => deviceIds.includes(device.id)) }
}

// A scanned device may initially have only transport identity, but an adapter
// model is a valid human-facing fallback for the device name and table column.
const scannedDevice: DeviceView = {
  id: "device-777",
  displayName: "192.168.1.123:5555",
  phoneModel: "SM-G9750",
  stableIdentity: "device-777",
  lifecycle: "registered",
  status: "online",
  platformVersion: "",
  batteryPercent: 71,
  latencyMs: 24,
  lastSeen: "12 sec ago",
  agentId: "",
  endpointId: "endpoint-777",
  expectation: "expected",
  observedAgainAfterRetirement: false,
  transport: "tcp",
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

function warningSnapshot(): ControlPlaneSnapshot {
  return snapshotWithDevices(["atlas-04"], {
    projectionWarnings: [{ source: "endpoints", message: "Endpoint read was incomplete." }],
  })
}

async function openInspect(user: ReturnType<typeof userEvent.setup>, index = 0) {
  await user.click(screen.getAllByRole("row", { name: /^Open details for / })[index])
  return await screen.findByRole("dialog")
}

describe("DevicesPage registry table", () => {
  it("states when the registry projection is incomplete", () => {
    renderDevicesPage(warningSnapshot())

    expect(screen.getByRole("alert")).toHaveTextContent("Registry read needs attention")
    expect(screen.getByRole("alert")).toHaveTextContent("Endpoint read was incomplete.")
  })

  it("sizes the registry container to its content and keeps the wide table scrollable", () => {
    renderDevicesPage()

    const region = screen.getByRole("region", { name: "Device Registry" })
    expect(region).toHaveClass("max-h-[min(62vh,680px)]")
    expect(region).toHaveClass("overflow-x-auto")
    expect(region).toHaveClass("overflow-y-auto")
    expect(region.className).not.toMatch(/(^|\s)h-\[/)
    expect(within(region).getByRole("table", { name: "Device Registry" })).toBeInTheDocument()
  })

  it("shows the requested projection columns and keeps transport fields separate", () => {
    renderDevicesPage(scannedSnapshot())

    const row = screen.getByRole("row", { name: /SM-G9750/ })
    const cells = within(row).getAllByRole("cell")
    expect(screen.getByRole("columnheader", { name: "Device Name" })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Phone Model" })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Serial Number" })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Endpoint" })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Port" })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Status" })).toBeInTheDocument()
    expect(screen.queryByRole("columnheader", { name: "Lifecycle" })).not.toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Last Seen" })).toBeInTheDocument()
    expect(cells[0]).toHaveTextContent("SM-G9750")
    expect(cells[0]).not.toHaveTextContent("stable · device-777")
    expect(cells[1]).toHaveTextContent("SM-G9750")
    expect(cells[2]).toHaveTextContent("192.168.1.123:5555")
    expect(cells[3]).toHaveTextContent("192.168.1.123")
    expect(cells[4]).toHaveTextContent("5555")
    expect(cells[5]).toHaveTextContent("Online")
    expect(cells[5]).not.toHaveTextContent("Registered")
    expect(cells[6]).toHaveTextContent("12 sec ago")
    expect(screen.queryByRole("columnheader", { name: "Actions" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /^Inspect / })).not.toBeInTheDocument()
  })

  it("opens device details from a focused row with Enter", async () => {
    const user = userEvent.setup()
    renderDevicesPage(scannedSnapshot())
    const row = screen.getByRole("row", { name: "Open details for SM-G9750" })
    row.focus()
    await user.keyboard("{Enter}")
    expect(await screen.findByRole("dialog")).toHaveTextContent("SM-G9750")
  })

  it("refreshes the selected device before presenting its details", async () => {
    const user = userEvent.setup()
    const page = renderDevicesPage(scannedSnapshot())
    await openInspect(user)

    expect(page.intents).toContainEqual({ type: "refresh", deviceId: "device-777" })
    expect(await screen.findByText("Mock diagnostics refreshed for device-777; no external service was contacted.")).toBeInTheDocument()
  })

  it("withholds a stale endpoint when the device projection names another record", () => {
    // The fixture has a current endpoint for the device, but the device record
    // points at a different endpoint ID. The table must not silently substitute
    // the current record and claim it is the named endpoint.
    const device = { ...scannedDevice, endpointId: "endpoint-missing" }
    const endpoint = { ...scannedEndpoint, id: "endpoint-current" }
    const snapshot = snapshotWithDevices([], { devices: [device], endpoints: [endpoint] })
    renderDevicesPage(snapshot)

    const row = screen.getByRole("row", { name: /SM-G9750/ })
    const cells = within(row).getAllByRole("cell")
    expect(cells[2]).toHaveTextContent("—")
    expect(cells[3]).toHaveTextContent("—")
    expect(cells[4]).toHaveTextContent("—")
  })
})

describe("DevicesPage adapter surface", () => {
  it("keeps adapter detail behind on-demand disclosure instead of a permanent header panel", async () => {
    const user = userEvent.setup()
    const base = new MockControlPlaneClient().getSnapshot()
    renderDevicesPage({
      ...base,
      labAdapter: { ...base.labAdapter, failureClass: "timeout" },
    })

    expect(screen.queryByRole("heading", { name: "Device Adapter Status" })).not.toBeInTheDocument()
    expect(screen.queryByText(/Read-only observation boundary/i)).not.toBeInTheDocument()

    expect(screen.getByText("Adapter Unavailable")).toBeInTheDocument()
    expect(screen.queryByText(/observed · 0 registered/i)).not.toBeInTheDocument()
    expect(screen.queryByText("Timeout")).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Adapter details" }))

    const sheet = await screen.findByRole("dialog")
    expect(within(sheet).getByRole("heading", { name: "Device Adapter Diagnostics" })).toBeInTheDocument()
    expect(within(sheet).getByText(/Observation is not registration/i)).toBeInTheDocument()
    expect(within(sheet).getByText(/Observed transports are devices the adapter saw/i)).toBeInTheDocument()
    expect(within(sheet).getByText("Last Adapter Result")).toBeInTheDocument()
    expect(within(sheet).getByText("Timeout")).toBeInTheDocument()
    expect(within(sheet).getByText("Registry Changes")).toBeInTheDocument()
    expect(within(sheet).getByText("None — diagnostics do not register devices")).toBeInTheDocument()
  })
})

describe("DevicesPage refresh", () => {
  it("dispatches one refresh, shows a pending state, and reports success with Sonner", async () => {
    const user = userEvent.setup()
    let settle: ((result: MutationResult) => void) | undefined
    const dispatch = vi.fn(() => new Promise<MutationResult>((resolve) => { settle = resolve }))
    const snapshot = new MockControlPlaneClient().getSnapshot()
    render(<><Toaster /><DevicesPage snapshot={snapshot} dispatch={dispatch} view="all" /></>)

    await user.click(screen.getByRole("button", { name: "Refresh" }))

    expect(dispatch).toHaveBeenCalledTimes(1)
    expect(dispatch).toHaveBeenCalledWith({ type: "refresh" })
    const pending = screen.getByRole("button", { name: "Refreshing…" })
    expect(pending).toBeDisabled()
    expect(pending.querySelector(".animate-spin")).toBeInTheDocument()

    settle?.({ ok: true, kind: "refresh", message: "Registry projection refreshed." })
    expect(await screen.findByText("Device registry refreshed")).toBeInTheDocument()
    expect(await screen.findByText("Registry projection refreshed.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled()
    expect(screen.getAllByText("Registry projection refreshed.")).toHaveLength(1)
  })

  it("reports a failed refresh with Sonner instead of persistent table text", async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn(async (): Promise<MutationResult> => ({ ok: false, kind: "refresh", message: "Registry service is unavailable.", errorCode: "precondition_failed" }))
    const snapshot = new MockControlPlaneClient().getSnapshot()
    render(<><Toaster /><DevicesPage snapshot={snapshot} dispatch={dispatch} view="all" /></>)

    await user.click(screen.getByRole("button", { name: "Refresh" }))

    expect(await screen.findByText("Device registry refresh failed")).toBeInTheDocument()
    expect(await screen.findByText("Registry service is unavailable.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled()
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
  it("titles the inspection surface on a device name fallback, never on the transport address", async () => {
    const user = userEvent.setup()
    renderDevicesPage(scannedSnapshot())

    const sheet = await openInspect(user)

    expect(within(sheet).getByRole("heading", { name: "SM-G9750" })).toBeInTheDocument()
    expect(within(sheet).getByText("Stable Identity")).toBeInTheDocument()
    expect(within(sheet).getAllByText("device-777").length).toBeGreaterThan(0)
    expect(within(sheet).queryByRole("heading", { name: "192.168.1.123:5555" })).not.toBeInTheDocument()

    expect(within(sheet).queryByRole("tablist")).not.toBeInTheDocument()
    expect(within(sheet).queryAllByRole("tab")).toHaveLength(0)
    expect(sheet).toHaveClass("bg-popover")
    expect(sheet.querySelectorAll(".bg-background")).toHaveLength(0)
    expect(within(sheet).queryByRole("heading", { name: "Connection & Endpoints" })).not.toBeInTheDocument()
    await user.click(within(sheet).getByRole("button", { name: /Connection & Endpoints/ }))
    const connectionSheet = screen.getByRole("dialog", { name: "Connection & Endpoints" })
    expect(within(connectionSheet).getByText("Address")).toBeInTheDocument()
    // the address is an endpoint attribute; for a network target the serial is the same string
    expect(within(connectionSheet).getAllByText("192.168.1.123:5555").length).toBeGreaterThan(0)
    expect(within(connectionSheet).getByText(/grouped by observation date/i)).toBeInTheDocument()
    await user.click(within(connectionSheet).getByRole("button", { name: "Back to device details" }))
    expect(screen.getByRole("dialog", { name: "SM-G9750" })).toBeInTheDocument()
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
  it("renders the complete backed projection as one sectioned inspection flow", async () => {
    const user = userEvent.setup()
    renderDevicesPage()

    const sheet = await openInspect(user, 0)

    // The title is the device name; stable identity remains a record inside the
    // identity section rather than replacing the human-facing name.
    expect(within(sheet).getByRole("heading", { name: "Atlas 04" })).toBeInTheDocument()
    expect(within(sheet).getAllByText(/device-101/).length).toBeGreaterThan(0)
    expect(within(sheet).getAllByText("Atlas 04").length).toBeGreaterThan(1)
    expect(within(sheet).queryByText("DEVICE INSPECTION")).not.toBeInTheDocument()
    expect(within(sheet).getByText("Stable Identity")).toBeInTheDocument()
    expect(within(sheet).queryByText("Lifecycle")).not.toBeInTheDocument()
    expect(within(sheet).getByText(/Rack A · Bay 04/)).toBeInTheDocument()

    expect(within(sheet).queryAllByRole("tab")).toHaveLength(0)
    expect(within(sheet).queryByRole("heading", { name: "Connection & Endpoints" })).not.toBeInTheDocument()
    await user.click(within(sheet).getByRole("button", { name: /Connection & Endpoints/ }))
    const connectionSheet = screen.getByRole("dialog", { name: "Connection & Endpoints" })
    expect(within(connectionSheet).getByText("192.0.2.10:5555")).toBeInTheDocument()
    expect(within(connectionSheet).getByText("Current")).toBeInTheDocument()
    // superseded endpoint history is a real projection, not a placeholder
    const dateTriggers = within(connectionSheet).getAllByRole("button").filter((button) => /record/.test(button.textContent ?? ""))
    expect(dateTriggers.length).toBe(2)
    await user.click(dateTriggers[1])
    expect(within(connectionSheet).getByText("192.0.2.9:5555")).toBeInTheDocument()
    expect(within(connectionSheet).getAllByText("Record 1").length).toBe(2)
    expect(within(connectionSheet).getByText("Historical")).toBeInTheDocument()
    await user.click(within(connectionSheet).getByRole("button", { name: "Back to device details" }))
    const detailsSheet = screen.getByRole("dialog", { name: "Atlas 04" })
    expect(detailsSheet).toBeInTheDocument()
    expect(within(detailsSheet).getByRole("heading", { name: "Health & Runtime" })).toBeInTheDocument()
    expect(within(detailsSheet).getByText("86%")).toBeInTheDocument()
    expect(within(detailsSheet).getByText("42 ms")).toBeInTheDocument()
    expect(within(detailsSheet).getAllByText("Edge Alpha").length).toBeGreaterThan(0)

    expect(within(detailsSheet).getByRole("heading", { name: "Recent Events" })).toBeInTheDocument()
    // the observation appears as its own record and as the run target's evidence
    expect(within(detailsSheet).getAllByText("observation-atlas-04").length).toBeGreaterThan(1)
    expect(within(detailsSheet).getByText("run-1042 · Content validation")).toBeInTheDocument()
    expect(within(detailsSheet).getByText("lease.renewed")).toBeInTheDocument()

    expect(within(detailsSheet).queryByRole("heading", { name: "Control Leases" })).not.toBeInTheDocument()
    await user.click(within(detailsSheet).getByRole("button", { name: /Access & Control/ }))
    const accessSheet = screen.getByRole("dialog", { name: "Access & Control" })
    expect(within(accessSheet).getByRole("heading", { name: "Control Leases" })).toBeInTheDocument()
    expect(within(accessSheet).getByText("operator-1")).toBeInTheDocument()
    expect(within(accessSheet).getByText("18")).toBeInTheDocument()
    expect(within(accessSheet).getByText("Operations demo")).toBeInTheDocument()
    await user.click(within(accessSheet).getByRole("button", { name: "Back to device details" }))
    expect(screen.getByRole("dialog", { name: "Atlas 04" })).toBeInTheDocument()
  })

  it("hides an inspection section the device has no data source for", async () => {
    const user = userEvent.setup()
    renderDevicesPage(snapshotWithDevices(["orion-03"]))

    const sheet = await openInspect(user)

    expect(within(sheet).getByRole("heading", { name: "Device Identity" })).toBeInTheDocument()
    expect(within(sheet).getByRole("heading", { name: "Observations" })).toBeInTheDocument()
    expect(within(sheet).queryByRole("heading", { name: "Control Leases" })).not.toBeInTheDocument()
    expect(within(sheet).queryByRole("heading", { name: "Lifecycle" })).not.toBeInTheDocument()
  })

  it("shows the access section when the device does have lease and account data", async () => {
    const user = userEvent.setup()
    renderDevicesPage(snapshotWithDevices(["atlas-04"]))

    const sheet = await openInspect(user)

    await user.click(within(sheet).getByRole("button", { name: /Access & Control/ }))
    expect(within(screen.getByRole("dialog", { name: "Access & Control" })).getByRole("heading", { name: "Control Leases" })).toBeInTheDocument()
  })
})
