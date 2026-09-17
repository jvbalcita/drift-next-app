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

  it("opens a transport to each observed endpoint when Connect is used", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    const observed = snapshot.devices.map((device) => snapshot.endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current"))
    expect(observed.some((endpoint) => endpoint === undefined)).toBe(false)
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    await user.click(screen.getByRole("button", { name: "Connect" }))

    await waitFor(() => {
      for (const endpoint of observed) {
        expect(intents).toEqual(expect.arrayContaining([{ type: "connectEndpoint", serial: endpoint?.serial, endpoint: `${endpoint?.host}:${endpoint?.port}` }]))
      }
    })
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
    expect(screen.queryByRole("button", { name: "Connect" })).toBeInTheDocument()
  })
})
