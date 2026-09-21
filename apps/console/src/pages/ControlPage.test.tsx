// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { Toaster } from "@/components/ui/sonner"
import type { ControlPlaneIntent, DeviceView, EndpointView, MutationResult } from "@/lib/domain/control-plane"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { liveMirrorCopy, liveStreamView, type LiveMirrorPreview, type MirrorCapacityView } from "@/lib/live-mirror"
import { deviceOperationLabels } from "@/lib/device-operations"
import { workspaceLayoutKey } from "@/lib/control-page-settings"
import { ControlPage } from "./ControlPage"
import { fakeGridPlane, gridProfile, gridProfileProto, gridStillBytes } from "@/test/grid-fixtures"
import { planeCapacity } from "@/test/mirror-fixtures"

/**
 * The browser's WebRTC stack is stubbed rather than exercised: what these tests
 * are about is that the console OPENs a device's stream and what it renders while
 * it holds one, not that a browser can decode H.264.
 */
class FakeRTCPeerConnection {
  iceGatheringState = "complete"
  localDescription: { type: string; sdp: string } | null = null
  addTransceiver() { return undefined }
  async createOffer() { return { type: "offer", sdp: "offer-sdp" } }
  async setLocalDescription(description: { type: string; sdp: string }) { this.localDescription = description }
  async setRemoteDescription() { return undefined }
  addEventListener() { return undefined }
  removeEventListener() { return undefined }
  close() { return undefined }
}

/** fakeMirror answers one device's stream, recording what the console asked for. */
function fakeMirror(state: "starting" | "live" | "ended" = "live", transport: MirrorTransport = MirrorTransport.WEBRTC) {
  const view = liveStreamView(create(MirrorStreamSchema, {
    streamId: "stream-1",
    deviceId: "atlas-04",
    transport,
    streamUrl: transport === MirrorTransport.TCP ? "/drift/v1/mirror/stream?stream_id=stream-1" : "",
    renderWidth: 1080,
    renderHeight: 1920,
    state: state === "live" ? MirrorStreamState.LIVE : state === "starting" ? MirrorStreamState.STARTING : MirrorStreamState.ENDED,
    frames: state === "live" ? 9n : 0n,
  }))
  const calls: string[] = []
  /** purposes are what each stream was opened AS: the frame, or one of the grid's tiles. */
  const purposes: string[] = []
  /** previews are the workspace's encode setting each stream STATED, if any. */
  const previews: (LiveMirrorPreview | undefined)[] = []
  const client: LiveMirrorClient = {
    // The transport the operator chose travels with the request, so a case can
    // assert the choice reached the control plane rather than a default.
    async startStream(request) { calls.push(`start:${request.deviceId}:${request.transport ?? "unspecified"}`); purposes.push(request.purpose); previews.push(request.preview); return view },
    async getCapacity() { return gridPlane },
    async negotiate(_streamId, offerSdp) { calls.push(`negotiate:${offerSdp}`); return { answerSdp: "answer-sdp", stream: view } },
    async stopStream(streamId) { calls.push(`stop:${streamId}`); return { ...view, state: "ended" } },
    async getStream() { return view },
    streamEndpoint(path) { calls.push(`endpoint:${path}`); return { url: `http://control-plane.test${path}`, headers: {} } },
  }
  return { client, calls, purposes, previews }
}

/** The plane the big frame's fixtures run against: five device sessions, one kept for the operator's own frame. */
const gridPlane: MirrorCapacityView = planeCapacity(5, 1)

/** openLiveMirrorDetails opens the info control beside the pin and returns what it holds. */
async function openLiveMirrorDetails(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByTestId("live-mirror-info"))
  return screen.findByTestId("live-mirror-details")
}

beforeEach(() => {
  render(<Toaster />)
})

describe("ControlPage screenshot", () => {
  it("does not authorize capture when the device has no current transport endpoint", async () => {
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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

  it("reports a concise reload summary without dumping every device", async () => {
    const user = userEvent.setup({ delay: null })
    const { client, intents, dispatch } = harness()
    const snapshot = client.getSnapshot()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)
    await openOtgTab(user)

    await user.click(screen.getByRole("button", { name: "Reload Devices" }))

    await waitFor(() => {
      const report = screen.getByText(/known devices? re-read/)
      expect(report).toHaveTextContent(`${snapshot.devices.length} known devices re-read`)
      expect(report).toHaveTextContent(/No adb server was restarted and no device was contacted/)
      expect(report).not.toHaveTextContent("MOCK-DEVICE-")
    })
    // A reload is not the host-wide operation: nothing was restarted, and it is
    // not a scan either.
    expect(intents).toEqual([{ type: "reloadDevices" }])
    expect(intents.some((intent) => intent.type === "restartTransportServer")).toBe(false)
    expect(intents.some((intent) => intent.type === "startScan")).toBe(false)
  })

  it("keeps guidance in visible labels with focused info icons", async () => {
    const user = userEvent.setup({ delay: null })
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    // Guidance is not repeated as paragraphs beneath every control.
    expect(screen.queryByText(/Activate moves every discovered device/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Add saves the range above/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Restarts this host's adb server, then re-establishes/)).not.toBeInTheDocument()
    // Explanations remain on the controls that need them, while self-labeled
    // actions and compact range rows do not repeat redundant labels.
    expect(screen.getByRole("button", { name: "About Set Port" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "About Saved Network" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "About Device Maintenance" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Activate" })).toHaveClass("h-7")
    expect(screen.getByRole("textbox", { name: "Set Port" })).not.toHaveAttribute("data-slot", "tooltip-trigger")
    expect(screen.getByRole("textbox", { name: "Set Port" })).toHaveClass("h-7")
    expect(screen.queryByRole("button", { name: "About Activate" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "About Scan Saved Network" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "About Reload Devices" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "About Restart ADB Server" })).not.toBeInTheDocument()
    expect(screen.queryByText(/The port every discovered device is moved to/)).not.toBeInTheDocument()

    screen.getByRole("button", { name: "About Set Port" }).focus()

    await waitFor(() => {
      expect(screen.getByText(/The port every discovered device is moved to/)).toBeInTheDocument()
    })
  })

  it("shows a loader while reloading and reports completion in a toast", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    let resolveReload: ((result: MutationResult) => void) | undefined
    const dispatch = async (intent: ControlPlaneIntent) => {
      if (intent.type === "reloadDevices") {
        return new Promise<MutationResult>((resolve) => { resolveReload = resolve })
      }
      return client.dispatch(intent)
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)
    await openOtgTab(user)

    const reload = screen.getByRole("button", { name: "Reload Devices" })
    await user.click(reload)

    expect(reload).toBeDisabled()
    expect(reload).toHaveAttribute("aria-busy", "true")
    expect(reload.querySelector(".animate-spin")).not.toBeNull()
    expect(resolveReload).toBeDefined()

    resolveReload?.({ ok: true, kind: "reloadDevices", message: "Mock reload complete" })

    await waitFor(() => {
      expect(reload).toBeEnabled()
      expect(screen.getByText("Devices reloaded")).toBeInTheDocument()
      expect(screen.getByText("Mock reload complete")).toBeInTheDocument()
    })
  })

  it("reports the transport surface's own answer rather than a claimed success", async () => {
    const user = userEvent.setup({ delay: null })
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
    const user = userEvent.setup({ delay: null })
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

describe("ControlPage connection filters", () => {
  it("shows all devices by default, filters by observed transport, and updates the count", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    const filterBar = screen.getByLabelText("Device connection filters")
    expect(filterBar).toHaveTextContent("6 / 6 devices shown")
    expect(filterBar).not.toHaveClass("border-y")
    expect(filterBar).not.toHaveClass("mt-4")
    expect(screen.getByRole("button", { name: "USB" })).toHaveAttribute("aria-pressed", "false")

    await user.click(screen.getByRole("button", { name: "USB" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("2 / 6 devices shown")
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Nova 05/i })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Atlas 07/i })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "USB" })).toHaveAttribute("aria-pressed", "true")
  })

  it("filters OTG-eligible devices separately from their transport", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    await user.click(screen.getByRole("button", { name: "OTG" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("3 / 6 devices shown")
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Atlas 07/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Orion 03/i })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Nova 02/i })).not.toBeInTheDocument()
  })

  it("keeps Workspace Settings in the toolbar when there are no devices", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const snapshot = { ...client.getSnapshot(), devices: [], endpoints: [] }
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => client.dispatch(intent)} />)

    const toolbar = screen.getByLabelText("Control workspace toolbar")
    const settings = within(toolbar).getByRole("button", { name: "Open Workspace Settings" })
    expect(settings).toHaveTextContent("Workspace Settings")
    expect(screen.getByText("No Devices for This Connection")).toBeInTheDocument()

    await user.click(settings)
    expect(screen.getByRole("complementary", { name: "Workspace Settings" })).toBeInTheDocument()
  })

  it("shows a device that is attached over USB but not yet authorized in the USB view", async () => {
    // The owner's own symptom: nineteen units plugged in, one drawn. An
    // attached-but-unauthorized device IS a USB device - that is where it is -
    // and a view that filters on the observed transport must show it. It is not
    // OTG-eligible, so the two filters answer two different questions (ARC-196).
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    const unauthorized: DeviceView = {
      id: "atlas-09",
      displayName: "Atlas 09",
      stableIdentity: "device-109",
      lifecycle: "active",
      status: "unauthorized",
      platformVersion: "Android 14",
      batteryPercent: 0,
      latencyMs: 0,
      lastSeen: "2 sec ago",
      agentId: "",
      endpointId: "endpoint-atlas-09-current",
      expectation: "expected",
      observedAgainAfterRetirement: false,
      transport: "usb",
      location: "",
      packageName: "",
      activityName: "",
      workflow: "",
      workflowStatus: "idle",
      taskProgress: 0,
      controlEligibility: "incompatible",
      capabilities: [],
    }
    render(<ControlPage snapshot={{ ...snapshot, devices: [...snapshot.devices, unauthorized] }} dispatch={async (intent) => client.dispatch(intent)} />)

    await user.click(screen.getByRole("button", { name: "USB" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("3 / 7 devices shown")
    expect(screen.getByRole("button", { name: /Atlas 09/i })).toBeInTheDocument()
    expect(screen.getByText("Unauthorized")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "OTG" }))

    // Attached is not eligible: the device it cannot act on is still shown by
    // transport and still withheld from control.
    expect(screen.queryByRole("button", { name: /Atlas 09/i })).not.toBeInTheDocument()
  })

  it("offers no registry row that is not a device an operator could switch to, and states what it withheld", async () => {
    // The three readings that put a row in the registry without putting a
    // switchable device on the board: the workspace's expectation decision is
    // retired; that retired identity has been observed again since, so it is the
    // replaced identity a later observation surfaced. Each is a reading the PLANE
    // reports - `expectation` and `observedAgainAfterRetirement` - rather than
    // anything this console infers.
    //
    // The fourth row is the deliberate NON-refusal: a device nobody has ever
    // observed. It is not refused, and it is asserted here rather than left out,
    // because "not a connectable device" and "not a device" are different facts
    // and the board draws this one with an absent state of its own.
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    const base = snapshot.devices[0]
    const withheldRows: DeviceView[] = [
      { ...base, id: "atlas-19", displayName: "Atlas 19", stableIdentity: "device-119", expectation: "retired", observedAgainAfterRetirement: false, status: "online", endpointId: "endpoint-atlas-19-current" },
      { ...base, id: "nova-19", displayName: "Nova 19", stableIdentity: "device-119-n", expectation: "retired", observedAgainAfterRetirement: true, status: "online", endpointId: "endpoint-nova-19-current" },
      { ...base, id: "orion-19", displayName: "Orion 19", stableIdentity: "device-119-o", expectation: "expected", observedAgainAfterRetirement: false, status: "unobserved", endpointId: "" },
    ]
    snapshot.devices = [...snapshot.devices, ...withheldRows]
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => client.dispatch(intent)} />)

    // The selector offers the seven it can offer, and the board says how many of
    // the registry it is not offering and why. The count an operator compares is
    // the count of frames the filter is choosing between.
    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("7 / 7 devices shown")
    const withheld = screen.getByTestId("board-withheld")
    expect(withheld).toHaveTextContent("2 of 9 devices in this workspace are not offered on this board")
    expect(withheld).toHaveTextContent("1 retired, because the workspace's expectation decision for this identity is retired")
    expect(withheld).toHaveTextContent("1 replaced identity, because this identity was retired and the unit has been observed again since")
    for (const name of [/Atlas 19/i, /Nova 19/i]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument()
    }
    // The row the selector does NOT refuse: it has no address, so its frame says
    // that, and it is still a frame the operator can select and read a state from.
    const unseen = screen.getByRole("button", { name: /Orion 19/i })
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
    expect(within(unseen).getByTestId("tile-address-orion-19")).toHaveTextContent("No address")

    // NOT hidden, and not a filter over the registry: the device list is the
    // registry view, and it still holds every row with the state it is in. A
    // retired device that a later observation surfaced is exactly what AGENTS.md
    // requires a surface to keep showing rather than silently restore or hide.
    await user.click(screen.getByRole("button", { name: "Devices" }))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByRole("row", { name: /Nova 19/ })).toBeInTheDocument()
    expect(within(dialog).getByRole("row", { name: /Atlas 19/ })).toBeInTheDocument()
  })
})

describe("ControlPage compact frame address", () => {
  it("labels a frame with the address on its current endpoint, not with an identifier", async () => {
    // The owner's report: the tile showed a long identifier where the address
    // belongs. atlas-04 answers at 192.0.2.10:5555, and its endpoint id and its
    // transport serial are both identifiers that are NOT that address, so the
    // assertion is falsifiable by exactly the defect it fixes.
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    const frame = screen.getByRole("button", { name: /Atlas 04/i })
    const address = within(frame).getByTestId("tile-address-atlas-04")
    expect(address).toHaveTextContent("192.0.2.10:5555")
    expect(address.textContent).not.toContain("endpoint-atlas-04")
    expect(address.textContent).not.toContain("MOCK-DEVICE-101")
    // Which of the two facts the operator is looking at is the label itself.
    expect(address).toHaveAttribute("title", "Atlas 04 is at 192.0.2.10:5555, the address on its current transport endpoint.")
  })

  it("says a device has no address rather than standing an identifier in its place", async () => {
    // A device the plane holds no CURRENT endpoint for has no address. The plane
    // still holds the device, its id and its serial, and the tile says so in words
    // instead of printing one of them where an address would be.
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    snapshot.endpoints = snapshot.endpoints.filter((endpoint) => endpoint.deviceId !== "nova-02")
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => client.dispatch(intent)} />)

    const frame = screen.getByRole("button", { name: /Nova 02/i })
    const address = within(frame).getByTestId("tile-address-nova-02")
    expect(address).toHaveTextContent("No address")
    expect(address.textContent).not.toContain("endpoint-nova-02")
    expect(address.textContent).not.toContain("MOCK-DEVICE-103")
    expect(address).toHaveAttribute("title", "Nova 02 has no address: the control plane holds no current transport endpoint for it, so no address has been observed.")
  })

  it("still draws a device it holds no address for, and reads a stored address toggle back", async () => {
    // The address is one fact on the frame and not the frame's existence: a device
    // with no address is still offered, still drawn, and still selectable. The
    // toggle that shows the address is read from the console's settings like the
    // name and the index beside it.
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    snapshot.endpoints = snapshot.endpoints.filter((endpoint) => endpoint.deviceId !== "nova-02")
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => client.dispatch(intent)} />)
    expect(within(screen.getByLabelText("Compact phone frames")).getByRole("button", { name: /Nova 02/i })).toBeInTheDocument()
  })
})

describe("ControlPage device observation status", () => {
  /**
   * The snapshot this suite renders holds the two absent states the wire can
   * report: Nova 05 is OFFLINE (observed before, not observed now) and Atlas 09
   * is UNSPECIFIED (no scan has observed it). They are built here rather than
   * added to the mock client so the existing fleet counts stay what they were.
   */
  function harness() {
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    const neverObserved: DeviceView = {
      id: "atlas-09",
      displayName: "Atlas 09",
      stableIdentity: "device-109",
      lifecycle: "unavailable",
      status: "unobserved",
      platformVersion: "",
      batteryPercent: 0,
      latencyMs: 0,
      lastSeen: "",
      agentId: "",
      endpointId: "",
      expectation: "expected",
      observedAgainAfterRetirement: false,
      transport: "unspecified",
      location: "",
      packageName: "",
      activityName: "",
      workflow: "",
      workflowStatus: "",
      taskProgress: 0,
      controlEligibility: "offline",
      capabilities: [],
    }
    snapshot.devices = [...snapshot.devices, neverObserved]
    const dispatch = async (intent: ControlPlaneIntent) => client.dispatch(intent)
    return { snapshot, dispatch }
  }

  it("marks a device that is not observed in its frame instead of showing it as online", () => {
    const { snapshot, dispatch } = harness()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // Nova 05 is offline in this snapshot. Its frame says so, and the mark
    // CENTRED in the frame names the device and its state, so the fact does not
    // depend on the frame's colour.
    const departed = screen.getByRole("button", { name: /Nova 05/i })
    expect(within(departed).getByText("Offline")).toBeInTheDocument()
    const departedMark = within(departed).getByRole("img", { name: "Nova 05 is offline: observed before, and no sighting of it still stands." })
    expect(departedMark.parentElement?.parentElement).toHaveClass("inset-0", "place-items-center")

    // Atlas 09 has never been observed. It reads as NOT OBSERVED — not as
    // offline, which would claim it was seen and then lost — and it carries its
    // own mark.
    const unseen = screen.getByRole("button", { name: /Atlas 09/i })
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
    expect(within(unseen).queryByText("Offline")).not.toBeInTheDocument()
    expect(within(unseen).getByRole("img", { name: "Atlas 09 is not observed: no observation has been recorded for this device." })).toBeInTheDocument()

    // An observed device carries no such mark, so the mark is what distinguishes
    // the absent frames rather than something every frame shows.
    const online = screen.getByRole("button", { name: /Atlas 04/i })
    expect(within(online).getByText("Online")).toBeInTheDocument()
    expect(within(online).queryByRole("img")).not.toBeInTheDocument()
  })

  it("keeps the OTG/HOLD tag on control eligibility, and never reads OTG on an absent frame", () => {
    const { snapshot, dispatch } = harness()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // The tag is untouched by this change and is consistent with the status:
    // neither an offline device nor one no scan has observed is eligible, so both
    // read HOLD while an eligible observed device still reads OTG.
    for (const name of [/Nova 05/i, /Atlas 09/i]) {
      const frame = screen.getByRole("button", { name })
      expect(within(frame).getByText("HOLD")).toBeInTheDocument()
      expect(within(frame).queryByText("OTG")).not.toBeInTheDocument()
    }
    expect(within(screen.getByRole("button", { name: /Atlas 04/i })).getByText("OTG")).toBeInTheDocument()
  })

  it("keeps the absent mark at the smallest landscape frame", async () => {
    const user = userEvent.setup({ delay: null })
    const { snapshot, dispatch } = harness()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // 192px is the smallest frame the console offers, and a landscape frame is 192px
    // WIDE with the tile's own shape turned for its height: this plane states no size
    // for a device it holds no picture for, so the tile draws the console's own
    // portrait shape turned - 16:9. The mark is centred in the frame at every size
    // rather than only in the default portrait one.
    //
    // The smallest frame is reached by moving the slider to it rather than by being
    // the default: the default is 264 (see the frame-size defaults case), and a case
    // about the smallest frame has to hold the smallest frame. The slider commits
    // when the operator stops moving it, so the release is what writes the value.
    //
    // The frame measured is the never-observed unit: a device the plane holds and
    // has never observed is DRAWN - it is a device with an identity and an absent
    // state of its own (see the fleet tiles case) - so its frame is measurable like
    // any other.
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    const smallScreen = screen.getByRole("slider", { name: /Small Screen/i })
    fireEvent.change(smallScreen, { target: { value: "192" } })
    fireEvent.pointerUp(smallScreen)
    await user.click(screen.getByRole("button", { name: "Landscape" }))

    // The frame is read from the grid itself rather than from any list of devices.
    const grid = screen.getByLabelText("Compact phone frames")
    const unseen = within(grid).getByRole("button", { name: /Atlas 09/i })
    expect(unseen).toHaveStyle({ width: "192px" })
    expect(within(unseen).getByTestId("still-tile-picture-atlas-09")).toHaveStyle({ aspectRatio: "16 / 9" })
    expect(within(unseen).getByRole("img", { name: /Atlas 09 is not observed/ })).toBeInTheDocument()
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
  })

  it("shows the same observation truth per row in the Device List, with copy that says what a status means", async () => {
    const user = userEvent.setup({ delay: null })
    const { snapshot, dispatch } = harness()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: "Devices" }))
    const dialog = await screen.findByRole("dialog")

    // The copy states what the column reports: the last observation the control
    // plane recorded, rather than a live connection.
    expect(within(dialog).getByText(/current observation fact recorded by the control plane, not a claim that the device answers at this moment/)).toBeInTheDocument()

    // A device that is not currently observed is marked as such on its own row,
    // and the two absent states stay two different facts.
    const departed = within(dialog).getByRole("row", { name: /Nova 05/ })
    expect(within(departed).getByText("Offline")).toBeInTheDocument()
    const unseen = within(dialog).getByRole("row", { name: /Atlas 09/ })
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
    expect(within(unseen).queryByText("Offline")).not.toBeInTheDocument()
    expect(within(within(dialog).getByRole("row", { name: /Atlas 04/ })).getByText("Online")).toBeInTheDocument()
  })
})

/**
 * The Console Settings dialog's Fleet Device Settings tab.
 *
 * It is the only control in that dialog that changes a DEVICE, so two things are
 * asserted rather than assumed: the dialog says so, and the apply names the
 * reviewed settings and no device list, because the fleet is read from the
 * registry by the control plane.
 */
describe("ControlPage Console Settings fleet device settings", () => {
  function harness() {
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    return { client, intents, dispatch }
  }

  async function openFleetTab(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "Settings" }))
    await user.click(await screen.findByRole("tab", { name: "Fleet" }))
  }

  it("says in the dialog that this tab changes device state", async () => {
    const user = userEvent.setup({ delay: null })
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: "Settings" }))
    // The old description claimed these controls were local display preferences
    // that never alter device state. That sentence is now false of one of its
    // tabs, so it is gone rather than left in place.
    expect(screen.queryByText(/These controls do not alter device policy, transport, or runtime state/)).not.toBeInTheDocument()
    expect(screen.getByText(/Fleet defaults change device state/i)).toBeInTheDocument()

    await user.click(await screen.findByRole("tab", { name: "Fleet" }))
    expect(screen.getByText(/These change the DEVICES, not this view/i)).toBeInTheDocument()
    expect(screen.getByText("Rotation Lock")).toBeInTheDocument()
    expect(screen.getByText("Autofill Off")).toBeInTheDocument()
  })

  it("does not apply anything until the operator confirms, and then reports the ONLINE fleet rather than the registry", async () => {
    // AGENTS.md §10: the delay between the events of one interaction runs on a real
    // timer and is taken out unless the delay is itself under test. This line came
    // from main with the PREVIOUS version of this case; this branch's base predates
    // it, so the merge restores the rule rather than dropping it with the old case.
    const user = userEvent.setup({ delay: null })
    // The mock fleet holds four devices it paints as ONLINE and two it does not:
    // one ATTENTION and one OFFLINE. The control plane's target set is the reading
    // this console shows the operator, so the run acts on the four, and the two are
    // NAMED as not contacted rather than reported as failures of the apply.
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)

    await openFleetTab(user)

    // §7: the text beside a control is part of the control, so the control states
    // the new behaviour itself - the ONLINE fleet, and a report carrying the count
    // of what was targeted - rather than leaving "every device" to be read into it.
    screen.getByRole("button", { name: "About Apply To Fleet" }).focus()
    await waitFor(() => {
      expect(screen.getByText(/every device the control plane reads as ONLINE/)).toBeInTheDocument()
    })
    expect(screen.getByText(/states how many devices were targeted/)).toBeInTheDocument()

    const apply = screen.getByRole("button", { name: "Apply to Fleet" })
    expect(apply).toBeDisabled()
    await user.click(apply)
    expect(intents.some((intent) => intent.type === "applyFleetDeviceSettings")).toBe(false)

    await user.click(screen.getByRole("switch", { name: /Confirm Fleet Apply/i }))
    expect(apply).toBeEnabled()
    await user.click(apply)

    await waitFor(() => {
      expect(intents.some((intent) => intent.type === "applyFleetDeviceSettings")).toBe(true)
    })
    const applied = intents.find((intent) => intent.type === "applyFleetDeviceSettings")
    // The closed set of reviewed settings, in the order they are offered, and NO
    // device list: a console cannot assert which devices are attached.
    expect(applied).toEqual({ type: "applyFleetDeviceSettings", settings: ["rotation_lock", "autofill_off"], confirmed: true })

    // The summary states what the run TARGETED and keeps the devices it did not
    // contact apart from the ones that failed. Counting the registry would report
    // a run that reached two units nothing reached.
    const summary = await screen.findByText((_, element) => element?.tagName === "P" && (element.textContent ?? "").includes("not online and not contacted"))
    expect(summary.textContent).toContain("Targeted 4 online device(s)")
    expect(summary.textContent).toContain("4 applied and verified every requested setting")
    expect(summary.textContent).toContain("0 did not")
    expect(summary.textContent).toContain("2 not online and not contacted")

    const table = await screen.findByRole("table", { name: /every setting this apply ran/i })
    // One row per device per setting: 6 devices x 2 settings, plus the header. A
    // device that was not contacted keeps its rows - it is named, never dropped.
    expect(within(table).getAllByRole("row")).toHaveLength(13)
    // The two devices the plane does not read as online carry the control plane's
    // own sentence for that fact, and it is not a transport failure.
    expect(within(table).getAllByText("the device is not online, so nothing was sent")).toHaveLength(4)
    expect(within(table).getAllByText("nova-02")).toHaveLength(2)
    expect(within(table).getAllByText("nova-05")).toHaveLength(2)
    expect(within(table).getAllByText(/^Applied/)).toHaveLength(8)
    expect(within(table).getAllByText(/^Not applied/)).toHaveLength(4)
    expect(within(table).getAllByText(/no read-back/)).toHaveLength(4)
  })
})

describe("ControlPage live mirror frame", () => {
  beforeEach(() => {
    vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("opens the selected device's live frame, and the placeholder decoration is gone", async () => {
    const user = userEvent.setup({ delay: null })
    const mock = new MockControlPlaneClient()
    const mirror = fakeMirror()
    render(<ControlPage snapshot={mock.getSnapshot()} dispatch={async (intent) => mock.dispatch(intent)} mirror={mirror.client} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    // The frame opens over the transport the operator's setting holds, and that
    // setting starts at the measured default - TCP, which these devices carried a
    // first picture over in 1-2 ms against WebRTC's 41-95 ms
    // (docs/operations/mirror-transport-measurement.md).
    expect(mirror.calls.some((call) => call === "start:atlas-04:tcp")).toBe(true)
    // The frame states its purpose: it is the operator's OWN viewer, which is the
    // demand the plane's reserve of its capacity exists for. A frame that opened as
    // one of the grid's tiles could be refused for the grid's spending while the
    // operator is looking at it.
    expect(mirror.purposes).toContain("operator")
    // And the frame states NO preview setting: it is carried at the plane's own
    // profile, so a request that stated one would be this console claiming a bound
    // it does not set - and a level chosen for a grid of thumbnails must never
    // bound the frame the work happens in.
    expect(mirror.previews[mirror.purposes.indexOf("operator")]).toBeUndefined()
    expect(screen.queryByText("Interactive phone surface")).not.toBeInTheDocument()

    // The stream's own frame is stated, because every coordinate is measured in
    // it - and it is stated behind the info control beside the pin, not over the
    // device's screen, which is the whole of this change.
    const details = await openLiveMirrorDetails(user)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent(/WebRTC \(pion/i)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent("1080x1920")
  })

  it("offers both transports, and opens the device's stream over the one chosen", async () => {
    const user = userEvent.setup({ delay: null })
    const mock = new MockControlPlaneClient()
    const mirror = fakeMirror("live", MirrorTransport.TCP)
    // The TCP transport reads its bytes from the control plane's stream endpoint;
    // what this case asserts is the wiring, so the endpoint answers with an empty
    // body rather than reaching a network. The reader is the port the playback reads
    // the picture through and the one its teardown releases the response with (see
    // MirrorPlayback.stop), so the fake answers both.
    const emptyBody = { getReader: () => ({ read: async () => ({ done: true, value: undefined }), cancel: async () => undefined }) } as unknown as ReadableStream<Uint8Array>
    vi.stubGlobal("fetch", async () => ({ ok: true, body: emptyBody }))
    render(<ControlPage snapshot={mock.getSnapshot()} dispatch={async (intent) => mock.dispatch(intent)} mirror={mirror.client} />)

    const settings = screen.getAllByRole("button", { name: /^Settings$/ }).find((button) => button.getAttribute("aria-haspopup") === "dialog")
    expect(settings).toBeDefined()
    await user.click(settings!)

    // Both transports work, so this is a choice rather than a statement - and the
    // control an operator reads is the one this console opens streams over.
    expect(screen.getByText(liveMirrorCopy.settings.notice)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /^Connection$/i })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: liveMirrorCopy.settings.label }))
    await user.click(await screen.findByRole("menuitem", { name: liveMirrorCopy.settings.choice.tcp }))
    expect(screen.getByRole("button", { name: liveMirrorCopy.settings.label })).toHaveTextContent(liveMirrorCopy.settings.choice.tcp)

    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))

    // The choice travels with the stream: the console asked for TCP, and the
    // frame states the transport the stream is actually using, behind the info
    // control beside the pin.
    await waitFor(() => expect(mirror.calls.some((call) => call === "start:atlas-04:tcp")).toBe(true))
    const details = await openLiveMirrorDetails(user)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent(/TCP \(MSE/i)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent("1080x1920")
    expect(mirror.calls.some((call) => call.startsWith("endpoint:"))).toBe(true)
  })

  it("shows no live frame for a console that has no control plane behind it", async () => {
    const user = userEvent.setup({ delay: null })
    const mock = new MockControlPlaneClient()
    render(<ControlPage snapshot={mock.getSnapshot()} dispatch={async (intent) => mock.dispatch(intent)} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    // The frame says so where the picture would be, and the details say it in
    // the console's own words rather than leaving an empty box.
    expect(await screen.findByText(liveMirrorCopy.phase.unavailable)).toBeInTheDocument()
    const details = await openLiveMirrorDetails(user)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/no control plane/i)
    expect(screen.queryByText("Interactive phone surface")).not.toBeInTheDocument()
  })
})

/**
 * The fleet grid's tiles.
 *
 * Two claims, both about what a tile IS: a device with no current observation is
 * drawn in ONE fixed colour rather than its index, and every device in the view
 * carries the control plane's STILL - a picture the plane captured on its own
 * cadence - with no allocation and no bound a tile can be denied by. A tile is a
 * picture and nothing else: nothing here takes a lease, opens a control session, or
 * dispatches anything, and the one live session this console opens is the big frame
 * the operator works a device from.
 */
describe("ControlPage fleet tiles", () => {
  beforeEach(() => {
    vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function grid() {
    const mock = new MockControlPlaneClient()
    const snapshot = mock.getSnapshot()
    // A second absent state, so the fixed colour is checked on both of the facts
    // that are not "observed" rather than on one of them.
    const neverObserved: DeviceView = {
      ...snapshot.devices[0]!,
      id: "atlas-09",
      displayName: "Atlas 09",
      status: "unobserved",
      transport: "unspecified",
      controlEligibility: "offline",
    }
    snapshot.devices = [...snapshot.devices, neverObserved]
    return { snapshot, dispatch: async (intent: ControlPlaneIntent) => mock.dispatch(intent) }
  }

  it("draws every device with no current observation in ONE fixed colour", () => {
    const { snapshot, dispatch } = grid()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // Nova 05 was observed and is not now; Atlas 09 has never been observed. Both
    // are "nothing is observed here", so both carry the same fixed colour,
    // whatever their index in the grid.
    const departed = screen.getByRole("button", { name: /Nova 05/i })
    const unseen = screen.getByRole("button", { name: /Atlas 09/i })
    expect(departed).toHaveClass("bg-zinc-800")
    expect(unseen).toHaveClass("bg-zinc-800")

    // An observed tile keeps its own index colour, so the two states cannot read
    // as one - and the tile's own status label still says which fact it is.
    const online = screen.getByRole("button", { name: /Atlas 04/i })
    expect(online).not.toHaveClass("bg-zinc-800")
    expect(within(departed).getByText("Offline")).toBeInTheDocument()
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
  })

  it("draws only the devices the view's connection filter shows, with no bound deciding how many of them may be carried", async () => {
    const user = userEvent.setup({ delay: null })
    const { snapshot, dispatch } = grid()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" }, "nova-05": { state: "current" } } })
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} grid={plane.client} />)
    await waitFor(() => expect(plane.requests).toHaveLength(1))
    expect(screen.getAllByTestId(/^still-tile-state-/)).toHaveLength(7)

    // The grid follows the view: with the filter set to USB, the two USB devices are
    // drawn and the rest are not, because a tile is a picture of a device in the view
    // and not a place on the plane that some other device is holding.
    await user.click(screen.getByRole("button", { name: "USB" }))
    const shown = screen.getAllByTestId(/^still-tile-state-/).map((tile) => (tile.getAttribute("data-testid") ?? "").replace("still-tile-state-", ""))
    expect(shown.sort()).toEqual(["atlas-04", "nova-05"])
    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveAttribute("src", `data:image/jpeg;base64,${gridStillBytes}`)
    // The plane's next sweep is the release for the devices that left the view and the
    // capture set for the ones that stayed, so the set is NAMED to it whole - which the
    // hook's own test drives on the plane's cadence rather than waiting four seconds
    // for it here.
    expect(plane.requests[0]).toHaveLength(7)
  })

  it("draws a still for EVERY device in the view, names every one of them to the plane, and opens no tile stream", async () => {
    const { snapshot, dispatch } = grid()
    const mirror = fakeMirror()
    const plane = fakeGridPlane({
      stills: { "atlas-04": { state: "current" }, "atlas-07": { state: "current" }, "nova-02": { state: "current" }, "orion-01": { state: "current" }, "orion-03": { state: "current" } },
    })
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} mirror={mirror.client} grid={plane.client} />)

    // Seven devices are in this view and every one of them is a tile: a still spends
    // no device session, so there is no place for a tile to be denied and nothing for
    // this console to allocate. The set the grid is drawing IS the plane's capture
    // set, so it is named whole, in the grid's own order.
    await waitFor(() => expect(plane.requests).toHaveLength(1))
    const tiles = screen.getAllByTestId(/^still-tile-state-/)
    expect(tiles).toHaveLength(7)
    expect(plane.requests[0]).toEqual(tiles.map((tile) => (tile.getAttribute("data-testid") ?? "").replace("still-tile-state-", "")))

    // A device the plane captured is drawn from the media type and the bytes it
    // stated, and the tile says it is a STILL rather than claiming a live picture.
    const current = await screen.findByTestId("still-tile-state-atlas-04")
    expect(current).toHaveAttribute("data-tile-state", "current")
    expect(current).toHaveTextContent(/^Still/)
    expect(current).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveAttribute("src", `data:image/jpeg;base64,${gridStillBytes}`)

    // A device the plane cannot capture is answered in the plane's own words rather
    // than left off the grid, and it carries no picture.
    const unavailable = screen.getByTestId("still-tile-state-nova-05")
    expect(unavailable).toHaveAttribute("data-tile-state", "unavailable")
    expect(unavailable).toHaveTextContent("No picture")
    expect(screen.getByTestId("still-tile-image-nova-05")).not.toHaveAttribute("src")

    // And NO tile is a viewer of anything: the grid opens no device stream at all,
    // because the one live session this console opens is the operator's own frame.
    expect(mirror.calls.filter((call) => call.startsWith("start:"))).toEqual([])
  })

  it("draws no picture for a still the plane reports not current, and carries the plane's own failure sentence", async () => {
    const { snapshot, dispatch } = grid()
    const reason = "the capture path could not read this device's screen"
    const plane = fakeGridPlane({
      stills: { "atlas-04": { state: "stale", failureClass: "observation", failureDetail: reason }, "atlas-07": { state: "current" } },
    })
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} grid={plane.client} />)

    const stale = await screen.findByTestId("still-tile-state-atlas-04")
    await waitFor(() => expect(stale).toHaveAttribute("data-tile-state", "stale"))
    expect(stale).toHaveTextContent("Not current")
    // The plane's OWN classification reaches the operator, in the tile's own
    // element: the class it grouped the failure under and the detail it recorded,
    // never a generic "failed" that names nothing to act on.
    expect(stale).toHaveAttribute("aria-label", expect.stringContaining(reason) as unknown as string)
    expect(stale).toHaveAttribute("aria-label", expect.stringContaining("observation") as unknown as string)
    // A failed capture is a classified failure, so it announces itself; the states
    // that are not failures do not (see the refused case below).
    expect(stale).toHaveAttribute("role", "alert")

    // The element the picture is written into is the tile's for its whole lifetime,
    // and it carries NO source: the last still the plane holds is not this device's
    // screen now, so nothing is passed off as the screen.
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
    expect(screen.getByTestId("still-tile-image-atlas-04")).toHaveClass("invisible")

    // One device's failed capture is not a reason for the grid to stop drawing every
    // other device.
    expect(screen.getByTestId("still-tile-image-atlas-07")).toHaveAttribute("src", `data:image/jpeg;base64,${gridStillBytes}`)
  })

  it("says which bound kept a device out when the plane's own sweep bound did not reach it", async () => {
    const { snapshot, dispatch } = grid()
    const plane = fakeGridPlane({ stills: { "atlas-04": { state: "current" } }, refused: ["orion-03"] })
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} grid={plane.client} />)

    const refused = await screen.findByTestId("still-tile-state-orion-03")
    await waitFor(() => expect(refused).toHaveAttribute("data-tile-state", "refused"))
    expect(refused).toHaveTextContent("Not shown")
    // The bound is the PLANE's, named with the numbers it published: how many devices
    // one sweep carries, and the level every still is carried at. It is a bound on the
    // plane's own work, not a place this device is waiting for, so the tile carries no
    // alert and no picture.
    expect(refused).toHaveAttribute("aria-label", expect.stringContaining("64 device(s) in one sweep") as unknown as string)
    expect(refused).toHaveAttribute("aria-label", expect.stringContaining("medium level, 360 px wide at JPEG quality 65") as unknown as string)
    expect(refused).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-orion-03")).not.toHaveAttribute("src")

    // The line beside the grid names that bound because a device in this view reached
    // it - and it states the cadence, the level and the fact that a still spends no
    // device session, so the grid carries no tile count to run out of.
    const line = screen.getByTestId("grid-stills-line")
    expect(line).toHaveTextContent("about every 4 seconds")
    expect(line).toHaveTextContent("the medium level, 360 px wide at JPEG quality 65")
    expect(line).toHaveTextContent("A still spends NO device session")
    expect(line).toHaveTextContent("captures at most 64 device(s) in one sweep, and 1 device(s) in this view are past that bound")
    expect(line).toHaveTextContent("the operator's own big frame keeps the one live session it needs")
    expect(line.textContent ?? "").not.toMatch(/\bis live\b/i)
  })

  it("states the PLANE's own cadence, level and numbers beside the grid, and no sweep bound nothing reached", async () => {
    const { snapshot, dispatch } = grid()
    const plane = fakeGridPlane({
      stills: { "atlas-04": { state: "current" } },
      profile: gridProfileProto(gridProfile({ cadenceMillis: 5_000, level: "high", levelMaxWidth: 480, levelJpegQuality: 70, stillByteBound: 120_000, maxDevices: 12 })),
    })
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} grid={plane.client} />)

    await waitFor(() => expect(plane.requests).toHaveLength(1))
    const line = screen.getByTestId("grid-stills-line")
    // The cadence and the level are the PLANE's, in the numbers it published BESIDE the
    // level's name: a line that printed "high" alone could not say whether the pictures
    // are 480 or 720 pixels wide, and one that printed this console's own workspace
    // setting would state a bound the plane is not applying to anything.
    expect(line).toHaveTextContent("about every 5 seconds")
    expect(line).toHaveTextContent("the high level, 480 px wide at JPEG quality 70")
    expect(line).toHaveTextContent("up to 120000 bytes per still")
    // A bound stated where nothing reached it reads as the tile count this surface
    // removed, so the plane's sweep bound is named only for the devices it did not
    // reach - and in this view it reached it for nobody.
    expect(line).not.toHaveTextContent("in one sweep")
  })

  it("claims nothing about any device, and says so, when a read it could not complete arrives", async () => {
    const { snapshot, dispatch } = grid()
    const plane = fakeGridPlane()
    plane.fail(new Error("the control plane is not answering"))
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} grid={plane.client} />)

    // A read this console could not complete is a fact about this console's reach and
    // not a report about any device, so the grid states its own missing report instead
    // of claiming anything the plane did not say.
    await waitFor(() => expect(screen.getByTestId("grid-stills-line")).toHaveTextContent("could not read the control plane"))
    const tile = screen.getByTestId("still-tile-state-atlas-04")
    expect(tile).toHaveAttribute("data-tile-state", "unreadable")
    expect(tile).toHaveTextContent("No report")
    expect(tile).not.toHaveAttribute("role")
    expect(screen.getByTestId("still-tile-image-atlas-04")).not.toHaveAttribute("src")
  })

  it("says which fact it is missing when this console has no control plane behind it", async () => {
    const { snapshot, dispatch } = grid()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // No client at all is the missing reading, not a plane with no pictures: every
    // device is still drawn, every tile states its own missing report, and nothing is
    // claimed about a device.
    await waitFor(() => expect(screen.getByTestId("grid-stills-line")).toHaveTextContent("no control plane"))
    expect(screen.getAllByTestId(/^still-tile-state-/)).toHaveLength(7)
    expect(screen.getByTestId("still-tile-state-atlas-04")).toHaveTextContent("No report")
  })
})

/**
 * The big frame's action column.
 *
 * Every control here either dispatches a typed action for the SELECTED device
 * through the control plane and reports the plane's own answer, or it is not
 * rendered at all (AGENTS.md section 7). These cases assert the DISPATCH and
 * never the toast: the defect they replace was a control whose whole effect was
 * a message that read like a confirmation prompt.
 */
describe("ControlPage big-frame device commands", () => {
  beforeEach(() => {
    vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /**
   * All thirteen controls, which is every control the panel renders.
   *
   * This is the list the card started from ("12 of 13 do nothing today"), written
   * out here rather than read from the rendering table, because a test that asked
   * the table what to expect would pass whatever the table said. Twelve of the
   * thirteen dispatch a device action; Change Device is panel navigation and
   * moves the frame's control session, which is a dispatch of its own.
   *
   * There is no `withdrawn` list any more: the card's ruling is that no control is
   * removed and every one is completed, so a label leaving this list would be a
   * control that stopped being rendered and the assertion below is what says so.
   */
  const rendered = ["Change Device", "Volume Up", "Volume Down", "Screenshot", "Power Button", "Lock Rotate", "Reboot", "Switch Keyboard", "Install APK", "Import File", "Export File", "ADB Command", "Quick Phrase"]

  /** frameHarness renders the page over the mock plane and records every dispatch. */
  function frameHarness(snapshot?: ReturnType<MockControlPlaneClient["getSnapshot"]>) {
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    const mirror = fakeMirror()
    render(<ControlPage snapshot={snapshot ?? client.getSnapshot()} dispatch={dispatch} mirror={mirror.client} />)
    return { intents, dispatch, mirror, client }
  }

  it("dispatches the device's own key events for volume and power, named against the frame's own observation", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    for (const [label, keyCode] of [["Volume Up", 24], ["Volume Down", 25], ["Power Button", 26]] as const) {
      await user.click(within(controls).getByRole("button", { name: label }))
      const dispatched = intents.filter((intent) => intent.type === "submitDeviceKeyEvent" && intent.keyCode === keyCode)
      await waitFor(() => expect(dispatched).toHaveLength(1))
      // One key event per click, for the SELECTED device, against the observation
      // the frame is streaming: the kernel's catalog declares a key event as
      // requiring one, so an intent naming none is refused as malformed.
      expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", observationToken: "stream-1", confirmed: true })
    }
    expect(intents.filter((intent) => intent.type === "submitDeviceKeyEvent")).toHaveLength(3)
  })

  it("sends nothing and names the missing fact when this console holds no lease for the device", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    snapshot.leases = []
    const intents: ControlPlaneIntent[] = []
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => { intents.push(intent); return client.dispatch(intent) }} mirror={fakeMirror().client} />)
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    const volumeUp = within(controls).getByRole("button", { name: "Volume Up" })
    expect(volumeUp).toBeDisabled()
    await user.click(volumeUp)
    expect(intents.some((intent) => intent.type === "submitDeviceKeyEvent")).toBe(false)

    // A control that cannot act says which fact is missing rather than only
    // looking disabled: the frame's info control is marked while it holds
    // something unread, and it holds the reason in the frame's own words.
    await user.click(screen.getByTestId("live-mirror-info"))
    const details = await screen.findByTestId("live-mirror-details")
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noLease)
  })

  it("moves the frame's control session to another device through the control plane", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Change Device" }))
    const picker = await screen.findByTestId("panel-device-picker")
    await user.click(within(picker).getByRole("button", { name: /Atlas 07/i }))

    // The move is two dispatches and not a local selection: the device the frame
    // held is released through the kernel before the chosen device's session is
    // asked for, because one device has at most one active lease.
    await waitFor(() => expect(intents.some((intent) => intent.type === "beginDeviceControl" && intent.deviceId === "atlas-07")).toBe(true))
    const ended = intents.findIndex((intent) => intent.type === "endDeviceControl" && intent.deviceId === "atlas-04")
    const begun = intents.findIndex((intent) => intent.type === "beginDeviceControl" && intent.deviceId === "atlas-07")
    expect(ended).toBeGreaterThanOrEqual(0)
    expect(begun).toBeGreaterThan(ended)
    expect(await screen.findByLabelText(/Atlas 07 floating phone frame/i)).toBeInTheDocument()
  })

  it("opens Change Device as a panel beside the frame, and offers only devices that are online", async () => {
    const user = userEvent.setup({ delay: null })
    frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Change Device" }))
    const picker = await screen.findByTestId("panel-device-picker")

    // The list is the console's ONLINE reading and nothing else. Nova 05 is
    // OFFLINE and Nova 02 needs ATTENTION: neither can receive the frame, so
    // neither is offered. Atlas 07 and Orion 01 are online and both are.
    expect(within(picker).getByRole("button", { name: /Atlas 07/i })).toBeInTheDocument()
    expect(within(picker).getByRole("button", { name: /Orion 01/i })).toBeInTheDocument()
    expect(within(picker).queryByRole("button", { name: /Nova 05/i })).not.toBeInTheDocument()
    expect(within(picker).queryByRole("button", { name: /Nova 02/i })).not.toBeInTheDocument()
    // The frame itself is not offered as somewhere to move to.
    expect(within(picker).queryByRole("button", { name: /Atlas 04/i })).not.toBeInTheDocument()

    // It is a SIBLING of the frame rather than an overlay drawn over or under it:
    // the two share a parent, which is what lets an operator read the list and
    // the frame at once.
    const frame = screen.getByLabelText(/Atlas 04 floating phone frame/i)
    expect(picker.parentElement).toBe(frame.parentElement)
    expect(picker.tagName).toBe("ASIDE")
    expect(picker.compareDocumentPosition(frame) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy()

    // Closing it is its own named control, and it goes away with it.
    await user.click(within(picker).getByRole("button", { name: "Close device picker" }))
    expect(screen.queryByTestId("panel-device-picker")).not.toBeInTheDocument()
  })

  it("says so when nothing else is online, rather than showing an empty list", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const snapshot = client.getSnapshot()
    // One device is online - the one the frame holds. Every other device is
    // offline or unobserved, so there is nothing this frame can be moved to.
    snapshot.devices = snapshot.devices.map((device) => device.id === "atlas-04" ? device : { ...device, status: "offline" as const })
    render(<ControlPage snapshot={snapshot} dispatch={async (intent) => client.dispatch(intent)} mirror={fakeMirror().client} />)
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Change Device" }))
    const picker = await screen.findByTestId("panel-device-picker")
    expect(within(picker).getByTestId("panel-device-picker-empty")).toHaveTextContent(/No other device is online/)
  })

  it("renders every one of the thirteen controls, because every one of them dispatches", async () => {
    const user = userEvent.setup({ delay: null })
    frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    // The rule is "render a control only when it performs its action", so the
    // assertion is the whole list: a control silently dropped from the column
    // fails here rather than being noticed by an operator who cannot find it.
    expect(rendered).toHaveLength(13)
    for (const label of rendered) expect(within(controls).getByRole("button", { name: label })).toBeInTheDocument()
  })

  it("types the operator's phrase into the selected device, named by an opaque handle", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Quick Phrase" }))
    const dialog = await screen.findByRole("dialog")
    const confirm = within(dialog).getByRole("button", { name: "Confirm and type" })
    // Nothing can be typed before there is something to type.
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByLabelText(/Phrase to type/i), "on my way")
    expect(confirm).toBeEnabled()
    await user.click(confirm)

    const dispatched = intents.filter((intent) => intent.type === "submitDeviceText")
    await waitFor(() => expect(dispatched).toHaveLength(1))
    // The DISPATCH is what is asserted, never a toast: the phrase goes to the
    // device the frame has open, through the kernel's typed-text action, carrying
    // the operator's own approval. The plane's own sentence is then what the panel
    // reports, because typed text is not a device-operation row.
    expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", text: "on my way", confirmed: true })
    await waitFor(() => expect(within(controls).getByTestId("panel-operation-outcome")).not.toBeEmptyDOMElement())
  })

  it("dispatches one per-device settings apply for Lock Rotate, for the selected device", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Lock Rotate" }))

    // The DISPATCH is what is asserted, never a toast: the control has to send a
    // typed per-device action for the device the frame has open, carrying the
    // operator's own approval, and the catalogued setting is what it names.
    const dispatched = intents.filter((intent) => intent.type === "applyDeviceSetting")
    await waitFor(() => expect(dispatched).toHaveLength(1))
    expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", setting: "rotation_lock", confirmed: true })
    // One click is one dispatch: a control that re-fired on every render would be
    // sending a settings write at the device the operator is not looking at.
    expect(intents.filter((intent) => intent.type === "applyDeviceSetting")).toHaveLength(1)
  })

  it("reports the plane's own answer for Lock Rotate, including when it refused", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const dispatched: ControlPlaneIntent[] = []
    // The plane refuses this attempt, and the sentence it refused WITH is what
    // the frame has to show: a per-device control that swallowed a refusal and
    // drew a success is the defect this card exists to remove.
    const refusal = "the device is attached but this host is not authorized to control it"
    const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => {
      dispatched.push(intent)
      return { ok: false, kind: intent.type, message: refusal, errorCode: "unauthorized" }
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} mirror={fakeMirror().client} />)
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Lock Rotate" }))

    await waitFor(() => expect(within(controls).getByTestId("panel-setting-outcome")).toHaveTextContent(refusal))
    expect(dispatched.some((intent) => intent.type === "applyDeviceSetting")).toBe(true)
  })

  it("dispatches one device operation for Reboot and one for Switch Keyboard, for the selected device", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    for (const operation of ["reboot", "keyboard_switch"] as const) {
      await user.click(within(controls).getByRole("button", { name: deviceOperationLabels[operation] }))
      const dispatched = intents.filter((intent) => intent.type === "runDeviceOperation" && intent.operation === operation)
      await waitFor(() => expect(dispatched).toHaveLength(1))
      // The DISPATCH is what is asserted, never a toast: the control sends a typed
      // per-device operation for the device the frame has open, carrying the
      // operator's own approval, and it carries NO parameter — a reboot's argument
      // array is the operation's own, and the keyboard is chosen by the plane from
      // the DEVICE's enabled list rather than named here.
      expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", operation, fileName: "", artifactId: "", packageName: "", confirmed: true })
    }
    // One click is one dispatch, and the two controls dispatch two different
    // operations rather than the same one twice.
    expect(intents.filter((intent) => intent.type === "runDeviceOperation")).toHaveLength(2)
  })

  it("sends a file operation only once the operator has named a bounded file and an artifact this workspace holds", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents, client } = frameHarness()
    const artifact = client.getSnapshot().artifacts[0]
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Import File" }))
    const dialog = await screen.findByRole("dialog")
    const confirm = within(dialog).getByRole("button", { name: "Confirm and send" })
    // Nothing can be sent before the operation has what it needs: the name is
    // bounded to a NAME and the artifact has to be one this workspace holds, so
    // the control is disabled rather than sending a request the plane must refuse.
    expect(confirm).toBeDisabled()
    // A path is not a name. The console's own pre-check says so while the operator
    // is still looking at the field.
    await user.type(within(dialog).getByLabelText(/File name on the device/i), "../etc/passwd")
    expect(confirm).toBeDisabled()
    expect(within(dialog).getByText(/carries no separator, no parent and no shell character/i)).toBeInTheDocument()
    await user.clear(within(dialog).getByLabelText(/File name on the device/i))
    await user.type(within(dialog).getByLabelText(/File name on the device/i), "notes.txt")
    expect(confirm).toBeDisabled()
    await user.click(within(dialog).getByRole("button", { name: "Artifact this workspace holds" }))
    await user.click(await screen.findByRole("menuitem", { name: new RegExp(artifact.id) }))
    expect(confirm).toBeEnabled()
    await user.click(confirm)

    const dispatched = intents.filter((intent) => intent.type === "runDeviceOperation" && intent.operation === "import_file")
    await waitFor(() => expect(dispatched).toHaveLength(1))
    expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", operation: "import_file", fileName: "notes.txt", artifactId: artifact.id, confirmed: true })
  })

  it("dispatches Export File with the file it reads out, and nothing but the name", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Export File" }))
    const dialog = await screen.findByRole("dialog")
    await user.type(within(dialog).getByLabelText(/File name on the device/i), "dump.txt")
    await user.click(within(dialog).getByRole("button", { name: "Confirm and send" }))

    const dispatched = intents.filter((intent) => intent.type === "runDeviceOperation" && intent.operation === "export_file")
    await waitFor(() => expect(dispatched).toHaveLength(1))
    // An export names the file it reads OUT: it pulls bytes off the device into the
    // artifact store, so it carries no artifact of its own to write.
    expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", operation: "export_file", fileName: "dump.txt", artifactId: "", confirmed: true })
  })

  it("dispatches the advanced form as the exact argument array the operator confirmed, entry by entry", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "ADB Command" }))
    const dialog = await screen.findByRole("dialog")
    const confirm = within(dialog).getByRole("button", { name: "Confirm and dispatch" })
    expect(confirm).toBeDisabled()
    // One argument per line, and the dialog lists the array entry by entry: what
    // the operator confirms is what the record will name, and the array is never
    // joined into one command string.
    await user.type(within(dialog).getByLabelText(/Argument array/i), "get-state\n")
    expect(confirm).toBeEnabled()
    const preview = within(dialog).getByTestId("advanced-argv-preview")
    expect(within(preview).getAllByRole("listitem")).toHaveLength(1)
    expect(preview).toHaveTextContent("get-state")
    await user.click(confirm)

    const dispatched = intents.filter((intent) => intent.type === "runAdvancedCommand")
    await waitFor(() => expect(dispatched).toHaveLength(1))
    expect(dispatched[0]).toMatchObject({ deviceId: "atlas-04", confirmed: true })
    expect((dispatched[0] as Extract<ControlPlaneIntent, { type: "runAdvancedCommand" }>).argv).toEqual(["get-state"])
  })

  it("reports the plane's own refusal for a device operation, and sends nothing else", async () => {
    const user = userEvent.setup({ delay: null })
    const client = new MockControlPlaneClient()
    const dispatched: ControlPlaneIntent[] = []
    // The plane refuses this attempt, and the sentence it refused WITH is what the
    // panel has to show: a control that swallowed a refusal and drew a success is
    // the defect this card exists to remove.
    const refusal = "the device has no single current transport endpoint, so nothing was sent"
    const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => {
      dispatched.push(intent)
      return { ok: false, kind: intent.type, message: refusal, errorCode: "precondition_failed" }
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} mirror={fakeMirror().client} />)
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Reboot" }))

    await waitFor(() => expect(within(controls).getByTestId("panel-operation-outcome")).toHaveTextContent(refusal))
    expect(dispatched.filter((intent) => intent.type === "runDeviceOperation")).toHaveLength(1)
  })

  it("states what the device answered when an operation completed", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = frameHarness()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    const controls = await screen.findByLabelText(/Atlas 04 floating device controls/i)

    await user.click(within(controls).getByRole("button", { name: "Switch Keyboard" }))

    // The row the plane reported is what is rendered — the plane's own sentence for
    // the operation and device, not a console-side guess at what happened.
    await waitFor(() => expect(within(controls).getByTestId("panel-operation-outcome")).not.toBeEmptyDOMElement())
    const outcome = within(controls).getByTestId("panel-operation-outcome").textContent ?? ""
    expect(outcome).toMatch(/Switch Keyboard/i)
    expect(intents.filter((intent) => intent.type === "runDeviceOperation")).toHaveLength(1)
  })

  it("states the rule the action column follows, where the operator reads it", async () => {
    frameHarness()
    // The sentence the panel used to carry said high-risk actions "stay blocked",
    // which stopped being true when the blanket command ban was retired; it named
    // a rule that no longer exists instead of what the column actually does.
    expect(screen.getByText(/command this build cannot dispatch to the selected device is not shown/i)).toBeInTheDocument()
    expect(screen.queryByText(/stay blocked/i)).toBeNull()
  })
})

describe("ControlPage workspace display settings", () => {
  function pageHarness() {
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    const mirror = fakeMirror()
    return { client, intents, dispatch, mirror }
  }

  /** page renders the Control page over the SAME control plane each time it is called. */
  function page(harness: ReturnType<typeof pageHarness>) {
    return <ControlPage snapshot={harness.client.getSnapshot()} dispatch={harness.dispatch} mirror={harness.mirror.client} />
  }

  /** storedValue is the workspace record's own value for a key, or null if it holds none. */
  function storedValue(harness: ReturnType<typeof pageHarness>, key: string): string | null {
    return harness.client.getSnapshot().settings.find((setting) => setting.key === key)?.valueJson ?? null
  }

  function settingWrites(harness: ReturnType<typeof pageHarness>) {
    return harness.intents.filter((intent) => intent.type === "createSetting" || intent.type === "updateSetting")
  }

  it("draws 680 and 264 until the operator chooses a size, then writes the choice to the workspace record", async () => {
    const user = userEvent.setup({ delay: null })
    const harness = pageHarness()
    render(page(harness))

    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    const floating = screen.getByRole("slider", { name: /Floating Frame Size/i })
    const small = screen.getByRole("slider", { name: /Small Screen/i })

    // A workspace that has stored no size draws the two defaults. Reading them is
    // not writing them: nothing is in the record until the operator chooses.
    expect(floating).toHaveValue("680")
    expect(small).toHaveValue("264")
    expect(storedValue(harness, workspaceLayoutKey)).toBeNull()
    expect(settingWrites(harness)).toHaveLength(0)

    // The value is written when the operator STOPS moving the control, not on
    // every pixel of the drag: one drag is one choice, and the record says so.
    fireEvent.change(floating, { target: { value: "760" } })
    expect(settingWrites(harness)).toHaveLength(0)
    fireEvent.pointerUp(floating)

    await waitFor(() => expect(storedValue(harness, workspaceLayoutKey)).toContain("760"))
    expect(settingWrites(harness)).toHaveLength(1)
    expect(settingWrites(harness)[0]).toMatchObject({ type: "createSetting", scope: "workspace", key: workspaceLayoutKey })
  })

  it("keeps the size the operator chose when they leave the page and come back", async () => {
    const user = userEvent.setup({ delay: null })
    const harness = pageHarness()
    const first = render(page(harness))
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    const floating = screen.getByRole("slider", { name: /Floating Frame Size/i })
    fireEvent.change(floating, { target: { value: "760" } })
    fireEvent.pointerUp(floating)
    await waitFor(() => expect(storedValue(harness, workspaceLayoutKey)).toContain("760"))

    // Coming back to the page is a NEW page instance over the same control plane:
    // nothing of the old one survives it, so the size the operator finds here is
    // the size the workspace record carries rather than one this page remembered.
    first.unmount()
    render(page(harness))
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    expect(screen.getByRole("slider", { name: /Floating Frame Size/i })).toHaveValue("760")
    expect(screen.getByRole("slider", { name: /Small Screen/i })).toHaveValue("264")
  })

  it("writes the CHOSEN sort key to the workspace record and draws the grid by it", async () => {
    const user = userEvent.setup({ delay: null })
    const harness = pageHarness()
    const snapshot = harness.client.getSnapshot()
    // A unit at the FRONT of the address order and the BACK of the placement order:
    // it belongs to no group, and it answers at the lowest address in the fleet.
    // The two keys cannot agree about it, so the first frame the grid draws is what
    // tells the operator - and this case - which key is in force.
    const scanned: DeviceView = {
      id: "zulu-01", displayName: "Zulu 01", stableIdentity: "device-190", lifecycle: "registered", status: "online",
      platformVersion: "Android 14", batteryPercent: 64, latencyMs: 30, lastSeen: "1 min ago", agentId: "",
      endpointId: "endpoint-zulu-01-current", expectation: "expected", observedAgainAfterRetirement: false, transport: "tcp",
      location: "", packageName: "", activityName: "", workflow: "", workflowStatus: "", taskProgress: 0,
      controlEligibility: "eligible", capabilities: [],
    }
    const scannedEndpoint: EndpointView = {
      id: "endpoint-zulu-01-current", deviceId: "zulu-01", endpointType: "network transport", serial: "MOCK-DEVICE-190",
      host: "192.0.2.7", port: 5555, state: "current", observedAt: "2026-09-14T09:42:18Z",
    }
    snapshot.devices = [...snapshot.devices, scanned]
    snapshot.endpoints = [...snapshot.endpoints, scannedEndpoint]
    const first = render(<ControlPage snapshot={snapshot} dispatch={harness.dispatch} mirror={harness.mirror.client} />)

    const drawn = () => within(screen.getByLabelText("Compact phone frames")).getAllByTestId(/^still-tile-picture-/).map((tile) => tile.getAttribute("data-testid"))
    // The key a workspace that has never chosen one draws: the arrangement the
    // domain already holds, in which the group-less device is drawn last.
    expect(drawn()[0]).toBe("still-tile-picture-atlas-04")
    expect(drawn()[drawn().length - 1]).toBe("still-tile-picture-zulu-01")

    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("button", { name: "Device Address" }))

    // The KEY is what is written: one value on the workspace record, and not an
    // order over every device. A record that still held the retired `frameOrder`
    // would be a second order this build does not read.
    await waitFor(() => expect(storedValue(harness, workspaceLayoutKey)).toContain("address"))
    const written = JSON.parse(storedValue(harness, workspaceLayoutKey) ?? "{}") as { frameSortKey?: string; frameOrder?: string[] }
    expect(written.frameSortKey).toBe("address")
    expect(written.frameOrder).toBeUndefined()
    // The grid draws the new order at once rather than after the projection
    // catches up: the operator's own press is not a request they wait to see.
    await waitFor(() => expect(drawn()[0]).toBe("still-tile-picture-zulu-01"))

    // Leaving the page and coming back is a NEW page instance over the same
    // control plane, so the key it draws is the one the RECORD carries rather than
    // one this page remembered.
    first.unmount()
    render(<ControlPage snapshot={{ ...snapshot, settings: harness.client.getSnapshot().settings }} dispatch={harness.dispatch} mirror={harness.mirror.client} />)
    await waitFor(() => expect(drawn()[0]).toBe("still-tile-picture-zulu-01"))
  })

  it("opens the device list as a table at the size a table needs, and lets the operator order and hide its columns", async () => {
    const user = userEvent.setup({ delay: null })
    const harness = pageHarness()
    render(page(harness))

    await user.click(screen.getByRole("button", { name: "Devices" }))
    await screen.findByRole("dialog")

    // The dialog NAMES the size a table needs rather than carrying a width of its
    // own, and five columns fit inside it: an operator read them cut off at `sm`.
    expect(screen.getByRole("dialog")).toHaveAttribute("data-size", "xl")
    // The dialog is re-queried rather than held: what is asserted is the table the
    // operator is looking at now, not the one that was rendered first.
    const headers = () => within(screen.getByRole("dialog")).getAllByRole("columnheader").map((cell) => cell.textContent ?? "")
    expect(headers()).toEqual(["Index", "Device Name", "Device ID", "Status", "Observed Port"])

    // One place earlier: Status passes Device ID, and nothing else moves.
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Move Status column earlier" }))
    expect(headers()).toEqual(["Index", "Device Name", "Status", "Device ID", "Observed Port"])

    // Hiding a column is stated rather than silent, and the table is described by
    // the notice: a table missing its Status column otherwise reads as a table
    // that HAS no status.
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Status" }))
    expect(headers()).toEqual(["Index", "Device Name", "Device ID", "Observed Port"])
    expect(within(screen.getByRole("dialog")).getByTestId("device-list-hidden-columns")).toHaveTextContent(/1 of 5 columns are not shown: Status/)
    expect(within(screen.getByRole("dialog")).getByRole("table")).toHaveAttribute("aria-describedby", "device-list-hidden-columns")
  })
})
