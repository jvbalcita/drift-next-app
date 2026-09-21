import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { DeviceView, DispatchIntent, EndpointView, GroupView, MembershipView, SettingView } from "@/lib/domain/control-plane"
import type { LiveMirrorTransportChoice } from "@/lib/live-mirror"
import { deviceAddress } from "@/lib/device-endpoints"
import { placementRanks, type PlacementRank } from "@/lib/device-placement"

/**
 * The Control page's own settings, and where they live.
 *
 * They live in the WORKSPACE RECORD, as two workspace-scoped settings, and not
 * in this console's component state. Leaving the page used to reset every one of
 * them, because they were `useState` defaults that died with the page: the
 * operator's frame sizes, the grid's gap, the transport the frame streams over
 * and the order of the frames on the board were all lost on a route change.
 *
 * Two mechanisms for one page would be two sources of truth for one surface, so
 * both panels persist through the settings the plane already keeps - the same
 * `settings` table, the same scopes, the same row versions and history the
 * Settings page reads and writes. The frame ORDER is why this is not merely
 * convenient: AGENTS.md requires persisted operator order to be written as a
 * whole order in one transaction, and the workspace record is the only place a
 * workspace-wide order can be written that way (see `workspaceLayoutKey`).
 */

/**
 * The key the Control page's workspace layout is stored under.
 *
 * The domain has a persisted placement order already - a group membership's
 * `position` - and that order is READ here, by the `placement` sort key, because
 * it is the durable operator order and a console may not keep a second one. What
 * this key holds is therefore not an order at all: it holds which of the sort
 * keys the board is drawn by (`frameSortKey`), together with the frame sizes and
 * the orientation.
 *
 * RETIRED RULE. This key used to hold `frameOrder`, a whole order over device
 * ids that an operator arranged frame by frame. The owner's verdict was that it
 * is too long to manage, and it is retired in favour of the sort key. Its stored
 * value is handled deliberately rather than ignored:
 *
 *  - it is READ as nothing. A stored `frameOrder` is not reinterpreted as a
 *    placement order (it is this console's own arrangement, not the domain's),
 *    and it is not turned into a sort key, because a key is a choice the operator
 *    makes and inferring one from a retired field would be this console choosing
 *    how their board is drawn. `readWorkspaceLayout` reads the fields it
 *    understands and falls back per field, so the sizes and the orientation an
 *    operator set in the same record survive the retirement;
 *  - it is DROPPED on the next write. `encodeWorkspaceLayout` writes the layout
 *    as this build knows it, so the dead field does not linger in the record
 *    where a later reader could mistake it for a live rule. The convergence is
 *    one write, and every field the operator is still using is written back with
 *    it.
 *
 * AGENTS.md section 11 requires the retired rule be corrected in the same change
 * as the code, and it is: the operator order a surface may read is the domain's
 * group and placement order, and the Control page's board is drawn by a chosen
 * sort key rather than by an order of its own.
 */
export const workspaceLayoutKey = "control_workspace_layout"

/** The key the Control page's per-console settings are stored under. */
export const consoleSettingsKey = "control_console_settings"

/** Settings the Control page owns are workspace-scoped: one Control page per workspace. */
export const controlSettingsScope = "workspace"

export type Orientation = "portrait" | "landscape"

/**
 * How the board is ordered.
 *
 * A sort KEY rather than an arrangement: the owner's verdict on the manual
 * `frameOrder` was that it is too long to manage, and what an operator wants is
 * to choose how the board is ordered rather than to place every frame by hand.
 *
 * The three keys are deliberately of two kinds, and the difference matters:
 * `placement` reads an order the OPERATOR already arranged and the domain
 * persists (a group's position, then a membership's position inside it), while
 * `name` and `address` are orders this console derives from a fact about each
 * device. None of them is an order this console stores: what is stored is which
 * key is chosen.
 */
export type FrameSortKey = "placement" | "name" | "address"

export interface FrameSortKeyOption {
  value: FrameSortKey
  label: string
  /** What the key orders by, in one clause, stated once and drawn by the control. */
  explanation: string
}

/**
 * The keys the board may be ordered by, stated ONCE.
 *
 * The control draws this list and the reader parses a stored value against it,
 * so a key that exists and is not offered, or is offered and cannot be read
 * back, cannot happen: both sides read the same array.
 */
export const frameSortKeys: readonly FrameSortKeyOption[] = [
  {
    value: "placement",
    label: "Placement Order",
    explanation: "The order the workspace already holds: each device's group by the group's own position, then its place inside that group. It is the durable operator order, so it is the same for every operator reading this workspace.",
  },
  {
    value: "name",
    label: "Device Name",
    explanation: "Alphabetical by the device's display name, with numbers read as numbers so a name ending 10 does not sort before a name ending 2. A device added later finds its place by its name rather than at the end.",
  },
  {
    value: "address",
    label: "Device Address",
    explanation: "By the address on each device's current transport endpoint, the same address its frame shows. A device the control plane holds no address for is drawn after the ones it can place.",
  },
]

/** The key a workspace that has never chosen one draws: the order that already exists. */
export const frameSortKeyDefault: FrameSortKey = "placement"

/**
 * How big the frames are drawn, which way round, and by which key they are ordered.
 */
export interface WorkspaceLayout {
  largeHeight: number
  smallHeight: number
  orientation: Orientation
  /**
   * The chosen order. It is a key and not an order: the retired `frameOrder`
   * this record used to carry is gone, and no order over device ids is stored
   * here at all (see `workspaceLayoutKey`).
   */
  frameSortKey: FrameSortKey
}

/**
 * This console's own presentation of the Control page.
 *
 * It used to carry a preview level and a frame rate as well, because each
 * compact frame was a live stream whose encoder level the panel set. A compact
 * frame is a STILL now, and the level and cadence a still is carried at are the
 * control plane's deployment inputs - not this console's - so the two controls
 * that set them are gone rather than left bounding nothing: a control an
 * operator can move that changes nothing is a surface claiming a bound it does
 * not own.
 */
export interface ConsoleSettings {
  gap: number
  opacity: number
  autoScreenOff: boolean
  controlSmall: boolean
  controlsSide: "left" | "right"
  workspaceSide: "left" | "right"
  showTag: boolean
  showIndex: boolean
  showName: boolean
  /**
   * Whether a compact frame draws the device's ADDRESS.
   *
   * It was `showIp` and it drew the device's endpoint ID: an identifier, not an
   * address. The name now says the fact rather than the transport it was expected
   * to be, because a tile that answers "where is this device" with an identifier
   * is answering a different question.
   */
  showAddress: boolean
  liveMirrorTransport: LiveMirrorTransportChoice
}

/**
 * The bounds each size slider draws within, and the step it moves in.
 *
 * They are stated once here rather than in the panel, because a stored value is
 * read back through them: a value outside a slider's own range is a value the
 * operator could not have chosen with the control that writes it, so it is
 * clamped to the range rather than drawn as a frame the control cannot return to.
 */
export const workspaceLayoutBounds = {
  largeHeight: { min: 480, max: 1240, step: 40 },
  smallHeight: { min: 192, max: 840, step: 24 },
} as const

/**
 * The layout a workspace that has never set one gets.
 *
 * 680 and 264 are the owner's own readings of the two frames this fleet is
 * driven at, and they are DEFAULTS: a workspace that has already stored a size
 * keeps it, because these are only ever reached where no value was stored.
 */
export const workspaceLayoutDefaults: WorkspaceLayout = { largeHeight: 680, smallHeight: 264, orientation: "portrait", frameSortKey: frameSortKeyDefault }

export const consoleSettingsDefaults: ConsoleSettings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showAddress: true, liveMirrorTransport: "tcp" }

/** The workspace-scoped setting this page reads a key from, or nothing. */
export function controlSetting(settings: readonly SettingView[], key: string): SettingView | undefined {
  return settings.find((candidate) => candidate.scope === controlSettingsScope && candidate.key === key)
}

function parseControlValue(settings: readonly SettingView[], key: string): Record<string, unknown> | undefined {
  const setting = controlSetting(settings, key)
  if (!setting) return undefined
  try {
    const parsed: unknown = JSON.parse(setting.valueJson)
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : undefined
  } catch {
    // A value this console cannot parse is not a value it can draw. The default
    // stands rather than a half-read layout, and nothing is written back: a read
    // that repaired what it read would be a console editing a record it was only
    // asked to project.
    return undefined
  }
}

function bounded(value: unknown, bounds: { min: number; max: number }, fallback: number): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return fallback
  return Math.min(bounds.max, Math.max(bounds.min, Math.round(value)))
}

function boolean(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback
}

function side(value: unknown, fallback: "left" | "right"): "left" | "right" {
  return value === "left" || value === "right" ? value : fallback
}

function orientation(value: unknown): Orientation {
  return value === "landscape" ? "landscape" : "portrait"
}

/**
 * frameSortKey reads a stored key against the SAME list the control offers.
 *
 * A value this build does not know - one a later build added and this one has
 * never heard of, or a retired one - falls back to the default rather than being
 * drawn, and nothing is written back: the record is the operator's, and a read
 * that repaired what it read would be this console editing a setting it was only
 * asked to project.
 */
function frameSortKey(value: unknown): FrameSortKey {
  return frameSortKeys.some((key) => key.value === value) ? (value as FrameSortKey) : frameSortKeyDefault
}

/**
 * readWorkspaceLayout reads the stored layout, field by field.
 *
 * A field the stored value does not carry - or carries unusably - falls back to
 * that field's own default rather than discarding the rest of the layout, so a
 * layout written by a later build that added a field is still readable here, and
 * a layout written by an EARLIER one that carried the retired `frameOrder` still
 * gives up the sizes and the orientation it holds (see `workspaceLayoutKey`).
 */
export function readWorkspaceLayout(settings: readonly SettingView[]): WorkspaceLayout {
  const stored = parseControlValue(settings, workspaceLayoutKey)
  if (!stored) return { ...workspaceLayoutDefaults }
  return {
    largeHeight: bounded(stored.largeHeight, workspaceLayoutBounds.largeHeight, workspaceLayoutDefaults.largeHeight),
    smallHeight: bounded(stored.smallHeight, workspaceLayoutBounds.smallHeight, workspaceLayoutDefaults.smallHeight),
    orientation: orientation(stored.orientation),
    frameSortKey: frameSortKey(stored.frameSortKey),
  }
}

export function readConsoleSettings(settings: readonly SettingView[]): ConsoleSettings {
  const stored = parseControlValue(settings, consoleSettingsKey)
  if (!stored) return { ...consoleSettingsDefaults }
  return {
    gap: bounded(stored.gap, { min: 4, max: 32 }, consoleSettingsDefaults.gap),
    opacity: bounded(stored.opacity, { min: 30, max: 100 }, consoleSettingsDefaults.opacity),
    autoScreenOff: boolean(stored.autoScreenOff, consoleSettingsDefaults.autoScreenOff),
    controlSmall: boolean(stored.controlSmall, consoleSettingsDefaults.controlSmall),
    controlsSide: side(stored.controlsSide, consoleSettingsDefaults.controlsSide),
    workspaceSide: side(stored.workspaceSide, consoleSettingsDefaults.workspaceSide),
    showTag: boolean(stored.showTag, consoleSettingsDefaults.showTag),
    showIndex: boolean(stored.showIndex, consoleSettingsDefaults.showIndex),
    showName: boolean(stored.showName, consoleSettingsDefaults.showName),
    showAddress: boolean(stored.showAddress, consoleSettingsDefaults.showAddress),
    liveMirrorTransport: stored.liveMirrorTransport === "webrtc" ? "webrtc" : "tcp",
  }
}

/**
 * encodeWorkspaceLayout writes the layout as this build knows it.
 *
 * It writes every field by name and no others, which is what retires `frameOrder`
 * in the record: the next write of this setting carries the sizes, the
 * orientation and the chosen key, and the dead order is gone rather than left
 * behind for a later reader to find (see `workspaceLayoutKey`).
 */
export function encodeWorkspaceLayout(layout: WorkspaceLayout): string {
  return JSON.stringify({ largeHeight: layout.largeHeight, smallHeight: layout.smallHeight, orientation: layout.orientation, frameSortKey: layout.frameSortKey })
}

export function encodeConsoleSettings(settings: ConsoleSettings): string {
  return JSON.stringify(settings)
}

/**
 * What the board's order is resolved from: the fleet's own facts, not a stored
 * arrangement. The endpoints are where each device IS (the `address` key), and
 * the groups and memberships are the operator's persisted placement (the
 * `placement` key).
 */
export interface FrameSortContext {
  endpoints: readonly EndpointView[]
  groups: readonly GroupView[]
  memberships: readonly MembershipView[]
}

/** Text ordering that reads numbers as numbers, so "Bay 10" follows "Bay 2". */
function compareText(left: string, right: string): number {
  return left.localeCompare(right, undefined, { numeric: true, sensitivity: "base" })
}

/**
 * applyFrameSort is the board's order, and the ONLY place it is decided.
 *
 * The grid, the frame sort control and every other consumer of this page read
 * one list produced here, so no surface can draw an order another one disagrees
 * with. The three keys:
 *
 *  - `placement` reads the operator's persisted arrangement through
 *    `placementRanks`: group by the group's own position, then the membership's
 *    position inside it. A device with no active membership is UNPLACED - the
 *    domain holds no position for it - and is drawn after the placed devices
 *    rather than at a position this console invented, exactly as "Ungrouped" is
 *    a computed view rather than a stored group;
 *  - `name` orders by the device's display name;
 *  - `address` orders by the address on the device's current endpoint, the same
 *    reading the frames are labelled with. A device the plane holds no current
 *    endpoint for has no address and is drawn after the devices it can place.
 *
 * Unplaced or addressless devices are not dropped: this board draws every device
 * it is given, and the order is stable for the devices a key cannot separate, so
 * two equal names keep the relative order the plane reported them in. That
 * tiebreak is the reading order and never the arrangement: where the operators'
 * own order exists it is the placement key, read from the domain.
 */
export function applyFrameSort<T extends DeviceView>(devices: readonly T[], key: FrameSortKey, context: FrameSortContext): T[] {
  const ranks = placementRanks(context.groups, context.memberships)
  const decorated = devices.map((device, index) => ({ device, index, address: deviceAddress(device, context.endpoints), rank: ranks.get(device.id) }))
  const byKey: Record<FrameSortKey, (left: (typeof decorated)[number], right: (typeof decorated)[number]) => number> = {
    placement: (left, right) => comparePlacement(left.rank, right.rank),
    name: (left, right) => compareText(left.device.displayName, right.device.displayName),
    // An addressless device is drawn after every device that has one, whichever
    // address it has: "" is the reading, and it is not a string that sorts first.
    address: (left, right) => (left.address === "" ? 1 : 0) - (right.address === "" ? 1 : 0) || compareText(left.address, right.address),
  }
  return decorated
    .sort((left, right) => byKey[key](left, right) || compareText(left.device.displayName, right.device.displayName) || left.index - right.index)
    .map((entry) => entry.device)
}

/** comparePlacement orders two placements: unplaced last, then group, then position. */
function comparePlacement(left: PlacementRank | undefined, right: PlacementRank | undefined): number {
  if (!left || !right) return (left ? 0 : 1) - (right ? 0 : 1)
  return left.group - right.group || left.position - right.position
}

/**
 * moveFrame moves one frame by one place within the WHOLE order.
 *
 * It returns the whole order, never a single occupied slot: what is written is
 * the order, and a write that carried only the moved frame's new position would
 * be a second, unstated rule about where every other frame goes.
 */
export function moveFrame<T>(order: readonly T[], id: T, direction: -1 | 1): T[] {
  const next = [...order]
  const from = next.indexOf(id)
  if (from < 0) return next
  const to = from + direction
  if (to < 0 || to >= next.length) return next
  next[from] = next[to] as T
  next[to] = id
  return next
}

export interface SettingWriteResult { ok: boolean; message: string }

/**
 * createControlSettingsWriter writes one of this page's settings, creating it
 * the first time and updating it after that.
 *
 * It remembers the row version of what it wrote, because a projection that has
 * not been re-read yet still holds the PREVIOUS version: two writes inside one
 * refresh would otherwise both address a version that no longer exists, or -
 * worse - a page that created a setting and had not yet read it back would
 * create a SECOND row for the same key on its next write. The projection is
 * always preferred where it holds the setting, so a value somebody else moved on
 * is read rather than overwritten from memory.
 */
export function createControlSettingsWriter(options: { dispatch: DispatchIntent; project: () => readonly SettingView[] }) {
  const held = new Map<string, { id: string; rowVersion: number }>()
  async function write(key: string, valueJson: string): Promise<SettingWriteResult> {
    const projected = controlSetting(options.project(), key)
    const current = projected ? { id: projected.id, rowVersion: projected.rowVersion } : held.get(key)
    if (current) {
      const result = await options.dispatch({ type: "updateSetting", settingId: current.id, valueJson, rowVersion: current.rowVersion })
      if (!result.ok) {
        // The write did not land, so what this writer remembers about the row is
        // not something it can build on: the next write resolves the setting
        // from the projection again rather than from a version it guessed.
        held.delete(key)
        return { ok: false, message: result.message }
      }
      held.set(key, { id: current.id, rowVersion: current.rowVersion + 1 })
      return { ok: true, message: result.message }
    }
    const created = await options.dispatch({ type: "createSetting", scope: controlSettingsScope, targetId: "", key, valueJson })
    if (!created.ok || !created.resourceId) return { ok: false, message: created.message }
    held.set(key, { id: created.resourceId, rowVersion: 1 })
    return { ok: true, message: created.message }
  }
  return { write }
}

/**
 * useControlPageSettings is the page's read and write path for both settings.
 *
 * The projection is the value: `layout` and `consoleSettings` are read from it
 * on every render, so a change that another surface makes - or that this surface
 * made and the plane recorded - is what the controls draw. What the operator is
 * moving is held beside it only until the projection agrees: a draft exists so a
 * slider does not snap back between the write and the read, and it is dropped
 * the moment the stored value equals it. A write the plane refused drops the
 * draft immediately, so a control never shows a value the workspace record does
 * not hold.
 */
export function useControlPageSettings(settings: readonly SettingView[], dispatch: DispatchIntent, onFailure: (message: string) => void) {
  const storedLayout = useMemo(() => readWorkspaceLayout(settings), [settings])
  const storedConsole = useMemo(() => readConsoleSettings(settings), [settings])
  const [draftLayout, setDraftLayout] = useState<WorkspaceLayout | null>(null)
  const [draftConsole, setDraftConsole] = useState<ConsoleSettings | null>(null)
  const settingsRef = useRef(settings)
  settingsRef.current = settings
  const dispatchRef = useRef(dispatch)
  dispatchRef.current = dispatch
  const onFailureRef = useRef(onFailure)
  onFailureRef.current = onFailure
  const writer = useMemo(() => createControlSettingsWriter({ dispatch: (intent) => dispatchRef.current(intent), project: () => settingsRef.current }), [])

  useEffect(() => {
    if (draftLayout && encodeWorkspaceLayout(draftLayout) === encodeWorkspaceLayout(storedLayout)) setDraftLayout(null)
  }, [draftLayout, storedLayout])
  useEffect(() => {
    if (draftConsole && encodeConsoleSettings(draftConsole) === encodeConsoleSettings(storedConsole)) setDraftConsole(null)
  }, [draftConsole, storedConsole])

  const updateLayout = useCallback((next: WorkspaceLayout) => {
    setDraftLayout(next)
    void writer.write(workspaceLayoutKey, encodeWorkspaceLayout(next)).then((result) => {
      if (result.ok) return
      setDraftLayout(null)
      onFailureRef.current(result.message)
    })
  }, [writer])

  const updateConsoleSettings = useCallback((next: ConsoleSettings) => {
    setDraftConsole(next)
    void writer.write(consoleSettingsKey, encodeConsoleSettings(next)).then((result) => {
      if (result.ok) return
      setDraftConsole(null)
      onFailureRef.current(result.message)
    })
  }, [writer])

  return { layout: draftLayout ?? storedLayout, consoleSettings: draftConsole ?? storedConsole, updateLayout, updateConsoleSettings }
}
