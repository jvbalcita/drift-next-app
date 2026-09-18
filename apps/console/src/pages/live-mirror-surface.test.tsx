// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { ControlPlaneIntent, DispatchIntent, DeviceView } from "@/lib/domain/control-plane"
import { liveMirrorCopy, liveStreamView, drawnContentRect, type LiveStreamView } from "@/lib/live-mirror"
import { LiveMirrorSurface } from "./live-mirror-surface"

const observationToken = "fresh-atlas-04"

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

function renderSurface(options: { mirror?: LiveMirrorClient; hasLease?: boolean; token?: string; rect?: DOMRect; picture?: { width: number; height: number }; reply?: (intent: ControlPlaneIntent) => { ok: boolean; message: string } } = {}) {
  const intents: ControlPlaneIntent[] = []
  const dispatch: DispatchIntent = async (intent) => {
    intents.push(intent)
    return { ok: true, kind: intent.type, message: "The control plane accepted the input.", ...(options.reply?.(intent) ?? {}) }
  }
  const mirror = options.hasOwnProperty("mirror") ? options.mirror : fakeMirror().client
  render(
    <LiveMirrorSurface
      device={device()}
      mirror={mirror}
      workspaceId="workspace-lab-local"
      observationToken={options.token ?? observationToken}
      hasLease={options.hasLease ?? true}
      dispatch={dispatch}
    />,
  )
  const stage = screen.getByTestId("live-mirror-stage")
  const video = screen.getByTestId("live-mirror-video") as HTMLVideoElement
  // jsdom has no media stack, so the two boxes the mapping reads are supplied the
  // way a layout is: the element's own box, and the size of the picture the
  // browser decoded into it. `picture` is the stream's shape unless a test says
  // otherwise, which is the case with no letterbox.
  const picture = options.picture ?? { width: 1080, height: 1920 }
  video.getBoundingClientRect = () => options.rect ?? stageRect(540, 960)
  Object.defineProperty(video, "videoWidth", { value: picture.width, configurable: true })
  Object.defineProperty(video, "videoHeight", { value: picture.height, configurable: true })
  return { intents, stage, video }
}

async function live(intents: ControlPlaneIntent[]) {
  await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/^Live\./))
  expect(intents).toHaveLength(0)
}

let originalMatchMedia: typeof window.matchMedia

/**
 * The browser's WebRTC stack is the one part of this surface a test cannot use,
 * so the global it is reached through is stubbed with the smallest object the
 * default peer factory asks anything of. Nothing here publishes a track: these
 * tests are about what the surface renders and what it sends back.
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

describe("the big frame as a live mirror", () => {
  it("renders the live stream and states the transport and frame it is using", async () => {
    const { intents } = renderSurface()
    await live(intents)

    expect(screen.getByTestId("live-mirror-transport")).toHaveTextContent(liveMirrorCopy.transport.webrtc)
    expect(screen.getByTestId("live-mirror-transport")).toHaveTextContent("frame 1080x1920")
    expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent("12 picture(s) carried")
  })

  it("sends a tap in the stream's own frame, never in the element's pixels", async () => {
    const { intents, stage } = renderSurface()
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
    // element's box read the picture's own left edge as x=107.
    const rect = stageRect(314, 531)
    const picture = { width: 1080, height: 2280 }
    const { intents, stage } = renderSurface({ rect, picture, mirror: fakeMirror(stream({ width: picture.width, height: picture.height })).client })
    await live(intents)

    const drawn = drawnContentRect({ left: rect.left, top: rect.top, width: rect.width, height: rect.height }, picture)
    if (!drawn) throw new Error("the fixture draws nothing")
    fireEvent.pointerDown(stage, { pointerId: 5, clientX: drawn.left, clientY: drawn.top })
    fireEvent.pointerUp(stage, { pointerId: 5, clientX: drawn.left, clientY: drawn.top })

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceTap", deviceId: "atlas-04", x: 0, y: 0, renderWidth: 1080, renderHeight: 2280 })
  })

  it("refuses a tap in the pillarbox, and reaches no device with it", async () => {
    const rect = stageRect(314, 531)
    const picture = { width: 1080, height: 2280 }
    const { intents, stage } = renderSurface({ rect, picture, mirror: fakeMirror(stream({ width: 1080, height: 2280 })).client })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 6, clientX: 5, clientY: 265 })
    fireEvent.pointerUp(stage, { pointerId: 6, clientX: 5, clientY: 265 })

    expect(screen.getByRole("alert")).toHaveTextContent(liveMirrorCopy.refusal.outsideFrame)
    expect(intents).toHaveLength(0)
  })

  it("refuses a tap before the browser has drawn a picture, and says which fact is missing", async () => {
    const { intents, stage } = renderSurface({ picture: { width: 0, height: 0 } })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 7, clientX: 200, clientY: 200 })
    fireEvent.pointerUp(stage, { pointerId: 7, clientX: 200, clientY: 200 })

    expect(screen.getByRole("alert")).toHaveTextContent(liveMirrorCopy.refusal.noPicture)
    expect(intents).toHaveLength(0)
  })

  it("compresses a drag into exactly one swipe, at the stream's scale and with no tap", async () => {
    const { intents, stage } = renderSurface({ rect: stageRect(270, 480, 20, 40) })
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

  it("dispatches key input from a control an operator can reach without a mouse", async () => {
    const user = userEvent.setup()
    const { intents } = renderSurface()
    await live(intents)

    const back = screen.getByRole("button", { name: "Send Back key" })
    back.focus()
    expect(back).toHaveFocus()
    await user.keyboard("{Enter}")

    await waitFor(() => expect(intents).toHaveLength(1))
    expect(intents[0]).toMatchObject({ type: "submitDeviceKeyEvent", deviceId: "atlas-04", keyCode: 4 })
  })

  it("refuses to describe an input it has no lease, observation or frame for", async () => {
    const { intents } = renderSurface({ hasLease: false })
    await live(intents)

    expect(screen.getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noLease)
    expect(screen.getByRole("button", { name: "Send Back key" })).toBeDisabled()
    fireEvent.pointerDown(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    fireEvent.pointerUp(screen.getByTestId("live-mirror-stage"), { pointerId: 3, clientX: 10, clientY: 10 })
    expect(intents).toHaveLength(0)
  })

  it("names the observation it is missing rather than inventing one", async () => {
    const { intents } = renderSurface({ token: "" })
    await live(intents)
    expect(screen.getByTestId("live-mirror-input-blocked")).toHaveTextContent(liveMirrorCopy.input.noObservation)
    expect(intents).toHaveLength(0)
  })

  it("surfaces the control plane's refusal instead of reporting a delivered input", async () => {
    const intents: ControlPlaneIntent[] = []
    const dispatch: DispatchIntent = async (intent) => {
      intents.push(intent)
      return { ok: false, kind: intent.type, message: "The control plane refused that input: the lease expired." }
    }
    render(
      <LiveMirrorSurface device={device()} mirror={fakeMirror().client} workspaceId="workspace-lab-local" observationToken={observationToken} hasLease dispatch={dispatch} />,
    )
    const stage = screen.getByTestId("live-mirror-stage")
    const video = screen.getByTestId("live-mirror-video") as HTMLVideoElement
    video.getBoundingClientRect = () => stageRect(540, 960)
    Object.defineProperty(video, "videoWidth", { value: 1080, configurable: true })
    Object.defineProperty(video, "videoHeight", { value: 1920, configurable: true })
    await live(intents)

    fireEvent.pointerDown(stage, { pointerId: 4, clientX: 100, clientY: 100 })
    fireEvent.pointerUp(stage, { pointerId: 4, clientX: 100, clientY: 100 })

    await waitFor(() => expect(screen.getByTestId("live-mirror-notice")).toHaveTextContent(/lease expired/i))
  })

  it("renders a failed stream with its reason and a way to reopen it, not a live mark", async () => {
    const failure = "the peer produced no picture within its bound"
    const { intents } = renderSurface({ mirror: fakeMirror(stream({ state: MirrorStreamState.FAILED, failure, frames: 0n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/failed/i))

    expect(screen.getByText(failure)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Reopen the live stream/i })).toBeInTheDocument()
    expect(screen.getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
    expect(intents).toHaveLength(0)
  })

  it("says a stream ended rather than leaving its last frame looking current", async () => {
    renderSurface({ mirror: fakeMirror(stream({ state: MirrorStreamState.ENDED, frames: 40n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/stream ended/i))
    // No peer is opened for a stream that is already over: there is no picture to
    // negotiate for, and opening one would be a capture for nobody.
    expect(livePeers).toHaveLength(0)
    expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/40 picture\(s\) were carried/i)
  })

  it("names the missing control plane when this console has no live mirror at all", async () => {
    renderSurface({ mirror: undefined })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/no control plane/i))
    // The sentence is the surface's state line and the overlay's, which is the
    // point: the frame says it in the one place an operator is looking.
    expect(screen.getAllByText(liveMirrorCopy.phase.unavailable).length).toBeGreaterThan(0)
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

    const { intents } = renderSurface()
    await live(intents)
    expect(screen.getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
  })

  it("drops the picture and covers the frame when a live stream ends, so its last frame cannot look current", async () => {
    const mirror = fakeMirror(stream({ state: MirrorStreamState.LIVE, frames: 20n }))
    const { intents } = renderSurface({ mirror: mirror.client })
    await live(intents)

    const media = {} as MediaStream
    expect(livePeers).toHaveLength(1)
    livePeers[0]!.publish(media)
    const video = document.querySelector("video")
    await waitFor(() => expect(video).toHaveProperty("srcObject", media))

    mirror.setState(stream({ state: MirrorStreamState.ENDED, frames: 20n }))
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/stream ended/i), { timeout: 4_000 })
    expect(video).toHaveProperty("srcObject", null)
    expect(document.querySelector("video")).toBe(video)
  })

  it("shows no live mark while a stream is connected but has carried no picture", async () => {
    const { intents } = renderSurface({ mirror: fakeMirror(stream({ state: MirrorStreamState.STARTING, frames: 0n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(liveMirrorCopy.phase.starting))
    expect(screen.getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
    expect(intents).toHaveLength(0)
  })

  it("types what the operator entered into the device, and clears it once it was typed", async () => {
    const user = userEvent.setup()
    const { intents } = renderSurface()
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
    const { intents } = renderSurface({ reply: () => ({ ok: false, message: "The device lease has expired." }) })
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
    const { intents } = renderSurface()
    await live(intents)

    await user.click(screen.getByTestId("live-mirror-text-send"))

    expect(screen.getByRole("alert")).toHaveTextContent(liveMirrorCopy.text.empty)
    expect(intents).toHaveLength(0)
  })

  it("offers no way to type into a device it holds no lease for, and says why", async () => {
    const { intents } = renderSurface({ hasLease: false })
    await live(intents)

    expect(screen.getByTestId("live-mirror-text")).toBeDisabled()
    expect(screen.getByTestId("live-mirror-text-send")).toBeDisabled()
    expect(screen.getByTestId("live-mirror-text-blocked")).toHaveTextContent(liveMirrorCopy.text.noLease)
  })

  it("stops offering typed text when the stream it would travel is over, and says why", async () => {
    const mirror = fakeMirror(stream({ state: MirrorStreamState.ENDED, frames: 20n }))
    const { intents } = renderSurface({ mirror: mirror.client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(/stream ended/i), { timeout: 4_000 })

    expect(screen.getByTestId("live-mirror-text")).toBeDisabled()
    expect(screen.getByTestId("live-mirror-text-blocked")).toHaveTextContent(liveMirrorCopy.text.noStream)
    expect(intents).toHaveLength(0)
  })
})
