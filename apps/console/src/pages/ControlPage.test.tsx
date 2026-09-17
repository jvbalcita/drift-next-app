// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent } from "@/lib/domain/control-plane"
import { ControlPage } from "./ControlPage"

describe("ControlPage screenshot", () => {
  it("does not authorize capture when the device has no current transport endpoint", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    const snapshot = client.getSnapshot()
    snapshot.endpoints = []
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: "Screenshot" }))

    await waitFor(() => {
      expect(screen.getByText(/no single current transport endpoint/i)).toBeInTheDocument()
    })
    expect(intents.some((intent) => intent.type === "submitDeviceAction" && intent.kind === "capture")).toBe(false)
    expect(intents.some((intent) => intent.type === "captureLabObservation")).toBe(false)
  })

  it("names the device's current endpoint serial when capturing", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    const snapshot = client.getSnapshot()
    const serial = snapshot.endpoints.find((endpoint) => endpoint.deviceId === "atlas-04" && endpoint.state === "current")?.serial ?? ""
    expect(serial).not.toBe("")
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: "Screenshot" }))

    await waitFor(() => {
      expect(intents.some((intent) => intent.type === "captureLabObservation" && intent.serial === serial)).toBe(true)
    })
    const captureIndex = intents.findIndex((intent) => intent.type === "submitDeviceAction" && intent.kind === "capture")
    const observeIndex = intents.findIndex((intent) => intent.type === "captureLabObservation")
    expect(captureIndex).toBeGreaterThanOrEqual(0)
    expect(observeIndex).toBeGreaterThan(captureIndex)
  })
})

describe("ControlPage OTG Setup tab", () => {
  function harness() {
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    return { client, intents, dispatch }
  }

  async function openOtgTab(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("tab", { name: "OTG Setup" }))
  }

  it("offers Set Port at 5555 and a fleet Activate, and no per-device target or transport control", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // The copy that said the tab opens no transport is gone with the behaviour.
    expect(screen.queryByText(/Discovery only\./)).not.toBeInTheDocument()
    expect(screen.getByText(/Scan observes every ADB-enabled device/)).toBeInTheDocument()

    // Set Port is one port, and it starts at 5555.
    const setPort = screen.getByRole("textbox", { name: "Set Port" })
    expect(setPort).toHaveValue("5555")

    // The two controls that duplicated the fleet operation are gone: a target
    // picker for a device the fleet operation does not need, and a second port
    // field with its own Change control.
    expect(screen.queryByRole("button", { name: "Target Device" })).not.toBeInTheDocument()
    expect(screen.queryByRole("textbox", { name: "Transport Mode Port" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Change" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument()
    // Connect is gone with them, and so is the copy that described it: after
    // Activate, the scan is what sees a moved device on the Set Port port, so a
    // standalone Connect is the same duplication as the target picker.
    expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument()
    expect(screen.queryByText(/Connect opens a transport/)).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Activate" })).toBeEnabled()

    await user.click(screen.getByRole("button", { name: "Activate" }))
    expect(intents).toEqual(expect.arrayContaining([{ type: "activateFleet", port: 5555 }]))

    // The port the operator sets is the port Activate carries, and nothing else
    // travels with it: the fleet is the control plane's to read.
    await user.clear(setPort)
    await user.type(setPort, "5556")
    await user.click(screen.getByRole("button", { name: "Activate" }))
    const activations = intents.filter((intent) => intent.type === "activateFleet")
    expect(activations[activations.length - 1]).toEqual({ type: "activateFleet", port: 5556 })

    await user.click(screen.getByRole("button", { name: "Restart ADB Server" }))
    await user.click(screen.getByRole("button", { name: "Add" }))
    expect(intents).toEqual(expect.arrayContaining([
      { type: "restartTransportServer", endpoints: ["192.0.2.10:5555"] },
      { type: "addDiscoveryRange", startIp: "192.168.1.1", endIp: "192.168.1.255", port: 5556 },
    ]))
  })

  it("reports a fleet activation per serial rather than as one verdict", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    const snapshot = client.getSnapshot()
    const serials = snapshot.devices.map((device) => snapshot.endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")?.serial ?? "")
    expect(serials.every((serial) => serial !== "")).toBe(true)
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    await user.click(screen.getByRole("button", { name: "Activate" }))

    await waitFor(() => {
      const feedback = screen.getByText(/Mock fleet activation/)
      // Every device the client can see is named in its own answer, so an
      // operator reading the report can tell which one is which.
      for (const serial of serials) expect(feedback).toHaveTextContent(serial)
    })
  })

  it("runs the whole flow — Set Port, Activate, Scan — with no Connect control in it", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    // Set Port: the port every discovered device is moved to. It is 5555
    // whether or not a device is already answering on it.
    expect(screen.getByRole("textbox", { name: "Set Port" })).toHaveValue("5555")

    // Activate, over the fleet, with one answer per serial.
    await user.click(screen.getByRole("button", { name: "Activate" }))
    const serials = snapshot.devices.map((device) => snapshot.endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")?.serial ?? "")
    expect(serials.every((serial) => serial !== "")).toBe(true)
    await waitFor(() => {
      const report = screen.getByText(/Mock fleet activation/)
      for (const serial of serials) expect(report).toHaveTextContent(serial)
    })

    // Then the scan over the saved network. The scan is the step that brings the
    // moved devices in — which is why nothing in this flow opens a transport by
    // hand, and why the panel no longer offers a control that would.
    expect(screen.getByRole("button", { name: "Saved Network" })).toHaveTextContent("Lab A staging · Default")
    await user.click(screen.getByRole("button", { name: "Scan" }))
    await waitFor(() => {
      expect(screen.getByText(/Mock scan completed; observed 3 device\(s\)/)).toBeInTheDocument()
    })

    // The order IS the flow: Set Port, Activate, then the scan. No Connect
    // intent is dispatched anywhere in it, and no Connect control is offered.
    // In this client no device is contacted, so what is proven here is which
    // operations the panel performs and in what order, not a real transport.
    expect(intents.filter((intent) => intent.type === "activateFleet")).toEqual([{ type: "activateFleet", port: 5555 }])
    expect(intents.filter((intent) => intent.type === "startScan")).toEqual([{ type: "startScan", profileId: "profile-lab-a" }])
    expect(intents.findIndex((intent) => intent.type === "activateFleet")).toBeLessThan(intents.findIndex((intent) => intent.type === "startScan"))
    expect(intents.some((intent) => intent.type === "connectEndpoint")).toBe(false)
    expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument()
  })

  it("reports the transport surface's own answer rather than a claimed success", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    await user.click(screen.getByRole("button", { name: "Restart ADB Server" }))

    await waitFor(() => {
      // In mock mode the honest answer is the mock's: it contacted nothing.
      expect(screen.getByText(/no adb process was touched/i)).toBeInTheDocument()
    })
  })

  it("does not render the adb restart without an observed endpoint to re-establish", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    const snapshot = client.getSnapshot()
    snapshot.endpoints = []
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    expect(screen.queryByRole("button", { name: "Restart ADB Server" })).not.toBeInTheDocument()
    // ...and there is no Connect control to fall back on for it either.
    expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument()
  })
})

describe("ControlPage connection filters", () => {
  it("shows all devices by default, filters by observed transport, and updates the count", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("6 / 6 devices shown")
    expect(screen.getByRole("button", { name: "USB" })).toHaveAttribute("aria-pressed", "false")

    await user.click(screen.getByRole("button", { name: "USB" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("2 / 6 devices shown")
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Nova 05/i })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Atlas 07/i })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "USB" })).toHaveAttribute("aria-pressed", "true")
  })

  it("renders an empty state when a connection filter has no matching devices", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    await user.click(screen.getByRole("button", { name: "OTG" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("0 / 6 devices shown")
    expect(screen.getByText("No Devices for This Connection")).toBeInTheDocument()
  })
})
