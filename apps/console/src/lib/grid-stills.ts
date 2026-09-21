import { GridStillLevel, GridStillState, type GridPreviewProfile, type GridStill, type SyncGridPreviewsResponse } from "@/gen/drift/v1/grid_preview_pb"

/**
 * The fleet grid's still previews: the console-side model and the copy.
 *
 * A tile in this grid is no longer a viewer of a device's live path. It is a
 * PICTURE the control plane captured on its own cadence, so the whole of what this
 * module has to keep straight is that a still is not a stream: a still spends no
 * device session (so there is no bound on how many tiles a grid may draw), it is
 * only ever the device's screen AS OF the plane's last successful capture, and a
 * picture the plane no longer calls CURRENT is not the device's screen at all and
 * must not be painted. The one live session this console still opens is the
 * operator's own big frame, in `live-mirror`.
 *
 * Nothing here is React and nothing here reaches the network: the wire types are
 * resolved into this console's own views, and every sentence an operator reads is
 * a function of those views, so the classification is testable on its own rather
 * than by reading a rendered tile.
 *
 * The rule every sentence below obeys: never say a still is live, never call it a
 * stream, and never present it as continuous. A reader who cannot tell a still from
 * a stream cannot tell a picture taken four seconds ago from a device's screen now.
 */

/**
 * A still's state as this console renders it, read from the PLANE's own vocabulary
 * rather than derived here: the plane holds both facts that decide it - whether a
 * capture succeeded and whether a later attempt failed - so a console that derived
 * the state from a timestamp would be a second opinion about the same device.
 */
export type GridStillStateName = "pending" | "current" | "stale" | "unavailable"

/**
 * GridStillView is one device's still as this console holds it.
 *
 * `picture` is the ONLY field that may be painted, and it is present only while
 * the plane reports the still CURRENT: a still that is pending, stale or
 * unavailable has no picture because the plane has no picture that is this
 * device's screen now, not because the bytes were inconvenient to carry.
 */
export interface GridStillView {
  deviceId: string
  state: GridStillStateName
  /** picture is the still as a `data:` URL, built from the plane's own media type and bytes. Null for every state but current. */
  picture: string | null
  /** capturedAt is the plane's own timestamp for the still's capture, RFC3339 in UTC. Empty when nothing was captured. */
  capturedAt: string
  /** width and height are the DELIVERED still's size: the level's cap applied to the device's screen. */
  width: number
  height: number
  /** bytes is the delivered still's size, and sourceBytes the capture's own size before the level was applied. */
  bytes: number
  sourceBytes: number
  /** ageMs is how long ago the plane captured this still, from its own capturedAt. Null when nothing has been captured. */
  ageMs: number | null
  frames: number
  failures: number
  /** failureClass and failureDetail are the plane's MOST RECENT capture failure, empty when that attempt succeeded. */
  failureClass: string
  failureDetail: string
  /** observedCadenceMillis is the interval the plane MEASURED between this device's last two captures: 0 until two succeeded. */
  observedCadenceMillis: number
  /** truncated reports a capture larger than the plane's still bound, which is delivered as no picture rather than as a partial one. */
  truncated: boolean
  /**
   * noPictureReason is THIS CONSOLE's sentence for a tile that carries no picture
   * for a reason the plane's answer cannot state itself: a still the plane called
   * CURRENT without the bytes to draw it, or an answer that stated no state at all.
   * It is empty wherever the plane explained itself, so the plane's own words win
   * every time it gave them.
   */
  noPictureReason: string
}

/** The level a still is carried at, named as the plane named it. */
export type GridStillLevelName = "unspecified" | "low" | "medium" | "high"

/**
 * GridProfileView is what this plane's grid costs and what it carries stills at,
 * as the plane published it.
 *
 * The level's numbers travel beside the level rather than being looked up here: a
 * tile told "medium" cannot say whether its picture is 360 or 720 pixels wide, and
 * the difference is the whole reason a deployment chooses a level. There is
 * deliberately no session capacity in it - a still spends no session.
 */
export interface GridProfileView {
  /** cadenceMillis is how often the plane aims to capture each device. */
  cadenceMillis: number
  level: GridStillLevelName
  levelMaxWidth: number
  levelJpegQuality: number
  /** stillByteBound is the largest still the plane delivers for one tile. */
  stillByteBound: number
  /** maxDevices is how many devices ONE SWEEP may carry: a bound on the plane's work, never a session bound and never a cap on what the grid may draw. */
  maxDevices: number
  /** subscribed is how many devices the plane is capturing for this grid. */
  subscribed: number
}

/**
 * gridStillStateOf reads the plane's state into this console's vocabulary.
 *
 * The switch is exhaustive on purpose: a value this console has not been taught is
 * a compile error rather than a silently wrong reading, and UNSPECIFIED - the
 * plane declining to state a state - resolves to "no picture" rather than to
 * anything a tile could paint. A still nothing classified is never shown.
 */
function gridStillStateOf(state: GridStillState): GridStillStateName {
  switch (state) {
    case GridStillState.PENDING:
      return "pending"
    case GridStillState.CURRENT:
      return "current"
    case GridStillState.STALE:
      return "stale"
    case GridStillState.UNAVAILABLE:
    case GridStillState.UNSPECIFIED:
      return "unavailable"
  }
}

/** gridStillLevelOf names the plane's level. UNSPECIFIED is stated as unstated rather than as a neighbouring level. */
function gridStillLevelOf(level: GridStillLevel): GridStillLevelName {
  switch (level) {
    case GridStillLevel.LOW:
      return "low"
    case GridStillLevel.MEDIUM:
      return "medium"
    case GridStillLevel.HIGH:
      return "high"
    case GridStillLevel.UNSPECIFIED:
      return "unspecified"
  }
}

/**
 * gridStillPicture builds the `data:` URL one still is drawn from, or says why it
 * cannot be drawn.
 *
 * The media type is the PLANE's, and a still that arrived without one is not
 * painted at a type this console guessed: a JPEG declared as a PNG paints as
 * nothing at all, which reads to an operator as a device with no picture. A still
 * the plane itself reported truncated is not painted either - a prefix of an image
 * is the wrong image delivered silently.
 */
function gridStillPicture(still: GridStill): { picture: string | null; reason: string } {
  const mediaType = still.mediaType.trim()
  const encoded = still.stillBase64.trim()
  if (still.truncated) {
    return { picture: null, reason: `the plane's capture for this device was larger than the bound it delivers a still within, so it sent no picture rather than a partial one` }
  }
  if (encoded === "") {
    return { picture: null, reason: "the plane reported this still as current and delivered no picture for it" }
  }
  if (mediaType === "") {
    return { picture: null, reason: "the plane delivered a still without stating what kind of image it is, so this console cannot paint it" }
  }
  return { picture: `data:${mediaType};base64,${encoded}`, reason: "" }
}

/**
 * gridStillView resolves one still the plane answered for one device.
 *
 * A state the plane called CURRENT without a picture to draw is reported as a
 * device this console has no picture for, not as a current one that happens to be
 * blank: the state decides the tile's words, and "Still 12s" over an empty frame
 * would claim a picture nobody has.
 */
export function gridStillView(still: GridStill, nowMs: number): GridStillView {
  const stated = gridStillStateOf(still.state)
  const painted = stated === "current" ? gridStillPicture(still) : { picture: null, reason: "" }
  const capturedAtMillis = Date.parse(still.capturedAt)
  const capturedAt = Number.isFinite(capturedAtMillis) ? capturedAtMillis : 0
  const state: GridStillStateName = stated === "current" && painted.picture === null ? "unavailable" : stated
  const unstated = still.state === GridStillState.UNSPECIFIED
  return {
    deviceId: still.deviceId,
    state,
    picture: painted.picture,
    capturedAt: still.capturedAt,
    width: still.width,
    height: still.height,
    bytes: still.bytes,
    sourceBytes: still.sourceBytes,
    ageMs: capturedAt > 0 ? Math.max(0, nowMs - capturedAt) : null,
    frames: still.frames,
    failures: still.failures,
    failureClass: still.failureClass,
    failureDetail: still.failureDetail,
    observedCadenceMillis: still.observedCadenceMillis,
    truncated: still.truncated,
    noPictureReason: painted.reason !== "" ? painted.reason : unstated ? "the plane answered without stating what this device's still is" : "",
  }
}

/** gridProfileView reads the plane's published profile, or nothing when the answer carried none. */
export function gridProfileView(profile: GridPreviewProfile | undefined): GridProfileView | null {
  if (!profile) return null
  return {
    cadenceMillis: profile.cadenceMillis,
    level: gridStillLevelOf(profile.level),
    levelMaxWidth: profile.levelMaxWidth,
    levelJpegQuality: profile.levelJpegQuality,
    stillByteBound: profile.stillByteBound,
    maxDevices: profile.maxDevices,
    subscribed: profile.subscribed,
  }
}

/** GridStillsAnswer is one reconciliation as this console holds it: a still per device, the plane's numbers, and the devices its sweep did not reach. */
export interface GridStillsAnswer {
  byDeviceId: Record<string, GridStillView>
  profile: GridProfileView | null
  refusedDeviceIds: readonly string[]
}

/**
 * gridStillsAnswer resolves a whole answer at once.
 *
 * The answer is held WHOLE rather than merged into what this console held before,
 * because the plane reconciles: the set it answered for is the set it is
 * capturing, so a device that has left the answer has left the capture set and a
 * device whose still went STALE has no picture any more. Merging would keep a
 * picture the plane has stopped calling current - the one thing a grid of stills
 * must not do.
 */
export function gridStillsAnswer(response: SyncGridPreviewsResponse, nowMs: number): GridStillsAnswer {
  const byDeviceId: Record<string, GridStillView> = {}
  for (const still of response.stills) byDeviceId[still.deviceId] = gridStillView(still, nowMs)
  return { byDeviceId, profile: gridProfileView(response.profile), refusedDeviceIds: [...response.refusedDeviceIds] }
}

/**
 * What a tile says about its still: the words drawn in the tile, and the whole
 * sentence they stand for.
 *
 * The two are not the same string, and the reason is the frame: a tile is a
 * device-shaped box a hundred-odd pixels wide, so the whole sentence cannot be
 * drawn in it without being clipped - and a clipped sentence is not a named
 * failure. The tile therefore draws a short mark and carries the whole sentence as
 * its own accessible name and its tooltip.
 */
export interface GridStillSentence {
  /** The words drawn inside the tile. */
  short: string
  /** The whole sentence, carried as the tile's accessible name and its tooltip. */
  long: string
}

/**
 * The copy a still tile renders, in one place because the text beside a control is
 * part of the control (AGENTS.md section 7).
 *
 * Each state's short mark is the same word every time, so a fleet scanned at a
 * glance reads one vocabulary; the sentences are built by the functions below,
 * because each of them carries a number or a reason the plane supplied.
 */
export const gridStillCopy = {
  /** A picture the plane captured and still calls current. */
  current: { short: "Still", prefix: "Still:" },
  /** The plane is capturing this device and nothing has been captured yet. */
  pending: { short: "Waiting", prefix: "Waiting:" },
  /** A capture succeeded and a later one failed: the picture the plane holds is not this device's screen. */
  stale: { short: "Not current", prefix: "Not current:" },
  /** This device cannot be captured at all, and the plane says why. */
  unavailable: { short: "No picture", prefix: "No picture:" },
  /** The plane's sweep bound did not reach this device. */
  refused: { short: "Not shown", prefix: "Not shown:" },
  /** The console has no report: a read it could not complete, or an answer that named nothing for this device. */
  unreadable: { short: "No report", prefix: "No report:" },
  /** The sentence a state falls back to when neither the plane nor the console has a reason to state. */
  unstatedReason: "neither the control plane nor this console gave a reason for it",
  /** The clause a tile uses when the plane's cadence could not be read. */
  unstatedCadence: "the control plane's own cadence, which this console could not read",
} as const

function plural(value: number, unit: string): string {
  return `${value} ${unit}${value === 1 ? "" : "s"}`
}

function rounded(value: number): number {
  return Math.round(value * 10) / 10
}

/** stillDurationSentence states a duration in the plane's own milliseconds as prose, so a cadence reads as a human wrote it. */
export function stillDurationSentence(millis: number): string {
  if (!Number.isFinite(millis) || millis <= 0) return ""
  const seconds = millis / 1000
  if (seconds < 1) return `${Math.max(1, Math.round(millis))} ms`
  if (seconds < 60) return plural(rounded(seconds), "second")
  const minutes = Math.floor(seconds / 60)
  const restSeconds = Math.round(seconds - minutes * 60)
  if (minutes < 60) return restSeconds === 0 ? plural(minutes, "minute") : `${plural(minutes, "minute")} ${plural(restSeconds, "second")}`
  const hours = Math.floor(minutes / 60)
  const restMinutes = minutes % 60
  return restMinutes === 0 ? plural(hours, "hour") : `${plural(hours, "hour")} ${plural(restMinutes, "minute")}`
}

/**
 * stillAgeShort is the age as it fits inside a tile: "3s", "1m 20s", "2h".
 *
 * It is empty for a still captured less than a second ago, because "Still 0s" in a
 * hundred-pixel frame reads as a number an operator has to decode rather than as a
 * picture taken just now.
 */
export function stillAgeShort(ageMs: number | null): string {
  if (ageMs === null || !Number.isFinite(ageMs) || ageMs < 1_000) return ""
  const seconds = Math.round(ageMs / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    const rest = seconds - minutes * 60
    return rest === 0 || minutes >= 10 ? `${minutes}m` : `${minutes}m ${rest}s`
  }
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

/** stillAgeSentence is the age as it is spoken in a tile's whole sentence. */
export function stillAgeSentence(ageMs: number | null): string {
  if (ageMs === null || !Number.isFinite(ageMs)) return "at a time the plane did not state"
  if (ageMs < 1_000) return "less than a second ago"
  return `${stillDurationSentence(ageMs)} ago`
}

/**
 * planeFailureSentence states a failure in the PLANE's own terms.
 *
 * It is the only place a tile's reason is composed, so no surface can report a
 * generic "failed" over a classification the plane supplied: the class the plane
 * grouped the failure under and the redacted detail it recorded are both carried,
 * and a tile whose plane gave one of them states the one it has rather than
 * inventing the other.
 */
export function planeFailureSentence(failureClass: string, failureDetail: string): string {
  const cls = failureClass.trim()
  const detail = failureDetail.trim()
  if (cls !== "" && detail !== "") return `${cls} - ${detail}`
  if (detail !== "") return detail
  if (cls !== "") return cls
  return gridStillCopy.unstatedReason
}

/**
 * deviceCadenceMillis is how often THIS device's still is refreshed: the interval
 * the plane MEASURED between its last two captures when it has measured one, and
 * the cadence the plane states it aims for otherwise. A tile states what happened
 * rather than only what was configured, and says which of the two it is.
 */
function cadenceClause(view: GridStillView, profile: GridProfileView | null): string {
  if (view.observedCadenceMillis > 0) {
    return `${stillDurationSentence(view.observedCadenceMillis)}, which is the interval the plane measured between this device's last two captures`
  }
  if (profile && profile.cadenceMillis > 0) {
    return `${stillDurationSentence(profile.cadenceMillis)}, which is the cadence the plane states it aims for`
  }
  return gridStillCopy.unstatedCadence
}

/** The level, named with the numbers the plane published beside it rather than by a label this console looked up. */
function levelClause(profile: GridProfileView | null): string {
  if (!profile) return "the level the plane carries its stills at, which this console could not read"
  if (profile.level === "unspecified") return `the level the plane did not name, ${profile.levelMaxWidth} px wide at JPEG quality ${profile.levelJpegQuality}`
  return `the ${profile.level} level, ${profile.levelMaxWidth} px wide at JPEG quality ${profile.levelJpegQuality}`
}

/**
 * currentStillSentence is the one sentence for a picture the plane calls current.
 *
 * It names the age of the picture and the cadence it is refreshed at, and it says
 * in the same breath that this is NOT a live stream - because the frame it is
 * drawn in is a device-shaped box, and a still in that box is indistinguishable
 * from the device's screen unless the sentence says which one it is.
 */
export function currentStillSentence(view: GridStillView, profile: GridProfileView | null): GridStillSentence {
  const age = stillAgeShort(view.ageMs)
  return {
    short: age === "" ? gridStillCopy.current.short : `${gridStillCopy.current.short} ${age}`,
    long: `${gridStillCopy.current.prefix} the control plane captured this device's screen ${stillAgeSentence(view.ageMs)} and refreshes the still about every ${cadenceClause(view, profile)}. This is a still picture and NOT a live stream: nothing here is continuous, and the device's live session - the one this console opens - is the big frame the operator works the device from.`,
  }
}

/**
 * pendingStillSentence is the sentence for a device the plane is capturing and has
 * not captured yet.
 *
 * It is stated as neither a failure nor a picture, because it is neither: a tile
 * that read as a failure here would send an operator looking for a device problem
 * on the plane's first sweep after every filter change.
 */
export function pendingStillSentence(): GridStillSentence {
  return {
    short: gridStillCopy.pending.short,
    long: `${gridStillCopy.pending.prefix} the control plane is capturing this device and has not delivered its first still yet, so this tile is not showing a picture. This is not a failure - the plane has not reported one - and it is not a picture either; the plane's next sweep will capture this device.`,
  }
}

/**
 * staleStillSentence is the sentence for a device whose last capture attempt
 * failed.
 *
 * The picture the plane holds is from an earlier capture, so it is not this
 * device's screen now and this console deliberately does not draw it: a grid that
 * kept the last picture on screen would show an operator a device that has moved
 * on. The plane's OWN class and detail are what the sentence carries - never a
 * generic "failed", which would tell an operator nothing they can act on.
 */
export function staleStillSentence(view: GridStillView): GridStillSentence {
  return {
    short: gridStillCopy.stale.short,
    long: `${gridStillCopy.stale.prefix} the control plane's most recent capture attempt for this device did not succeed (${planeFailureSentence(view.failureClass, view.failureDetail)}). The picture the plane holds is from an earlier capture, so it is not the device's screen now and this tile deliberately draws none; the plane keeps trying.`,
  }
}

/**
 * unavailableStillSentence is the sentence for a device the plane cannot show at
 * all, in the plane's own reason.
 *
 * Where the plane itself answered CURRENT without a picture to draw, or answered
 * without a state at all, the console's own reading of that answer is stated
 * instead - and it is a fact about the ANSWER, never a reason invented for the
 * device.
 */
export function unavailableStillSentence(view: GridStillView): GridStillSentence {
  const reason = view.noPictureReason.trim() !== "" ? view.noPictureReason.trim() : planeFailureSentence(view.failureClass, view.failureDetail)
  return {
    short: gridStillCopy.unavailable.short,
    long: `${gridStillCopy.unavailable.prefix} the control plane cannot show this device's screen - ${reason}. No picture is drawn, and this console is claiming nothing about the device beyond what the plane said.`,
  }
}

/**
 * refusedStillSentence is the sentence for a device the plane's SWEEP BOUND did not
 * reach.
 *
 * It names the bound and the level, because those are the two things an operator
 * can act on: how many devices one sweep carries is the deployment's own
 * `DRIFT_GRID_MAX_DEVICES`, and the level is what each still costs. It is
 * deliberately not a device failure and not a session bound: a still spends no
 * device session, so this device is not waiting for a place on the plane.
 */
export function refusedStillSentence(profile: GridProfileView | null): GridStillSentence {
  if (!profile) {
    return {
      short: gridStillCopy.refused.short,
      long: `${gridStillCopy.refused.prefix} the control plane's capture set did not reach this device, so it holds no still for it - and this console could not read the bound that decided it, so it names no number of its own.`,
    }
  }
  return {
    short: gridStillCopy.refused.short,
    long: `${gridStillCopy.refused.prefix} the control plane captures at most ${profile.maxDevices} device(s) in one sweep of the grid, and this device is past that bound. Each still is carried at ${levelClause(profile)}, and a still spends no device session - this is the plane's own bound on the work one sweep may do, not a place this device is waiting for.`,
  }
}

/**
 * unreadableStillSentence is the sentence for a tile this console has no report
 * for.
 *
 * There are two ways to have no report and they are not the same fact, so they do
 * not share a sentence: a read this console could not COMPLETE is a fact about
 * this console's reach, while an answer that named nothing for a device is a fact
 * about the plane's answer. Neither is a report about the device, which is why
 * neither borrows the words of a failure the plane never reported.
 */
export function unreadableStillSentence(report: GridTileStill["report"]): GridStillSentence {
  if (report === "answer") {
    return {
      short: gridStillCopy.unreadable.short,
      long: `${gridStillCopy.unreadable.prefix} the control plane's answer did not state anything for this device, so this console is not claiming anything about it. Nothing was drawn and nothing was inferred.`,
    }
  }
  return {
    short: gridStillCopy.unreadable.short,
    long: `${gridStillCopy.unreadable.prefix} this console could not read the control plane, so it is not claiming anything about this device. The last still the plane reported is left on screen rather than withdrawn - a read this console could not complete is not a report about the device - and the read is retried on a bounded backoff.`,
  }
}

/**
 * GridTileState is what one tile of the grid IS: the plane's own reading of the
 * device's still, or one of the two states only this console can be in.
 */
export type GridTileState = GridStillStateName | "refused" | "unreadable"

/** GridTileStill is one tile's still, resolved: the state it draws and the picture it may paint. */
export interface GridTileStill {
  deviceId: string
  state: GridTileState
  /**
   * picture is the `data:` URL this tile draws, and it is set ONLY for a still the
   * plane reports CURRENT - with one deliberate exception: while this console
   * cannot read the plane, the still it last held is left on screen, because a read
   * the console could not complete is not a report about the device. The tile stops
   * claiming it is current in the same breath, which is what its own state says.
   */
  picture: string | null
  /** still is the plane's own reading for this device, when its answer held one. */
  still: GridStillView | null
  /** report is why a no-report tile has no report: a read this console could not complete, or an answer that named nothing for this device. */
  report: "read" | "answer" | ""
}

/** gridTileStill resolves one device's tile from the answer this console holds. */
export function gridTileStill(deviceId: string, input: { stills: Readonly<Record<string, GridStillView>>; refusedDeviceIds: readonly string[]; unreadable: boolean }): GridTileStill {
  const still = input.stills[deviceId] ?? null
  if (input.unreadable) {
    return { deviceId, state: "unreadable", picture: still?.picture ?? null, still, report: "read" }
  }
  if (input.refusedDeviceIds.includes(deviceId)) {
    return { deviceId, state: "refused", picture: null, still: null, report: "" }
  }
  if (!still) {
    // The plane's contract answers every device it was given, so an absence here is
    // an answer this console cannot read for this device - and it is stated as this
    // console's own missing report rather than as anything about the device.
    return { deviceId, state: "unreadable", picture: null, still: null, report: "answer" }
  }
  return { deviceId, state: still.state, picture: still.picture, still, report: "" }
}

/** StillPictureShape is a shape a tile draws at: a width and a height in the same units as the numbers they came from. */
export interface StillPictureShape {
  width: number
  height: number
}

/**
 * consolePortraitShape is the shape a tile draws when the plane stated no size for
 * its still.
 *
 * It is this console's own portrait shape - the 9:16 box the fleet grid has always
 * laid a frame out at, and the same fallback the big frame uses when its stream has
 * not reported a size yet - and it is only ever the shape of a tile with no
 * picture: the plane states the delivered still's size WITH the picture, so a tile
 * whose still is pending, stale or unavailable has no size of its own to take.
 */
const consolePortraitShape: StillPictureShape = { width: 9, height: 16 }

/**
 * stillPictureShape is the shape the picture of one tile is drawn at.
 *
 * It is the PLANE's own reading, never one derived here: `width` and `height` are
 * the delivered still's size, which is the level's cap applied to the device's
 * screen, so a 1080x2280 device's still arrives at 360x760 and the tile draws that
 * device's screen at that shape. A size the plane did not state - a still it holds
 * no picture for, or an answer that named none - is not a shape this console may
 * invent: the tile keeps its own portrait shape and says in words that it has no
 * picture.
 */
export function stillPictureShape(tile: GridTileStill): StillPictureShape {
  const still = tile.still
  if (!still) return consolePortraitShape
  const { width, height } = still
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return consolePortraitShape
  return { width, height }
}

/**
 * stillTileShape is the shape one tile is DRAWN at: the picture's own shape, turned
 * when the workspace draws landscape frames.
 *
 * The turn is what makes a landscape frame a phone rather than a letterbox: the
 * picture keeps its own aspect (it is never stretched and never cropped) and the
 * frame takes it, so the screen fills the frame in either orientation.
 */
export function stillTileShape(tile: GridTileStill, orientation: "portrait" | "landscape"): StillPictureShape {
  const shape = stillPictureShape(tile)
  return orientation === "portrait" ? shape : { width: shape.height, height: shape.width }
}

/**
 * stillFrameWidth is the width this grid lays one frame out at.
 *
 * It is the console's own shape at the workspace's size, which is the geometry the
 * fleet grid's columns have always been laid out on and is deliberately NOT the
 * device's: a frame is as wide as the operator asked for, and the picture inside it
 * takes that width. The frame's height is the picture's own business - it is the
 * shape the tile draws at - so a tile with a picture is exactly as tall as the
 * device's screen at that width.
 */
export function stillFrameWidth(size: number, orientation: "portrait" | "landscape"): number {
  return orientation === "portrait" ? Math.round(size * consolePortraitShape.width / consolePortraitShape.height) : size
}

/**
 * gridStillClassified reports whether a tile is drawing a failure the PLANE
 * classified.
 *
 * It is what decides whether a tile announces itself as an alert, and the states
 * it excludes are excluded on purpose: waiting is not a failure, a device the
 * sweep bound did not reach is the plane's own work bound rather than a device
 * problem, and a read this console could not complete is this console's own state.
 * Only a plane that reported a capture failing is an alert.
 */
export function gridStillClassified(tile: GridTileStill): boolean {
  return tile.state === "stale" || tile.state === "unavailable"
}

/**
 * gridTileSentence is the one sentence a tile says, whichever state it is in.
 *
 * It exists so the tile component is a renderer and nothing else: every word an
 * operator reads about a still is decided here, in one table of states, which is
 * what makes the classification testable without a browser.
 */
export function gridTileSentence(tile: GridTileStill, profile: GridProfileView | null): GridStillSentence {
  switch (tile.state) {
    case "current":
      return tile.still ? currentStillSentence(tile.still, profile) : unavailableStillSentence(missingStill(tile.deviceId))
    case "pending":
      return pendingStillSentence()
    case "stale":
      return staleStillSentence(tile.still ?? missingStill(tile.deviceId))
    case "unavailable":
      return unavailableStillSentence(tile.still ?? missingStill(tile.deviceId))
    case "refused":
      return refusedStillSentence(profile)
    case "unreadable":
      return unreadableStillSentence(tile.report)
  }
}

/** missingStill is the reading for a state that arrived without one, which is this console's own missing report rather than a reason about a device. */
function missingStill(deviceId: string): GridStillView {
  return {
    deviceId, state: "unavailable", picture: null, capturedAt: "", width: 0, height: 0, bytes: 0, sourceBytes: 0,
    ageMs: null, frames: 0, failures: 0, failureClass: "", failureDetail: "", observedCadenceMillis: 0, truncated: false,
    noPictureReason: "the plane's answer named this device's state without the reading that belongs to it",
  }
}

/**
 * gridSentence is the ONE line drawn beside the grid.
 *
 * It states the cadence, the level with the plane's OWN published numbers, and the
 * two facts an operator has to be able to trust about a grid of pictures: a still
 * spends NO device session, so this grid draws every device in the view and there
 * is no tile count for it to run out of; and the operator's own big frame keeps the
 * one live session it needs. The plane's sweep bound is named ONLY when a device in
 * this view is past it, because a number stated where nothing reached it would read
 * as the tile cap this surface exists to remove.
 */
export function gridSentence(input: { devices: number; refused: number; profile: GridProfileView | null; unreadable: boolean; failure?: string }): string {
  if (input.unreadable) {
    const why = (input.failure ?? "").trim()
    return `Grid stills: this console could not read the control plane, so it is not claiming anything about the ${input.devices} device(s) in this view. The stills already on screen are left as they were, the read is retried on a bounded backoff, and nothing here is presented as live${why === "" ? "" : ` (${why})`}.`
  }
  if (!input.profile) {
    return `Grid stills: the control plane did not state the cadence or the level it carries this grid's stills at, so this console states none of its own - what is drawn is the still the plane sent, and nothing more. A still spends no device session, so this grid draws every device in the view (${input.devices} here) and holds no place on the plane; the operator's own big frame keeps the one live session it needs. Nothing in this grid is a stream.`
  }
  const cadence = input.profile.cadenceMillis > 0 ? stillDurationSentence(input.profile.cadenceMillis) : ""
  const every = cadence === "" ? "on the plane's own cadence" : `about every ${cadence}`
  const bound = input.refused > 0
    ? ` The plane captures at most ${input.profile.maxDevices} device(s) in one sweep, and ${input.refused} device(s) in this view are past that bound and say so in their tiles.`
    : ""
  return `Grid stills: the control plane captures each device ${every} and carries each still at ${levelClause(input.profile)}, up to ${input.profile.stillByteBound} bytes per still. A still spends NO device session, so this grid draws every device in the view (${input.devices} here) and no tile holds one of the plane's device sessions; the operator's own big frame keeps the one live session it needs. Nothing in this grid is a stream.${bound}`
}
