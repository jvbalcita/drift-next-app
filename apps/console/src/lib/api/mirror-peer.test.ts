// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest"
import { browserMirrorPeerFactory } from "@/lib/api/use-live-mirror"

type PeerEventListener = (event: unknown) => void

class FakeRTCPeerConnection {
  static latest: FakeRTCPeerConnection | null = null

  connectionState = "new" as RTCPeerConnectionState
  iceConnectionState = "new" as RTCIceConnectionState
  iceGatheringState = "complete" as RTCIceGatheringState
  localDescription: RTCSessionDescription | null = null
  private readonly listeners = new Map<string, PeerEventListener[]>()

  constructor() {
    FakeRTCPeerConnection.latest = this
  }

  addTransceiver(): void {}

  addEventListener(type: string, listener: PeerEventListener): void {
    const listeners = this.listeners.get(type) ?? []
    listeners.push(listener)
    this.listeners.set(type, listeners)
  }

  async createOffer(): Promise<RTCSessionDescriptionInit> {
    return { type: "offer", sdp: "offer-sdp" }
  }

  async setLocalDescription(description: RTCSessionDescriptionInit): Promise<void> {
    this.localDescription = { type: "offer", sdp: description.sdp } as RTCSessionDescription
  }

  async setRemoteDescription(): Promise<void> {}

  createDataChannel(): RTCDataChannel {
    return {} as RTCDataChannel
  }

  async getStats(): Promise<RTCStatsReport> {
    return new Map() as RTCStatsReport
  }

  close(): void {}

  emit(type: string, event: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }
}

class FakeMediaStream {
  constructor(readonly tracks: MediaStreamTrack[]) {}
}

afterEach(() => vi.unstubAllGlobals())

describe("the browser WebRTC peer", () => {
  it("rearms its render window when the remote track becomes active", () => {
    vi.stubGlobal("RTCPeerConnection", FakeRTCPeerConnection as unknown as typeof RTCPeerConnection)
    vi.stubGlobal("MediaStream", FakeMediaStream as unknown as typeof MediaStream)
    const peer = browserMirrorPeerFactory()
    const connection = FakeRTCPeerConnection.latest
    if (!connection) throw new Error("the browser peer was not constructed")

    const mediaActivity: string[] = []
    let receivedMedia: MediaStream | null = null
    peer.onMediaActivity?.(() => mediaActivity.push("active"))
    peer.onStream((media) => { receivedMedia = media })
    const trackEvent = { unmute: null as (() => void) | null }
    const track = {
      readyState: "live",
      muted: true,
      addEventListener(_type: string, listener: () => void) { trackEvent.unmute = listener },
      removeEventListener() {},
    } as unknown as MediaStreamTrack
    connection.emit("track", { track, streams: [] } as unknown as RTCTrackEvent)
    expect(mediaActivity).toEqual([])
    expect(receivedMedia).toBeInstanceOf(FakeMediaStream)
    expect((receivedMedia as unknown as FakeMediaStream).tracks).toEqual([track])

    trackEvent.unmute?.()
    expect(mediaActivity).toEqual(["active"])
    peer.close()
  })
})
