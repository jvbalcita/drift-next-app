// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { ConnectJsonError } from "@/lib/api/connect-json"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { ControlPlaneIntent, DispatchIntent, DeviceView, ObservationView } from "@/lib/domain/control-plane"
import { deviceStatusMeanings } from "@/lib/device-status"
import { liveMirrorCopy, liveStreamView, scrollStepUnits, type LiveStreamView } from "@/lib/live-mirror"
import { FloatingDevice } from "./ControlPage"

/**
 * The big frame, as `FloatingDevice` composes it.
 *
 * The panel is rendered rather than the frame alone, and deliberately: what this
 * file checks is a property of the COMPOSITION - that the frame's own body holds
 * the picture and the pointer and nothing else, that the details live behind the
 * info control beside the pin, and that the device controls live in the action
 * column. Rendering one of those surfaces on its own would pass while the panel
 * around it put the chrome straight back over the device's screen.
 */
const observationToken = "fresh-atlas-04"
const workspaceId = "workspace-lab-local"

const workspace = { largeHeight: 480, smallHeight: 192, quality: "High", frameRate: 15, orientation: "portrait" } as const
const settings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, priority: "Quality", controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showIp: true, liveMirrorTransport: "webrtc" } as const

function stream(overrides: { state?: MirrorStreamState; failure?: string; frames?: bigint; width?: number; height?: number } = {}): LiveStreamView {
  return liveStreamView(create(MirrorStreamSchema, {
    streamId: "stream-1",
    deviceId: "atlas-04",
    transport: MirrorTransport.WEBRTC,
    renderWidth: overrides.width ?? 1080,
    renderHeight: overrides.height ?? 1920,
    state: overrides.state ?? MirrorStreamState.LIVE,
    frames: overrides.frames ?? 12n,
    failure: overrides.failure ?? "",
  }))
}

function fakeMirror(initial: LiveStreamView = stream()): { client: LiveMirrorClient; calls: string[]; setState(next: LiveStreamView): void } {
  const calls: string[] = []
  let state = initial
  return {
    calls,
    setState(next) { state = next },
    client: {
      async startStream(request) { calls.push(`start:${request.deviceId}`); return state },
      async negotiate(_streamId, offerSdp) { calls.push(`negotiate:${offerSdp}`); return { answerSdp: "answer-sdp", stream: state } },
      async stopStream(streamId) { calls.push(`stop:${streamId}`); return { ...state, state: "ended" } },
      async getStream() { return state },
      streamEndpoint(path) { calls.push(`endpoint:${path}`); return { url: `http://control-plane.test${path}`, headers: {} } },
    },
  }
}

/** stageRect describes the box the video element occupies on screen. */
function stageRect(width: number, height: number, left = 0, top = 0): DOMRect {
  return {
    x: left, y: top, width, height, left, top, right: left + width, bottom: top + height,
    toJSON: () => ({}),
  } as DOMRect
}

function device(): DeviceView {
  const found = new MockControlPlaneClient().getSnapshot().devices[0]
  if (!found) throw new Error("the mock control plane has no device to mirror")
  return found
}

function observation(overrides: Partial<ObservationView> = {}): ObservationView {
  return {
    id: "observation-atlas-04",
    deviceId: "atlas-04",
    capturedAt: "2026-09-18T09:00:00Z",
    source: "device",
    captureStatus: "complete",
    packageName: "",
    activityName: "",
    coordinateSpace: "display:1080x1920",
    freshnessToken: "fresh-read-1",
    artifactCount: 1,
    ...overrides,
  }
}

/**
 * renderPanel supplies the two boxes the pointer mapping reads the way a layout
 * is: the element's own box, and the size of the picture the browser decoded into
 * it. `picture` is the stream's shape unless a test says otherwise, which is the
 * case with no letterbox.
 */
function renderPanel(options: { mirror?: LiveMirrorClient; hasLease?: boolean; token?: string; rect?: DOMRect; picture?: { width: number; height: number }; device?: DeviceView; reply?: (intent: ControlPlaneIntent) => { ok: boolean; message: string }; observationsForDevice?: (deviceId: string) => Promise<readonly ObservationView[]> } = {}) {
  const intents: ControlPlaneIntent[] = []
  const dispatch: DispatchIntent = async (intent) => {
    intents.push(intent)
    return { ok: true, kind: intent.type, message: "The control plane accepted the input.", ...(options.reply?.(intent) ?? {}) }
  }
  const mirror = Object.prototype.hasOwnProperty.call(options, "mirror") ? options.mirror : fakeMirror().client
  render(
    <FloatingDevice
      device={options.device ?? device()}
      followers={[]}
      workspace={{ ...workspace }}
      settings={{ ...settings }}
      position={{ x: 0, y: 0 }}
      pinned={false}
      onPinChange={() => undefined}
      onPointerDown={() => undefined}
      onPointerMove={() => undefined}
      onPointerUp={() => undefined}
      onClose={() => undefined}
      onPreview={() => undefined}
      onAction={() => undefined}
      mirror={mirror}
      mirrorTransport="webrtc"
      workspaceId={workspaceId}
      observationToken={options.token ?? observationToken}
      hasLease={options.hasLease ?? true}
      dispatch={dispatch}
      observationsForDevice={options.observationsForDevice}
    />,
  )
  const frame = screen.getByLabelText(/floating phone frame/i)
  const stage = screen.getByTestId("live-mirror-stage")
  const video = screen.getByTestId("live-mirror-video") as HTMLVideoElement
  const picture = options.picture ?? { width: 1080, height: 1920 }
  video.getBoundingClientRect = () => options.rect ?? stageRect(540, 960)
  Object.defineProperty(video, "videoWidth", { value: picture.width, configurable: true })
  Object.defineProperty(video, "videoHeight", { value: picture.height, configurable: true })
  return { intents, frame, stage, video }
}

/** openDetails opens the info control and returns the details it holds. */
async function openDetails(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByTestId("live-mirror-info"))
  return screen.findByTestId("live-mirror-details")
}

/** live waits for the panel to be up and asserts nothing has been dispatched. */
async function live(intents: ControlPlaneIntent[]) {
  await waitFor(() => expect(screen.getByTestId("live-mirror-info")).toBeInTheDocument())
  expect(intents).toHaveLength(0)
}

let originalMatchMedia: typeof window.matchMedia

/**
 * The browser's WebRTC stack is the one part of this surface a test cannot use,
 * so the global it is reached through is stubbed with the smallest object the
 * default peer factory asks anything of. Nothing here publishes a track: these
 * tests are about what the panel renders and what it sends back.
 */
const livePeers: FakeRTCPeerConnection[] = []

class FakeRTCPeerConnection {
  iceGatheringState = "complete"
  localDescription: { type: string; sdp: string } | null = null
  private tracks: ((event: { streams: MediaStream[] }) => void)[] = []
  constructor() { livePeers.push(this) }
  addTransceiver() { return undefined }
  async createOffer() { return { type: "offer", sdp: "offer-sdp" } }
  async setLocalDescription(description: { type: string; sdp: string }) { this.localDescription = description }
  async setRemoteDescription() { return undefined }
  addEventListener(type: string, listener: (event: { streams: MediaStream[] }) => void) {
    if (type === "track") this.tracks.push(listener)
    return undefined
  }
  removeEventListener() { return undefined }
  close() { return undefined }
  /** publish is the device's first picture arriving on the negotiated track. */
  publish(media: MediaStream) { for (const listener of this.tracks) listener({ streams: [media] }) }
}

beforeEach(() => {
  originalMatchMedia = window.matchMedia
  livePeers.length = 0
  vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
})

afterEach(() => {
  window.matchMedia = originalMatchMedia
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("the big frame is the device's screen", () => {
  it("holds the picture and the pointer and nothing else, while the chrome moves to the panel", async () => {
    const user = userEvent.setup()
    renderPanel()
    await live([])

    // What the frame's own body is: the stage, which holds the video.
    const frame = screen.getByLabelText(/floating phone frame/i)
    expect(within(frame).getAllByTestId(/^live-mirror-/)).toHaveLength(2)
    expect(within(frame).queryAllByRole("button")).toHaveLength(0)

    // Nothing the owner saw over the screen is still in it: the state line, the
    // transport line, the coordinates paragraph, the key blocks, the text field
    // and its button, the stop button and the input warning are all elsewhere.
    expect(within(frame).queryByTestId("live-mirror-transport")).not.toBeInTheDocument()
    expect(within(frame).queryByTestId("live-mirror-phase")).not.toBeInTheDocument()
    expect(within(frame).queryByText(/Coordinates are measured in the frame/)).not.toBeInTheDocument()
    expect(within(frame).queryByTestId("live-mirror-text")).not.toBeInTheDocument()
    expect(within(frame).queryByRole("button", { name: /Send Back key/ })).not.toBeInTheDocument()
    expect(within(frame).queryByRole("button", { name: /Stop mirror/ })).not.toBeInTheDocument()

    // The capabilities did not leave with the copy: the details hold every
    // sentence that was in the frame, and the action column still dispatches.
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent("frame 1080x1920")
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent("12 picture(s) carried")
    expect(within(details).getByTestId("live-mirror-drawn")).toHaveTextContent("540x960")
    expect(within(details).getByText(liveMirrorCopy.details.pointer)).toBeInTheDocument()
  })

  it("takes the stream's own aspect, so the frame is the screen rather than a box around it", async () => {
    const { frame } = renderPanel({ mirror: fakeMirror(stream({ width: 1080, height: 2280 })).client })
    // The panel sizes the frame from the stream it is carrying: 480px of
    // largeHeight gives a 270px-wide request, and the frame's height follows the
    // 1080x2280 stream rather than this console's own 9:16.
    await waitFor(() => expect(frame).toHaveStyle({ width: "270px" }))
    expect(frame).toHaveStyle({ height: `${Math.round(270 * (2280 / 1080))}px` })
  })

  it("sends a tap in the stream's own frame, never in the element's pixels", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 1, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 1, clientX: 270, clientY: 480 })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({
      type: "submitDeviceTap",
      deviceId: "atlas-04",
      x: 540,
      y: 960,
      renderWidth: 1080,
      renderHeight: 1920,
      observationToken,
    })
  })

  it("maps a tap through the picture's drawn box, not through the element's", async () => {
    // The measured defect, at the sizes it was measured at: this console's 9:16
    // frame drawing a 19:9 stream, with a pillarbox on each side. Mapping the
    // element's box read the picture's own left edge as x=107. The fit is
    // written out here rather than asked of drawnContentRect, so the fixture
    // cannot move with the mapping it is checking.
    const rect = stageRect(314, 531)
    const picture = { width: 1080, height: 2280 }
    const pictureLeft = (rect.width - picture.width * (rect.height / picture.height)) / 2
    const { intents, stage } = renderPanel({ rect, picture, mirror: fakeMirror(stream({ width: picture.width, height: picture.height })).client })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 5, clientX: pictureLeft, clientY: rect.top })
    fireEvent.pointerUp(stage, { pointerId: 5, clientX: pictureLeft, clientY: rect.top })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceTap", deviceId: "atlas-04", x: 0, y: 0, renderWidth: 1080, renderHeight: 2280 })
  })

  it("refuses a tap in the pillarbox, names it in the details, and reaches no device", async () => {
    const user = userEvent.setup()
    const rect = stageRect(314, 531)
    const picture = { width: 1080, height: 2280 }
    const { intents, stage } = renderPanel({ rect, picture, mirror: fakeMirror(stream({ width: 1080, height: 2280 })).client })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 6, clientX: 5, clientY: 265 })
    fireEvent.pointerUp(stage, { pointerId: 6, clientX: 5, clientY: 265 })

    expect(intents).toHaveLength(0)
    // The refusal is not silent: the info control marks itself, and the sentence
    // is one click away rather than drawn over the device's screen.
    expect(screen.getByTestId("live-mirror-info-mark")).toBeInTheDocument()
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.refusal.outsideFrame)
  })

  it("refuses a tap before the browser has drawn a picture, and says which fact is missing", async () => {
    const user = userEvent.setup()
    const { intents, stage } = renderPanel({ picture: { width: 0, height: 0 } })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 7, clientX: 200, clientY: 200 })
    fireEvent.pointerUp(stage, { pointerId: 7, clientX: 200, clientY: 200 })

    expect(intents).toHaveLength(0)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.refusal.noPicture)
  })

  it("compresses a drag into exactly one swipe, at the stream's scale and with no tap", async () => {
    const { intents, stage } = renderPanel({ rect: stageRect(270, 480, 20, 40) })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 2, clientX: 20, clientY: 40 })
    for (const step of [40, 80, 120, 160, 200, 240]) {
      fireEvent.pointerMove(stage, { pointerId: 2, clientX: 20 + step / 2, clientY: 40 + step / 2 })
    }
    fireEvent.pointerUp(stage, { pointerId: 2, clientX: 140, clientY: 160 })

    await waitFor(() => expect(intents).toHaveLength(1))
    const swipe = intents[0]
    expect(swipe.type).toBe("submitDeviceSwipe")
    if (swipe.type !== "submitDeviceSwipe") throw new Error("expected one swipe")
    // The press maps to the frame's origin and the 120x120 element-space drag maps
    // to 480x480 of the encoded frame: the report is the frame's, not the element's.
    expect(swipe).toMatchObject({ startX: 0, startY: 0, endX: 480, endY: 480, renderWidth: 1080, renderHeight: 1920 })
    expect(swipe.durationMs).toBeGreaterThanOrEqual(16)
    expect(intents.some((intent) => intent.type === "submitDeviceTap")).toBe(false)
  })
})

describe("the frame controls the device with the mouse", () => {
  /**
   * The device input contract has no scroll kind - tap, swipe, typed text, key
   * event and app launch are what a device can be told - so a wheel is dispatched
   * as the swipe a finger would make, in the frame the stream is encoded at,
   * through the same kernel path as every other gesture.
   */
  it("turns a wheel turn into one scroll gesture in the stream's frame", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    // A 120px turn in a 540x960 drawn box is 240 frame units: one whole step of
    // this frame (1920/12 = 160 units) with a remainder carried to the next turn.
    fireEvent.wheel(stage, { deltaY: 120, deltaX: 0, clientX: 270, clientY: 480 })

    await waitFor(() => expect(intents).toHaveLength(1))
    const scroll = intents[0]
    expect(scroll.type).toBe("submitDeviceSwipe")
    if (scroll.type !== "submitDeviceSwipe") throw new Error("expected one scroll swipe")
    const step = scrollStepUnits({ width: 1080, height: 1920 }, "y")
    // The point maps to the frame's middle, and the gesture moves against the
    // scroll (a wheel turned down scrolls the content up, as a finger drag up
    // does) by exactly one step of the frame.
    expect(scroll).toMatchObject({ startX: 540, startY: 960, endX: 540, endY: 960 - step, renderWidth: 1080, renderHeight: 1920 })
    expect(scroll.durationMs).toBeGreaterThanOrEqual(16)
  })

  it("carries a partial wheel turn to the next one instead of dispatching it", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    // A 60px turn in a 960px-tall drawn box is 120 frame units: under one step of
    // this frame, so nothing is sent for it.
    fireEvent.wheel(stage, { deltaY: 60, deltaX: 0, clientX: 270, clientY: 480 })
    expect(intents).toHaveLength(0)

    // The next turn takes the accumulation past a whole step, and that one
    // gesture is what the device sees - not one action per browser event.
    fireEvent.wheel(stage, { deltaY: 60, deltaX: 0, clientX: 270, clientY: 480 })
    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0].type).toBe("submitDeviceSwipe")
  })

  it("refuses a wheel turn in the pillarbox rather than moving the device", async () => {
    const user = userEvent.setup()
    const rect = stageRect(314, 531)
    const picture = { width: 1080, height: 2280 }
    const { intents, stage } = renderPanel({ rect, picture, mirror: fakeMirror(stream({ width: 1080, height: 2280 })).client })
    await live(intents)

    fireEvent.wheel(stage, { deltaY: 120, deltaX: 0, clientX: 2, clientY: 265 })

    expect(intents).toHaveLength(0)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.refusal.outsideFrame)
  })
})

describe("the observation a frame's input is dispatched against", () => {
  /**
   * The reported defect: a live frame refused every click with "input needs the
   * observation the coordinates are measured from, and this console has none for
   * this device", because the projection is one page of a WORKSPACE's
   * observations and this device's own were not in it.
   */
  it("reads the device's own observation when the projection holds none, and dispatches with it", async () => {
    const reads: string[] = []
    const observationsForDevice = async (deviceId: string) => { reads.push(deviceId); return [observation()] }
    const { intents, stage } = renderPanel({ token: "", observationsForDevice })
    await live(intents)
    await waitFor(() => expect(reads).toEqual(["atlas-04"]))

    fireEvent.pointerDown(stage, { pointerId: 9, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 9, clientX: 270, clientY: 480 })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceTap", observationToken: "fresh-read-1" })
  })

  it("refuses the input while it is reading, naming what it is doing, rather than claiming the device has none", async () => {
    let resolveRead: ((value: readonly ObservationView[]) => void) | undefined
    const observationsForDevice = () => new Promise<readonly ObservationView[]>((resolve) => { resolveRead = resolve })
    const { intents, stage } = renderPanel({ token: "", observationsForDevice })
    await live(intents)

    const user = userEvent.setup()
    const details = await openDetails(user)
    expect(await within(details).findByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.readingObservation)

    fireEvent.pointerDown(stage, { pointerId: 10, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 10, clientX: 270, clientY: 480 })
    expect(intents).toHaveLength(0)

    resolveRead?.([observation()])
  })

  it("keeps the named refusal when the device genuinely has no observation", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ token: "", observationsForDevice: async () => [] })
    await live(intents)

    const details = await openDetails(user)
    await waitFor(() => expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noObservation))
    expect(intents).toHaveLength(0)
  })

  it("prefers the observation the projection names over reading the device's own", async () => {
    const reads: string[] = []
    const { intents, stage } = renderPanel({ observationsForDevice: async (deviceId: string) => { reads.push(deviceId); return [observation({ freshnessToken: "fresh-read-2" })] } })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 11, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 11, clientX: 270, clientY: 480 })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ observationToken })
    expect(reads).toEqual([])
  })
})

describe("the info control beside the pin", () => {
  it("names what it opens in a tooltip, and holds the state, transport, frame and drawn box", async () => {
    const user = userEvent.setup()
    renderPanel()
    await live([])

    // The tooltip names the control's subject: it is not a second copy of the
    // details, and a screen reader reads the same name.
    const info = screen.getByTestId("live-mirror-info")
    expect(info).toHaveAttribute("aria-label", liveMirrorCopy.details.label)
    await user.hover(info)
    expect(await screen.findByText(liveMirrorCopy.details.tooltip)).toBeInTheDocument()
    await user.unhover(info)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/^Live\./)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent(liveMirrorCopy.transport.webrtc)
    expect(within(details).getByTestId("live-mirror-transport")).toHaveTextContent("frame 1080x1920")
    expect(within(details).getByTestId("live-mirror-frame")).toHaveTextContent("1080x1920")
    expect(within(details).getByTestId("live-mirror-drawn")).toHaveTextContent("A point is measured through this box")
  })

  it("keeps the device controls on the panel's action column, reachable without a mouse", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    const inputs = screen.getByTestId("live-mirror-inputs")
    const back = within(inputs).getByRole("button", { name: "Send Back key" })
    expect(back).toBeEnabled()
    back.focus()
    expect(back).toHaveFocus()
    await user.keyboard("{Enter}")

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceKeyEvent", deviceId: "atlas-04", keyCode: 4 })
  })

  it("refuses to describe an input it has no lease, observation or frame for", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ hasLease: false })
    await live(intents)

    expect(within(screen.getByTestId("live-mirror-inputs")).getByRole("button", { name: "Send Back key" })).toBeDisabled()
    fireEvent.pointerDown(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    fireEvent.pointerUp(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    expect(intents).toHaveLength(0)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noLease)
  })

  it("names the observation it is missing rather than inventing one", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ token: "" })
    await live(intents)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noObservation)
    expect(intents).toHaveLength(0)
  })

  it("surfaces the control plane's refusal instead of reporting a delivered input", async () => {
    const { intents } = renderPanel({ reply: () => ({ ok: false, message: "The control plane refused that input: the lease expired." }) })
    const stage = screen.getByTestId("live-mirror-stage")
    const video = screen.getByTestId("live-mirror-video") as HTMLVideoElement
    video.getBoundingClientRect = () => stageRect(540, 960)
    Object.defineProperty(video, "videoWidth", { value: 1080, configurable: true })
    Object.defineProperty(video, "videoHeight", { value: 1920, configurable: true })
    await waitFor(() => expect(stage).toHaveClass("cursor-crosshair"))

    fireEvent.pointerDown(stage, { pointerId: 4, clientX: 100, clientY: 100 })
    fireEvent.pointerUp(stage, { pointerId: 4, clientX: 100, clientY: 100 })

    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent(/lease expired/i))
    expect(intents).toHaveLength(1)
  })

  it("renders a failed stream with its reason in the details and a way to reopen it, not a live mark", async () => {
    const user = userEvent.setup()
    const failure = "the peer produced no picture within its bound"
    const { intents } = renderPanel({ mirror: fakeMirror(stream({ state: MirrorStreamState.FAILED, failure, frames: 0n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-failure")).toHaveTextContent(failure)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/failed/i)
    expect(within(details).getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
    expect(intents).toHaveLength(0)
  })

  it("says a stream ended rather than leaving its last frame looking current", async () => {
    const user = userEvent.setup()
    renderPanel({ mirror: fakeMirror(stream({ state: MirrorStreamState.ENDED, frames: 40n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())
    // No peer is opened for a stream that is already over: there is no picture to
    // negotiate for, and opening one would be a capture for nobody.
    expect(livePeers).toHaveLength(0)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/stream ended/i)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/40 picture\(s\) were carried/i)
  })

  it("names the missing control plane when this console has no live mirror at all", async () => {
    renderPanel({ mirror: undefined })
    // The sentence is the frame's own overlay, which is the point: a frame with
    // nothing to carry says so where the picture would be.
    expect(await screen.findByText(liveMirrorCopy.phase.unavailable)).toBeInTheDocument()
  })

  /**
   * The owner's screen, reproduced end to end: the control plane refuses to carry
   * a stream for a device nobody has observed, and the console rendered
   * "Transport: a transport this console does not recognise". The device has no
   * observation, so there is no observation token either - the same cause showing
   * a second time on the input line - and the fix is to observe the device, not
   * to find a transport this console supports.
   */
  it("reads a device with no current observation as an observation missing, not as a transport it does not support", async () => {
    const user = userEvent.setup()
    const refusal = "device has no current transport endpoint"
    const mirror: LiveMirrorClient = {
      ...fakeMirror().client,
      async startStream() { throw new ConnectJsonError("failed_precondition", refusal) },
    }
    const unobserved: DeviceView = { ...device(), status: "unobserved", transport: "unspecified" }
    const { intents } = renderPanel({ mirror, device: unobserved, token: "" })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    const details = await openDetails(user)
    const transport = within(details).getByTestId("live-mirror-transport")
    expect(transport).not.toHaveTextContent(/does not recognise/)
    expect(transport).toHaveTextContent(deviceStatusMeanings.unobserved)
    expect(transport).toHaveTextContent("scan")

    // The refusal names the device and the plane's own answer, and keeps both.
    const failure = within(details).getByTestId("live-mirror-failure")
    expect(failure).toHaveTextContent(unobserved.displayName)
    expect(failure).toHaveTextContent(refusal)
    expect(failure).toHaveTextContent("scan")

    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noObservation)
    // The refusal is not weakened: no picture is claimed and no device was reached.
    expect(screen.getByTestId("live-mirror-video")).toHaveProperty("srcObject", null)
    expect(transport).not.toHaveTextContent(/frame \d/)
    expect(intents).toHaveLength(0)
  })

  it("does not pulse the live mark when the operator asked for reduced motion", async () => {
    window.matchMedia = ((query: string) => ({
      matches: query.includes("prefers-reduced-motion"),
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia

    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
  })

  it("drops the picture and covers the frame when a live stream ends, so its last frame cannot look current", async () => {
    const mirror = fakeMirror(stream({ state: MirrorStreamState.LIVE, frames: 20n }))
    const { intents } = renderPanel({ mirror: mirror.client })
    await live(intents)

    const media = {} as MediaStream
    expect(livePeers).toHaveLength(1)
    livePeers[0]!.publish(media)
    const video = document.querySelector("video")
    await waitFor(() => expect(video).toHaveProperty("srcObject", media))

    mirror.setState(stream({ state: MirrorStreamState.ENDED, frames: 20n }))
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument(), { timeout: 4_000 })
    expect(video).toHaveProperty("srcObject", null)
    expect(document.querySelector("video")).toBe(video)
    // The overlay is opaque over it, so the last frame cannot be read as the
    // device's screen now.
    expect(document.querySelector("video")?.parentElement?.textContent).toContain("The stream ended")
  })

  it("shows no live mark while a stream is connected but has carried no picture", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ mirror: fakeMirror(stream({ state: MirrorStreamState.STARTING, frames: 0n })).client })
    await live(intents)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(liveMirrorCopy.phase.starting)
    expect(within(details).getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
  })
})

describe("typing into the device from the panel's action column", () => {
  it("types what the operator entered into the device, and clears it once it was typed", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    const field = screen.getByTestId("live-mirror-text")
    await user.type(field, "hello device")
    await user.click(screen.getByTestId("live-mirror-text-send"))

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceText", deviceId: device().id, text: "hello device" })
    // The console keeps no copy of a value it typed: the field is empty again,
    // and nothing it renders carries the text back.
    await waitFor(() => expect(field).toHaveValue(""))
    expect(screen.getByTestId("live-mirror-notice")).not.toHaveTextContent("hello device")
  })

  it("keeps what the operator typed when the control plane refused it, and says so", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ reply: () => ({ ok: false, message: "The device lease has expired." }) })
    await live(intents)

    const field = screen.getByTestId("live-mirror-text")
    await user.type(field, "hello device")
    await user.click(screen.getByTestId("live-mirror-text-send"))

    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent("The device lease has expired."))
    expect(field).toHaveValue("hello device")
    expect(intents).toHaveLength(1)
  })

  it("refuses an empty entry rather than dispatching it", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    await user.click(screen.getByTestId("live-mirror-text-send"))

    expect(intents).toHaveLength(0)
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.text.empty)
  })

  it("offers no way to type into a device it holds no lease for, and says why", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ hasLease: false })
    await live(intents)

    expect(screen.getByTestId("live-mirror-text")).toBeDisabled()
    expect(screen.getByTestId("live-mirror-text-send")).toBeDisabled()
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-text-blocked")).toHaveTextContent(liveMirrorCopy.text.noLease)
  })

  it("stops offering typed text when the stream it would travel is over, and says why", async () => {
    const user = userEvent.setup()
    const mirror = fakeMirror(stream({ state: MirrorStreamState.ENDED, frames: 20n }))
    const { intents } = renderPanel({ mirror: mirror.client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    expect(screen.getByTestId("live-mirror-text")).toBeDisabled()
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-text-blocked")).toHaveTextContent(liveMirrorCopy.text.noStream)
    expect(intents).toHaveLength(0)
  })
})
