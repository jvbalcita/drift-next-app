import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { DispatchIntent, SettingView } from "@/lib/domain/control-plane"
import type { LiveMirrorTransportChoice } from "@/lib/live-mirror"

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
 * workspace-wide order can be written that way (see `frameOrderKey`).
 */

/**
 * The key the Control page's workspace layout is stored under.
 *
 * The domain has a persisted placement order already - a group membership's
 * `position` - and this console reuses it nowhere, because it cannot express
 * what this grid draws. A membership orders the devices INSIDE one group, and
 * "Ungrouped" is a computed view over devices with no active membership rather
 * than a group that could hold a position, so the memberships can order neither
 * the ungrouped devices nor the grid as a whole - this grid draws EVERY device
 * it is given, in the workspace's own order, whatever its group. So this key was
 * added, and it is written the way AGENTS.md requires an order to be written:
 * the whole order, in ONE settings write, never a single occupied slot, and
 * every reader resolves a device's position by looking the id up in that array
 * rather than by trusting the array's insertion order for anything else.
 */
export const frameOrderKey = "control_workspace_layout"

/** The key the Control page's per-console settings are stored under. */
export const consoleSettingsKey = "control_console_settings"

/** Settings the Control page owns are workspace-scoped: one Control page per workspace. */
export const controlSettingsScope = "workspace"

export type Orientation = "portrait" | "landscape"

/**
 * How big the frames are drawn, which way round, and in what order.
 *
 * `frameOrder` is a whole order over device ids. A device the stored order does
 * not name keeps the order the plane gave it and is drawn after the devices the
 * order does name, so adding a device cannot disturb an order an operator set.
 */
export interface WorkspaceLayout {
  largeHeight: number
  smallHeight: number
  orientation: Orientation
  frameOrder: readonly string[]
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
  showIp: boolean
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
export const workspaceLayoutDefaults: WorkspaceLayout = { largeHeight: 680, smallHeight: 264, orientation: "portrait", frameOrder: [] }

export const consoleSettingsDefaults: ConsoleSettings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showIp: true, liveMirrorTransport: "tcp" }

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

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const ids: string[] = []
  for (const entry of value) {
    if (typeof entry !== "string" || entry.trim() === "" || seen.has(entry)) continue
    seen.add(entry)
    ids.push(entry)
  }
  return ids
}

/**
 * readWorkspaceLayout reads the stored layout, field by field.
 *
 * A field the stored value does not carry - or carries unusably - falls back to
 * that field's own default rather than discarding the rest of the layout, so a
 * layout written by a later build that added a field is still readable here.
 */
export function readWorkspaceLayout(settings: readonly SettingView[]): WorkspaceLayout {
  const stored = parseControlValue(settings, frameOrderKey)
  if (!stored) return { ...workspaceLayoutDefaults, frameOrder: [] }
  return {
    largeHeight: bounded(stored.largeHeight, workspaceLayoutBounds.largeHeight, workspaceLayoutDefaults.largeHeight),
    smallHeight: bounded(stored.smallHeight, workspaceLayoutBounds.smallHeight, workspaceLayoutDefaults.smallHeight),
    orientation: orientation(stored.orientation),
    frameOrder: stringArray(stored.frameOrder),
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
    showIp: boolean(stored.showIp, consoleSettingsDefaults.showIp),
    liveMirrorTransport: stored.liveMirrorTransport === "webrtc" ? "webrtc" : "tcp",
  }
}

export function encodeWorkspaceLayout(layout: WorkspaceLayout): string {
  return JSON.stringify({ largeHeight: layout.largeHeight, smallHeight: layout.smallHeight, orientation: layout.orientation, frameOrder: [...layout.frameOrder] })
}

export function encodeConsoleSettings(settings: ConsoleSettings): string {
  return JSON.stringify(settings)
}

/**
 * applyFrameOrder is the grid's order: the persisted operator order first, then
 * every device it does not name in the order the plane gave them.
 *
 * A device the stored order has never seen is DRAWN - this grid draws every
 * device it is given - and it is drawn last rather than dropped or inserted at
 * an invented position. The sort is stable, so two devices the order does not
 * name keep their relative reading order.
 */
export function applyFrameOrder<T extends { id: string }>(devices: readonly T[], order: readonly string[]): T[] {
  const rank = new Map<string, number>()
  order.forEach((id, index) => { if (!rank.has(id)) rank.set(id, index) })
  return devices
    .map((device, index) => ({ device, index }))
    .sort((left, right) => (rank.get(left.device.id) ?? Number.MAX_SAFE_INTEGER) - (rank.get(right.device.id) ?? Number.MAX_SAFE_INTEGER) || left.index - right.index)
    .map((entry) => entry.device)
}

/** The whole order over the devices the grid holds, as it would be written. */
export function wholeFrameOrder(devices: readonly { id: string }[], order: readonly string[]): string[] {
  return applyFrameOrder(devices, order).map((device) => device.id)
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
    void writer.write(frameOrderKey, encodeWorkspaceLayout(next)).then((result) => {
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
