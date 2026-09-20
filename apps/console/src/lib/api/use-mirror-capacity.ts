import { useEffect, useState } from "react"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import type { MirrorCapacityView } from "@/lib/live-mirror"

/**
 * The control plane's live-stream capacity, as this console read it.
 *
 * The grid's tile allocation hangs off this reading, so the two things it has to
 * keep straight are that it is a READ of the plane's own bound rather than a number
 * this console keeps, and that a reading which did not arrive is reported as a
 * reading that did not arrive rather than as a plane with no room in it. Those lead
 * to the same place - the grid carries no pictures - and they are not the same fact,
 * so a tile says which one it is looking at.
 */
export interface MirrorCapacityReading {
  /** capacity is the plane's own bound, or null when this console could not read it. */
  capacity: MirrorCapacityView | null
  /** failure is why the capacity is not known, when it is not. Empty when it is. */
  failure: string
}

/** What a console with no control plane behind it has to say about a capacity. */
const noControlPlaneReading = "this console has no control plane to read a live-stream capacity from."

/** NOT_YET_READ is the reading before one has arrived, which is not a plane with no room in it. */
const NOT_YET_READ: MirrorCapacityReading = { capacity: null, failure: "" }

/**
 * useMirrorCapacity reads the plane's capacity once for the surface that allocates
 * tiles.
 *
 * Once, and not on a poll: the bound belongs to the DEPLOYMENT - it is read from the
 * plane's configuration when the plane starts - so it cannot change while a console
 * is open, and re-reading it every few seconds would only add a request whose answer
 * is known. A console opened against a plane whose capacity has since changed reads
 * the new bound the next time it is opened, which is the same moment the tiles are
 * allocated.
 */
export function useMirrorCapacity(mirror?: LiveMirrorClient, workspaceId = ""): MirrorCapacityReading {
  const [reading, setReading] = useState<MirrorCapacityReading>(NOT_YET_READ)
  useEffect(() => {
    if (!mirror) return
    let cancelled = false
    mirror.getCapacity(workspaceId).then(
      (capacity) => { if (!cancelled) setReading({ capacity, failure: "" }) },
      (error: unknown) => {
        if (cancelled) return
        setReading({ capacity: null, failure: error instanceof Error ? error.message : String(error) })
      },
    )
    return () => { cancelled = true }
  }, [mirror, workspaceId])
  // A console with no control plane behind it is not a plane whose capacity is zero:
  // nothing is stated, and the failure says so - derived here rather than written into
  // state from an effect, so the answer does not depend on a render the effect caused.
  if (!mirror) return { capacity: null, failure: noControlPlaneReading }
  return reading
}
