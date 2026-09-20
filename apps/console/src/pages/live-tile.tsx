import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useLiveMirror } from "@/lib/api/use-live-mirror"
import type { DeviceView } from "@/lib/domain/control-plane"
import type { LiveMirrorTransportChoice } from "@/lib/live-mirror"
import { tilePictureSentence, type TileViewerBudget } from "@/lib/live-tiles"

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
 *  - the video element is mounted for the whole lifetime of the SUBSCRIPTION,
 *    exactly as the big frame mounts its own. It is the element the device's
 *    picture is written into, so an element that appears only once the stream
 *    says it is up is an element that can miss the picture it was opened for: the
 *    track arrives with the handshake, and a tile that mounts its element after
 *    the handshake drops that first picture and shows its background colour under
 *    a state label reading Live. What keeps a stale frame off the grid is not the
 *    element's absence but the teardown: the session clears the element's source
 *    before it states that a stream ended or failed, so an element that outlives
 *    its stream is empty;
 *  - a picture that is not live is not SHOWN. `showing` still gates what is
 *    painted rather than what is mounted, so a tile whose stream has not carried
 *    a picture yet, or has failed, draws no picture over the device's colour and
 *    reports the state it is in;
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
  /**
   * budget is what the CONTROL PLANE stated this console may carry, which is what
   * decides how many tiles subscribe. It is passed down rather than derived here so
   * that every tile in one grid reads the same number, and so a tile that holds no
   * place says which bound it did not reach.
   */
  budget: TileViewerBudget
}

export function LiveTilePicture({ device, mirror, transport, workspaceId, viewing, budget }: LiveTilePictureProps) {
  // The session is opened for this device only while this tile holds a place:
  // an empty device id keeps the hook idle, so a tile that is not viewing opens
  // nothing and captures nothing. The element below follows the same condition,
  // so a tile that subscribes is an element the picture can be written into from
  // the first frame the handshake delivers.
  const subscribes = viewing && Boolean(mirror)
  // A tile states its PURPOSE because the plane's capacity is spent per purpose and
  // its reserve is the place the operator's own frame needs: this is a picture and
  // nothing else, so it is an ambient viewer, and it is the kind the plane may
  // refuse when the grid's share is spent.
  const { phase, failure, attachVideo } = useLiveMirror(subscribes ? device.id : "", { client: mirror, workspaceId, transport, purpose: "ambient" })
  const showing = phase === "live" || phase === "starting"
  const sentence = tilePictureSentence(phase, failure, device, viewing, Boolean(mirror), budget)
  return <>
    {subscribes ? <video ref={attachVideo} data-testid={`live-tile-video-${device.id}`} muted playsInline autoPlay aria-hidden="true" className={`absolute inset-0 size-full object-contain ${showing ? "" : "invisible"}`} /> : null}
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
