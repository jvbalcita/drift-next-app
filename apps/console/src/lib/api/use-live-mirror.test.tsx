// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { MirrorPlayback, MirrorPlaybackFactory, MirrorPlaybackRequest } from "@/lib/api/mirror-playback"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import { liveMirrorCopy, liveStreamView, type LiveMirrorTransportChoice, type LiveMirrorViewerPurpose, type LiveStreamView } from "@/lib/live-mirror"

interface StreamOverrides {
  state?: MirrorStreamState
  failure?: string
  frames?: bigint
  transport?: MirrorTransport
  streamUrl?: string
}

function stream(overrides: StreamOverrides = {}): LiveStreamView {
  return liveStreamView(create(MirrorStreamSchema, {
    streamId: "stream-1",
    deviceId: "device-1",
    transport: MirrorTransport.WEBRTC,
    renderWidth: 1080,
    renderHeight: 1920,
    state: MirrorStreamState.STARTING,
    frames: 0n,
    ...overrides,
  }))
}

/**
 * fakePlayback stands in for the browser's media stack: what a case asserts is
 * which stream this console fetched and how it was torn down, neither of which
 * needs a decoder.
 */
function fakePlayback() {
  const starts: MirrorPlaybackRequest[] = []
  let stops = 0
  const factory: MirrorPlaybackFactory = (request) => {
    starts.push(request)
    const playback: MirrorPlayback = {
      async start() {
        // The endpoint's own state is read by the poll, not by the fetch: a body
        // that never carries a picture is a failure the poll reports.
      },
      stop() { stops += 1 },
    }
    return playback
  }
  return { factory, starts, stops: () => stops }
}

interface FakeClient {
  client: LiveMirrorClient
  calls: string[]
  /** transports are the transports this console asked the control plane for. */
  transports: string[]
  /** purposes are the purposes each stream was opened as: a tile or the operator's frame. */
  purposes: string[]
  setState(next: LiveStreamView): void
  failReads(failure: unknown | null): void
  reads: number
}

function fakeClient(initial: LiveStreamView = stream()): FakeClient {
  const calls: string[] = []
  const transports: string[] = []
  const purposes: string[] = []
  let state = initial
  let readFailure: unknown | null = null
  const handle: FakeClient = {
    calls,
    transports,
    purposes,
    get reads() { return calls.filter((call) => call.startsWith("get:")).length },
    setState(next) { state = next },
    failReads(failure) { readFailure = failure },
    client: {
      async startStream(request) {
        calls.push(`start:${request.deviceId}`)
        transports.push(request.transport ?? "unspecified")
        purposes.push(request.purpose)
        return state
      },
      async getCapacity() { return { sessionCapacity: 4, operatorReserve: 1, tilePlaces: 3 } },
      async negotiate(streamId, offerSdp) {
        calls.push(`negotiate:${streamId}:${offerSdp}`)
        return { answerSdp: "answer-sdp", stream: state }
      },
      async stopStream(streamId) {
        calls.push(`stop:${streamId}`)
        return { ...state, state: "ended" }
      },
      async getStream(streamId) {
        calls.push(`get:${streamId}`)
        if (readFailure) throw readFailure
        return state
      },
      streamEndpoint(path) {
        calls.push(`endpoint:${path}`)
        return { url: `http://control-plane.test${path}`, headers: { "X-Drift-Lab-Token": "token" } }
      },
    },
  }
  return handle
}

interface FakePeer {
  factory: () => { createOffer(): Promise<string>; acceptAnswer(sdp: string): Promise<void>; onStream(listener: (stream: MediaStream) => void): void; close(): void }
  calls: string[]
  emitStream(): void
  media: MediaStream
}

function fakePeer(): FakePeer {
  const calls: string[] = []
  const media = {} as MediaStream
  let listener: ((stream: MediaStream) => void) | null = null
  const peer = {
    async createOffer() { calls.push("offer"); return "offer-sdp" },
    async acceptAnswer(sdp: string) { calls.push(`answer:${sdp}`) },
    onStream(next: (stream: MediaStream) => void) { listener = next },
    close() { calls.push("close") },
  }
  return { factory: () => peer, calls, emitStream: () => listener?.(media), media }
}

function Harness({ client, peerFactory, playbackFactory, transport, purpose, deviceId = "device-1" }: { client?: LiveMirrorClient; peerFactory?: FakePeer["factory"]; playbackFactory?: MirrorPlaybackFactory; transport?: LiveMirrorTransportChoice; purpose?: LiveMirrorViewerPurpose; deviceId?: string }) {
  const session = useLiveMirror(deviceId, { client, peerFactory, playbackFactory, transport, purpose, workspaceId: "workspace-lab-local", pollIntervalMs: 5, pollFailureLimit: 2 })
  return (
    <div>
      <span data-testid="phase">{session.phase}</span>
      <span data-testid="failure">{session.failure}</span>
      <span data-testid="frames">{session.stream?.frames ?? -1}</span>
      <video data-testid="video" ref={session.attachVideo} />
      <button type="button" onClick={session.stop}>stop</button>
    </div>
  )
}

describe("the console's live mirror session", () => {
  it("opens the stream, negotiates the browser's own offer, and paints the peer's stream into the video", async () => {
    const handle = fakeClient()
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)

    await waitFor(() => expect(peer.calls).toContain("answer:answer-sdp"))
    expect(handle.calls.slice(0, 2)).toEqual(["start:device-1", "negotiate:stream-1:offer-sdp"])
    expect(screen.getByTestId("phase")).toHaveTextContent("starting")

    peer.emitStream()
    await waitFor(() => expect(screen.getByTestId("video")).toHaveProperty("srcObject", peer.media))
  })

  it("opens the stream AS what it is, because the plane spends its capacity per purpose", async () => {
    // The plane keeps a place of its device-session capacity for the operator's own
    // frame, so the purpose decides whether the plane can carry this stream at all:
    // a grid tile that opened as the operator's frame would spend the place the big
    // frame needs, and the frame is where a device is worked from.
    const tiled = fakeClient()
    const tiledPeer = fakePeer()
    render(<Harness client={tiled.client} peerFactory={tiledPeer.factory} purpose="ambient" />)
    await waitFor(() => expect(tiled.purposes).toEqual(["ambient"]))

    // A surface that did not say which it is gets the OPERATOR's place, which is
    // the demand the reserve exists for: an undeclared viewer must never be the one
    // that loses the operator a frame.
    const undeclared = fakeClient()
    const undeclaredPeer = fakePeer()
    render(<Harness client={undeclared.client} peerFactory={undeclaredPeer.factory} />)
    await waitFor(() => expect(undeclared.purposes).toEqual(["operator"]))
  })

  it("reports LIVE only when the control plane reports pictures carried", async () => {
    const handle = fakeClient()
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)

    await waitFor(() => expect(handle.reads).toBeGreaterThan(0))
    expect(screen.getByTestId("phase")).toHaveTextContent("starting")

    handle.setState(stream({ state: MirrorStreamState.LIVE, frames: 7n }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    expect(screen.getByTestId("frames")).toHaveTextContent("7")
  })

  it("takes the picture down and tells the operator when the stream fails", async () => {
    const handle = fakeClient()
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)
    await waitFor(() => expect(handle.calls).toContain("negotiate:stream-1:offer-sdp"))

    handle.setState(stream({ state: MirrorStreamState.FAILED, failure: "the peer produced no picture within its bound" }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    expect(screen.getByTestId("failure")).toHaveTextContent("the peer produced no picture within its bound")
    expect(peer.calls).toContain("close")
    expect(handle.calls).toContain("stop:stream-1")
    expect(screen.getByTestId("video")).toHaveProperty("srcObject", null)
  })

  it("stops claiming to show a stream the control plane has stopped answering for", async () => {
    const handle = fakeClient()
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)
    await waitFor(() => expect(handle.reads).toBeGreaterThan(0))

    handle.failReads(new Error("control plane is gone"))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    expect(screen.getByTestId("failure")).toHaveTextContent(liveMirrorCopy.failure.lost)
    expect(peer.calls).toContain("close")
  })

  it("ends visibly when the stream is no longer carried, rather than freezing on its last frame", async () => {
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 30n }))
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))

    handle.setState(stream({ state: MirrorStreamState.ENDED, frames: 30n }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("ended"))
    expect(screen.getByTestId("video")).toHaveProperty("srcObject", null)
    expect(peer.calls).toContain("close")
  })

  it("opens and ends the stream with the operator's own session, and releases it on unmount", async () => {
    const user = userEvent.setup()
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 3n }))
    const peer = fakePeer()
    const view = render(<Harness client={handle.client} peerFactory={peer.factory} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))

    await user.click(screen.getByRole("button", { name: "stop" }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("ended"))
    expect(handle.calls.filter((call) => call === "stop:stream-1")).toHaveLength(1)
    expect(peer.calls).toContain("close")

    // A stream the operator already stopped is not stopped a second time when the
    // frame finally unmounts.
    view.unmount()
    expect(handle.calls.filter((call) => call === "stop:stream-1")).toHaveLength(1)
  })

  it("fetches the stream's own endpoint over the TCP transport, and never negotiates a peer", async () => {
    const handle = fakeClient(stream({ transport: MirrorTransport.TCP, streamUrl: "/drift/v1/mirror/stream?stream_id=stream-1", state: MirrorStreamState.LIVE, frames: 4n }))
    const peer = fakePeer()
    const playback = fakePlayback()
    render(<Harness client={handle.client} peerFactory={peer.factory} playbackFactory={playback.factory} transport="tcp" />)

    await waitFor(() => expect(playback.starts).toHaveLength(1))
    expect(handle.transports).toEqual(["tcp"])
    expect(playback.starts[0].url).toContain("stream_id=stream-1")
    expect(playback.starts[0].headers["X-Drift-Lab-Token"]).toBe("token")
    // A TCP stream is fetched, not negotiated: no peer was built and no offer was
    // sent, and the phase follows the control plane's own report of pictures.
    expect(peer.calls).toEqual([])
    expect(handle.calls.some((call) => call.startsWith("negotiate"))).toBe(false)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
  })

  it("stops the playback and tells the control plane to end the stream when the operator stops it", async () => {
    const handle = fakeClient(stream({ transport: MirrorTransport.TCP, streamUrl: "/drift/v1/mirror/stream?stream_id=stream-1" }))
    const playback = fakePlayback()
    const user = userEvent.setup()
    render(<Harness client={handle.client} playbackFactory={playback.factory} transport="tcp" />)

    await waitFor(() => expect(playback.starts).toHaveLength(1))
    await user.click(screen.getByRole("button", { name: "stop" }))

    await waitFor(() => expect(playback.stops()).toBe(1))
    expect(handle.calls).toContain("stop:stream-1")
  })

  it("reports a TCP stream the control plane opened without naming an endpoint", async () => {
    const handle = fakeClient(stream({ transport: MirrorTransport.TCP, streamUrl: "" }))
    const playback = fakePlayback()
    render(<Harness client={handle.client} playbackFactory={playback.factory} transport="tcp" />)

    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    expect(screen.getByTestId("failure")).toHaveTextContent(liveMirrorCopy.failure.noEndpoint)
    expect(playback.starts).toHaveLength(0)
  })

  it("tells an operator whose console has no control plane that there is nothing to show", async () => {
    render(<Harness />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("unavailable"))
    expect(screen.getByTestId("failure")).toHaveTextContent("")
  })

  it("reports a stream that could not be opened rather than opening a surface that shows nothing", async () => {
    const handle = fakeClient()
    const failing: LiveMirrorClient = {
      ...handle.client,
      startStream: async () => { throw new Error("the live mirror is not constructed") },
    }
    render(<Harness client={failing} peerFactory={fakePeer().factory} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    expect(screen.getByTestId("failure")).toHaveTextContent("the live mirror is not constructed")
  })
})
