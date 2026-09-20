// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { create } from "@bufbuild/protobuf"
import { MirrorStreamSchema, MirrorStreamState, MirrorTransport } from "@/gen/drift/v1/device_mirror_pb"
import { ConnectJsonError } from "@/lib/api/connect-json"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { MirrorPlayback, MirrorPlaybackFactory, MirrorPlaybackRequest } from "@/lib/api/mirror-playback"
import { mirrorRetryDelayMs, useLiveMirror, type MirrorSchedule } from "@/lib/api/use-live-mirror"
import { liveMirrorCopy, livePictureHeld, liveStreamView, type LiveMirrorPhase, type LiveMirrorTransportChoice, type LiveMirrorViewerPurpose, type LiveStreamView } from "@/lib/live-mirror"

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
  /** unknown answers every read the way a control plane that RESTARTED does. */
  unknown(on: boolean): void
  reads: number
}

function fakeClient(initial: LiveStreamView = stream()): FakeClient {
  const calls: string[] = []
  const transports: string[] = []
  const purposes: string[] = []
  let state = initial
  let readFailure: unknown | null = null
  let unknown = false
  const handle: FakeClient = {
    calls,
    transports,
    purposes,
    get reads() { return calls.filter((call) => call.startsWith("get:")).length },
    setState(next) { state = next },
    failReads(failure) { readFailure = failure },
    unknown(on) { unknown = on },
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
        // A control plane that restarted holds no session this console opened, so
        // every question about the identity it holds answers not-found.
        if (unknown) throw new ConnectJsonError("not_found", `no live stream is carried under ${streamId}`)
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

/**
 * fakeSchedule stands in for the browser's timers.
 *
 * The retry policy this console implements is a wall-clock claim - a read that
 * could not be completed is asked again after this many milliseconds, and never
 * after more than that - and a case that waits for the claim is asserting the
 * host's load rather than the policy. This records the delay of every piece of
 * work the console schedules and runs it only when a case says so, which is what
 * the delays below are asserted against.
 */
function fakeSchedule() {
  const waits: { delayMs: number; run: () => void }[] = []
  const schedule: MirrorSchedule = (delayMs, run) => {
    const entry = { delayMs, run }
    waits.push(entry)
    return () => {
      const at = waits.indexOf(entry)
      if (at >= 0) waits.splice(at, 1)
    }
  }
  return {
    schedule,
    /** delays is what this console is waiting, in wall-clock milliseconds. */
    delays: () => waits.map((wait) => wait.delayMs),
    /** runNext does the work the console is waiting on, exactly once. */
    async runNext() {
      const next = waits.shift()
      if (!next) throw new Error("this console is waiting on nothing")
      await act(async () => {
        next.run()
        await Promise.resolve()
      })
    },
  }
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

function Harness({ client, peerFactory, playbackFactory, transport, purpose, deviceId = "device-1", schedule, pollIntervalMs = 5, pollRetryCeilingMs, reopenLimit }: { client?: LiveMirrorClient; peerFactory?: FakePeer["factory"]; playbackFactory?: MirrorPlaybackFactory; transport?: LiveMirrorTransportChoice; purpose?: LiveMirrorViewerPurpose; deviceId?: string; schedule?: MirrorSchedule; pollIntervalMs?: number; pollRetryCeilingMs?: number; reopenLimit?: number }) {
  const session = useLiveMirror(deviceId, { client, peerFactory, playbackFactory, transport, purpose, workspaceId: "workspace-lab-local", pollIntervalMs, pollFailureLimit: 2, schedule, pollRetryCeilingMs, reopenLimit })
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

/**
 * The retry policy, on its own.
 *
 * It is a wall-clock claim and it is asserted as one: what a console that could not
 * read the plane waits before asking again, in the numbers it ships with. A test
 * that waited for these delays would be measuring the host, not the policy.
 */
describe("the console's read retry policy", () => {
  it("doubles the wait from the poll cadence, and never past its bound", () => {
    const waits = [1, 2, 3, 4, 5, 20].map((failures) => mirrorRetryDelayMs(failures, 1_000, 4_000))
    expect(waits).toEqual([1_000, 2_000, 4_000, 4_000, 4_000, 4_000])
  })

  it("reads a degenerate policy as the shipped one rather than as no wait at all", () => {
    expect(mirrorRetryDelayMs(0, 1_000, 4_000)).toBe(1_000)
    expect(mirrorRetryDelayMs(Number.NaN, 1_000, 4_000)).toBe(1_000)
    expect(mirrorRetryDelayMs(2, 0, 0)).toBe(2_000)
    // A bound below the cadence still bounds: a console never waits less than its
    // own poll cadence, and never more than the bound it was given.
    expect(mirrorRetryDelayMs(9, 1_000, 3_000)).toBe(3_000)
  })

  it("holds a picture while this console has a stream open, and never claims one it could not read", () => {
    const phases: LiveMirrorPhase[] = ["idle", "unavailable", "opening", "starting", "live", "unreadable", "ended", "failed"]
    expect(phases.filter((phase) => livePictureHeld(phase))).toEqual(["starting", "live", "unreadable"])
    // The unreadable state is not a failure of the stream, and its sentence must
    // never read as one: the plane has said nothing about this stream.
    expect(liveMirrorCopy.phase.unreadable).not.toMatch(/failed/i)
  })
})

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

  it("still ends the stream and says the plane's own sentence when the plane reports it failed", async () => {
    const handle = fakeClient()
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} />)
    await waitFor(() => expect(handle.calls).toContain("negotiate:stream-1:offer-sdp"))

    const planeReason = "the peer produced no picture within its bound"
    handle.setState(stream({ state: MirrorStreamState.FAILED, failure: planeReason }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    // The PLANE's sentence, not this console's generic one: a reported failure is
    // reported with its reason, and resilience must not swallow it.
    expect(screen.getByTestId("failure")).toHaveTextContent(planeReason)
    expect(screen.getByTestId("failure")).not.toHaveTextContent(liveMirrorCopy.phase.failed)
    expect(peer.calls).toContain("close")
    expect(handle.calls).toContain("stop:stream-1")
    expect(screen.getByTestId("video")).toHaveProperty("srcObject", null)
  })

  /**
   * The defect this file exists for: two consecutive failed polls took the picture
   * down and told the control plane to stop carrying the device, and on this plane
   * the last viewer detaching is what ENDS the session - so a couple of seconds of
   * a busy control plane destroyed a working stream. What the console may do with a
   * read it could not complete is stop CLAIMING the stream is live; it may not stop
   * the stream, and it may not take the picture away.
   */
  it("survives a read outage of several poll intervals and brings the picture back", async () => {
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 9n }))
    const peer = fakePeer()
    render(<Harness client={handle.client} peerFactory={peer.factory} pollRetryCeilingMs={50} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    peer.emitStream()
    await waitFor(() => expect(screen.getByTestId("video")).toHaveProperty("srcObject", peer.media))

    // The outage: several poll intervals of reads the plane does not answer.
    handle.failReads(new Error("the control plane is not answering"))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("unreadable"))
    await waitFor(() => expect(handle.reads).toBeGreaterThan(4))

    // It stopped claiming live, and that is the WHOLE of what it did. The stream
    // was not stopped, the peer was not closed, the picture is still in the
    // element, and the failure line is not carrying a failure the plane never
    // reported.
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)
    expect(peer.calls).not.toContain("close")
    expect(screen.getByTestId("video")).toHaveProperty("srcObject", peer.media)
    expect(screen.getByTestId("failure")).toHaveTextContent("")

    // The plane answers again: the picture returns, with what it has carried since.
    handle.failReads(null)
    handle.setState(stream({ state: MirrorStreamState.LIVE, frames: 11n }))
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    expect(screen.getByTestId("frames")).toHaveTextContent("11")
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)
  })

  /**
   * The same fact in wall-clock terms rather than by waiting for it: what a console
   * that could not read the plane does next is ask again, on a backoff that doubles
   * and then STOPS DOUBLING. The numbers below are the shipped policy - a one-second
   * cadence doubling to a four-second bound - so the bound is visible where it is
   * decided rather than inferred from a test that happens to be quick.
   */
  it("keeps asking on a bounded backoff instead of hammering a plane that is not answering", async () => {
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 2n }))
    const clock = fakeSchedule()
    render(<Harness client={handle.client} peerFactory={fakePeer().factory} schedule={clock.schedule} pollIntervalMs={1_000} pollRetryCeilingMs={4_000} />)
    await waitFor(() => expect(clock.delays()).toEqual([1_000]))

    handle.failReads(new Error("the control plane is not answering"))
    const waited: number[] = []
    for (let read = 0; read < 6; read += 1) {
      await clock.runNext()
      waited.push(clock.delays()[0])
    }
    // Doubling, then bounded: 2s, 4s, and 4s for every further failure - never 8s,
    // 16s or a delay that grows until the operator stops being told anything.
    expect(waited).toEqual([1_000, 2_000, 4_000, 4_000, 4_000, 4_000])
    expect(clock.delays()).toEqual([4_000])
    expect(screen.getByTestId("phase")).toHaveTextContent("unreadable")
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)
  })

  it("re-opens the same device's stream when the plane no longer knows it, instead of showing a dead frame", async () => {
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 7n }))
    const peer = fakePeer()
    const clock = fakeSchedule()
    render(<Harness client={handle.client} peerFactory={peer.factory} schedule={clock.schedule} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    peer.emitStream()
    await waitFor(() => expect(screen.getByTestId("video")).toHaveProperty("srcObject", peer.media))
    await waitFor(() => expect(clock.delays()).toEqual([5]))

    // The plane RESTARTS: the sessions belonged to the process that ended, so every
    // read about the identity this console holds answers not-found. That is not the
    // plane reporting a failure - it has nothing to report about an identity it does
    // not hold.
    handle.unknown(true)
    await clock.runNext()
    expect(screen.getByTestId("phase")).toHaveTextContent("opening")
    // The plane has forgotten the stream, so its picture goes... and the stream is
    // NOT stopped: a stop for a stream the plane last reported live is exactly the
    // destructive stop this path exists to remove.
    expect(screen.getByTestId("video")).toHaveProperty("srcObject", null)
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)

    // Re-entry asks for the SAME device's stream again, which the plane resolves to
    // the session it already carries or starts again.
    handle.unknown(false)
    await clock.runNext()
    await waitFor(() => expect(handle.calls.filter((call) => call.startsWith("start:"))).toHaveLength(2))
    expect(handle.calls).toContain("start:device-1")
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    expect(screen.getByTestId("frames")).toHaveTextContent("7")
    expect(screen.getByTestId("phase")).not.toHaveTextContent("failed")
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)
  })

  it("reports a plane that hands out a stream and forgets it, rather than asking it forever", async () => {
    const handle = fakeClient(stream({ state: MirrorStreamState.LIVE, frames: 3n }))
    const clock = fakeSchedule()
    render(<Harness client={handle.client} peerFactory={fakePeer().factory} schedule={clock.schedule} reopenLimit={1} />)
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("live"))
    await waitFor(() => expect(clock.delays()).toEqual([5]))

    handle.unknown(true)
    for (let step = 0; step < 4 && screen.getByTestId("phase").textContent !== "failed"; step += 1) {
      await clock.runNext()
    }
    await waitFor(() => expect(screen.getByTestId("phase")).toHaveTextContent("failed"))
    expect(screen.getByTestId("failure")).toHaveTextContent(liveMirrorCopy.failure.unresumable)
    expect(handle.calls.filter((call) => call.startsWith("start:"))).toHaveLength(2)
    // Nothing was stopped even here, where the plane is the one that forgot the
    // stream: the report names the plane's own fact and no stop was issued for it.
    expect(handle.calls.some((call) => call.startsWith("stop:"))).toBe(false)
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
