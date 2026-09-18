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
import { liveMirrorCopy, liveStreamView, type LiveStreamView } from "@/lib/live-mirror"
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

function fakeMirror(initial: LiveStreamView = stream()): { client: LiveMirrorClient; calls: string[] } {
  const calls: string[] = []
  let state = initial
  return {
    calls,
    client: {
      async startStream(request) { calls.push(`start:${request.deviceId}`); return state },
      async negotiate(_streamId, offerSdp) { calls.push(`negotiate:${offerSdp}`); return { answerSdp: "answer-sdp", stream: state } },
      async stopStream(streamId) { calls.push(`stop:${streamId}`); return { ...state, state: "ended" } },
      async getStream() { return state },
    },
  }
}

/** stageRect describes a surface that draws the stream at the given size. */
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

function renderSurface(options: { mirror?: LiveMirrorClient; hasLease?: boolean; token?: string; rect?: DOMRect } = {}) {
  const intents: ControlPlaneIntent[] = []
  const dispatch: DispatchIntent = async (intent) => {
    intents.push(intent)
    return { ok: true, kind: intent.type, message: "The control plane accepted the input." }
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
  stage.getBoundingClientRect = () => options.rect ?? stageRect(540, 960)
  return { intents, stage }
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

beforeEach(() => {
  originalMatchMedia = window.matchMedia
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
    stage.getBoundingClientRect = () => stageRect(540, 960)
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

  it("shows no live mark while a stream is connected but has carried no picture", async () => {
    const { intents } = renderSurface({ mirror: fakeMirror(stream({ state: MirrorStreamState.STARTING, frames: 0n })).client })
    await waitFor(() => expect(screen.getByTestId("live-mirror-phase")).toHaveTextContent(liveMirrorCopy.phase.starting))
    expect(screen.getByTestId("live-mirror-mark")).not.toHaveClass("animate-pulse")
    expect(intents).toHaveLength(0)
  })
})
