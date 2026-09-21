import type { DeviceView } from "@/lib/domain/control-plane"
import { gridStillClassified, gridTileSentence, type GridProfileView, type GridTileStill } from "@/lib/grid-stills"

/**
 * One fleet tile's still picture.
 *
 * It is a PICTURE and never a stream: the bytes are the control plane's own
 * bounded still, carried at the level the plane published, and nothing in here
 * opens, negotiates or ends anything. That is the whole point of the grid - a
 * still spends no device session, so a tile is not a viewer competing for one -
 * and the one live session this console opens is the big frame the operator works
 * a device from.
 *
 * Three things are deliberate:
 *
 *  - the element the picture is written into is mounted for the WHOLE lifetime of
 *    the tile, exactly as the big frame mounts its own, and what changes with the
 *    state is whether it is painted, never whether it exists. The picture arrives
 *    with the answer, so an element that appeared only once a tile was current
 *    would be an element that missed the still it was opened for;
 *  - a tile that is not CURRENT paints NO picture: the last still the plane holds
 *    is not the device's screen now, so the image is left with no source and made
 *    invisible rather than left showing a screen the device has moved on from. The
 *    one exception is stated in `gridTileStill`, and it is not an exception to
 *    this rule but to which state the tile is in: while the console cannot read
 *    the plane it is not claiming anything, and it withdraws nothing;
 *  - the sentence is the classified one. A tile whose capture failed carries the
 *    control plane's own class and detail as its accessible name and its tooltip -
 *    the whole sentence, because a tile is a hundred-odd pixels wide and a clipped
 *    sentence is not a named failure - and never a generic "failed".
 */
export interface StillTileProps {
  device: DeviceView
  /** tile is what this console holds for this device: the plane's own state, and a picture only while it is current. */
  tile: GridTileStill
  /** profile is what the plane published about its cost, which is what a device its sweep did not reach is told. */
  profile: GridProfileView | null
}

export function StillTile({ device, tile, profile }: StillTileProps) {
  const sentence = gridTileSentence(tile, profile)
  const paints = tile.picture !== null
  return <>
    <img
      data-testid={`still-tile-image-${device.id}`}
      data-tile-state={tile.state}
      src={tile.picture ?? undefined}
      alt=""
      aria-hidden="true"
      draggable={false}
      className={`absolute inset-0 size-full object-contain ${paints ? "" : "invisible"}`}
    />
    <span
      data-testid={`still-tile-state-${device.id}`}
      data-tile-state={tile.state}
      // An alert is for a failure the PLANE classified, and for nothing else: a
      // device waiting for its first still, one past the plane's sweep bound and a
      // read this console could not complete are not failures of a device, and
      // announcing them as alerts would train an operator to ignore the one that is.
      role={gridStillClassified(tile) ? "alert" : undefined}
      title={sentence.long}
      aria-label={sentence.long}
      className="pointer-events-none absolute inset-x-0 bottom-8 flex items-center justify-center gap-1 px-1 text-[8px] font-semibold uppercase tracking-wide text-white/90"
    >
      {tile.state === "current" ? <span aria-hidden="true" className="inline-block size-1.5 bg-emerald-400" /> : null}
      <span>{sentence.short}</span>
    </span>
  </>
}
