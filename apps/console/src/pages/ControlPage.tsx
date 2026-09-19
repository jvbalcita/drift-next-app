import { useEffect, useRef, useState, type PointerEvent } from "react"
import { createPortal } from "react-dom"
import { ChevronDown, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, ClipboardCopy, Crosshair, Download, Grid3X3, Image, Info, Keyboard, LoaderCircle, Network, Pin, Power, RotateCw, ScanLine, SearchX, Settings2, SlidersHorizontal, Smartphone, Unplug, Upload, Volume1, Volume2, X } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import type { ControlPlaneIntent, ControlPlaneSnapshot, DeviceSettingName, DeviceSettingsApplyView, DeviceView, DispatchIntent, MutationResult } from "@/lib/domain/control-plane"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { liveMirrorCopy, type LiveMirrorTransportChoice } from "@/lib/live-mirror"
import { allocateTileViewers } from "@/lib/live-tiles"
import { LiveMirrorDeviceKeys, LiveMirrorInfo, LiveMirrorSurface, useLiveMirrorSession } from "./live-mirror-surface"
import { LiveTilePicture } from "./live-tile"
import { deviceObservationSentence, deviceStatusLabels, notObserved } from "@/lib/device-status"
import { deviceSettingLabels, deviceSettingNames } from "@/lib/device-settings"
import { LabModeBadges, LabObservationFrame, LabStatusStrip } from "./lab-adapter"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { captureSerialForDevice } from "./page-utils"
import { DeviceStatus as DeviceStatusBadge, EmptyState, OperatorNotice, StatusBadge } from "./shared"

type Workspace = { largeHeight: number; smallHeight: number; quality: "Low" | "Medium" | "High" | "Extra"; frameRate: number; orientation: "portrait" | "landscape" }
type ConsoleSettings = { gap: number; opacity: number; autoScreenOff: boolean; controlSmall: boolean; priority: "Speed" | "Quality"; controlsSide: "left" | "right"; workspaceSide: "left" | "right"; showTag: boolean; showIndex: boolean; showName: boolean; showIp: boolean; liveMirrorTransport: LiveMirrorTransportChoice }
type FloatingPosition = { x: number; y: number }
type ConnectionFilter = "all" | "usb" | "wifi" | "otg"
type TaskAction = "activate" | "addRange" | "scanRange" | "scanSavedNetwork" | "reloadDevices" | "restartAdbServer" | "refreshDeviceList"
type TaskCopy = { loading: string; success: string }

const taskCopy: Record<TaskAction, TaskCopy> = {
  activate: { loading: "Activating devices…", success: "Device activation completed" },
  addRange: { loading: "Saving network range…", success: "Network range saved" },
  scanRange: { loading: "Scanning IP range…", success: "IP range scan completed" },
  scanSavedNetwork: { loading: "Scanning saved network…", success: "Saved network scan completed" },
  reloadDevices: { loading: "Reloading devices…", success: "Devices reloaded" },
  restartAdbServer: { loading: "Restarting ADB server…", success: "ADB server restart completed" },
  refreshDeviceList: { loading: "Refreshing device list…", success: "Device list refreshed" },
}
const connectionFilters: { value: ConnectionFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "usb", label: "USB" },
  { value: "wifi", label: "WiFi" },
  { value: "otg", label: "OTG" },
]

const workspaceDefaults: Workspace = { largeHeight: 480, smallHeight: 192, quality: "High", frameRate: 15, orientation: "portrait" }
const settingsDefaults: ConsoleSettings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, priority: "Quality", controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showIp: true, liveMirrorTransport: "webrtc" }
const initialPosition: FloatingPosition = { x: 120, y: 88 }
const phoneColors = ["bg-emerald-700", "bg-sky-700", "bg-teal-700", "bg-fuchsia-700", "bg-rose-700", "bg-slate-950", "bg-neutral-950", "bg-cyan-800", "bg-violet-800", "bg-purple-800", "bg-teal-800", "bg-slate-600"]
/**
 * The ONE colour a device with no current observation is drawn in.
 *
 * A tile's colour is otherwise its index into `phoneColors`, which is a
 * placement fact: two disconnected devices read as two different colours, and
 * the same device changes colour when a filter or a group moves it. "Nothing is
 * observed here" is one fact, so it has one colour, on every tile, whatever the
 * tile's index, group or placement - and it is a colour no observed tile uses,
 * so a tile that is carrying a picture cannot be mistaken for one that is not.
 * The colour is decoration: the tile's centred mark and its status label are
 * what state the fact in words, and they are unchanged.
 */
const absentPhoneColor = "bg-zinc-800"

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === "string") return error
  return "The action could not be completed."
}

export function ControlPage({ snapshot, dispatch, dispatchLab, labNotice = "", mirror }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; dispatchLab?: (intent: ControlPlaneIntent) => Promise<MutationResult>; labNotice?: string; mirror?: LiveMirrorClient }) {
  const [workspace, setWorkspace] = useState(workspaceDefaults)
  const [settings, setSettings] = useState(settingsDefaults)
  const [sourceId, setSourceId] = useState<string | null>(null)
  const [followerIds, setFollowerIds] = useState<string[]>([])
  /**
   * What the control plane answered when this frame's control session and lease
   * were requested, when the answer did not leave the console holding the device.
   *
   * Opening a device dispatches the intent that asks for both, and until now the
   * answer was a toast: a console that was refused the lease looked exactly like
   * one that had it - a live picture under a `not-allowed` pointer - and the
   * operator had nothing to read. The plane's own sentence is held here and
   * rendered in the frame's details (see `LiveMirrorInfo`), so the refusal is
   * named at the frame rather than scrolling past in a notification.
   */
  const [controlRefusal, setControlRefusal] = useState("")
  const [previewStarted, setPreviewStarted] = useState("")
  const [position, setPosition] = useState(initialPosition)
  const [workspaceOpen, setWorkspaceOpen] = useState(false)
  const [workspacePinned, setWorkspacePinned] = useState(false)
  const [modalPinned, setModalPinned] = useState(false)
  const [pendingAction, setPendingAction] = useState<TaskAction | null>(null)
  const [connectionFilter, setConnectionFilter] = useState<ConnectionFilter>("all")
  // Set Port is where every device is moved to, and it starts at 5555. It is not
  // preselected from a device's observation: Activate acts over the whole
  // discovered fleet, so there is no single device whose port it could be read
  // from, and a field that changed with the fleet would be a port an operator
  // never chose.
  const [port, setPort] = useState("5555")
  const [startIp, setStartIp] = useState("192.168.1.1")
  // The second range differs from the first only in its FINAL octet, so only that
  // octet is state. The first three are derived from the first range, which is
  // what makes them impossible to edit: an operator types a subnet once.
  const [endIpLastOctet, setEndIpLastOctet] = useState("255")
  const endIp = rangeEndIp(startIp, endIpLastOctet)
  const [profileId, setProfileId] = useState(snapshot.networkProfiles.find((profile) => profile.isDefault)?.id ?? snapshot.networkProfiles[0]?.id ?? "")
  const drag = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null)
  const source = snapshot.devices.find((device) => device.id === sourceId)
  const followers = snapshot.devices.filter((device) => followerIds.includes(device.id))
  const selectedProfile = snapshot.networkProfiles.find((profile) => profile.id === profileId)
  // The endpoints a Restart ADB Server re-establishes: what the registry
  // currently holds, deduplicated and in a stable order rather than in list
  // order, and never a claim about which devices are attached.
  const observedEndpoints = Array.from(new Set(snapshot.endpoints.filter((endpoint) => endpoint.state === "current" && endpoint.host.trim() !== "").map((endpoint) => `${endpoint.host}:${endpoint.port}`))).sort()
  const visibleDevices = snapshot.devices.filter((device) => matchesConnectionFilter(device, connectionFilter))
  /**
   * The tiles that carry a live picture, in the grid's own order.
   *
   * The bound is the console's, not the projection's (see `allocateTileViewers`):
   * a fleet grid is a fleet view, and each live tile is a stream on the control
   * plane, so how many tiles subscribe is decided here rather than by how many
   * devices happen to be in the view.
   */
  const tileViewers = allocateTileViewers(visibleDevices)

  function showToastMessage(message: string) {
    if (message.trim() !== "") toast.info(message)
  }
  function runTask(action: TaskAction, intent: ControlPlaneIntent): Promise<MutationResult> {
    const copy = taskCopy[action]
    setPendingAction(action)
    const operation = dispatch(intent).then((result) => {
      if (!result.ok) throw new Error(result.message)
      return result
    })
    toast.promise(operation, {
      loading: copy.loading,
      success: (result) => ({ message: copy.success, description: result.message }),
      error: (error) => ({ message: "Action failed", description: errorMessage(error) }),
    })
    void operation.then(
      () => setPendingAction((current) => current === action ? null : current),
      () => setPendingAction((current) => current === action ? null : current),
    )
    return operation
  }

  function choosePhone(device: DeviceView) {
    if (settings.controlSmall) { showToastMessage(`${device.displayName} received a compact-frame control selection. No device command was sent.`); return }
    if (!source) {
      setSourceId(device.id)
      setFollowerIds([])
      setControlRefusal("")
      // Opening a frame asks the plane for the device's control session and its
      // lease. The answer decides whether this frame's input can reach anything,
      // so it is kept rather than only announced: a refusal names itself at the
      // frame (see `controlRefusal`), and the gate in `useLiveMirrorSession`
      // still refuses the input either way.
      void reportDispatch(dispatch, { type: "beginDeviceControl", deviceId: device.id }, showToastMessage).then((result) => {
        setControlRefusal(result.ok ? "" : result.message)
      })
      return
    }
    if (source.id === device.id) {
      void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: device.id }, showToastMessage)
      setSourceId(null)
      setFollowerIds([])
      return
    }
    setFollowerIds((ids) => ids.includes(device.id) ? ids.filter((id) => id !== device.id) : [...ids, device.id])
    showToastMessage(`${device.displayName} ${followerIds.includes(device.id) ? "removed from" : "added to"} the follower selection. No command was sent to followers.`)
  }
  function handleDeviceAction(action: string) {
    const blocked = new Set(["Install APK", "Import File", "Export File", "ADB Command", "Quick Phrase"])
    if (blocked.has(action)) {
      showToastMessage(`${action} is unavailable. Packages cannot use shell, unrestricted files, or credentials.`)
      return
    }
    if (action === "Screenshot" && source) {
      void (async () => {
        const serial = captureSerialForDevice(snapshot.endpoints, source.id)
        if (!serial) {
          showToastMessage("This device has no single current transport endpoint to observe.")
          return
        }
        const authorized = await reportDispatch(dispatch, { type: "submitDeviceAction", deviceId: source.id, kind: "capture", confirmed: true }, showToastMessage)
        if (!authorized.ok) return
        await reportDispatch(dispatch, { type: "captureLabObservation", serial }, showToastMessage)
      })()
      return
    }
    showToastMessage(`${action} requires confirmation. No unauthorized command was sent.`)
  }
  function beginDrag(event: PointerEvent<HTMLDivElement>) {
    if (modalPinned || (event.target as HTMLElement).closest("button")) return
    drag.current = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: position.x, originY: position.y }
    event.currentTarget.setPointerCapture(event.pointerId)
  }
  function moveDrag(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current
    if (!current || current.pointerId !== event.pointerId) return
    setPosition({ x: Math.max(0, current.originX + event.clientX - current.startX), y: Math.max(0, current.originY + event.clientY - current.startY) })
  }
  function endDrag(event: PointerEvent<HTMLDivElement>) {
    if (drag.current?.pointerId === event.pointerId) { drag.current = null; event.currentTarget.releasePointerCapture(event.pointerId) }
  }
  /**
   * The follower selection IS the preview, with no control in between.
   *
   * The panel used to carry a `Start Preview` button and a sentence beside it
   * saying what a preview does; the owner removed both and asked for selecting a
   * small frame to be what makes it follow. So the selection is the trigger:
   * whenever this frame has a source and at least one follower, the console asks
   * the plane to carry the preview for exactly that set, and it asks again only
   * when the set changes - a selection that has not moved must not open a second
   * session on the plane, and closing the frame ends the selection that was.
   */
  const previewSignature = source ? `${source.id}|${[...followerIds].sort().join(",")}` : ""
  useEffect(() => {
    if (source === undefined || followerIds.length === 0) {
      setPreviewStarted("")
      return
    }
    if (previewSignature === previewStarted) return
    setPreviewStarted(previewSignature)
    void reportDispatch(dispatch, { type: "startMirrorPreview", sourceDeviceId: source.id, followerDeviceIds: followerIds }, showToastMessage)
    // The signature is the selection: re-running this effect on a render that did
    // not move the selection would start the same preview again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewSignature])
  /**
   * The saved network's own scan. It names the profile the operator selected, so
   * this control's target is the saved network and nothing else.
   */
  function scanSavedNetwork() {
    if (!selectedProfile) {
      toast.error("Saved network scan unavailable", { description: "Choose a saved network profile before starting a scan." })
      return
    }
    void runTask("scanSavedNetwork", { type: "startScan", profileId: selectedProfile.id })
  }
  /**
   * The entered range's own scan. Its target is what the operator typed into the
   * IP Range inputs, read from those inputs and never from the saved network
   * control, so the two Scans in this tab cannot scan the same thing.
   */
  function scanEnteredRange() {
    void runTask("scanRange", { type: "scanRange", startIp, endIp, port: Number(port.trim()) })
  }
  /**
   * Reload re-observes the devices this workspace already knows. It restarts
   * nothing, so a device that is already discoverable is re-read rather than
   * having the transport it answers on dropped.
   */
  function reloadDevices() {
    void runTask("reloadDevices", { type: "reloadDevices" })
  }
  /**
   * Activate moves every discovered device that is not already answering on the
   * Set Port port onto it. It names the port and NOTHING else: the control plane
   * reads the fleet from the devices, so this panel cannot assert which devices
   * are attached, and each device's own result comes back separately.
   */
  function activateFleet() {
    void runTask("activate", { type: "activateFleet", port: Number(port.trim()) })
  }
  function restartAdbServer() {
    if (observedEndpoints.length === 0) {
      toast.error("ADB server restart unavailable", { description: "At least one observed endpoint is required to re-establish a transport. Nothing was sent." })
      return
    }
    void runTask("restartAdbServer", { type: "restartTransportServer", endpoints: observedEndpoints })
  }
  function addDiscoveryRange() {
    void runTask("addRange", { type: "addDiscoveryRange", startIp, endIp, port: Number(port.trim()) })
  }
  /**
   * The second range's only editable octet. The field hands back the whole
   * address it now holds and this stores the fourth octet alone, so an edit that
   * reaches the first three octets — which the field disables anyway — cannot
   * change them here either.
   */
  function setRangeEndLastOctet(next: string) {
    setEndIpLastOctet(next.split(".")[3] ?? "")
  }

  /**
   * Whether this console holds this device's active lease, read from the
   * projection it already holds. It is what makes input dispatchable at all: the
   * console refuses locally only what it can see it cannot describe, and the
   * kernel remains the authority for everything it does send (AGENTS.md
   * section 3). Which observation a coordinate is measured from is the frame's
   * own business now - the live stream it draws - so nothing about it is read
   * here.
   */
  const sourceLease = source ? snapshot.leases.some((candidate) => candidate.deviceId === source.id && candidate.state === "active") : false
  const deviceModal = source ? <FloatingDevice device={source} followers={followers} workspace={workspace} settings={settings} position={position} pinned={modalPinned} onPinChange={setModalPinned} onPointerDown={beginDrag} onPointerMove={moveDrag} onPointerUp={endDrag} onClose={() => { void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: source.id }, showToastMessage); setSourceId(null); setFollowerIds([]); setControlRefusal("") }} onAction={handleDeviceAction} mirror={mirror} mirrorTransport={settings.liveMirrorTransport} workspaceId={snapshot.workspaceId} leaseRefusal={controlRefusal} hasLease={sourceLease} dispatch={dispatch} /> : null
  const selectedCount = (source ? 1 : 0) + followers.length

  return <div className="relative min-h-full">
    <div className="mb-5 flex items-center justify-between gap-3"><div className="drift-kicker flex items-center gap-3"><span className="h-px w-8 bg-primary" aria-hidden="true" /><span>Control / Device Workspace</span></div><div className="flex flex-wrap items-center justify-end gap-2"><StatusBadge label={snapshot.runtimeConnection.state} tone={snapshot.runtimeConnection.state === "connected" ? "healthy" : snapshot.runtimeConnection.state === "reconnecting" ? "attention" : "danger"} /><LabModeBadges adapter={snapshot.labAdapter} /></div></div>
    <OperatorNotice>Selecting a phone opens a control session and acquires a per-device lease. Preview admits followers independently and does not copy commands. High-risk shell, APK, and file actions stay blocked. Workspace display settings only change this local view.</OperatorNotice>
    <LabStatusStrip
      adapter={snapshot.labAdapter}
      runtimeConnection={snapshot.runtimeConnection}
      spoolHealth={snapshot.spoolHealth}
      indeterminateActions={snapshot.indeterminateActions}
      dispatch={dispatch}
      dispatchLab={dispatchLab}
      onFeedback={showToastMessage}
      notice={labNotice}
    />
    <div className="mt-6 flex flex-wrap items-center gap-2 border-y border-border py-3" aria-label="Control workspace toolbar">
      <span className="mr-auto text-xs"><span className="drift-data font-semibold">{selectedCount}</span> selected · {settings.controlSmall ? "compact-frame control enabled" : source ? "click another phone to select followers" : "click a phone to open its large frame"}</span>
      {!workspacePinned ? <WorkspaceToggle side={settings.workspaceSide} onToggle={() => { setWorkspaceOpen(true); setWorkspacePinned(true) }} /> : null}
      <ConsoleSettingsDialog settings={settings} onChange={setSettings} modalPinned={modalPinned} onModalPinnedChange={setModalPinned} dispatch={dispatch} />
      <DeviceListDialog devices={snapshot.devices} endpoints={snapshot.endpoints} onReload={() => { void runTask("refreshDeviceList", { type: "refresh" }) }} pendingAction={pendingAction} />
      {source ? <Button size="sm" variant="outline" onClick={() => { void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: source.id }, showToastMessage); setSourceId(null); setFollowerIds([]); setControlRefusal("") }}><X className="size-3.5" aria-hidden="true" />Close Screen</Button> : null}
      <Button size="sm" variant="outline" disabled={!source || modalPinned} onClick={() => setPosition(initialPosition)}><Crosshair className="size-3.5" aria-hidden="true" />Reset Position</Button>
    </div>
    <div className={`mt-4 grid items-start gap-4 ${workspaceOpen ? settings.workspaceSide === "right" ? "xl:grid-cols-[minmax(0,1fr)_360px]" : "xl:grid-cols-[360px_minmax(0,1fr)]" : ""}`}>
      {workspaceOpen ? <div className={`xl:sticky xl:top-4 ${settings.workspaceSide === "right" ? "xl:order-2" : "xl:order-1"}`}><WorkspacePanel workspace={workspace} onWorkspaceChange={setWorkspace} pinned={workspacePinned} onPinnedChange={(value) => { setWorkspacePinned(value); setWorkspaceOpen(value) }} side={settings.workspaceSide} port={port} onPortChange={setPort} startIp={startIp} onStartIpChange={setStartIp} endIp={endIp} onEndIpChange={setRangeEndLastOctet} profileId={profileId} onProfileIdChange={setProfileId} profiles={snapshot.networkProfiles} endpoints={snapshot.endpoints} pendingAction={pendingAction} onActivate={activateFleet} onRestartServer={restartAdbServer} onAddRange={addDiscoveryRange} onScanRange={scanEnteredRange} onScanSavedNetwork={scanSavedNetwork} onReloadDevices={reloadDevices} /></div> : null}
      <section className={`min-w-0 ${workspaceOpen && settings.workspaceSide === "left" ? "xl:order-2" : ""}`} aria-label="Phone control workspace">
        <LabObservationFrame adapter={snapshot.labAdapter} height={workspace.largeHeight} />
        <ConnectionFilterBar filter={connectionFilter} onChange={setConnectionFilter} shown={visibleDevices.length} total={snapshot.devices.length} />
        <div className={`grid items-start gap-4 ${modalPinned && source ? "xl:grid-cols-[minmax(0,1fr)_auto]" : ""}`}>
          {visibleDevices.length === 0 ? <EmptyState label="No Devices for This Connection" detail="No device in the current view has an observed transport matching this filter." /> : <ScrollArea className="h-[calc(100vh-15rem)] min-h-[420px] min-w-0 border border-border bg-muted/20 p-3"><div className="grid content-start justify-start" style={{ gridTemplateColumns: `repeat(auto-fill, ${workspace.orientation === "portrait" ? Math.round(workspace.smallHeight * 9 / 16) : workspace.smallHeight}px)`, gap: settings.gap }} aria-label="Compact phone frames">{visibleDevices.map((device, index) => <CompactPhone key={device.id} device={device} index={index} size={workspace.smallHeight} orientation={workspace.orientation} active={source?.id === device.id} follower={followerIds.includes(device.id)} settings={settings} mirror={mirror} transport={settings.liveMirrorTransport} workspaceId={snapshot.workspaceId} viewing={tileViewers.includes(device.id)} onClick={() => choosePhone(device)} />)}</div></ScrollArea>}
          {modalPinned ? deviceModal : null}
        </div>
      </section>
    </div>
    {!modalPinned && deviceModal && typeof document !== "undefined" ? createPortal(deviceModal, document.body) : null}
  </div>
}
function ConnectionFilterBar({ filter, onChange, shown, total }: { filter: ConnectionFilter; onChange: (value: ConnectionFilter) => void; shown: number; total: number }) {
  return <div className="flex flex-wrap items-center gap-2 py-3" aria-label="Device connection filters">
    <span className="mr-2 text-xs font-semibold">Connection type</span>
    <div className="flex flex-wrap gap-1" role="group" aria-label="Filter devices by connection type">
      {connectionFilters.map((item) => <Button key={item.value} type="button" size="sm" variant={filter === item.value ? "default" : "outline"} aria-pressed={filter === item.value} onClick={() => onChange(item.value)}>{item.label}</Button>)}
    </div>
    <span className="ml-auto text-xs text-muted-foreground" aria-live="polite"><span className="drift-data font-semibold">{shown} / {total}</span> devices shown</span>
  </div>
}
function matchesConnectionFilter(device: DeviceView, filter: ConnectionFilter) {
  if (filter === "all") return true
  if (filter === "usb") return device.transport === "usb"
  if (filter === "wifi") return device.transport === "tcp"
  return device.controlEligibility === "eligible"
}
function WorkspaceToggle({ side, onToggle }: { side: "left" | "right"; onToggle: () => void }) {
  return <Button size="sm" variant="outline" aria-label="Open Workspace Settings" onClick={onToggle}>{side === "left" ? <ChevronLeft className="size-3.5" aria-hidden="true" /> : <ChevronRight className="size-3.5" aria-hidden="true" />}Workspace Settings</Button>
}

function TaskButton({ action, pendingAction, icon: Icon, children, className, disabled = false, onClick, variant = "default" }: { action: TaskAction; pendingAction: TaskAction | null; icon?: typeof Network; children: string; className?: string; disabled?: boolean; onClick: () => void; variant?: "default" | "outline" }) {
  const pending = pendingAction === action
  return <Button type="button" size="sm" variant={variant} className={className} disabled={disabled || pendingAction !== null} aria-busy={pending} onClick={onClick}>{pending ? <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" /> : Icon ? <Icon className="size-3.5" aria-hidden="true" /> : null}{children}</Button>
}

function WorkspacePanel({ workspace, onWorkspaceChange, pinned, onPinnedChange, side, port, onPortChange, startIp, onStartIpChange, endIp, onEndIpChange, profileId, onProfileIdChange, profiles, endpoints, pendingAction, onActivate, onRestartServer, onAddRange, onScanRange, onScanSavedNetwork, onReloadDevices }: { workspace: Workspace; onWorkspaceChange: (value: Workspace) => void; pinned: boolean; onPinnedChange: (value: boolean) => void; side: "left" | "right"; port: string; onPortChange: (value: string) => void; startIp: string; onStartIpChange: (value: string) => void; endIp: string; onEndIpChange: (value: string) => void; profileId: string; onProfileIdChange: (value: string) => void; profiles: ControlPlaneSnapshot["networkProfiles"]; endpoints: ControlPlaneSnapshot["endpoints"]; pendingAction: TaskAction | null; onActivate: () => void; onRestartServer: () => void; onAddRange: () => void; onScanRange: () => void; onScanSavedNetwork: () => void; onReloadDevices: () => void }) {
  return <aside className="border border-border bg-card" aria-label="Workspace Settings">
    <div className="flex items-start gap-3 border-b border-border p-4">
      <span className="grid size-8 shrink-0 place-items-center border border-primary/40 bg-secondary text-primary"><SlidersHorizontal className="size-4" aria-hidden="true" /></span>
      <div className="min-w-0 flex-1"><h2 className="text-sm font-semibold">Workspace Settings</h2><p className="mt-1 text-[11px] leading-4 text-muted-foreground">Display and OTG setup</p></div>
      <Button size="icon-sm" variant="ghost" aria-label="Unpin Workspace Settings" aria-pressed={pinned} onClick={() => onPinnedChange(false)}>{side === "left" ? <ChevronsLeft className="size-3.5" aria-hidden="true" /> : <ChevronsRight className="size-3.5" aria-hidden="true" />}</Button>
    </div>
    <Tabs defaultValue="display">
      <TabsList aria-label="Workspace Settings Sections"><TabsTrigger value="display">Display</TabsTrigger><TabsTrigger value="otg">OTG Setup</TabsTrigger></TabsList>
      <TabsContent value="display" className="space-y-5 p-4">
        <div className="border-l-2 border-primary pl-3"><p className="text-xs font-semibold">Floating Device</p><p className="mt-1 text-[11px] text-muted-foreground">One scale keeps the phone and controls in proportion.</p></div>
        <Slider label="Floating Frame Size" value={workspace.largeHeight} min={480} max={1240} step={40} unit="px" onChange={(largeHeight) => onWorkspaceChange({ ...workspace, largeHeight })} />
        <div className="border-t border-border pt-4"><p className="text-xs font-semibold">Compact Phone Frames</p><p className="mt-1 text-[11px] text-muted-foreground">Orientation applies only to compact frames.</p></div>
        <Slider label="Small Screen" value={workspace.smallHeight} min={192} max={840} step={24} unit="px" onChange={(smallHeight) => onWorkspaceChange({ ...workspace, smallHeight })} />
        <div className="grid grid-cols-2 gap-2"><Button size="sm" variant={workspace.orientation === "portrait" ? "default" : "outline"} onClick={() => onWorkspaceChange({ ...workspace, orientation: "portrait" })}>Portrait</Button><Button size="sm" variant={workspace.orientation === "landscape" ? "default" : "outline"} onClick={() => onWorkspaceChange({ ...workspace, orientation: "landscape" })}>Landscape</Button></div>
        <Choice label="Preview Quality" value={workspace.quality} options={["Low", "Medium", "High", "Extra"]} onChange={(quality) => onWorkspaceChange({ ...workspace, quality: quality as Workspace["quality"] })} />
        <Slider label="Frame Rate" value={workspace.frameRate} min={1} max={24} unit="fps" onChange={(frameRate) => onWorkspaceChange({ ...workspace, frameRate })} />
        <Button variant="outline" className="w-full rounded-none" onClick={() => onWorkspaceChange(workspaceDefaults)}>Reset Workspace</Button>
      </TabsContent>
      <TabsContent value="otg" className="space-y-5 p-4">
        <OtgSetupPanel endpoints={endpoints} profiles={profiles} port={port} onPortChange={onPortChange} startIp={startIp} onStartIpChange={onStartIpChange} endIp={endIp} onEndIpChange={onEndIpChange} profileId={profileId} onProfileIdChange={onProfileIdChange} pendingAction={pendingAction} onActivate={onActivate} onRestartServer={onRestartServer} onAddRange={onAddRange} onScanRange={onScanRange} onScanSavedNetwork={onScanSavedNetwork} onReloadDevices={onReloadDevices} />
      </TabsContent>
    </Tabs>
  </aside>
}

/**
 * The OTG Setup tab keeps the two scan targets separate: the IP Range section
 * scans the entered range, while Saved Network scans the selected profile. Help
 * is attached to the visible label for each input or action so the primary
 * control remains a primary control rather than also acting as a tooltip trigger.
 */
function OtgSetupPanel({ endpoints, profiles, port, onPortChange, startIp, onStartIpChange, endIp, onEndIpChange, profileId, onProfileIdChange, pendingAction, onActivate, onRestartServer, onAddRange, onScanRange, onScanSavedNetwork, onReloadDevices }: { endpoints: ControlPlaneSnapshot["endpoints"]; profiles: ControlPlaneSnapshot["networkProfiles"]; port: string; onPortChange: (value: string) => void; startIp: string; onStartIpChange: (value: string) => void; endIp: string; onEndIpChange: (value: string) => void; profileId: string; onProfileIdChange: (value: string) => void; pendingAction: TaskAction | null; onActivate: () => void; onRestartServer: () => void; onAddRange: () => void; onScanRange: () => void; onScanSavedNetwork: () => void; onReloadDevices: () => void }) {
  // The transports a restart can re-establish: the endpoints the registry
  // currently holds, deduplicated and in a stable order rather than list order.
  const reestablishable = Array.from(new Set(endpoints.filter((endpoint) => endpoint.state === "current" && endpoint.host.trim() !== "").map((endpoint) => `${endpoint.host}:${endpoint.port}`))).sort()
  return <>
    <div>
      <p className="text-xs font-semibold">Quick OTG Setup</p>
      <p className="mt-1 text-[11px] text-muted-foreground">Configure the host port, discovery bounds, and saved scan targets.</p>
    </div>
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2">
      <div className="min-w-0 space-y-1">
        <ControlLabel label="Set Port" htmlFor="set-port" explanation="The port every discovered device is moved to. Activate names this port and nothing else: the control plane reads the fleet from the devices." />
        <Input id="set-port" aria-label="Set Port" inputMode="numeric" value={port} onChange={(event) => onPortChange(event.target.value)} className="h-7 font-mono text-xs" />
      </div>
      <TaskButton action="activate" pendingAction={pendingAction} onClick={onActivate}>Activate</TaskButton>
    </div>
    <div>
      <ControlLabel label="IP Range" explanation="Bound the scan to the IPv4 range entered below. The scan observes transports already reported by the authorized device runtime; it does not infer devices outside that observation." />
      <div className="mt-2 space-y-2">
        <IpAddressInput value={startIp} onChange={onStartIpChange} />
        <IpAddressInput value={endIp} lockedOctets={3} onChange={onEndIpChange} />
      </div>
      <div className="mt-2 space-y-1">
        <ControlLabel label="Range Actions" explanation="Add saves the entered range as a Network Profile. Scan Range observes the entered range without saving it." />
        <div className="grid grid-cols-2 gap-2">
          <TaskButton action="addRange" pendingAction={pendingAction} variant="outline" icon={Network} onClick={onAddRange}>Add</TaskButton>
          <TaskButton action="scanRange" pendingAction={pendingAction} icon={ScanLine} onClick={onScanRange}>Scan Range</TaskButton>
        </div>
      </div>
    </div>
    <div className="border-t border-border pt-4">
      <div className="space-y-3 border border-primary/25 bg-secondary/20 p-3">
        <div className="flex items-start gap-2">
          <span className="grid size-7 shrink-0 place-items-center border border-primary/30 bg-card text-primary"><Network className="size-3.5" aria-hidden="true" /></span>
          <div className="min-w-0">
            <p className="text-xs font-semibold">Saved Network</p>
            <p className="mt-1 text-[11px] leading-4 text-muted-foreground">Choose a saved profile when the scan target is managed policy.</p>
          </div>
        </div>
        <Choice label="Saved Network" value={profileId} options={profiles.map((profile) => profile.id)} labels={Object.fromEntries(profiles.map((profile) => [profile.id, profile.isDefault ? `${profile.name} · Default` : profile.name]))} explanation="The saved Network Profile that this control's own Scan observes. It is not what the IP Range section's Scan observes." onChange={onProfileIdChange} />
        <TaskButton action="scanSavedNetwork" pendingAction={pendingAction} className="w-full rounded-none" icon={ScanLine} onClick={onScanSavedNetwork} disabled={!profileId}>Scan Saved Network</TaskButton>
      </div>
      <div className="mt-3 space-y-3 border-t border-border pt-3">
        <div>
          <ControlLabel label="Device Maintenance" explanation="Reload reads the current control-plane projection. Restart ADB Server is a separate host-wide operation and is only offered when there are observed TCP endpoints to re-establish." />
          <p className="mt-1 text-[11px] leading-4 text-muted-foreground">Refresh the device view without changing the host transport.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <TaskButton action="reloadDevices" pendingAction={pendingAction} variant="outline" className="min-w-[9rem] flex-1 rounded-none" icon={RotateCw} onClick={onReloadDevices}>Reload Devices</TaskButton>
          {reestablishable.length > 0 ? <TaskButton action="restartAdbServer" pendingAction={pendingAction} variant="outline" className="min-w-[9rem] flex-1 rounded-none" icon={Power} onClick={onRestartServer}>Restart ADB Server</TaskButton> : null}
        </div>
      </div>
    </div>
  </>
}

/**
 * The Console Settings dialog.
 *
 * Its description is load-bearing: two of its tabs are local display preferences
 * and one of them is not. The Fleet Device Settings tab changes the DEVICES
 * themselves, so the description says so rather than leaving a disclaimer that
 * the controls "do not alter device policy, transport, or runtime state" over a
 * control that does alter device state.
 */
function ConsoleSettingsDialog({ settings, onChange, modalPinned, onModalPinnedChange, dispatch }: { settings: ConsoleSettings; onChange: (value: ConsoleSettings) => void; modalPinned: boolean; onModalPinnedChange: (value: boolean) => void; dispatch: DispatchIntent }) {
  return <Dialog><DialogTrigger render={<Button size="sm" variant="outline" />}><Settings2 className="size-3.5" aria-hidden="true" />Settings</DialogTrigger><DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto rounded-none sm:max-w-2xl"><DialogHeader><DialogTitle>Console Settings</DialogTitle><DialogDescription>Appearance and presentation are local console preferences. Fleet defaults change device state across the workspace through the control plane&apos;s lease, fencing, and postcondition checks.</DialogDescription></DialogHeader><Tabs defaultValue="appearance" className="border-y border-border py-4"><TabsList aria-label="Console Settings sections"><TabsTrigger value="appearance">Appearance</TabsTrigger><TabsTrigger value="presentation">Presentation</TabsTrigger><TabsTrigger value="fleet">Fleet</TabsTrigger></TabsList><TabsContent value="appearance" className="space-y-5 p-1 pt-5"><Slider label="Devices Gap" value={settings.gap} min={4} max={32} unit="px" onChange={(gap) => onChange({ ...settings, gap })} /><Slider label="Info Opacity" value={settings.opacity} min={30} max={100} unit="%" onChange={(opacity) => onChange({ ...settings, opacity })} /><SettingToggle label="Auto Screen Off" description="Preference for inactive compact frames." checked={settings.autoScreenOff} onChange={(autoScreenOff) => onChange({ ...settings, autoScreenOff })} /><SettingToggle label="Control Small Screen" description="Uses compact-frame control rather than opening the big device." checked={settings.controlSmall} onChange={(controlSmall) => onChange({ ...settings, controlSmall })} /><div className="border-t border-border pt-4"><Choice label={liveMirrorCopy.settings.label} value={settings.liveMirrorTransport} options={["webrtc", "tcp"]} labels={liveMirrorCopy.settings.choice} onChange={(value) => onChange({ ...settings, liveMirrorTransport: value as LiveMirrorTransportChoice })} /><p className="mt-2 text-[11px] leading-4 text-muted-foreground">{liveMirrorCopy.settings.notice}</p></div><Choice label="Priority" value={settings.priority} options={["Speed", "Quality"]} onChange={(priority) => onChange({ ...settings, priority: priority as ConsoleSettings["priority"] })} /></TabsContent><TabsContent value="presentation" className="space-y-5 p-1 pt-5"><PositionToggle label="Control Position" value={settings.controlsSide} onChange={(controlsSide) => onChange({ ...settings, controlsSide })} /><PositionToggle label="Workspace Position" value={settings.workspaceSide} onChange={(workspaceSide) => onChange({ ...settings, workspaceSide })} /><SettingToggle label="Connect Tag" description="Show the OTG or Hold badge on compact frames." checked={settings.showTag} onChange={(showTag) => onChange({ ...settings, showTag })} /><SettingToggle label="Device Index" description="Show the numbered device index." checked={settings.showIndex} onChange={(showIndex) => onChange({ ...settings, showIndex })} /><SettingToggle label="Device Name" description="Show the device display name." checked={settings.showName} onChange={(showName) => onChange({ ...settings, showName })} /><SettingToggle label="Device IP" description="Show the compact-frame endpoint identifier." checked={settings.showIp} onChange={(showIp) => onChange({ ...settings, showIp })} /><SettingToggle label="Modal Control Position" description="Pinned places the big frame beside the device grid; drag floats it above the page." checked={modalPinned} onChange={onModalPinnedChange} onLabel="Pinned" offLabel="Drag" /></TabsContent><TabsContent value="fleet" className="space-y-5 p-1 pt-5"><FleetDeviceSettingsPanel dispatch={dispatch} /></TabsContent></Tabs></DialogContent></Dialog>
}

/**
 * The fleet device-settings apply.
 *
 * Two settings, one control, because they are two halves of one preparation step:
 * the recorded skills are coordinate-based, which a rotating device invalidates,
 * and the Android autofill popup lands on top of the login forms this fleet
 * drives. Both are the control plane's catalogued operations, so this panel
 * offers them and nothing else: there is no settings key here to compose, and no
 * per-device list, because the fleet is read from the registry by the control
 * plane rather than asserted by this console.
 *
 * The outcomes are rendered PER DEVICE AND PER SETTING. An aggregate verdict
 * would hide the one device that is still rotating, which is the only row an
 * operator has to act on.
 */
function FleetDeviceSettingsPanel({ dispatch }: { dispatch: DispatchIntent }) {
  const [confirmed, setConfirmed] = useState(false)
  const [pending, setPending] = useState(false)
  const [applyView, setApplyView] = useState<DeviceSettingsApplyView | null>(null)
  const [failureMessage, setFailureMessage] = useState("")
  const requested = [...deviceSettingNames]

  function applyToFleet() {
    if (!confirmed || pending) return
    setPending(true)
    const operation = dispatch({ type: "applyFleetDeviceSettings", settings: requested, confirmed: true })
    toast.promise(operation, {
      loading: "Applying device settings to the fleet…",
      success: (result) => result.ok
        ? { message: "Fleet device settings applied", description: result.message }
        : { message: "Fleet device settings not applied", description: result.message },
      error: (error) => ({ message: "Fleet device settings not applied", description: errorMessage(error) }),
    })
    void operation.then(
      (result) => {
        setApplyView(result.ok ? result.deviceSettingsApply ?? null : null)
        setFailureMessage(result.ok ? "" : result.message)
        setPending(false)
      },
      (error: unknown) => {
        setApplyView(null)
        setFailureMessage(errorMessage(error))
        setPending(false)
      },
    )
  }

  return <>
    <div className="border-l-2 border-primary pl-3">
      <p className="text-xs font-semibold">Fleet Device Settings</p>
      <p className="mt-1 text-[11px] leading-4 text-muted-foreground">These change the DEVICES, not this view. Applying runs through the control plane: one control session, a lease per device, one attempt per setting, and the setting read back off the device before it is reported as applied.</p>
    </div>
    <ul className="space-y-2">
      {requested.map((name) => <li key={name} className="flex items-start gap-2 border border-border p-3">
        <span className="grid size-7 shrink-0 place-items-center border border-primary/30 bg-card text-primary">{name === "rotation_lock" ? <RotateCw className="size-3.5" aria-hidden="true" /> : <Keyboard className="size-3.5" aria-hidden="true" />}</span>
        <span className="min-w-0">
          <span className="block text-xs font-semibold">{deviceSettingLabels[name]}</span>
          <span className="mt-1 block text-[11px] leading-4 text-muted-foreground">{deviceSettingPurpose(name)}</span>
        </span>
      </li>)}
    </ul>
    <SettingToggle label="Confirm Fleet Apply" description="Required for every device: one of these settings rewrites a secure setting, so the control plane's policy decision refuses it without the operator's explicit approval." checked={confirmed} onChange={setConfirmed} onLabel="Confirmed" offLabel="Not confirmed" />
    <div className="space-y-2">
      <ControlLabel label="Apply To Fleet" explanation="Sends all of the settings above to every device in this workspace's registry that is not retired, one device at a time. A device that is under another operator's control, or that has no current transport, is reported as its own refusal and the rest of the fleet is still prepared." />
      <Button type="button" className="w-full rounded-none" disabled={!confirmed || pending} aria-busy={pending} onClick={applyToFleet}>{pending ? <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" /> : <RotateCw className="size-3.5" aria-hidden="true" />}Apply to Fleet</Button>
    </div>
    {failureMessage !== "" ? <OperatorNotice>{failureMessage}</OperatorNotice> : null}
    {applyView ? <FleetSettingsResult view={applyView} /> : null}
  </>
}

/** What each setting is for, stated once so the panel cannot disagree with itself. */
function deviceSettingPurpose(name: DeviceSettingName): string {
  return name === "rotation_lock"
    ? "Holds the device in its natural orientation and turns auto-rotate off. A coordinate-recorded skill is invalid in any other orientation, so this is what makes a recording replayable."
    : "Removes the selected autofill service and disables the augmented one. The autofill popup otherwise lands on top of the login forms this fleet drives."
}

/**
 * FleetSettingsResult renders the control plane's own rows.
 *
 * Every device gets its own row for every setting, with the sentence the control
 * plane reported for it, and a row whose setting was not read back off the device
 * says so rather than reading like a confirmed change.
 */
function FleetSettingsResult({ view }: { view: DeviceSettingsApplyView }) {
  return <div className="border border-border">
    <p className="border-b border-border bg-muted/30 px-3 py-2 text-xs" aria-live="polite"><span className="drift-data font-semibold">{view.appliedDevices}</span> of <span className="drift-data font-semibold">{view.totalDevices}</span> device(s) applied and verified every requested setting · <span className="drift-data font-semibold">{view.failedDevices}</span> did not</p>
    {view.outcomes.length === 0
      ? <p className="px-3 py-3 text-[11px] leading-4 text-muted-foreground">No device was reported for this apply, so there is no per-device outcome to read.</p>
      : <div className="max-h-64 overflow-y-auto"><table className="w-full text-left text-xs"><caption className="sr-only">Every device&apos;s outcome for every setting this apply ran</caption><thead className="sticky top-0 bg-muted/80 text-[10px] uppercase tracking-[.08em] text-muted-foreground"><tr><th scope="col" className="px-3 py-2 font-semibold">Device</th><th scope="col" className="px-3 py-2 font-semibold">Setting</th><th scope="col" className="px-3 py-2 font-semibold">Outcome</th><th scope="col" className="px-3 py-2 font-semibold">Detail</th></tr></thead><tbody className="divide-y divide-border">{view.outcomes.map((outcome, index) => <tr key={`${outcome.deviceId}-${outcome.setting}-${index}`}>
        <td className="px-3 py-2 font-mono text-[10px] text-muted-foreground">{outcome.deviceId}</td>
        <td className="px-3 py-2">{deviceSettingLabels[outcome.setting]}</td>
        <td className="px-3 py-2 font-medium">{outcome.applied ? "Applied" : "Not applied"}{outcome.verified ? " · read back off the device" : " · no read-back"}</td>
        <td className="px-3 py-2 text-muted-foreground">{outcome.message}</td>
      </tr>)}</tbody></table></div>}
  </div>
}

function DeviceListDialog({ devices, endpoints, onReload, pendingAction }: { devices: readonly DeviceView[]; endpoints: ControlPlaneSnapshot["endpoints"]; onReload: () => void; pendingAction: TaskAction | null }) {
  return <Dialog><DialogTrigger render={<Button size="sm" variant="outline" />}><Smartphone className="size-3.5" aria-hidden="true" />Devices</DialogTrigger><DialogContent className="max-w-3xl rounded-none"><DialogHeader><DialogTitle>Device List</DialogTitle><DialogDescription>Devices in this workspace&apos;s registry. Status reports the current observation fact recorded by the control plane, not a claim that the device answers at this moment. The ADB server restart lives in Workspace Settings → OTG Setup, so the host-wide operation has one control.</DialogDescription></DialogHeader><div className="flex flex-wrap gap-2 border-y border-border py-3"><TaskButton action="refreshDeviceList" pendingAction={pendingAction} variant="outline" icon={RotateCw} onClick={onReload}>Reload</TaskButton></div><div className="max-h-[60vh] overflow-y-auto"><table className="w-full text-left text-xs"><thead className="sticky top-0 bg-muted/80 text-[10px] uppercase tracking-[.08em] text-muted-foreground"><tr><th className="px-3 py-2 font-semibold">Index</th><th className="px-3 py-2 font-semibold">Device Name</th><th className="px-3 py-2 font-semibold">Device ID</th><th className="px-3 py-2 font-semibold">Status</th><th className="px-3 py-2 font-semibold">Observed Port</th></tr></thead><tbody className="divide-y divide-border">{devices.map((device, index) => {
    // The port is the one the device was OBSERVED on, or nothing. Painting one
    // field into every row would show a port no device answered on.
    const observed = endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")
    return <tr key={device.id}><td className="px-3 py-3 font-mono text-muted-foreground">{index + 1}</td><td className="px-3 py-3 font-medium">{device.displayName}</td><td className="px-3 py-3 font-mono text-[10px] text-muted-foreground">{device.id}</td><td className="px-3 py-3"><DeviceStatusBadge device={device} /></td><td className="px-3 py-3 font-mono">{observed ? observed.port : "—"}</td></tr>
  })}</tbody></table></div></DialogContent></Dialog>
}

function CompactPhone({ device, index, size, orientation, active, follower, settings, mirror, transport, workspaceId, viewing, onClick }: { device: DeviceView; index: number; size: number; orientation: Workspace["orientation"]; active: boolean; follower: boolean; settings: ConsoleSettings; mirror?: LiveMirrorClient; transport: LiveMirrorTransportChoice; workspaceId: string; viewing: boolean; onClick: () => void }) {
  const width = orientation === "portrait" ? Math.round(size * 9 / 16) : size
  const height = orientation === "portrait" ? size : Math.round(size * 9 / 16)
  // A device that is not currently observed carries a mark CENTRED in its frame,
  // because that is the one place an operator scanning the grid sees it. The mark
  // is an icon with its own accessible name rather than a colour: the frame tint
  // is decorative, so the fact has to survive a monochrome view and a screen
  // reader, and the frame's own status text says the same thing in words. Offline
  // and never-observed take different icons so the two facts cannot be read as
  // one. The OTG/HOLD tag is left alone: it reports control eligibility, which
  // this mark does not change.
  //
  // The colour is the second half of the same rule: a device with no current
  // observation takes ONE fixed colour (see `absentPhoneColor`) rather than its
  // index, so every disconnected tile reads the same whatever its placement, and
  // the index colour is left to the tiles that are actually carrying something.
  const absent = notObserved(device.status)
  const AbsentIcon = device.status === "unobserved" ? SearchX : Unplug
  const frameColor = absent ? absentPhoneColor : phoneColors[index % phoneColors.length]
  return <button type="button" aria-pressed={active || follower} onClick={onClick} className={`relative justify-self-center overflow-hidden rounded-[9px] border-[3px] text-left text-white transition-colors focus-visible:outline-3 focus-visible:outline-primary ${active ? "border-primary ring-2 ring-primary/40" : follower ? "border-primary/70" : "border-slate-500"} ${frameColor}`} style={{ width, height, boxSizing: "border-box" }}>{absent ? null : <LiveTilePicture device={device} mirror={mirror} transport={transport} workspaceId={workspaceId} viewing={viewing} />}<span className="absolute inset-0 bg-black/10" style={{ opacity: 1 - settings.opacity / 100 }} />{settings.showTag ? <span className="absolute left-0 top-0 bg-red-500 px-1 text-[8px] font-bold leading-4">{device.controlEligibility === "eligible" ? "OTG" : "HOLD"}</span> : null}<span className="absolute inset-x-0 top-6 text-center">{settings.showIndex ? <span className="block text-lg font-bold leading-none">{index + 1}</span> : null}{settings.showName ? <span className="mt-1 block text-[10px] font-semibold">{device.displayName}</span> : null}{settings.showIp ? <span className="mt-1 block font-mono text-[8px] text-white/80">{device.endpointId}</span> : null}</span>{absent ? <span className="pointer-events-none absolute inset-0 grid place-items-center"><span className="grid place-items-center border border-white/40 bg-slate-950/75 p-1.5"><AbsentIcon role="img" aria-label={deviceObservationSentence(device.displayName, device.status)} className="size-5" /></span></span> : null}<span className="absolute bottom-3 left-3 right-3 flex justify-between text-[10px] text-white/90"><Smartphone className="size-3" aria-hidden="true" /><span>{deviceStatusLabels[device.status]}</span></span>{active ? <span className="absolute inset-x-0 bottom-7 text-center text-[8px] font-semibold uppercase">Open</span> : follower ? <span className="absolute inset-x-0 bottom-7 text-center text-[8px] font-semibold uppercase">Follower</span> : null}</button>
}

/**
 * The floating panel: the big frame, and the controls that surround it.
 *
 * The frame's own body is the device's screen - the picture and the pointer, and
 * nothing else. Everything that used to be printed over that screen is here,
 * in the panel that is a separate surface from the frame:
 *
 *  - the title bar keeps the device's NAME, which now has the room the battery
 *    percentage used to take: the percentage was a second copy of what the
 *    device's own status bar draws in the picture, and a name an operator cannot
 *    read is worse than a number they can already see. It keeps its controls -
 *    pin, close, the info control that holds the stream's state, transport,
 *    encoded frame, the observation its coordinates are measured from, and every
 *    refusal;
 *  - the action column keeps every device command it had and the one control that
 *    stops the stream. The blocks that were added here are gone: the row of key
 *    buttons and the field to type into the device (the keys were the device's
 *    own, so they are its navigation bar in the footer, and typing into a device
 *    is the operator's own keyboard), and the keyboard-capture block with its
 *    paragraph and its `Release keyboard` button. The capture block went because
 *    it was not a control at all: the frame's focus IS the capture boundary, so
 *    leaving capture was always blurring the frame, and the fact is stated once,
 *    where the rest of the frame's state is read - in the info control;
 *  - the footer IS the device's navigation bar - menu/recents, home, back - and
 *    the follower count, and the count reads ABOVE the row: the row is the panel's
 *    LAST element, where the phone itself puts its navigation, and everything the
 *    footer says about the row is stated above it. The `Start Preview` control and
 *    its sentence are gone with the button: selecting a small frame is what makes
 *    it a follower now, so there is nothing left to press.
 *
 * The frame takes the STREAM's aspect: `phoneHeight` is read from the session's
 * frame rather than pinned at 9:16, so an operator sees the device's screen AS
 * the frame instead of a picture inset in a dark box. A stream that has not
 * reported a size yet keeps this console's own portrait shape - the one case
 * with no aspect to take - and the picture's drawn box stays honest either way,
 * so a pointer is still measured through the box the picture is actually in.
 */
export function FloatingDevice({ device, followers, workspace, settings, position, pinned, onPinChange, onPointerDown, onPointerMove, onPointerUp, onClose, onAction, mirror, mirrorTransport, workspaceId, leaseRefusal, hasLease, dispatch }: { device: DeviceView; followers: readonly DeviceView[]; workspace: Workspace; settings: ConsoleSettings; position: FloatingPosition; pinned: boolean; onPinChange: (value: boolean) => void; onPointerDown: (event: PointerEvent<HTMLDivElement>) => void; onPointerMove: (event: PointerEvent<HTMLDivElement>) => void; onPointerUp: (event: PointerEvent<HTMLDivElement>) => void; onClose: () => void; onAction: (value: string) => void; mirror?: LiveMirrorClient; mirrorTransport: LiveMirrorTransportChoice; workspaceId: string; leaseRefusal?: string; hasLease: boolean; dispatch: DispatchIntent }) {
  const session = useLiveMirrorSession({ device, mirror, transport: mirrorTransport, workspaceId, hasLease, leaseRefusal, dispatch })
  const controlsWidth = 220
  const frameGap = 12
  const viewportWidth = typeof window === "undefined" ? Number.POSITIVE_INFINITY : window.innerWidth
  const viewportMargin = 16
  const availableWidth = Number.isFinite(viewportWidth) ? Math.max(320, viewportWidth - viewportMargin * 2) : Number.POSITIVE_INFINITY
  const requestedPhoneWidth = Math.round(workspace.largeHeight * 9 / 16)
  const phoneWidth = Math.min(requestedPhoneWidth, Math.max(96, availableWidth - controlsWidth - frameGap))
  const phoneHeight = Math.round(phoneWidth * (session.frame ? session.frame.height / session.frame.width : 16 / 9))
  const controlsLeft = settings.controlsSide === "left"
  const frameSize = { width: phoneWidth + controlsWidth + frameGap, height: phoneHeight }
  const maxLeft = Number.isFinite(viewportWidth) ? Math.max(viewportMargin, viewportWidth - frameSize.width - viewportMargin) : position.x
  const positionStyle = pinned ? frameSize : { ...frameSize, left: Math.min(Math.max(viewportMargin, position.x), maxLeft), top: Math.max(viewportMargin, position.y) }
  const phoneFrameStyle = { width: phoneWidth, minWidth: phoneWidth, maxWidth: phoneWidth, height: phoneHeight, boxSizing: "border-box" as const }
  const controlsFrameStyle = { width: controlsWidth, minWidth: controlsWidth, maxWidth: controlsWidth, height: phoneHeight, boxSizing: "border-box" as const }
  return <div className={`${pinned ? "relative z-20 self-start" : `fixed ${liveMirrorCopy.layers.floatingFrame}`} flex items-start gap-3 ${controlsLeft ? "flex-row-reverse" : ""}`} style={positionStyle}>
    <div aria-label={`${device.displayName} floating phone frame`} className="flex shrink-0 flex-col overflow-hidden rounded-[22px] border-[3px] border-primary bg-slate-900 text-white" style={phoneFrameStyle}>
      <LiveMirrorSurface session={session} />
    </div>
    <div aria-label={`${device.displayName} floating device controls`} className="flex min-h-0 shrink-0 flex-col overflow-hidden rounded-[22px] border-[3px] border-primary bg-popover text-foreground" style={controlsFrameStyle}>
      <div className={`flex shrink-0 items-center gap-2 border-b border-primary/30 bg-secondary/50 px-3 py-2 ${pinned ? "" : "cursor-grab active:cursor-grabbing"}`} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp}><span className="min-w-0 flex-1 truncate text-sm font-semibold text-primary">{device.displayName}</span><LiveMirrorInfo session={session} /><Button size="icon-sm" variant="ghost" aria-label={pinned ? "Unpin floating device" : "Pin floating device beside frames"} aria-pressed={pinned} onClick={() => onPinChange(!pinned)}><Pin className="size-3.5" /></Button><Button size="icon-sm" variant="ghost" aria-label="Close floating device" onClick={onClose}><X className="size-3.5" /></Button></div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">{session.phase === "live" || session.phase === "starting" ? <Button type="button" size="sm" variant="ghost" className="h-9 w-full justify-start rounded-none px-2 text-xs" onClick={session.stop} data-testid="live-mirror-stop"><X className="size-3.5 text-muted-foreground" aria-hidden="true" />Stop mirror</Button> : null}<div className="my-2 border-t border-border" /><ControlButton icon={Smartphone} label="Change Device" onClick={() => onAction("Change Device")} /><ControlButton icon={Volume2} label="Volume Up" onClick={() => onAction("Volume Up")} /><ControlButton icon={Volume1} label="Volume Down" onClick={() => onAction("Volume Down")} /><ControlButton icon={Image} label="Screenshot" onClick={() => onAction("Screenshot")} /><ControlButton icon={Power} label="Power Button" onClick={() => onAction("Power Button")} /><ControlButton icon={RotateCw} label="Lock Rotate" onClick={() => onAction("Lock Rotate")} /><ControlButton icon={Grid3X3} label="Install APK" onClick={() => onAction("Install APK")} /><ControlButton icon={Upload} label="Import File" onClick={() => onAction("Import File")} /><ControlButton icon={Download} label="Export File" onClick={() => onAction("Export File")} /><ControlButton icon={ClipboardCopy} label="ADB Command" onClick={() => onAction("ADB Command")} /><ControlButton icon={Keyboard} label="Quick Phrase" onClick={() => onAction("Quick Phrase")} /><ControlButton icon={RotateCw} label="Reboot" onClick={() => onAction("Reboot")} /><ControlButton icon={Keyboard} label="Switch Keyboard" onClick={() => onAction("Switch Keyboard")} /></div>
      <div className="shrink-0 border-t border-border px-2 py-2">
        <p data-testid="live-mirror-followers" className="mb-1 text-center text-[10px] text-muted-foreground">{followers.length} follower{followers.length === 1 ? "" : "s"} selected</p>
        <LiveMirrorDeviceKeys session={session} />
      </div>
    </div>
  </div>
}
function ControlButton({ icon: Icon, label, onClick }: { icon: typeof Smartphone; label: string; onClick: () => void }) { return <Button variant="ghost" className="h-9 w-full justify-start rounded-none px-2 text-xs" onClick={onClick}><Icon className="size-3.5 text-muted-foreground" aria-hidden="true" />{label}</Button> }
function SettingToggle({ label, description, checked, onChange, onLabel = "On", offLabel = "Off" }: { label: string; description: string; checked: boolean; onChange: (value: boolean) => void; onLabel?: string; offLabel?: string }) { const id = label.toLowerCase().replaceAll(" ", "-"); return <div className="flex items-start gap-3 border-t border-border pt-4"><Switch id={id} checked={checked} onCheckedChange={onChange} className="mt-0.5 shrink-0" /><label htmlFor={id} className="min-w-0 flex-1 cursor-pointer"><span className="flex justify-between gap-2 text-xs font-medium"><span>{label}</span><span className="text-muted-foreground">{checked ? onLabel : offLabel}</span></span><span className="mt-1 block text-[11px] leading-4 text-muted-foreground">{description}</span></label></div> }
/**
 * rangeEndIp is the second range: its first three octets are the first range's,
 * so they follow it, and only the final octet is the operator's own. The locked
 * octets are DERIVED rather than stored, which is what makes an edit to one of
 * them impossible rather than merely discouraged.
 */
function rangeEndIp(startIp: string, lastOctet: string): string {
  return [...startIp.split(".").slice(0, 3), lastOctet].join(".")
}

function InfoHint({ label, explanation }: { label: string; explanation: string }) {
  return <TooltipProvider delay={0}><Tooltip><TooltipTrigger render={<button type="button" aria-label={`About ${label}`} className="inline-flex size-4 items-center justify-center border border-transparent text-muted-foreground outline-none hover:border-border hover:text-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50"><Info className="size-3" aria-hidden="true" /></button>} /><TooltipContent>{explanation}</TooltipContent></Tooltip></TooltipProvider>
}
function ControlLabel({ label, explanation, htmlFor }: { label: string; explanation?: string; htmlFor?: string }) {
  return <div className="flex min-h-4 items-center gap-1 text-xs font-medium">{htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span>{label}</span>}{explanation ? <InfoHint label={label} explanation={explanation} /> : null}</div>
}
function Choice({ label, value, options, labels, explanation, onChange }: { label: string; value: string; options: readonly string[]; labels?: Record<string, string>; explanation?: string; onChange: (value: string) => void }) { const display = (option: string) => labels?.[option] ?? option.charAt(0).toUpperCase() + option.slice(1); const trigger = <DropdownMenuTrigger render={<Button type="button" variant="outline" aria-label={label} className="mt-2 h-9 w-full justify-between rounded-none px-2 text-xs font-normal" />}><span className="truncate">{display(value)}</span><ChevronDown className="size-3.5" aria-hidden="true" /></DropdownMenuTrigger>; return <div className="block"><ControlLabel label={label} explanation={explanation} /><DropdownMenu>{trigger}<DropdownMenuContent align="start" className="min-w-[var(--anchor-width)]">{options.map((option) => <DropdownMenuItem key={option} onClick={() => onChange(option)}>{display(option)}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu></div> }
function PositionToggle({ label, value, onChange }: { label: string; value: "left" | "right"; onChange: (value: "left" | "right") => void }) { return <div className="block text-xs font-medium"><span>{label}</span><div role="group" aria-label={label} className="mt-2 grid grid-cols-2 border border-input p-1"><Button type="button" size="sm" variant={value === "left" ? "default" : "ghost"} aria-pressed={value === "left"} onClick={() => onChange("left")} className="rounded-none">Left</Button><Button type="button" size="sm" variant={value === "right" ? "default" : "ghost"} aria-pressed={value === "right"} onClick={() => onChange("right")} className="rounded-none">Right</Button></div></div> }
function IpAddressInput({ value, onChange, lockedOctets = 0 }: { value: string; onChange: (value: string) => void; lockedOctets?: number }) { const octets = value.split("."); const label = lockedOctets > 0 ? "IP Range End" : "IP Range Start"; return <div><fieldset aria-label={label}><legend className="sr-only">{label}</legend><div className="grid grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr] items-center gap-1">{octets.map((octet, index) => { const locked = index < lockedOctets; const field = <Input aria-label={`${label} octet ${index + 1}`} inputMode="numeric" maxLength={3} value={octet} disabled={locked} onChange={(event) => { const next = [...octets]; next[index] = event.target.value.replace(/\D/g, "").slice(0, 3); onChange(next.join(".")) }} className="h-9 min-w-0 px-1 text-center font-mono text-xs" />; return <span key={`${label}-${index}`} className="contents">{field}{index < 3 ? <span aria-hidden="true" className="text-muted-foreground">.</span> : null}</span> })}</div></fieldset></div> }
function Slider({ label, value, min, max, unit, step = 1, onChange }: { label: string; value: number; min: number; max: number; step?: number; unit: string; onChange: (value: number) => void }) { const id = label.toLowerCase().replaceAll(" ", "-"); return <label htmlFor={id} className="block text-xs font-medium">{label}<span className="float-right font-mono text-muted-foreground">{value} {unit}</span><input id={id} type="range" min={min} max={max} value={value} step={step} onChange={(event) => onChange(Number(event.target.value))} className="mt-3 w-full accent-primary" /><span className="mt-1 flex justify-between text-[10px] text-muted-foreground"><span>{min} {unit}</span><span>{max} {unit}</span></span></label> }
