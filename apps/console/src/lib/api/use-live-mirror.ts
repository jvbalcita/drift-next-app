import { useCallback, useEffect, useRef, useState } from "react"
import { ConnectJsonError } from "@/lib/api/connect-json"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { liveMirrorCopy, type LiveMirrorPhase, type LiveStreamView } from "@/lib/live-mirror"

/**
 * The browser's half of one live stream.
 *
 * The session is a small state machine with one rule that matters: everything is
 * torn down before the console says the stream stopped. The peer is closed, the
 * video element is emptied and the control plane is told to stop carrying the
 * device - in that order - so an operator never reads "ended" over a frame that is
 * still being painted, and a device whose last viewer has gone stops being
 * captured.
 *
 * The peer is a port rather than a direct RTCPeerConnection because a test has no
 * WebRTC stack and because the browser's own implementation is the only part of
 * this that cannot be exercised deterministically. Everything around it - the
 * order of start/negotiate, the poll, the teardown, what is rendered when - is
 * driven in tests through a fake peer.
 */
export interface MirrorPeer {
  /** createOffer returns a complete offer: the SDP carries its candidates already. */
  createOffer(): Promise<string>
  acceptAnswer(answerSdp: string): Promise<void>
  onStream(listener: (stream: MediaStream) => void): void
  close(): void
}

export type MirrorPeerFactory = () => MirrorPeer

/**
 * iceGatheringTimeoutMs bounds waiting for a complete offer. The offer is sent
 * untrickled, so its candidates have to be gathered first; a host that never
 * reports completion must not hold the stream open forever, and an offer without
 * candidates fails the handshake visibly rather than silently.
 */
export const iceGatheringTimeoutMs = 3_000

export function browserMirrorPeerFactory(): MirrorPeer {
  const peer = new RTCPeerConnection({ iceServers: [] })
  // recvonly: this console watches a device's screen and publishes nothing back.
  peer.addTransceiver("video", { direction: "recvonly" })
  const streamListeners: ((stream: MediaStream) => void)[] = []
  peer.addEventListener("track", (event) => {
    const media = event.streams[0]
    if (!media) return
    for (const listener of streamListeners) listener(media)
  })
  return {
    async createOffer() {
      const offer = await peer.createOffer()
      await peer.setLocalDescription(offer)
      await waitForIceGathering(peer)
      return peer.localDescription?.sdp ?? offer.sdp ?? ""
    },
    async acceptAnswer(answerSdp: string) {
      await peer.setRemoteDescription({ type: "answer", sdp: answerSdp })
    },
    onStream(listener) {
      streamListeners.push(listener)
    },
    close() {
      try {
        peer.close()
      } catch {
        // A peer that was already closed is not an error worth reporting: the
        // stream is being torn down either way.
      }
    },
  }
}

async function waitForIceGathering(peer: RTCPeerConnection): Promise<void> {
  if (peer.iceGatheringState === "complete") return
  await new Promise<void>((resolve) => {
    const finish = () => {
      clearTimeout(timer)
      peer.removeEventListener("icegatheringstatechange", check)
      resolve()
    }
    const check = () => {
      if (peer.iceGatheringState === "complete") finish()
    }
    const timer = setTimeout(finish, iceGatheringTimeoutMs)
    peer.addEventListener("icegatheringstatechange", check)
  })
}

export interface UseLiveMirrorOptions {
  /** client is the control plane's live mirror surface. Absent means this console has none. */
  client?: LiveMirrorClient
  workspaceId?: string
  /** peerFactory is the seam a test supplies in place of the browser's WebRTC stack. */
  peerFactory?: MirrorPeerFactory
  /** pollIntervalMs is how often the stream's own state is read while it is open. */
  pollIntervalMs?: number
  /**
   * pollFailureLimit is how many consecutive reads may fail before the console
   * stops claiming to show a live stream. A stream whose state is no longer known
   * is not live: after this many failures the picture is taken down and the
   * operator is told, rather than being left with a frame nobody can vouch for.
   */
  pollFailureLimit?: number
}

export interface LiveMirrorSession {
  phase: LiveMirrorPhase
  stream: LiveStreamView | null
  /** failure is why the stream is not being shown, when it is not. */
  failure: string
  /** attachVideo is the video element the stream is painted into. */
  attachVideo: (element: HTMLVideoElement | null) => void
  retry: () => void
  stop: () => void
}

export const defaultMirrorPollIntervalMs = 1_000
export const defaultMirrorPollFailureLimit = 2

export function useLiveMirror(deviceId: string, options: UseLiveMirrorOptions = {}): LiveMirrorSession {
  const { client, workspaceId = "", peerFactory, pollIntervalMs = defaultMirrorPollIntervalMs, pollFailureLimit = defaultMirrorPollFailureLimit } = options
  const [phase, setPhase] = useState<LiveMirrorPhase>("idle")
  const [stream, setStream] = useState<LiveStreamView | null>(null)
  const [failure, setFailure] = useState("")
  const [attempt, setAttempt] = useState(0)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const teardownRef = useRef<(() => void) | null>(null)

  const attachVideo = useCallback((element: HTMLVideoElement | null) => {
    videoRef.current = element
  }, [])
  const retry = useCallback(() => setAttempt((current) => current + 1), [])
  const stop = useCallback(() => {
    const teardown = teardownRef.current
    teardownRef.current = null
    teardown?.()
  }, [])

  useEffect(() => {
    if (!deviceId) {
      setPhase("idle")
      setStream(null)
      setFailure("")
      return
    }
    if (!client) {
      setPhase("unavailable")
      setStream(null)
      setFailure("")
      return
    }
    let disposed = false
    let settled = false
    let streamId = ""
    let peer: MirrorPeer | null = null
    let timer: ReturnType<typeof setInterval> | undefined
    let pollFailures = 0

    const closePeer = () => {
      peer?.close()
      peer = null
      const element = videoRef.current
      // The picture is dropped before the console says anything about it: a video
      // element left holding its last frame is exactly the frozen frame an
      // operator must never be shown in place of a device's screen.
      if (element) element.srcObject = null
    }
    const stopPolling = () => {
      if (timer === undefined) return
      clearInterval(timer)
      timer = undefined
    }
    /** release tells the control plane to stop carrying the device, once. */
    const release = () => {
      const ended = streamId
      streamId = ""
      if (!ended) return
      // A stream the control plane has already forgotten answers not-found, and
      // that is not a failure of the ending.
      void client.stopStream(ended).catch(() => undefined)
    }
    /** finish tears the session down completely and only then states how it ended. */
    const finish = (next: LiveMirrorPhase, reason: string) => {
      if (settled) return
      settled = true
      stopPolling()
      closePeer()
      release()
      setPhase(next)
      setFailure(reason)
    }

    setPhase("opening")
    setFailure("")

    function poll() {
      void client!.getStream(streamId).then((next) => {
        if (disposed || settled) return
        pollFailures = 0
        setStream(next)
        if (next.state === "failed") {
          finish("failed", next.failure || liveMirrorCopy.phase.failed)
          return
        }
        if (next.state === "ended") {
          finish("ended", "")
          return
        }
        // LIVE is the control plane reporting pictures carried, never something
        // this console infers from a peer connection.
        setPhase(next.state === "live" ? "live" : "starting")
      }).catch((cause: unknown) => {
        if (disposed || settled) return
        if (isNotFound(cause)) {
          finish("ended", "")
          return
        }
        pollFailures += 1
        if (pollFailures >= pollFailureLimit) finish("failed", liveMirrorCopy.failure.lost)
      })
    }

    void (async () => {
      try {
        const opened = await client.startStream({ workspaceId, deviceId })
        if (disposed) {
          void client.stopStream(opened.streamId).catch(() => undefined)
          return
        }
        streamId = opened.streamId
        setStream(opened)
        // A stream that is already over is reported as over: an operator who
        // opened a device whose session ended must not be shown a surface that is
        // waiting for a picture that will never come.
        if (opened.state === "failed") {
          finish("failed", opened.failure || liveMirrorCopy.phase.failed)
          return
        }
        if (opened.state === "ended") {
          finish("ended", "")
          return
        }
        const created = peerFactory ? peerFactory() : browserMirrorPeerFactory()
        peer = created
        created.onStream((media) => {
          const element = videoRef.current
          if (!element) return
          element.srcObject = media
          // Autoplay can be refused and jsdom does not implement play() at all.
          // Neither is a stream failure: the picture is in the element either way,
          // and a refusal is the operator's own browser policy, not a broken stream.
          void element.play?.()?.catch(() => undefined)
        })
        const offer = await created.createOffer()
        if (disposed || settled) return
        const answer = await client.negotiate(streamId, offer)
        if (disposed || settled) return
        setStream(answer.stream)
        // The same rule for the stream the handshake returns: an answer that
        // carries a stream which is already over is reported as over rather than
        // accepted as a picture that is coming.
        if (answer.stream.state === "failed") {
          finish("failed", answer.stream.failure || liveMirrorCopy.phase.failed)
          return
        }
        if (answer.stream.state === "ended") {
          finish("ended", "")
          return
        }
        await created.acceptAnswer(answer.answerSdp)
        if (disposed || settled) return
        setPhase(answer.stream.state === "live" ? "live" : "starting")
        timer = setInterval(poll, pollIntervalMs)
      } catch (cause: unknown) {
        if (!disposed) finish("failed", errorSentence(cause))
      }
    })()

    teardownRef.current = () => finish("ended", "")

    return () => {
      disposed = true
      stopPolling()
      closePeer()
      release()
      teardownRef.current = null
    }
  }, [attempt, client, deviceId, peerFactory, pollFailureLimit, pollIntervalMs, workspaceId])

  return { phase, stream, failure, attachVideo, retry, stop }
}

function isNotFound(cause: unknown): boolean {
  return cause instanceof ConnectJsonError && cause.code === "not_found"
}

function errorSentence(cause: unknown): string {
  if (cause instanceof ConnectJsonError && cause.message.trim() !== "") return cause.message
  if (cause instanceof Error && cause.message.trim() !== "") return cause.message
  return liveMirrorCopy.failure.openFailed
}
