// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { Toaster } from "@/components/ui/sonner"
import type { ControlPlaneIntent, DeviceView, MutationResult } from "@/lib/domain/control-plane"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { liveMirrorCopy, liveStreamView } from "@/lib/live-mirror"
import { ControlPage } from "./ControlPage"
import { liveTileCopy, liveTileViewerLimit } from "@/lib/live-tiles"

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
  const client: LiveMirrorClient = {
    // The transport the operator chose travels with the request, so a case can
    // assert the choice reached the control plane rather than a default.
    async startStream(request) { calls.push(`start:${request.deviceId}:${request.transport ?? "unspecified"}`); return view },
    async negotiate(_streamId, offerSdp) { calls.push(`negotiate:${offerSdp}`); return { answerSdp: "answer-sdp", stream: view } },
    async stopStream(streamId) { calls.push(`stop:${streamId}`); return { ...view, state: "ended" } },
    async getStream() { return view },
    streamEndpoint(path) { calls.push(`endpoint:${path}`); return { url: `http://control-plane.test${path}`, headers: {} } },
  }
  return { client, calls }
}

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

  it("reports a concise reload summary without dumping every device", async () => {
    const user = userEvent.setup()
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
    const user = userEvent.setup()
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
    const user = userEvent.setup()
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

describe("ControlPage connection filters", () => {
  it("shows all devices by default, filters by observed transport, and updates the count", async () => {
    const user = userEvent.setup()
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
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={async (intent) => client.dispatch(intent)} />)

    await user.click(screen.getByRole("button", { name: "OTG" }))

    expect(screen.getByLabelText("Device connection filters")).toHaveTextContent("3 / 6 devices shown")
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Atlas 07/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Orion 03/i })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Nova 02/i })).not.toBeInTheDocument()
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
    const departedMark = within(departed).getByRole("img", { name: "Nova 05 is offline: observed before, but no current transport is recorded." })
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
    const user = userEvent.setup()
    const { snapshot, dispatch } = harness()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    // 192px is the smallest frame the console offers, and landscape makes it
    // 192x108. The mark is centred in the frame at every size rather than only in
    // the default portrait one.
    await user.click(screen.getByRole("button", { name: "Open Workspace Settings" }))
    await user.click(screen.getByRole("button", { name: "Landscape" }))

    const unseen = screen.getByRole("button", { name: /Atlas 09/i })
    expect(unseen).toHaveStyle({ width: "192px", height: "108px" })
    expect(within(unseen).getByRole("img", { name: /Atlas 09 is not observed/ })).toBeInTheDocument()
    expect(within(unseen).getByText("Not Observed")).toBeInTheDocument()
  })

  it("shows the same observation truth per row in the Device List, with copy that says what a status means", async () => {
    const user = userEvent.setup()
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
    await user.click(await screen.findByRole("tab", { name: "Fleet Device Settings" }))
  }

  it("says in the dialog that this tab changes device state", async () => {
    const user = userEvent.setup()
    const { client, dispatch } = harness()
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: "Settings" }))
    // The old description claimed these controls were local display preferences
    // that never alter device state. That sentence is now false of one of its
    // tabs, so it is gone rather than left in place.
    expect(screen.queryByText(/These controls do not alter device policy, transport, or runtime state/)).not.toBeInTheDocument()
    expect(screen.getByText(/Fleet Device Settings DOES alter device state/i)).toBeInTheDocument()

    await user.click(await screen.findByRole("tab", { name: "Fleet Device Settings" }))
    expect(screen.getByText(/These change the DEVICES, not this view/i)).toBeInTheDocument()
    expect(screen.getByText("Rotation Lock")).toBeInTheDocument()
    expect(screen.getByText("Autofill Off")).toBeInTheDocument()
  })

  it("does not apply anything until the operator confirms, and then reports every device and setting", async () => {
    const user = userEvent.setup()
    // The fleet is seeded WITHOUT one device's current endpoint, and both the page
    // and the client are given that same fleet: the control plane reads the fleet
    // from its own registry, so a device nothing has observed has to be reported
    // as its own refusal rather than folded into a count.
    const seeded = new MockControlPlaneClient().getSnapshot()
    seeded.endpoints = seeded.endpoints.filter((endpoint) => endpoint.deviceId !== "nova-02")
    const client = new MockControlPlaneClient(seeded)
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)

    await openFleetTab(user)

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

    const table = await screen.findByRole("table", { name: /every setting this apply ran/i })
    // One row per device per setting: 6 devices x 2 settings, plus the header.
    expect(within(table).getAllByRole("row")).toHaveLength(13)
    expect(within(table).getAllByText("nova-02")).toHaveLength(2)
    // The outcome cell also carries whether the device was read back, so it is
    // matched by prefix: "Applied · read back off the device" is a confirmed
    // change and "Not applied · no read-back" is a device nothing was read from.
    expect(within(table).getAllByText(/^Not applied/)).toHaveLength(2)
    expect(within(table).getAllByText(/^Applied/)).toHaveLength(10)
    expect(within(table).getAllByText(/no read-back/)).toHaveLength(2)
    // The refused device carries the control plane's own sentence, not a generic
    // failure.
    expect(within(table).getAllByText(/no single current transport endpoint/)).toHaveLength(2)
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
    const user = userEvent.setup()
    const mock = new MockControlPlaneClient()
    const mirror = fakeMirror()
    render(<ControlPage snapshot={mock.getSnapshot()} dispatch={async (intent) => mock.dispatch(intent)} mirror={mirror.client} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    expect(mirror.calls.some((call) => call === "start:atlas-04:webrtc")).toBe(true)
    expect(screen.queryByText("Interactive phone surface")).not.toBeInTheDocument()

    // The stream's own frame is stated, because every coordinate is measured in
    // it - and it is stated behind the info control beside the pin, not over the
    // device's screen, which is the whole of this change.
    const details = await openLiveMirrorDetails(user)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent(/WebRTC \(pion/i)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent("1080x1920")
  })

  it("offers both transports, and opens the device's stream over the one chosen", async () => {
    const user = userEvent.setup()
    const mock = new MockControlPlaneClient()
    const mirror = fakeMirror("live", MirrorTransport.TCP)
    // The TCP transport reads its bytes from the control plane's stream endpoint;
    // what this case asserts is the wiring, so the endpoint answers with an empty
    // body rather than reaching a network.
    const emptyBody = { getReader: () => ({ read: async () => ({ done: true, value: undefined }) }) } as unknown as ReadableStream<Uint8Array>
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
    const user = userEvent.setup()
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
 * drawn in ONE fixed colour rather than its index, and an observed tile carries
 * the device's live picture within a bound the console decides rather than the
 * grid's size. A tile is a viewer: nothing here takes a lease, opens a control
 * session, or dispatches anything.
 */
describe("ControlPage fleet tiles", () => {
  beforeEach(() => {
    vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** mirrorFor answers each device's own stream, and records every subscription. */
  function mirrorFor(): { client: LiveMirrorClient; started: string[] } {
    const started: string[] = []
    const client: LiveMirrorClient = {
      async startStream(request) {
        started.push(request.deviceId)
        return liveStreamView(create(MirrorStreamSchema, { streamId: `stream-${request.deviceId}`, deviceId: request.deviceId, transport: MirrorTransport.WEBRTC, renderWidth: 1080, renderHeight: 1920, state: MirrorStreamState.LIVE, frames: 4n }))
      },
      async negotiate(_streamId, _offerSdp) { return { answerSdp: "answer-sdp", stream: liveStreamView(create(MirrorStreamSchema, { streamId: "stream", deviceId: "atlas-04", renderWidth: 1080, renderHeight: 1920, state: MirrorStreamState.LIVE, frames: 4n })) } },
      async stopStream() { return liveStreamView(create(MirrorStreamSchema, { streamId: "stream", deviceId: "atlas-04", renderWidth: 1080, renderHeight: 1920, state: MirrorStreamState.ENDED })) },
      async getStream(streamId) { return liveStreamView(create(MirrorStreamSchema, { streamId, deviceId: "atlas-04", renderWidth: 1080, renderHeight: 1920, state: MirrorStreamState.LIVE, frames: 4n })) },
      streamEndpoint(path) { return { url: `http://control-plane.test${path}`, headers: {} } },
    }
    return { client, started }
  }

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

  it("carries a live picture on an observed tile, subscribed no further than the console's own bound", async () => {
    const { snapshot, dispatch } = grid()
    const mirror = mirrorFor()
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} mirror={mirror.client} />)

    // The observed tiles carry a picture, and the grid holds five of them while
    // the console carries four: the subscriber set is the console's bound, not
    // the grid's size.
    expect(await screen.findByTestId("live-tile-video-atlas-04")).toBeInTheDocument()
    const pictures = screen.getAllByTestId(/^live-tile-video-/)
    expect(pictures).toHaveLength(liveTileViewerLimit)
    expect(mirror.started.sort()).toEqual(["atlas-04", "atlas-07", "nova-02", "orion-01"])

    // The device with no current observation is not subscribed at all, and spends
    // none of the bound: there is nothing to carry for it.
    expect(mirror.started).not.toContain("nova-05")
    expect(screen.queryByTestId("live-tile-video-nova-05")).not.toBeInTheDocument()
    expect(screen.queryByTestId("live-tile-video-atlas-09")).not.toBeInTheDocument()

    // The tile the bound does not reach says so in the tile, and does not imply
    // it is live: no picture element, and the whole sentence behind the mark.
    const unshown = screen.getByTestId("live-tile-state-orion-03")
    expect(unshown).toHaveAttribute("aria-label", liveTileCopy.unshown(liveTileViewerLimit))
    expect(unshown).toHaveTextContent(liveTileCopy.unshownShort)
    expect(screen.queryByTestId("live-tile-video-orion-03")).not.toBeInTheDocument()
  })

  it("says a tile is not live rather than showing the last frame a failed stream produced", async () => {
    const { snapshot, dispatch } = grid()
    const refusal = "the peer produced no picture within its bound"
    const client: LiveMirrorClient = {
      ...mirrorFor().client,
      async startStream(request) {
        if (request.deviceId === "atlas-04") throw new Error(refusal)
        return liveStreamView(create(MirrorStreamSchema, { streamId: `stream-${request.deviceId}`, deviceId: request.deviceId, transport: MirrorTransport.WEBRTC, renderWidth: 1080, renderHeight: 1920, state: MirrorStreamState.LIVE, frames: 4n }))
      },
    }
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} mirror={client} />)

    const failed = await screen.findByTestId("live-tile-state-atlas-04")
    expect(failed).toHaveAttribute("data-tile-state", "failed")
    // The classification reaches the operator: the plane's own reason, in the
    // tile's own element, and no picture kept from before the failure.
    expect(failed).toHaveAttribute("aria-label", expect.stringContaining(refusal) as unknown as string)
    expect(screen.queryByTestId("live-tile-video-atlas-04")).not.toBeInTheDocument()

    // The other observed tiles are unaffected: one tile's failure is not a reason
    // for the grid to stop carrying every other device.
    expect(await screen.findByTestId("live-tile-video-atlas-07")).toBeInTheDocument()
  })
})
