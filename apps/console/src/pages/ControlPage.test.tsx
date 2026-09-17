// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
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

  function octet(label: string, index: number) {
    return screen.getByRole("textbox", { name: `${label} octet ${index}` })
  }

  async function setOctet(user: ReturnType<typeof userEvent.setup>, label: string, index: number, value: string) {
    const field = octet(label, index)
    await user.clear(field)
    await user.type(field, value)
  }

  it("offers Set Port at 5555 and a fleet Activate, and no per-device target or transport control", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // The paragraph the tab used to carry is gone with the copy it held: the
    // explanations now live on the controls themselves, in a tooltip.
    expect(screen.queryByText(/Discovery only\./)).not.toBeInTheDocument()
    expect(screen.queryByText(/Scan observes every ADB-enabled device/)).not.toBeInTheDocument()

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
    await user.click(screen.getByRole("button", { name: "Scan Saved Network" }))
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

  it("sits the saved network with the ADB server controls, with its own scan of the saved profile", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // The Saved network control lives in the section the ADB server controls
    // live in — the shared ancestor is the section itself — and the entered
    // range's Scan is not in it. Two scans, two targets, two places.
    const savedNetwork = screen.getByRole("button", { name: "Saved Network" })
    const restart = screen.getByRole("button", { name: "Restart ADB Server" })
    let section: HTMLElement | null = savedNetwork.parentElement
    while (section && !section.contains(restart)) section = section.parentElement
    expect(section).not.toBeNull()
    expect(section?.contains(screen.getByRole("button", { name: "Scan Range" }))).toBe(false)
    expect(section?.contains(screen.getByRole("button", { name: "Reload Devices" }))).toBe(true)

    await user.click(screen.getByRole("button", { name: "Scan Saved Network" }))

    await waitFor(() => {
      expect(intents.filter((intent) => intent.type === "startScan")).toEqual([{ type: "startScan", profileId: "profile-lab-a" }])
    })
    // The saved network's Scan opened no entered-range scan: its target is the
    // saved profile and the range it holds.
    expect(intents.some((intent) => intent.type === "scanRange")).toBe(false)
  })

  it("scans the entered range from the IP Range inputs, and never the selected profile", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // The saved network control is pointed somewhere ELSE. Whatever the range
    // Scan observes, it cannot be this profile's range (198.51.100.0/24).
    await user.click(screen.getByRole("button", { name: "Saved Network" }))
    await user.click(await screen.findByRole("menuitem", { name: "Lab B review" }))
    expect(screen.getByRole("button", { name: "Saved Network" })).toHaveTextContent("Lab B review")

    // The range the operator types is the scan's target: the second range's
    // first three octets follow the first, and only its last octet is typed.
    await setOctet(user, "IP Range Start", 2, "0")
    await setOctet(user, "IP Range Start", 3, "2")
    await setOctet(user, "IP Range End", 4, "20")
    await user.click(screen.getByRole("button", { name: "Scan Range" }))

    await waitFor(() => {
      expect(intents).toEqual(expect.arrayContaining([{ type: "scanRange", startIp: "192.0.2.1", endIp: "192.0.2.20", port: 5555 }]))
    })
    // The selected saved profile was read by nothing: the range Scan names no
    // profile, and no scan of one was started.
    expect(intents.some((intent) => intent.type === "startScan")).toBe(false)
    // And the observation is bounded by the ENTERED range: this client holds
    // three transports, and only the two inside 192.0.2.1-192.0.2.20 were
    // observed by a scan whose target was the range inputs.
    await waitFor(() => {
      expect(screen.getByText(/Mock range scan of 192\.0\.2\.1-192\.0\.2\.20 on port 5555 completed; observed 2 of the 3 transport/)).toBeInTheDocument()
    })
  })

  it("locks the second range's first three octets to the first range, and refuses an edit to them", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // The second range mirrors the first, and its locked fields are disabled, so
    // the browser refuses input at them.
    for (const index of [1, 2, 3]) expect(octet("IP Range End", index)).toBeDisabled()
    expect(octet("IP Range End", 1)).toHaveValue("192")
    expect(octet("IP Range End", 2)).toHaveValue("168")
    expect(octet("IP Range End", 3)).toHaveValue("1")
    expect(octet("IP Range End", 4)).toBeEnabled()

    // Editing the first range moves the second range's locked octets with it,
    // and the last octet stays the operator's own.
    await setOctet(user, "IP Range Start", 1, "10")
    expect(octet("IP Range End", 1)).toHaveValue("10")
    await setOctet(user, "IP Range End", 4, "9")
    expect(octet("IP Range End", 4)).toHaveValue("9")

    // An edit aimed at a locked octet cannot change it, even when it reaches the
    // field's own handler: only the final octet is stored.
    fireEvent.change(octet("IP Range End", 2), { target: { value: "99" } })

    await user.click(screen.getByRole("button", { name: "Add" }))
    expect(intents).toEqual(expect.arrayContaining([{ type: "addDiscoveryRange", startIp: "10.168.1.1", endIp: "10.168.1.9", port: 5555 }]))
  })

  it("refuses an out-of-range last octet with a named reason and scans nothing", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    await setOctet(user, "IP Range End", 4, "999")
    await user.click(screen.getByRole("button", { name: "Scan Range" }))

    await waitFor(() => {
      expect(screen.getByText(/must be four octets of 0 through 255/)).toBeInTheDocument()
    })
    // The refusal opened no scan run: the projection holds only the runs it
    // started with.
    expect(client.getSnapshot().scanRuns).toHaveLength(2)
  })

  it("reloads known devices one answer per device without restarting anything", async () => {
    const user = userEvent.setup()
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    const serials = snapshot.endpoints.filter((endpoint) => endpoint.state === "current").map((endpoint) => endpoint.serial)
    expect(serials.length).toBeGreaterThan(0)
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    await user.click(screen.getByRole("button", { name: "Reload Devices" }))

    await waitFor(() => {
      const report = screen.getByText(/Mock reload re-read/)
      for (const serial of serials) expect(report).toHaveTextContent(serial)
    })
    // A reload is not the host-wide operation: nothing was restarted, and it is
    // not a scan either.
    expect(intents).toEqual([{ type: "reloadDevices" }])
    expect(intents.some((intent) => intent.type === "restartTransportServer")).toBe(false)
    expect(intents.some((intent) => intent.type === "startScan")).toBe(false)
  })

  it("explains an input-bearing control in a tooltip that keyboard focus opens", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // No paragraph-length description is left in the tab.
    expect(screen.queryByText(/Activate moves every discovered device/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Add saves the range above/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Restarts this host's adb server, then re-establishes/)).not.toBeInTheDocument()
    // The explanation is not simply always on screen either: it is on the
    // control, and it opens when the control is reached.
    expect(screen.queryByText(/The port every discovered device is moved to/)).not.toBeInTheDocument()

    screen.getByRole("textbox", { name: "Set Port" }).focus()

    await waitFor(() => {
      expect(screen.getByText(/The port every discovered device is moved to/)).toBeInTheDocument()
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
    // ...and there is no Connect control to fall back on for it either.
    expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument()
    // The section itself stays: the saved network's own Scan is not the adb
    // restart, and it does not disappear with it.
    expect(screen.getByRole("button", { name: "Scan Saved Network" })).toBeInTheDocument()
  })
})
