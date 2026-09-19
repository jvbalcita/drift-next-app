// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { DeviceView } from "@/lib/domain/control-plane"
import { liveStreamView, type LiveStreamView } from "@/lib/live-mirror"
import { LiveTilePicture } from "./live-tile"

/**
 * One fleet tile's live picture, on its own.
 *
 * The tile is where the fleet's pictures land, and the owner's report was that a
 * tile which SHOULD be carrying one showed a coloured background instead. The
 * defect is in when the tile mounts the element its picture is written into, so
 * that is what this file checks: the element is the tile's for as long as the
 * tile subscribes, and a track that arrives before the stream says it is up is
 * written into it rather than dropped on the floor.
 *
 * The peer connection is the one part of this a test cannot use, so the global it
 * is reached through is stubbed with the smallest object the tile's session asks
 * anything of, exactly as the big frame's own tests do.
 */
const workspaceId = "workspace-lab-local"

function stream(overrides: { state?: MirrorStreamState; frames?: bigint } = {}): LiveStreamView {
  return liveStreamView(create(MirrorStreamSchema, {
    streamId: "stream-atlas-04",
    deviceId: "atlas-04",
    transport: MirrorTransport.WEBRTC,
    renderWidth: 1080,
    renderHeight: 1920,
    state: overrides.state ?? MirrorStreamState.LIVE,
    frames: overrides.frames ?? 4n,
  }))
}

function device(): DeviceView {
  return {
    id: "atlas-04",
    displayName: "Atlas 04",
    status: "online",
    transport: "usb",
    endpointId: "endpoint-atlas-04",
    batteryPercent: 80,
    controlEligibility: "eligible",
  } as DeviceView
}

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
  /** publish is the device's picture arriving on the negotiated track. */
  publish(media: MediaStream) { for (const listener of this.tracks) listener({ streams: [media] }) }
}

/** deferred is a handshake this test holds open, so the tile's own state is known. */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined
  const promise = new Promise<T>((settle) => { resolve = settle })
  return { promise, resolve }
}

function tileMirror(negotiation: Promise<{ answerSdp: string; stream: LiveStreamView }>): LiveMirrorClient {
  return {
    async startStream() { return stream() },
    async negotiate() { return negotiation },
    async stopStream() { return stream({ state: MirrorStreamState.ENDED }) },
    async getStream() { return stream() },
    streamEndpoint(path) { return { url: `http://control-plane.test${path}`, headers: {} } },
  }
}

beforeEach(() => {
  livePeers.length = 0
  vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("a fleet tile's live picture", () => {
  it("keeps the element its picture is written into for as long as it subscribes", async () => {
    const held = deferred<{ answerSdp: string; stream: LiveStreamView }>()
    render(<LiveTilePicture device={device()} mirror={tileMirror(held.promise)} transport="webrtc" workspaceId={workspaceId} viewing />)

    // The stream is open and has not been negotiated: the tile is not live yet,
    // and the element is already there - invisible, because a picture that is not
    // live is not shown - waiting for the frame the handshake will deliver.
    const video = await screen.findByTestId("live-tile-video-atlas-04")
    expect(video).toHaveClass("invisible")
    expect(livePeers).toHaveLength(1)

    const media = {} as MediaStream
    livePeers[0]!.publish(media)
    expect(video).toHaveProperty("srcObject", media)

    held.resolve({ answerSdp: "answer-sdp", stream: stream() })
    await waitFor(() => expect(screen.getByTestId("live-tile-video-atlas-04")).not.toHaveClass("invisible"))
    // The picture arrived before the stream reported itself live, and it is still
    // the tile's picture: a tile that mounted its element late would be showing
    // its background colour under a state label reading Live.
    expect(screen.getByTestId("live-tile-video-atlas-04")).toHaveProperty("srcObject", media)
  })

  it("opens nothing and draws nothing for a tile the console's bound does not reach", async () => {
    const started: string[] = []
    const client: LiveMirrorClient = { ...tileMirror(Promise.resolve({ answerSdp: "answer-sdp", stream: stream() })), async startStream(request) { started.push(request.deviceId); return stream() } }
    render(<LiveTilePicture device={device()} mirror={client} transport="webrtc" workspaceId={workspaceId} viewing={false} />)

    expect(screen.queryByTestId("live-tile-video-atlas-04")).not.toBeInTheDocument()
    expect(started).toEqual([])
    await waitFor(() => expect(screen.getByTestId("live-tile-state-atlas-04")).toHaveAttribute("data-tile-state", "idle"))
  })
})
