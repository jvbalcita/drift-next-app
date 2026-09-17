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

  it("does not claim the tab is discovery only, and performs each transport action", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    const serial = snapshot.endpoints.find((endpoint) => endpoint.deviceId === "atlas-04" && endpoint.state === "current")?.serial ?? ""
    const observed = snapshot.endpoints.find((endpoint) => endpoint.deviceId === "atlas-04" && endpoint.state === "current")
    expect(serial).not.toBe("")
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("tab", { name: "OTG Setup" }))

    // The copy that said the tab opens no transport is gone with the behaviour.
    expect(screen.queryByText(/Discovery only\./)).not.toBeInTheDocument()
    expect(screen.getByText(/Scan observes every ADB-enabled device/)).toBeInTheDocument()

    const endpoint = `${observed?.host}:${observed?.port}`
    await user.click(screen.getByRole("button", { name: "Connect" }))
    await user.click(screen.getByRole("button", { name: "Activate" }))
    await user.click(screen.getByRole("button", { name: "Change" }))
    await user.click(screen.getByRole("button", { name: "Restart ADB Server" }))
    await user.click(screen.getByRole("button", { name: "Add" }))

    expect(intents).toEqual(expect.arrayContaining([
      { type: "connectEndpoint", serial, endpoint },
      { type: "activatePort", serial, endpoint },
      { type: "changeTransportMode", serial, port: 5555 },
      { type: "restartTransportServer", endpoints: [endpoint] },
      { type: "addDiscoveryRange", startIp: "192.168.1.1", endIp: "192.168.1.255", port: 5555 },
    ]))
  })

  it("reports the transport surface's own answer rather than a claimed success", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("tab", { name: "OTG Setup" }))

    await user.click(screen.getByRole("button", { name: "Restart ADB Server" }))

    await waitFor(() => {
      // In mock mode the honest answer is the mock's: it contacted nothing.
      expect(screen.getByText(/no adb process was touched/i)).toBeInTheDocument()
    })
  })

  it("refuses an invented activation port and sends nothing", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("tab", { name: "OTG Setup" }))

    const setPort = screen.getByRole("textbox", { name: "Set Port" })
    await user.clear(setPort)
    await user.type(setPort, "5556")
    await user.click(screen.getByRole("button", { name: "Activate" }))

    await waitFor(() => {
      expect(screen.getByText(/Activate applies to the port Atlas 04 was observed on, 192\.0\.2\.10:5555/)).toBeInTheDocument()
    })
    expect(intents.some((intent) => intent.type === "activatePort")).toBe(false)
  })

  it("does not render the adb restart without an observed endpoint to re-establish", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    const snapshot = client.getSnapshot()
    snapshot.endpoints = []
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("tab", { name: "OTG Setup" }))

    expect(screen.getByText("none observed")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Restart ADB Server" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Connect" })).toBeInTheDocument()
  })
})
