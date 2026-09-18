import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import type { DeviceView } from "@/lib/domain/control-plane"
import type { LiveMirrorTransportChoice } from "@/lib/live-mirror"
import { tilePictureSentence } from "@/lib/live-tiles"

/**
 * One fleet tile's live picture.
 *
 * It is a viewer and only a viewer: the tile subscribes to the device's own live
 * path, paints what arrives into its own element, and has no input path, no
 * lease and no control session anywhere in it (AGENTS.md section 2). An operator
 * who wants to touch the device opens the big frame.
 *
 * Three things are deliberate:
 *
 *  - the tile carries a picture only while it holds one of the console's bounded
 *    viewer places (`viewing`). A tile the bound does not reach says so in the
 *    tile rather than quietly opening a fifth stream, which is what keeps the
 *    subscriber set the console's own decision rather than the grid's size;
 *  - a picture that is not live is not shown at all. The last frame a stream
 *    carried is not the device's screen now, and a still image sitting in a tile
 *    reads as exactly that, so the element is dropped and the tile's own
 *    sentence takes its place;
 *  - the sentence a tile shows is the classified one: a tile whose stream failed
 *    reports the control plane's own reason, the same sentence the big frame
 *    reports, and never a generic failure.
 */
export interface LiveTilePictureProps {
  device: DeviceView
  /** mirror is the control plane's live mirror surface; absent means this console has none. */
  mirror?: LiveMirrorClient
  /** transport is the transport Console Settings chose for this console's streams. */
  transport?: LiveMirrorTransportChoice
  workspaceId: string
  /** viewing is whether this tile holds one of the console's bounded viewer places. */
  viewing: boolean
}

export function LiveTilePicture({ device, mirror, transport, workspaceId, viewing }: LiveTilePictureProps) {
  // The session is opened for this device only while this tile holds a place:
  // an empty device id keeps the hook idle, so a tile that is not viewing opens
  // nothing and captures nothing.
  const subscribes = viewing && Boolean(mirror)
  const { phase, failure, attachVideo } = useLiveMirror(subscribes ? device.id : "", { client: mirror, workspaceId, transport })
  const showing = phase === "live" || phase === "starting"
  const sentence = tilePictureSentence(phase, failure, device, viewing, Boolean(mirror))
  return <>
    {showing ? <video ref={attachVideo} data-testid={`live-tile-video-${device.id}`} muted playsInline autoPlay aria-hidden="true" className="absolute inset-0 size-full object-contain" /> : null}
    <span
      data-testid={`live-tile-state-${device.id}`}
      data-tile-state={phase}
      role={phase === "failed" ? "alert" : undefined}
      title={sentence.long}
      aria-label={sentence.long}
      className="pointer-events-none absolute inset-x-0 bottom-8 flex items-center justify-center gap-1 px-1 text-[8px] font-semibold uppercase tracking-wide text-white/90"
    >
      {phase === "live" ? <span aria-hidden="true" className="inline-block size-1.5 bg-emerald-400" /> : null}
      <span>{sentence.short}</span>
    </span>
  </>
}
