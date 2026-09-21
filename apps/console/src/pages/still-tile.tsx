import type { DeviceView } from "@/lib/domain/control-plane"
import { gridStillClassified, gridTileSentence, stillPictureShape, stillTileShape, type GridProfileView, type GridTileStill } from "@/lib/grid-stills"
import driftNowMark from "../../src-tauri/icons/128x128.png"

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
 * Four things are deliberate:
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
 *    sentence is not a named failure - and never a generic "failed";
 *  - the picture is drawn at the DEVICE's own shape and takes the frame's whole
 *    width, so a tile IS the phone rather than a phone-shaped picture centred in a
 *    wider box. See `stillPictureShape` and `stillTileShape` in `@/lib/grid-stills`.
 */
export interface StillTileProps {
  device: DeviceView
  /** tile is what this console holds for this device: the plane's own state, and a picture only while it is current. */
  tile: GridTileStill
  /** profile is what the plane published about its cost, which is what a device its sweep did not reach is told. */
  profile: GridProfileView | null
  /**
   * orientation is how the workspace draws its frames. A landscape frame is a frame
   * TURNED, so the picture is drawn turned with it: a portrait screen laid into a
   * wide frame either leaves the frame's own colour down both sides or is cropped,
   * and a cropped device screen is a frame an operator can mistake for the whole
   * screen.
   */
  orientation: "portrait" | "landscape"
}

export function StillTile({ device, tile, profile, orientation }: StillTileProps) {
  const sentence = gridTileSentence(tile, profile)
  const paints = tile.picture !== null
  // The shape the picture is drawn at, and the picture's own shape: a landscape
  // frame is the device's screen turned, so the two differ only by the turn.
  const drawn = stillTileShape(tile, orientation)
  const shape = stillPictureShape(tile)
  const turned = orientation === "landscape"
  return <>
    {/* The picture's box IS the tile's shape: it takes the frame's whole width - no
        padding and no inset of its own, which is what leaves a frame's own colour
        down both sides of a narrower picture - and its height is the device's own.
        It is mounted for the whole lifetime of the tile, so a tile with no picture
        still draws the shape of the frame it is in. */}
    <span
      data-testid={`still-tile-picture-${device.id}`}
      className="relative block w-full"
      style={{ aspectRatio: `${drawn.width} / ${drawn.height}` }}
    >
      <img
        data-testid={`still-tile-image-${device.id}`}
        data-tile-state={tile.state}
        src={tile.picture ?? undefined}
        alt=""
        aria-hidden="true"
        draggable={false}
        // The picture fills the box it is given, and `object-contain` is what keeps
        // that honest: the box and the picture are the same shape, so nothing is
        // letterboxed and nothing is cropped, and a picture of another shape than
        // the box would be inset rather than stretched. A turned frame lays the
        // picture out at its own shape and turns it about the box's centre, so the
        // turned picture fills the turned box exactly.
        className={`absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 object-contain ${turned ? "rotate-90" : ""} ${paints ? "" : "invisible"}`}
        style={{
          width: turned ? `${(shape.width / shape.height) * 100}%` : "100%",
          height: turned ? `${(shape.height / shape.width) * 100}%` : "100%",
        }}
      />
      {tile.state === "pending" ? <span
        data-testid={`still-tile-loader-${device.id}`}
        role="status"
        aria-label={`${device.displayName} is loading its first still`}
        title={`${device.displayName} is loading its first still`}
        className="pointer-events-none absolute inset-0 grid place-items-center"
      ><span className="grid size-12 place-items-center"><img src={driftNowMark} alt="" aria-hidden="true" draggable={false} className="drift-device-loader-mark size-full" /></span></span> : null}
    </span>
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
