import { useEffect, useRef, useState } from "react"
import type { GridPreviewClient } from "@/lib/api/control-plane-clients"
import { browserMirrorSchedule, mirrorRetryDelayMs, type MirrorSchedule } from "@/lib/api/use-live-mirror"
import { gridStillsAnswer, type GridProfileView, type GridStillView } from "@/lib/grid-stills"

/**
 * The fleet grid's stills, as the console holds them.
 *
 * A tile used to be a viewer of a device's live path, so the number of tiles a
 * grid could carry was the control plane's own device-session capacity - which is
 * a session the operator's big frame is kept out of, and which cannot reach
 * "every device" at any preview level. A still spends no session and holds no
 * encoder, so this hook carries ONE STILL PER DEVICE the grid names, with no
 * allocation, no cap and nothing dropped: what bounds the grid is the plane's own
 * sweep and cadence, and the only number that could ever refuse a device is the
 * plane's, stated in the tile it did not reach.
 *
 * Two rules decide everything else here, and they are the same two the live path
 * learned the hard way:
 *
 *  - a read this console could not complete is a fact about this console's REACH,
 *    never a report about a device. So a failed read leaves every still exactly
 *    where it was and only stops this console CLAIMING anything (its own
 *    `unreadable`), and the read is retried on a bounded backoff rather than
 *    hammering a plane that is not answering;
 *  - the plane owns the cadence, so this console POLLS the reconciliation it names
 *    rather than driving a capture per device: one call states the set the grid is
 *    drawing and reads what the plane's own sweeps produced. The interval it polls
 *    at is the cadence the PLANE published, never a number invented here, because
 *    asking faster than the plane captures would only ask the same still again.
 */
export interface GridStillsRead {
  /** atMs is when the last read settled, on this console's own clock. 0 before one has. */
  atMs: number
  /** failure is why the last read could not be completed, empty when it was. */
  failure: string
}

export interface GridStillsView {
  /** byDeviceId is the last still the plane reported for each device, held WHOLE per answer rather than merged. */
  byDeviceId: Readonly<Record<string, GridStillView>>
  /** profile is what the plane published about its cost, or null when it published none this console could read. */
  profile: GridProfileView | null
  /** refusedDeviceIds are the devices the plane's own sweep bound did not reach, so a tile can say which bound it did not pass. */
  refusedDeviceIds: readonly string[]
  /**
   * unreadable is this console's own state: the last read did not complete, so
   * nothing is claimed about any device. The stills already held are left alone.
   */
  unreadable: boolean
  /** reading is when the plane was last read and why that read did not complete, when it did not. */
  reading: GridStillsRead
}

/**
 * The cadence this console asks at until the plane has stated its own.
 *
 * It is the plane's documented default rather than a preference: the console has
 * nothing to read before the first answer arrives, so the first re-ask is a
 * placeholder - and every ask after it uses the cadence the plane published.
 */
export const defaultGridCadenceMillis = 4_000

/**
 * The bound a re-ask after a read this console could not complete doubles up to.
 *
 * It is a small multiple of the plane's default cadence on purpose: the backoff
 * exists to stop a console hammering a plane that is not answering, NOT to make it
 * wait longer than an operator would notice before it asks again.
 */
export const defaultGridRetryCeilingMs = 16_000

export interface UseGridStillsOptions {
  /** client is the control plane's grid still-preview surface. Absent means this console has none. */
  client?: GridPreviewClient
  workspaceId: string
  /**
   * deviceIds are the devices the grid is drawing, in the grid's own order. They
   * are read through a ref rather than depended on: the plane RECONCILES, so a
   * filter change is picked up by the next sweep without tearing the poll down and
   * without releasing a capture set the next request is about to name again.
   */
  deviceIds: readonly string[]
  /** schedule is the seam a test supplies in place of the browser's timers, so the waits below are asserted rather than waited for. */
  schedule?: MirrorSchedule
  /** now is the seam a test supplies in place of the wall clock, so a still's age is a number the case chose. */
  now?: () => number
  /** retryCeilingMs is the bound the re-ask doubles up to (see `defaultGridRetryCeilingMs`). */
  retryCeilingMs?: number
}

/** What a console with no control plane behind it has to say about its grid. */
const noControlPlaneReading = "this console has no control plane to read a grid still from."

interface GridStillsHeld {
  byDeviceId: Record<string, GridStillView>
  profile: GridProfileView | null
  refusedDeviceIds: readonly string[]
  unreadable: boolean
  failure: string
  atMs: number
}

const NOT_YET_READ: GridStillsHeld = { byDeviceId: {}, profile: null, refusedDeviceIds: [], unreadable: false, failure: "", atMs: 0 }

/**
 * useGridStills names every device it is given to the control plane, reads the
 * still held for each on the plane's own cadence, and releases the capture set when
 * the grid goes away.
 */
export function useGridStills(options: UseGridStillsOptions): GridStillsView {
  const { client, workspaceId, deviceIds, schedule = browserMirrorSchedule, now = Date.now, retryCeilingMs = defaultGridRetryCeilingMs } = options
  const [held, setHeld] = useState<GridStillsHeld>(NOT_YET_READ)
  const idsRef = useRef<readonly string[]>(deviceIds)
  // The clock is read through a ref for the same reason the grid's list is: a caller
  // that passes an inline function would otherwise re-open the poll on every render,
  // which is a read per render of the grid rather than one per cadence.
  const nowRef = useRef(now)
  useEffect(() => {
    nowRef.current = now
  })
  // The grid's own list, as the poll's next value. It is written in an effect
  // declared BEFORE the poll's, so the first read of a mount already names the
  // devices that mount is drawing, and a later change is named by the next sweep.
  useEffect(() => {
    idsRef.current = deviceIds
  })

  // A console with no control plane is not a plane with no stills: it states which
  // fact is missing, claims nothing about any device, and opens nothing. That is a
  // fact about the console it can state while it renders, so nothing is written to
  // the held answer for it - a state write here would be a re-render for an answer
  // the render already has.
  const noPlane = client === undefined || workspaceId.trim() === ""

  useEffect(() => {
    if (noPlane) return
    let disposed = false
    let cancelScheduled: (() => void) | null = null
    let inFlight: Promise<unknown> | null = null
    let readFailures = 0
    // The cadence the plane stated, once it has stated one. Until then the
    // documented default is what this console asks at, and every ask after the
    // first answer uses the plane's own number.
    let cadence = defaultGridCadenceMillis

    const cancelRead = () => {
      cancelScheduled?.()
      cancelScheduled = null
    }
    /** readAgain asks the plane again after `delayMs`, which is all a console whose read failed does: nothing is taken down and nothing is stopped. */
    const readAgain = (delayMs: number) => {
      cancelRead()
      cancelScheduled = schedule(delayMs, () => { void read() })
    }

    async function read() {
      if (disposed) return
      // EVERY device, in the grid's own order, with nothing dropped: this list is
      // the plane's next capture set, and a console that shortened it would stop
      // capturing every device past the cut.
      const asked = [...idsRef.current]
      try {
        const request = client!.syncGridPreviews({ workspaceId, deviceIds: asked })
        // Held so the release below can wait for it: see the cleanup.
        inFlight = request
        const answer = await request
        inFlight = null
        if (disposed) return
        readFailures = 0
        const resolved = gridStillsAnswer(answer, nowRef.current())
        if (resolved.profile && resolved.profile.cadenceMillis > 0) cadence = resolved.profile.cadenceMillis
        setHeld({ byDeviceId: resolved.byDeviceId, profile: resolved.profile, refusedDeviceIds: resolved.refusedDeviceIds, unreadable: false, failure: "", atMs: nowRef.current() })
        readAgain(cadence)
      } catch (cause: unknown) {
        inFlight = null
        if (disposed) return
        readFailures += 1
        // The stills are LEFT WHERE THEY ARE and this console only stops claiming
        // them: a read that could not be completed says nothing about any device, so
        // withdrawing a picture on it would turn a two-second hiccup in the control
        // plane into a grid-wide outage of the operator's own reading. The tiles say
        // the console has no report, and the read is asked again on a bounded
        // backoff.
        setHeld((current) => ({ ...current, unreadable: true, failure: readFailureSentence(cause), atMs: nowRef.current() }))
        readAgain(mirrorRetryDelayMs(readFailures, cadence, retryCeilingMs))
      }
    }

    void read()

    return () => {
      disposed = true
      cancelRead()
      // The capture set is released where the grid goes away: a device nothing is
      // showing must not stay captured. The stop is issued AFTER any read still in
      // flight has settled, because the plane reconciles from the last request it
      // receives - a stop sent while a sync is on the wire would be overtaken by
      // it, and a plane left capturing a grid that no longer exists is exactly the
      // work this release exists to end. A stop that fails changes nothing a reader
      // can see, so it is best-effort and never reported as a grid state.
      const settled = inFlight ?? Promise.resolve()
      void settled.catch(() => undefined).then(() => client.stopGridPreviews(workspaceId).catch(() => undefined))
    }
  }, [client, noPlane, retryCeilingMs, schedule, workspaceId])

  if (noPlane) return { byDeviceId: {}, profile: null, refusedDeviceIds: [], unreadable: true, reading: { atMs: 0, failure: noControlPlaneReading } }

  return {
    byDeviceId: held.byDeviceId,
    profile: held.profile,
    refusedDeviceIds: held.refusedDeviceIds,
    unreadable: held.unreadable,
    reading: { atMs: held.atMs, failure: held.failure },
  }
}

function readFailureSentence(cause: unknown): string {
  if (cause instanceof Error && cause.message.trim() !== "") return cause.message
  if (typeof cause === "string" && cause.trim() !== "") return cause
  return "the read did not complete and no reason was stated for it."
}
