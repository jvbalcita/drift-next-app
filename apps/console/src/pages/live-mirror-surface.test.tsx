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
import type { ControlPlaneIntent, DispatchIntent, DeviceView } from "@/lib/domain/control-plane"
import { deviceStatusMeanings } from "@/lib/device-status"
import { keyRepeatIntervalMs, liveMirrorCopy, liveStreamView, scrollStepUnits, type LiveStreamView } from "@/lib/live-mirror"
import { FloatingDevice } from "./ControlPage"
import { LiveMirrorInfo, LiveMirrorSurface, type LiveMirrorSessionView } from "./live-mirror-surface"
import { planeCapacity } from "@/test/mirror-fixtures"

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
/**
 * The observation a coordinate is bound to is the frame's own live stream, so
 * what a dispatched input names is the stream the picture came from - the fake
 * mirror below answers every start with this stream identity.
 */
const streamToken = "stream-1"
const workspaceId = "workspace-lab-local"

const workspace = { largeHeight: 480, smallHeight: 192, quality: "High", frameRate: 15, orientation: "portrait" } as const
const settings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showIp: true, liveMirrorTransport: "webrtc" } as const

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
      async startStream(request) { calls.push(`start:${request.deviceId}:${request.purpose}`); return state },
      async getCapacity() { return planeCapacity(4, 1) },
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

/**
 * renderPanel supplies the two boxes the pointer mapping reads the way a layout
 * is: the element's own box, and the size of the picture the browser decoded into
 * it. `picture` is the stream's shape unless a test says otherwise, which is the
 * case with no letterbox.
 */
function renderPanel(options: { mirror?: LiveMirrorClient; hasLease?: boolean; leaseRefusal?: string; rect?: DOMRect; picture?: { width: number; height: number }; device?: DeviceView; devices?: readonly DeviceView[]; reply?: (intent: ControlPlaneIntent) => { ok: boolean; message: string } } = {}) {
  const intents: ControlPlaneIntent[] = []
  const dispatch: DispatchIntent = async (intent) => {
    intents.push(intent)
    return { ok: true, kind: intent.type, message: "The control plane accepted the input.", ...(options.reply?.(intent) ?? {}) }
  }
  const mirror = Object.prototype.hasOwnProperty.call(options, "mirror") ? options.mirror : fakeMirror().client
  render(
    <FloatingDevice
      device={options.device ?? device()}
      artifacts={[]} devices={options.devices ?? []}
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
      onCapture={() => undefined}
      onChangeDevice={() => undefined}
      mirror={mirror}
      mirrorTransport="webrtc"
      workspaceId={workspaceId}
      leaseRefusal={options.leaseRefusal}
      hasLease={options.hasLease ?? true}
      dispatch={dispatch}
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

/**
 * keystroke presses one key on the operator's own keyboard, at a stated moment.
 *
 * `timeStamp` is set on the event rather than left to the clock, because what one
 * of these tests checks is a RATE: a test that read the machine's own clock would
 * be measuring the host instead of the console.
 */
function keystroke(target: Element, init: { key: string; repeat?: boolean; shiftKey?: boolean; ctrlKey?: boolean; altKey?: boolean; metaKey?: boolean; atMs?: number }) {
  const event = new KeyboardEvent("keydown", {
    key: init.key,
    repeat: init.repeat ?? false,
    shiftKey: init.shiftKey ?? false,
    ctrlKey: init.ctrlKey ?? false,
    altKey: init.altKey ?? false,
    metaKey: init.metaKey ?? false,
    bubbles: true,
    cancelable: true,
  })
  Object.defineProperty(event, "timeStamp", { value: init.atMs ?? 0 })
  fireEvent(target, event)
}

/** keyEvents is the key events among the intents the panel sent. */
function keyEvents(intents: ControlPlaneIntent[]) {
  return intents.filter((intent) => intent.type === "submitDeviceKeyEvent")
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

  it("draws nothing over the picture while a pointer is down", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 7, clientX: 270, clientY: 480 })

    // Mid-gesture the frame holds the picture and nothing else: the stage's own
    // children are the video element, and no element is drawn across the top of
    // the device's screen. The line that used to be drawn there was the defect -
    // "when tapping or swiping, there's a separator or horizontal line showing on
    // top" - and it was the only thing `dragging` rendered, so the state that
    // fed it is gone with it rather than left unread.
    expect(within(stage).getByTestId("live-mirror-video")).toBeInTheDocument()
    expect(stage.querySelectorAll("span")).toHaveLength(0)
    expect(stage.children).toHaveLength(1)

    fireEvent.pointerUp(stage, { pointerId: 7, clientX: 270, clientY: 480 })
    expect(stage.querySelectorAll("span")).toHaveLength(0)
    expect(stage.children).toHaveLength(1)
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
      observationToken: streamToken,
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
    // does) by exactly one step of the frame. The observation is asserted here
    // with the frame, the endpoints and the duration, because the wheel is the
    // one gesture that reaches the device by a route of its own: a scroll that
    // named a different observation from the picture it was measured in is the
    // divergence this assertion exists to catch.
    expect(scroll).toMatchObject({ startX: 540, startY: 960, endX: 540, endY: 960 - step, renderWidth: 1080, renderHeight: 1920, observationToken: streamToken })
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

describe("the observation a frame's input is bound to", () => {
  /**
   * The reported defect: a live frame refused every click for want of an
   * OBSERVATION, and the observation it wanted was a capture the plane had never
   * recorded. What the operator is actually pointing at is the picture this
   * stream carried, in the frame this stream is encoded at, so the stream is the
   * observation the coordinate belongs to - and the kernel still refuses a
   * declared frame the device does not present at.
   */
  it("binds a tap to the frame's own live stream and dispatches it with that", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 9, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 9, clientX: 270, clientY: 480 })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceTap", observationToken: streamToken })
  })

  it("binds a swipe to the same stream the picture it was measured in came from", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 12, clientX: 100, clientY: 100 })
    fireEvent.pointerMove(stage, { pointerId: 12, clientX: 200, clientY: 300 })
    fireEvent.pointerUp(stage, { pointerId: 12, clientX: 200, clientY: 300 })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceSwipe", observationToken: streamToken })
  })

  it("names the observation a coordinate is measured from, in the frame's own details", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-observation")).toHaveTextContent(streamToken)
    expect(within(details).getByTestId("live-mirror-observation")).toHaveTextContent(liveMirrorCopy.details.observationPresent(streamToken))
  })

  it("refuses a coordinate when the frame has no live stream to measure it in, and says so", async () => {
    const user = userEvent.setup()
    // A stream this console cannot open: there is no picture, and so no frame and
    // no observation either - the input is refused rather than bound to anything.
    const mirror: LiveMirrorClient = {
      ...fakeMirror().client,
      async startStream() { throw new ConnectJsonError("failed_precondition", "device has no current transport endpoint") },
    }
    const { intents, stage } = renderPanel({ mirror })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    fireEvent.pointerDown(stage, { pointerId: 13, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 13, clientX: 270, clientY: 480 })
    expect(intents).toHaveLength(0)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noObservation)
    expect(within(details).getByTestId("live-mirror-observation")).toHaveTextContent(liveMirrorCopy.details.observationAbsent)
  })

  it("refuses a coordinate rather than binding it to a frame the stream has not reported", async () => {
    const user = userEvent.setup()
    const { intents, stage } = renderPanel({ mirror: fakeMirror(stream({ width: 0, height: 0 })).client })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 14, clientX: 270, clientY: 480 })
    fireEvent.pointerUp(stage, { pointerId: 14, clientX: 270, clientY: 480 })
    expect(intents).toHaveLength(0)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noFrame)
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

  it("draws the info tooltip above the floating device it belongs to", async () => {
    const user = userEvent.setup({ delay: null })
    renderPanel()
    await live([])

    // The measured defect behind "the tooltip is showing below the frame": the
    // popup is portaled to the document, so it leaves the floating device's
    // stacking context, and at the console's standard overlay layer it was
    // painted UNDERNEATH the frame it describes - in Chromium, at the frame's own
    // header, every point inside the tooltip's box hit the frame instead, so the
    // sentence was drawn where nobody could read it. jsdom computes no paint
    // order, so what is pinned here is the two layers the surfaces declare and
    // that the tooltip's outranks the frame's; the paint was measured in a real
    // browser.
    const info = screen.getByTestId("live-mirror-info")
    await user.hover(info)
    const popup = await screen.findByText(liveMirrorCopy.details.tooltip)
    const positioner = popup.parentElement as HTMLElement
    const widget = screen.getByLabelText(/floating device controls/i).parentElement as HTMLElement
    const layerOf = (element: HTMLElement) => {
      const match = (element.className || "").match(/(?:^|\s)z-\[(\d+)\]/)
      expect(match ? match[1] : null).not.toBeNull()
      return Number(match?.[1])
    }

    expect(layerOf(positioner)).toBeGreaterThan(layerOf(widget))
    // And it is not a descendant of the frame: being portaled away from the
    // frame's own stacking context is exactly why its layer has to be its own.
    expect(widget.contains(popup)).toBe(false)
  })

  it("draws the device's own navigation bar in the footer, reachable without a mouse", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    // Three controls, as the device's own bottom bar has three - menu/recents,
    // home, back - and each dispatches the device's own key code through the
    // kernel rather than a console-invented action.
    const keys = screen.getByTestId("live-mirror-device-keys")
    expect(within(keys).getByRole("group", { name: liveMirrorCopy.navigationKeys.label })).toBeInTheDocument()
    const back = within(keys).getByRole("button", { name: "Back" })
    expect(within(keys).getByRole("button", { name: "Home" })).toBeEnabled()
    expect(within(keys).getByRole("button", { name: "Menu (recent apps)" })).toBeEnabled()
    expect(back).toBeEnabled()
    back.focus()
    expect(back).toHaveFocus()
    await user.keyboard("{Enter}")

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceKeyEvent", deviceId: "atlas-04", keyCode: 4, observationToken: streamToken })
  })

  it("sends the app switcher and home from the same row, as the device's own bar does", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel()
    await live(intents)

    const keys = screen.getByTestId("live-mirror-device-keys")
    await user.click(within(keys).getByRole("button", { name: "Menu (recent apps)" }))
    await user.click(within(keys).getByRole("button", { name: "Home" }))

    await waitFor(() => expect(intents).toHaveLength(2))
    expect(intents[0]).toMatchObject({ type: "submitDeviceKeyEvent", keyCode: 187, observationToken: streamToken })
    expect(intents[1]).toMatchObject({ type: "submitDeviceKeyEvent", keyCode: 3, observationToken: streamToken })
  })

  /**
   * The owner's instruction: the menu row and the follower count are inverted.
   * The row belongs where the phone puts its own navigation - the bottom of the
   * panel - and the count reads above it. Asserted as ORDER rather than as
   * presence, because both elements were already present and it is their order
   * that was wrong; the row is also asserted to be the panel's last element, so
   * nothing the panel draws can end up under the device's own navigation bar.
   */
  it("reads the follower count above the nav row, which is the panel's last element", async () => {
    const { intents } = renderPanel()
    await live(intents)

    const followers = screen.getByTestId("live-mirror-followers")
    const keys = screen.getByTestId("live-mirror-device-keys")
    const footer = followers.parentElement as HTMLElement

    expect(followers.compareDocumentPosition(keys) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(footer.lastElementChild).toBe(keys)
    expect(screen.getByLabelText(/floating device controls/i).lastElementChild).toBe(footer)
  })

  it("refuses to describe an input it has no lease for, and names the missing lease", async () => {
    const user = userEvent.setup()
    const { intents } = renderPanel({ hasLease: false })
    await live(intents)

    expect(within(screen.getByTestId("live-mirror-device-keys")).getByRole("button", { name: "Back" })).toBeDisabled()
    fireEvent.pointerDown(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    fireEvent.pointerUp(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    expect(intents).toHaveLength(0)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noLease)
  })

  it("reads the plane's own refusal of the lease at the frame, not only as a not-allowed pointer", async () => {
    const user = userEvent.setup()
    const refusal = "This device is already under another operator's control."
    const { intents } = renderPanel({ hasLease: false, leaseRefusal: refusal })
    await live(intents)

    // The control marks itself, so a refusal is never behind a control an
    // operator has no reason to open.
    expect(screen.getByTestId("live-mirror-info-mark")).toBeInTheDocument()
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-lease-refusal")).toHaveTextContent(refusal)
    expect(within(details).getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noLease)
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

  it("shows the PLANE's own capacity refusal on the frame, not a generic failure", async () => {
    // The card's scene: the grid is showing its tiles, the operator opens a fifth
    // device's frame, and the plane refuses the stream for its capacity. What the
    // frame has to carry is the plane's own sentence - a frame that said only "The
    // stream failed" would leave the operator closing tiles that are not the reason.
    const user = userEvent.setup()
    const refusal = "media: 4 devices are already being mirrored, which is the configured device session capacity"
    const mirror = { ...fakeMirror().client, async startStream(): Promise<LiveStreamView> { throw new Error(refusal) } }
    renderPanel({ mirror })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-failure")).toHaveTextContent("device session capacity")
    expect(within(details).getByTestId("live-mirror-failure")).toHaveTextContent("4 devices are already being mirrored")
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(/failed/i)
    // And the frame's own body carries it too. The details panel is behind the
    // info control and the frame is what the operator is looking at: a body that
    // substituted "The stream failed." would put the plane's sentence one click
    // away from the picture it explains.
    expect(screen.getByTestId("live-mirror-overlay")).toHaveTextContent(refusal)
    expect(screen.getByTestId("live-mirror-overlay")).not.toHaveTextContent(liveMirrorCopy.phase.failed)
  })

  it("keeps the plane's own sentence in the frame for a stream the plane reported as failed", async () => {
    // The other path to a failed frame: the stream was live and the plane then
    // reported it failed, with the reason it classified. The frame renders that
    // sentence rather than the console's own copy, because the console has no
    // idea why the device's stream stopped and the plane does.
    const failure = "media: the stream from atlas-04 ended: scrcpy: the device-side server exited"
    renderPanel({ mirror: fakeMirror(stream({ state: MirrorStreamState.FAILED, failure })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-retry")).toBeInTheDocument())

    expect(screen.getByTestId("live-mirror-overlay")).toHaveTextContent(failure)
    expect(screen.getByTestId("live-mirror-overlay")).not.toHaveTextContent(liveMirrorCopy.phase.failed)
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
    const { intents } = renderPanel({ mirror, device: unobserved })
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

describe("the operator's own keyboard types into the device", () => {
  it("sends the key the operator pressed while the frame holds focus, as one key event through the kernel", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents, stage } = renderPanel()
    await live(intents)

    // Clicking into the picture gives the frame the operator's keyboard: the
    // press focuses the frame, which is the capture boundary.
    await user.click(stage)
    expect(stage).toHaveFocus()

    keystroke(stage, { key: "Enter" })

    await waitFor(() => expect(keyEvents(intents)).toHaveLength(1))
    expect(keyEvents(intents)[0]).toMatchObject({ type: "submitDeviceKeyEvent", deviceId: "atlas-04", keyCode: 66, confirmed: true, observationToken: streamToken })
  })

  /**
   * The measured defect, and the assertion that would have caught it: the key
   * event the console built named NO observation, and the kernel's catalog
   * declares a key event as requiring one - so every keystroke and every press
   * of the device's own navigation keys came back as the single sentence
   * "device input intent is invalid", with nothing in it to act on.
   *
   * Both paths a key event takes are checked here, because both were refused:
   * the operator's own keyboard, and the device's own navigation bar. What they
   * must name is the frame's own live stream, the same observation a coordinate
   * in that frame is measured from - the picture the operator is typing into.
   */
  it("names the frame's own observation on every key event, from the keyboard and from the nav row", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents, stage } = renderPanel()
    await live(intents)

    stage.focus()
    keystroke(stage, { key: "Enter" })
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(1))
    expect(keyEvents(intents)[0]).toMatchObject({ observationToken: streamToken })
    expect(keyEvents(intents)[0]).not.toHaveProperty("observationToken", "")

    await user.click(within(screen.getByTestId("live-mirror-device-keys")).getByRole("button", { name: "Menu (recent apps)" }))
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(2))

    for (const event of keyEvents(intents)) {
      expect(event).toMatchObject({ observationToken: streamToken })
    }
  })

  it("reaches no device while the frame does not hold focus, and is not captured from elsewhere", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)

    // Nothing holds the frame's focus: the console has one focus, so a keystroke
    // typed at the console belongs to whatever control has it.
    keystroke(document.body, { key: "Enter" })
    keystroke(stage, { key: "Enter" })
    expect(intents).toHaveLength(0)

    // And it is not captured from elsewhere while the frame DOES hold focus: the
    // listener is the frame's own, so a keystroke outside it is not the frame's.
    stage.focus()
    expect(stage).toHaveFocus()
    keystroke(document.body, { key: "Enter" })
    expect(intents).toHaveLength(0)

    keystroke(stage, { key: "Enter" })
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(1))
  })

  /**
   * The owner's instruction: the keyboard-capture block leaves the panel. The
   * behaviour stays, and so does the fact - an operator can still read whether
   * this frame holds their keyboard, and that the frame can be left from the
   * keyboard alone - but it is stated ONCE, in the info control, and the panel
   * carries no block, no paragraph and no release control for it.
   */
  it("states the keyboard once, in the info control, and carries no capture block in the panel", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents } = renderPanel()
    await live(intents)

    // Gone from the panel: the block, its line and the control that handed the
    // keyboard back. Nothing in the panel restates any of it.
    expect(screen.queryByTestId("live-mirror-keyboard-capture")).not.toBeInTheDocument()
    expect(screen.queryByTestId("live-mirror-release")).not.toBeInTheDocument()
    expect(screen.queryByText(liveMirrorCopy.capture.note)).not.toBeInTheDocument()

    // Stated once, where the rest of the frame's state is read: how the keyboard
    // reaches the device, and how it is given back - which is the fact that keeps
    // capture leavable from the keyboard alone, now that no control leaves it.
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-capture")).toHaveTextContent(liveMirrorCopy.capture.note)
    expect(within(details).getByText(liveMirrorCopy.details.field.keyboard)).toBeInTheDocument()
  })

  /**
   * Capture is leavable from the keyboard ALONE, and that is the whole of it: the
   * frame's focus is the gate, so Tab - which this surface deliberately does not
   * preventDefault - moves focus out of the frame and ends capture. There is no
   * control to press and nothing a device or the control plane could swallow.
   */
  it("leaves capture from the keyboard alone, and the keystroke after it reaches no device", async () => {
    const user = userEvent.setup({ delay: null })
    const { intents, stage } = renderPanel()
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "Enter" })
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(1))

    await user.tab()

    // Tab is dispatched for the device, and it also leaves: the frame no longer
    // holds the keyboard, and nothing had to be pressed to give it back.
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(2))
    expect(keyEvents(intents)[1]).toMatchObject({ keyCode: 61 })
    expect(stage).not.toHaveFocus()

    // The keyboard is the console's again: the keystroke reaches no device even
    // though this dispatch would have accepted it.
    keystroke(stage, { key: "Enter" })
    expect(keyEvents(intents)).toHaveLength(2)
  })

  it("names a key the contract cannot express instead of sending a key nobody pressed", async () => {
    const user = userEvent.setup()
    const { intents, stage } = renderPanel()
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "a", shiftKey: true })

    expect(intents).toHaveLength(0)
    expect(await screen.findByTestId("live-mirror-info-mark")).toBeInTheDocument()
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent("Shift held with a")
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent("Nothing was sent to the device")
  })

  it("reports the control plane's own refusal of a keystroke rather than dropping it", async () => {
    const { intents, stage } = renderPanel({ reply: () => ({ ok: false, message: "The control plane refused that input: the lease expired." }) })
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "Backspace" })

    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent(/lease expired/))
    expect(keyEvents(intents)).toHaveLength(1)
  })

  it("refuses a keystroke it cannot describe, naming the key and the reason, instead of dropping it", async () => {
    const user = userEvent.setup()
    const { intents, stage } = renderPanel({ hasLease: false })
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "Enter" })

    expect(intents).toHaveLength(0)
    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent(liveMirrorCopy.input.noLease))
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.input.noLease)
  })

  /**
   * The other half of the observation binding: a key event is built with the
   * observation it is measured against, so a frame that has none refuses HERE,
   * in the console's own words, and sends nothing. It never asks the kernel to
   * refuse a malformed intent - which is what put "device input intent is
   * invalid" in front of the operator with no field named and nothing to do.
   */
  it("refuses a keystroke it cannot measure, naming the missing observation, and sends nothing", async () => {
    const user = userEvent.setup()
    const { intents, stage } = renderPanel({ mirror: undefined })
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "Enter" })

    expect(intents).toHaveLength(0)
    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent(liveMirrorCopy.input.noObservation))
    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-refusal")).toHaveTextContent(liveMirrorCopy.input.noObservation)
  })

  it("bounds a held key by the control session's own rate, not by the browser's event rate", async () => {
    const { intents, stage } = renderPanel()
    await live(intents)
    stage.focus()

    keystroke(stage, { key: "a", atMs: 1_000 })
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(1))

    // The browser's own auto-repeat arrives faster than this session's rate: the
    // burst is not dispatched, and the repeat past the bound is, so a held key
    // still moves.
    keystroke(stage, { key: "a", repeat: true, atMs: 1_000 + keyRepeatIntervalMs / 2 })
    expect(keyEvents(intents)).toHaveLength(1)

    keystroke(stage, { key: "a", repeat: true, atMs: 1_000 + keyRepeatIntervalMs })
    await waitFor(() => expect(keyEvents(intents)).toHaveLength(2))
    expect(keyEvents(intents)[1]).toMatchObject({ keyCode: 29 })
  })
})

/**
 * sessionView is a session view with nothing behind it: the frame and its info
 * control are given what the session above produces, without a control plane.
 *
 * It exists because the phase below is not one a control plane can report - it is
 * produced by the console failing to READ one, which the hook's own tests drive
 * through a failing read. What is pinned here is the surface's decision, made on
 * that phase.
 */
function sessionView(overrides: Partial<LiveMirrorSessionView>): LiveMirrorSessionView {
  return {
    device: device(),
    phase: "live",
    stream: stream(),
    frame: { width: 1080, height: 1920 },
    failure: "",
    notice: "",
    refusal: "",
    inputBlockedReason: "",
    inputReady: true,
    leaseRefusal: "",
    observationToken: streamToken,
    detailsAttention: "",
    reducedMotion: false,
    attachVideo: () => undefined,
    retry: () => undefined,
    stop: () => undefined,
    readDrawn: () => null,
    beginPointer: () => undefined,
    movePointer: () => undefined,
    endPointer: () => undefined,
    cancelPointer: () => undefined,
    wheelScroll: () => undefined,
    sendKey: () => undefined,
    attachStage: () => undefined,
    beginCapture: () => undefined,
    releaseCapture: () => undefined,
    pressKey: () => undefined,
    ...overrides,
  }
}

describe("a read the console could not complete", () => {
  /**
   * The measured failure this exists for: a couple of seconds of a busy control
   * plane destroyed a working stream, and the operator read it as the picture
   * crashing. The frame's own rule is that a picture which is NOT there is covered
   * by the overlay - so the test that makes this one bite is the second half: the
   * same frame, given a stream that really is over, does paint the overlay.
   */
  it("paints nothing over the picture it is still holding, and still covers a picture that is gone", () => {
    const held = render(<LiveMirrorSurface session={sessionView({ phase: "unreadable" })} />)
    expect(screen.getByTestId("live-mirror-video")).toBeInTheDocument()
    expect(screen.queryByText(liveMirrorCopy.phase.unreadable)).not.toBeInTheDocument()
    held.unmount()

    render(<LiveMirrorSurface session={sessionView({ phase: "ended" })} />)
    expect(screen.getByText(liveMirrorCopy.phase.ended)).toBeInTheDocument()
  })

  it("is not silent about it: the info control marks itself and the details hold the sentence", async () => {
    const user = userEvent.setup()
    render(<LiveMirrorInfo session={sessionView({ phase: "unreadable", detailsAttention: liveMirrorCopy.details.unreadable })} />)

    const info = screen.getByTestId("live-mirror-info")
    expect(screen.getByTestId("live-mirror-info-mark")).toBeInTheDocument()
    expect(info).toHaveAccessibleName(`${liveMirrorCopy.details.label}. ${liveMirrorCopy.details.unreadable}`)

    const details = await openDetails(user)
    expect(within(details).getByTestId("live-mirror-phase")).toHaveTextContent(liveMirrorCopy.phase.unreadable)
    // The mark names the read, not a refusal: this frame has refused nothing.
    expect(info).not.toHaveAccessibleName(new RegExp(liveMirrorCopy.details.unread))
  })
})
