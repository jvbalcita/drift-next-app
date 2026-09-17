import { useRef, useState, type PointerEvent, type ReactElement } from "react"
import { createPortal } from "react-dom"
import { ChevronDown, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, ClipboardCopy, Crosshair, Download, Grid3X3, Image, Keyboard, MousePointer2, Network, Pin, Power, RotateCw, ScanLine, Settings2, SlidersHorizontal, Smartphone, Upload, Volume1, Volume2, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import type { ControlPlaneIntent, ControlPlaneSnapshot, DeviceView, DispatchIntent, MutationResult } from "@/lib/domain/control-plane"
import { LabModeBadges, LabObservationFrame, LabStatusStrip } from "./lab-adapter"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { captureSerialForDevice } from "./page-utils"
import { EmptyState, OperatorNotice, StatusBadge } from "./shared"

type Workspace = { largeHeight: number; smallHeight: number; quality: "Low" | "Medium" | "High" | "Extra"; frameRate: number; orientation: "portrait" | "landscape" }
type ConsoleSettings = { gap: number; opacity: number; autoScreenOff: boolean; controlSmall: boolean; connection: "WebRTC" | "TCP"; priority: "Speed" | "Quality"; controlsSide: "left" | "right"; workspaceSide: "left" | "right"; showTag: boolean; showIndex: boolean; showName: boolean; showIp: boolean }
type FloatingPosition = { x: number; y: number }
type ConnectionFilter = "all" | "usb" | "wifi" | "otg"
const connectionFilters: { value: ConnectionFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "usb", label: "USB" },
  { value: "wifi", label: "WiFi" },
  { value: "otg", label: "OTG" },
]

const workspaceDefaults: Workspace = { largeHeight: 480, smallHeight: 192, quality: "High", frameRate: 15, orientation: "portrait" }
const settingsDefaults: ConsoleSettings = { gap: 16, opacity: 100, autoScreenOff: false, controlSmall: false, connection: "WebRTC", priority: "Quality", controlsSide: "right", workspaceSide: "left", showTag: true, showIndex: true, showName: true, showIp: true }
const initialPosition: FloatingPosition = { x: 120, y: 88 }
const phoneColors = ["bg-emerald-700", "bg-sky-700", "bg-teal-700", "bg-fuchsia-700", "bg-rose-700", "bg-slate-950", "bg-neutral-950", "bg-cyan-800", "bg-violet-800", "bg-purple-800", "bg-teal-800", "bg-slate-600"]

export function ControlPage({ snapshot, dispatch, dispatchLab, labNotice = "" }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; dispatchLab?: (intent: ControlPlaneIntent) => Promise<MutationResult>; labNotice?: string }) {
  const [workspace, setWorkspace] = useState(workspaceDefaults)
  const [settings, setSettings] = useState(settingsDefaults)
  const [sourceId, setSourceId] = useState<string | null>(null)
  const [followerIds, setFollowerIds] = useState<string[]>([])
  const [position, setPosition] = useState(initialPosition)
  const [workspaceOpen, setWorkspaceOpen] = useState(false)
  const [workspacePinned, setWorkspacePinned] = useState(false)
  const [modalPinned, setModalPinned] = useState(false)
  const [feedback, setFeedback] = useState("")
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

  function choosePhone(device: DeviceView) {
    if (settings.controlSmall) { setFeedback(`${device.displayName} received a compact-frame control selection. No device command was sent.`); return }
    if (!source) {
      setSourceId(device.id)
      setFollowerIds([])
      void reportDispatch(dispatch, { type: "beginDeviceControl", deviceId: device.id }, setFeedback)
      return
    }
    if (source.id === device.id) {
      void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: device.id }, setFeedback)
      setSourceId(null)
      setFollowerIds([])
      return
    }
    setFollowerIds((ids) => ids.includes(device.id) ? ids.filter((id) => id !== device.id) : [...ids, device.id])
    setFeedback(`${device.displayName} ${followerIds.includes(device.id) ? "removed from" : "added to"} the follower selection. No command was sent to followers.`)
  }
  function handleDeviceAction(action: string) {
    const blocked = new Set(["Install APK", "Import File", "Export File", "ADB Command", "Quick Phrase"])
    if (blocked.has(action)) {
      setFeedback(`${action} is unavailable. Packages cannot use shell, unrestricted files, or credentials.`)
      return
    }
    if (action === "Screenshot" && source) {
      void (async () => {
        const serial = captureSerialForDevice(snapshot.endpoints, source.id)
        if (!serial) {
          setFeedback("This device has no single current transport endpoint to observe.")
          return
        }
        const authorized = await reportDispatch(dispatch, { type: "submitDeviceAction", deviceId: source.id, kind: "capture", confirmed: true }, setFeedback)
        if (!authorized.ok) return
        await reportDispatch(dispatch, { type: "captureLabObservation", serial }, setFeedback)
      })()
      return
    }
    setFeedback(`${action} requires confirmation. No unauthorized command was sent.`)
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
  function startPreview() {
    if (!source) return
    void reportDispatch(dispatch, { type: "startMirrorPreview", sourceDeviceId: source.id, followerDeviceIds: followerIds }, setFeedback)
  }
  /**
   * The saved network's own scan. It names the profile the operator selected, so
   * this control's target is the saved network and nothing else.
   */
  function scanSavedNetwork() {
    if (!selectedProfile) { setFeedback("Choose a saved network profile before starting a scan."); return }
    void reportDispatch(dispatch, { type: "startScan", profileId: selectedProfile.id }, setFeedback)
  }
  /**
   * The entered range's own scan. Its target is what the operator typed into the
   * IP Range inputs, read from those inputs and never from the saved network
   * control, so the two Scans in this tab cannot scan the same thing.
   */
  function scanEnteredRange() {
    void reportDispatch(dispatch, { type: "scanRange", startIp, endIp, port: Number(port.trim()) }, setFeedback)
  }
  /**
   * Reload re-observes the devices this workspace already knows. It restarts
   * nothing, so a device that is already discoverable is re-read rather than
   * having the transport it answers on dropped.
   */
  function reloadDevices() {
    void reportDispatch(dispatch, { type: "reloadDevices" }, setFeedback)
  }
  /**
   * Activate moves every discovered device that is not already answering on the
   * Set Port port onto it. It names the port and NOTHING else: the control plane
   * reads the fleet from the devices, so this panel cannot assert which devices
   * are attached, and each device's own result comes back separately.
   */
  function activateFleet() {
    void reportDispatch(dispatch, { type: "activateFleet", port: Number(port.trim()) }, setFeedback)
  }
  function restartAdbServer() {
    if (observedEndpoints.length === 0) { setFeedback("Restarting the adb server needs at least one observed endpoint to re-establish. Nothing was sent."); return }
    void reportDispatch(dispatch, { type: "restartTransportServer", endpoints: observedEndpoints }, setFeedback)
  }
  function addDiscoveryRange() {
    void reportDispatch(dispatch, { type: "addDiscoveryRange", startIp, endIp, port: Number(port.trim()) }, setFeedback)
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

  const deviceModal = source ? <FloatingDevice device={source} followers={followers} workspace={workspace} settings={settings} position={position} pinned={modalPinned} onPinChange={setModalPinned} onPointerDown={beginDrag} onPointerMove={moveDrag} onPointerUp={endDrag} onClose={() => { void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: source.id }, setFeedback); setSourceId(null); setFollowerIds([]) }} onPreview={startPreview} onAction={handleDeviceAction} /> : null
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
      onFeedback={setFeedback}
      notice={labNotice}
    />
    {!workspacePinned ? <WorkspaceToggle side={settings.workspaceSide} onToggle={() => { setWorkspaceOpen(true); setWorkspacePinned(true) }} /> : null}
    <div className="mt-6 flex flex-wrap items-center gap-2 border-y border-border py-3" aria-label="Control workspace toolbar">
      <span className="mr-auto text-xs"><span className="drift-data font-semibold">{selectedCount}</span> selected · {settings.controlSmall ? "compact-frame control enabled" : source ? "click another phone to select followers" : "click a phone to open its large frame"}</span>
      <ConsoleSettingsDialog settings={settings} onChange={setSettings} modalPinned={modalPinned} onModalPinnedChange={setModalPinned} />
      <DeviceListDialog devices={snapshot.devices} endpoints={snapshot.endpoints} dispatch={dispatch} onFeedback={setFeedback} />
      {source ? <Button size="sm" variant="outline" onClick={() => { void reportDispatch(dispatch, { type: "endDeviceControl", deviceId: source.id }, setFeedback); setSourceId(null); setFollowerIds([]) }}><X className="size-3.5" aria-hidden="true" />Close Screen</Button> : null}
      <Button size="sm" variant="outline" disabled={!source || modalPinned} onClick={() => setPosition(initialPosition)}><Crosshair className="size-3.5" aria-hidden="true" />Reset Position</Button>
    </div>
    <div className={`mt-4 grid items-start gap-4 ${workspaceOpen ? settings.workspaceSide === "right" ? "xl:grid-cols-[minmax(0,1fr)_360px]" : "xl:grid-cols-[360px_minmax(0,1fr)]" : ""}`}>
      {workspaceOpen ? <div className={`xl:sticky xl:top-4 ${settings.workspaceSide === "right" ? "xl:order-2" : "xl:order-1"}`}><WorkspacePanel workspace={workspace} onWorkspaceChange={setWorkspace} pinned={workspacePinned} onPinnedChange={(value) => { setWorkspacePinned(value); setWorkspaceOpen(value) }} side={settings.workspaceSide} port={port} onPortChange={setPort} startIp={startIp} onStartIpChange={setStartIp} endIp={endIp} onEndIpChange={setRangeEndLastOctet} profileId={profileId} onProfileIdChange={setProfileId} profiles={snapshot.networkProfiles} endpoints={snapshot.endpoints} onActivate={activateFleet} onRestartServer={restartAdbServer} onAddRange={addDiscoveryRange} onScanRange={scanEnteredRange} onScanSavedNetwork={scanSavedNetwork} onReloadDevices={reloadDevices} /></div> : null}
      <section className={`min-w-0 ${workspaceOpen && settings.workspaceSide === "left" ? "xl:order-2" : ""}`} aria-label="Phone control workspace">
        <LabObservationFrame adapter={snapshot.labAdapter} height={workspace.largeHeight} />
        <ConnectionFilterBar filter={connectionFilter} onChange={setConnectionFilter} shown={visibleDevices.length} total={snapshot.devices.length} />
        <div className={`grid items-start gap-4 ${modalPinned && source ? "xl:grid-cols-[minmax(0,1fr)_auto]" : ""}`}>
          {visibleDevices.length === 0 ? <EmptyState label="No Devices for This Connection" detail="No device in the current view has an observed transport matching this filter." /> : <ScrollArea className="h-[calc(100vh-15rem)] min-h-[420px] min-w-0 border border-border bg-muted/20 p-3"><div className="grid content-start justify-start" style={{ gridTemplateColumns: `repeat(auto-fill, ${workspace.orientation === "portrait" ? Math.round(workspace.smallHeight * 9 / 16) : workspace.smallHeight}px)`, gap: settings.gap }} aria-label="Compact phone frames">{visibleDevices.map((device, index) => <CompactPhone key={device.id} device={device} index={index} size={workspace.smallHeight} orientation={workspace.orientation} active={source?.id === device.id} follower={followerIds.includes(device.id)} settings={settings} onClick={() => choosePhone(device)} />)}</div></ScrollArea>}
          {modalPinned ? deviceModal : null}
        </div>
        <p aria-live="polite" className="mt-4 border-l-2 border-primary bg-secondary/60 p-3 text-xs leading-5 text-muted-foreground">{feedback || "Open a compact phone frame to begin. Follower selection is available only while a large frame is open."}</p>
      </section>
    </div>
    {!modalPinned && deviceModal && typeof document !== "undefined" ? createPortal(deviceModal, document.body) : null}
  </div>
}
function ConnectionFilterBar({ filter, onChange, shown, total }: { filter: ConnectionFilter; onChange: (value: ConnectionFilter) => void; shown: number; total: number }) {
  return <div className="mt-4 flex flex-wrap items-center gap-2 border-y border-border py-3" aria-label="Device connection filters">
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
  return false
}
function WorkspaceToggle({ side, onToggle }: { side: "left" | "right"; onToggle: () => void }) {
  return <div className="group absolute left-0 top-1/2 z-20 flex h-24 w-10 -translate-y-1/2 items-center justify-start"><Button size="icon-sm" variant="outline" className="bg-card/95 opacity-80 transition-opacity group-hover:opacity-100 focus-visible:opacity-100" aria-label="Open Workspace Settings" onClick={onToggle}>{side === "left" ? <ChevronLeft className="size-3.5" aria-hidden="true" /> : <ChevronRight className="size-3.5" aria-hidden="true" />}</Button></div>
}

function WorkspacePanel({ workspace, onWorkspaceChange, pinned, onPinnedChange, side, port, onPortChange, startIp, onStartIpChange, endIp, onEndIpChange, profileId, onProfileIdChange, profiles, endpoints, onActivate, onRestartServer, onAddRange, onScanRange, onScanSavedNetwork, onReloadDevices }: { workspace: Workspace; onWorkspaceChange: (value: Workspace) => void; pinned: boolean; onPinnedChange: (value: boolean) => void; side: "left" | "right"; port: string; onPortChange: (value: string) => void; startIp: string; onStartIpChange: (value: string) => void; endIp: string; onEndIpChange: (value: string) => void; profileId: string; onProfileIdChange: (value: string) => void; profiles: ControlPlaneSnapshot["networkProfiles"]; endpoints: ControlPlaneSnapshot["endpoints"]; onActivate: () => void; onRestartServer: () => void; onAddRange: () => void; onScanRange: () => void; onScanSavedNetwork: () => void; onReloadDevices: () => void }) {
  return <aside className="border border-border bg-card" aria-label="Workspace Settings">
    <div className="flex items-start gap-3 border-b border-border p-4">
      <span className="grid size-8 shrink-0 place-items-center border border-primary/40 bg-secondary text-primary"><SlidersHorizontal className="size-4" aria-hidden="true" /></span>
      <div className="min-w-0 flex-1"><h2 className="text-sm font-semibold">Workspace Settings</h2><p className="mt-1 text-[11px] leading-4 text-muted-foreground">Display and OTG setup</p></div>
      <Button size="icon-sm" variant="ghost" aria-label="Unpin Workspace Settings" aria-pressed={pinned} onClick={() => onPinnedChange(false)}>{side === "left" ? <ChevronsLeft className="size-3.5" aria-hidden="true" /> : <ChevronsRight className="size-3.5" aria-hidden="true" />}</Button>
    </div>
    <Tabs defaultValue="display">
      <TabsList className="grid w-full grid-cols-2 rounded-none border-b border-border bg-muted/30 p-1" aria-label="Workspace Settings Sections"><TabsTrigger value="display" className="rounded-none">Display</TabsTrigger><TabsTrigger value="otg" className="rounded-none">OTG Setup</TabsTrigger></TabsList>
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
        <OtgSetupPanel endpoints={endpoints} profiles={profiles} port={port} onPortChange={onPortChange} startIp={startIp} onStartIpChange={onStartIpChange} endIp={endIp} onEndIpChange={onEndIpChange} profileId={profileId} onProfileIdChange={onProfileIdChange} onActivate={onActivate} onRestartServer={onRestartServer} onAddRange={onAddRange} onScanRange={onScanRange} onScanSavedNetwork={onScanSavedNetwork} onReloadDevices={onReloadDevices} />
      </TabsContent>
    </Tabs>
  </aside>
}

/**
 * The OTG Setup tab. Every explanation sits on the control it explains, in the
 * console's own Tooltip, rather than in a paragraph beneath the control: the tab
 * reads as controls, and the copy beside a control is part of the control.
 *
 * The tab holds TWO scans with two targets, each labelled with what it scans.
 * The IP Range section's Scan observes the range the operator entered; the Saved
 * Network control keeps its own Scan, which observes the selected saved profile.
 * Neither reads the other's target.
 */
function OtgSetupPanel({ endpoints, profiles, port, onPortChange, startIp, onStartIpChange, endIp, onEndIpChange, profileId, onProfileIdChange, onActivate, onRestartServer, onAddRange, onScanRange, onScanSavedNetwork, onReloadDevices }: { endpoints: ControlPlaneSnapshot["endpoints"]; profiles: ControlPlaneSnapshot["networkProfiles"]; port: string; onPortChange: (value: string) => void; startIp: string; onStartIpChange: (value: string) => void; endIp: string; onEndIpChange: (value: string) => void; profileId: string; onProfileIdChange: (value: string) => void; onActivate: () => void; onRestartServer: () => void; onAddRange: () => void; onScanRange: () => void; onScanSavedNetwork: () => void; onReloadDevices: () => void }) {
  // The transports a restart can re-establish: the endpoints the registry
  // currently holds, deduplicated and in a stable order rather than list order.
  const reestablishable = Array.from(new Set(endpoints.filter((endpoint) => endpoint.state === "current" && endpoint.host.trim() !== "").map((endpoint) => `${endpoint.host}:${endpoint.port}`))).sort()
  return <>
    <div>
      <p className="text-xs font-semibold">Quick OTG Setup</p>
      <p className="mt-1 text-[11px] text-muted-foreground">Every control explains itself on focus or hover.</p>
    </div>
    <div>
      <p className="text-xs font-medium">Set Port</p>
      <div className="mt-2 flex gap-2">
        <ControlExplanation explanation="The port every discovered device is moved to. Activate names this port and nothing else: the control plane reads the fleet from the devices.">
          <Input aria-label="Set Port" inputMode="numeric" value={port} onChange={(event) => onPortChange(event.target.value)} className="font-mono text-xs" />
        </ControlExplanation>
        <ControlExplanation explanation="Moves every discovered device that is not already answering on this port onto it, one at a time. A device that is not attached and authorized over USB is refused with its own message, and every device's result is reported separately.">
          <Button size="sm" onClick={onActivate}>Activate</Button>
        </ControlExplanation>
      </div>
    </div>
    <div>
      <p className="text-xs font-medium">IP Range</p>
      <div className="mt-2 space-y-2">
        <IpAddressInput label="IP Range Start" explanation="The first address of the range this section's Scan observes. Type the subnet here once: the second range's first three octets mirror this address." value={startIp} onChange={onStartIpChange} />
        <IpAddressInput label="IP Range End" explanation="The last address of the entered range, and the only octet here you can change: the first three mirror the first range. An octet outside 0 through 255 is refused, and nothing is scanned." value={endIp} lockedOctets={3} onChange={onEndIpChange} />
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2">
        <ControlExplanation explanation="Saves the entered range above as a Network Profile for the Set Port port, only when no saved profile already holds an equivalent range. Created and already-exists are different answers.">
          <Button variant="outline" size="sm" onClick={onAddRange}><Network className="size-3.5" aria-hidden="true" />Add</Button>
        </ControlExplanation>
        <ControlExplanation explanation="Observes ADB-enabled devices inside the entered range above, whatever port they answer on, and reports what it observed. It saves nothing: Add is the control that writes saved policy.">
          <Button size="sm" onClick={onScanRange}><ScanLine className="size-3.5" aria-hidden="true" />Scan Range</Button>
        </ControlExplanation>
      </div>
    </div>
    <div className="space-y-3 border-t border-border pt-4">
      <div className="flex flex-wrap items-end gap-2">
        <div className="min-w-0 flex-1">
          <Choice label="Saved Network" value={profileId} options={profiles.map((profile) => profile.id)} labels={Object.fromEntries(profiles.map((profile) => [profile.id, profile.isDefault ? `${profile.name} · Default` : profile.name]))} explanation="The saved Network Profile that this control's own Scan observes. It is not what the IP Range section's Scan observes." onChange={onProfileIdChange} />
        </div>
        <ControlExplanation explanation="Observes the devices inside the selected saved Network Profile's range. It reads the Saved Network control beside it and never the entered range above.">
          <Button variant="outline" size="sm" onClick={onScanSavedNetwork} disabled={!profileId}><ScanLine className="size-3.5" aria-hidden="true" />Scan Saved Network</Button>
        </ControlExplanation>
      </div>
      <ControlExplanation explanation="Re-reads the devices this workspace already knows, one answer per device, so a device that is already discoverable is reloaded without restarting anything. It restarts no adb server, so the transports a device answers on are not dropped to answer it.">
        <Button variant="outline" size="sm" className="w-full rounded-none" onClick={onReloadDevices}><RotateCw className="size-3.5" aria-hidden="true" />Reload Devices</Button>
      </ControlExplanation>
      {reestablishable.length > 0 ? <ControlExplanation explanation={`Restarts this host's adb server, then re-establishes the ${reestablishable.length} observed endpoint(s) in order. It is a host operation: every transport the server holds drops, including devices outside this workspace, and each endpoint's result is reported separately.`}>
        <Button className="w-full rounded-none" size="sm" variant="outline" onClick={onRestartServer}><Power className="size-3.5" aria-hidden="true" />Restart ADB Server</Button>
      </ControlExplanation> : null}
    </div>
  </>
}

function ConsoleSettingsDialog({ settings, onChange, modalPinned, onModalPinnedChange }: { settings: ConsoleSettings; onChange: (value: ConsoleSettings) => void; modalPinned: boolean; onModalPinnedChange: (value: boolean) => void }) {
  return <Dialog><DialogTrigger render={<Button size="sm" variant="outline" />}><Settings2 className="size-3.5" aria-hidden="true" />Settings</DialogTrigger><DialogContent className="max-h-[calc(100vh-2rem)] max-w-xl overflow-y-auto rounded-none"><DialogHeader><DialogTitle>Console Settings</DialogTitle><DialogDescription>Local display and interaction preferences. These controls do not alter device policy, transport, or runtime state.</DialogDescription></DialogHeader><Tabs defaultValue="appearance" className="border-y border-border py-4"><TabsList className="grid w-full grid-cols-2 rounded-none border-b border-border bg-muted/30 p-1" aria-label="Console Settings sections"><TabsTrigger value="appearance">Workspace Appearance</TabsTrigger><TabsTrigger value="presentation">Device Presentation</TabsTrigger></TabsList><TabsContent value="appearance" className="space-y-5 p-1 pt-5"><Slider label="Devices Gap" value={settings.gap} min={4} max={32} unit="px" onChange={(gap) => onChange({ ...settings, gap })} /><Slider label="Info Opacity" value={settings.opacity} min={30} max={100} unit="%" onChange={(opacity) => onChange({ ...settings, opacity })} /><SettingToggle label="Auto Screen Off" description="Preference for inactive compact frames." checked={settings.autoScreenOff} onChange={(autoScreenOff) => onChange({ ...settings, autoScreenOff })} /><SettingToggle label="Control Small Screen" description="Uses compact-frame control rather than opening the big device." checked={settings.controlSmall} onChange={(controlSmall) => onChange({ ...settings, controlSmall })} /><Choice label="Connection" value={settings.connection} options={["WebRTC", "TCP"]} onChange={(connection) => onChange({ ...settings, connection: connection as ConsoleSettings["connection"] })} /><Choice label="Priority" value={settings.priority} options={["Speed", "Quality"]} onChange={(priority) => onChange({ ...settings, priority: priority as ConsoleSettings["priority"] })} /></TabsContent><TabsContent value="presentation" className="space-y-5 p-1 pt-5"><PositionToggle label="Control Position" value={settings.controlsSide} onChange={(controlsSide) => onChange({ ...settings, controlsSide })} /><PositionToggle label="Workspace Position" value={settings.workspaceSide} onChange={(workspaceSide) => onChange({ ...settings, workspaceSide })} /><SettingToggle label="Connect Tag" description="Show the OTG or Hold badge on compact frames." checked={settings.showTag} onChange={(showTag) => onChange({ ...settings, showTag })} /><SettingToggle label="Device Index" description="Show the numbered device index." checked={settings.showIndex} onChange={(showIndex) => onChange({ ...settings, showIndex })} /><SettingToggle label="Device Name" description="Show the device display name." checked={settings.showName} onChange={(showName) => onChange({ ...settings, showName })} /><SettingToggle label="Device IP" description="Show the compact-frame endpoint identifier." checked={settings.showIp} onChange={(showIp) => onChange({ ...settings, showIp })} /><SettingToggle label="Modal Control Position" description="Pinned places the big frame beside the device grid; drag floats it above the page." checked={modalPinned} onChange={onModalPinnedChange} onLabel="Pinned" offLabel="Drag" /></TabsContent></Tabs></DialogContent></Dialog>
}

function DeviceListDialog({ devices, endpoints, dispatch, onFeedback }: { devices: readonly DeviceView[]; endpoints: ControlPlaneSnapshot["endpoints"]; dispatch: DispatchIntent; onFeedback: (value: string) => void }) {
  return <Dialog><DialogTrigger render={<Button size="sm" variant="outline" />}><Smartphone className="size-3.5" aria-hidden="true" />Devices</DialogTrigger><DialogContent className="max-w-3xl rounded-none"><DialogHeader><DialogTitle>Device List</DialogTitle><DialogDescription>Connected devices available in this workspace. The ADB server restart lives in Workspace Settings → OTG Setup, so the host-wide operation has one control.</DialogDescription></DialogHeader><div className="flex flex-wrap gap-2 border-y border-border py-3"><Button size="sm" variant="outline" onClick={() => { void reportDispatch(dispatch, { type: "refresh" }, onFeedback) }}><RotateCw className="size-3.5" aria-hidden="true" />Reload</Button></div><div className="max-h-[60vh] overflow-y-auto"><table className="w-full text-left text-xs"><thead className="sticky top-0 bg-muted/80 text-[10px] uppercase tracking-[.08em] text-muted-foreground"><tr><th className="px-3 py-2 font-semibold">Index</th><th className="px-3 py-2 font-semibold">Device Name</th><th className="px-3 py-2 font-semibold">Device ID</th><th className="px-3 py-2 font-semibold">Observed Port</th></tr></thead><tbody className="divide-y divide-border">{devices.map((device, index) => {
    // The port is the one the device was OBSERVED on, or nothing. Painting one
    // field into every row would show a port no device answered on.
    const observed = endpoints.find((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")
    return <tr key={device.id}><td className="px-3 py-3 font-mono text-muted-foreground">{index + 1}</td><td className="px-3 py-3 font-medium">{device.displayName}</td><td className="px-3 py-3 font-mono text-[10px] text-muted-foreground">{device.id}</td><td className="px-3 py-3 font-mono">{observed ? observed.port : "—"}</td></tr>
  })}</tbody></table></div></DialogContent></Dialog>
}

function CompactPhone({ device, index, size, orientation, active, follower, settings, onClick }: { device: DeviceView; index: number; size: number; orientation: Workspace["orientation"]; active: boolean; follower: boolean; settings: ConsoleSettings; onClick: () => void }) {
  const width = orientation === "portrait" ? Math.round(size * 9 / 16) : size
  const height = orientation === "portrait" ? size : Math.round(size * 9 / 16)
  return <button type="button" aria-pressed={active || follower} onClick={onClick} className={`relative justify-self-center overflow-hidden rounded-[9px] border-[3px] text-left text-white transition-colors focus-visible:outline-3 focus-visible:outline-primary ${active ? "border-primary ring-2 ring-primary/40" : follower ? "border-primary/70" : "border-slate-500"} ${phoneColors[index % phoneColors.length]}`} style={{ width, height, boxSizing: "border-box" }}><span className="absolute inset-0 bg-black/10" style={{ opacity: 1 - settings.opacity / 100 }} />{settings.showTag ? <span className="absolute left-0 top-0 bg-red-500 px-1 text-[8px] font-bold leading-4">{device.controlEligibility === "eligible" ? "OTG" : "HOLD"}</span> : null}<span className="absolute inset-x-0 top-6 text-center">{settings.showIndex ? <span className="block text-lg font-bold leading-none">{index + 1}</span> : null}{settings.showName ? <span className="mt-1 block text-[10px] font-semibold">{device.displayName}</span> : null}{settings.showIp ? <span className="mt-1 block font-mono text-[8px] text-white/80">{device.endpointId}</span> : null}</span><span className="absolute bottom-3 left-3 right-3 flex justify-between text-[10px] text-white/90"><Smartphone className="size-3" aria-hidden="true" /><span>{device.status.charAt(0).toUpperCase() + device.status.slice(1)}</span></span>{active ? <span className="absolute inset-x-0 bottom-7 text-center text-[8px] font-semibold uppercase">Open</span> : follower ? <span className="absolute inset-x-0 bottom-7 text-center text-[8px] font-semibold uppercase">Follower</span> : null}</button>
}

function FloatingDevice({ device, followers, workspace, settings, position, pinned, onPinChange, onPointerDown, onPointerMove, onPointerUp, onClose, onPreview, onAction }: { device: DeviceView; followers: readonly DeviceView[]; workspace: Workspace; settings: ConsoleSettings; position: FloatingPosition; pinned: boolean; onPinChange: (value: boolean) => void; onPointerDown: (event: PointerEvent<HTMLDivElement>) => void; onPointerMove: (event: PointerEvent<HTMLDivElement>) => void; onPointerUp: (event: PointerEvent<HTMLDivElement>) => void; onClose: () => void; onPreview: () => void; onAction: (value: string) => void }) {
  const controlsWidth = 220
  const frameGap = 12
  const viewportWidth = typeof window === "undefined" ? Number.POSITIVE_INFINITY : window.innerWidth
  const viewportMargin = 16
  const availableWidth = Number.isFinite(viewportWidth) ? Math.max(320, viewportWidth - viewportMargin * 2) : Number.POSITIVE_INFINITY
  const requestedPhoneWidth = Math.round(workspace.largeHeight * 9 / 16)
  const phoneWidth = Math.min(requestedPhoneWidth, Math.max(96, availableWidth - controlsWidth - frameGap))
  const phoneHeight = Math.round(phoneWidth * 16 / 9)
  const controlsLeft = settings.controlsSide === "left"
  const frameSize = { width: phoneWidth + controlsWidth + frameGap, height: phoneHeight }
  const maxLeft = Number.isFinite(viewportWidth) ? Math.max(viewportMargin, viewportWidth - frameSize.width - viewportMargin) : position.x
  const positionStyle = pinned ? frameSize : { ...frameSize, left: Math.min(Math.max(viewportMargin, position.x), maxLeft), top: Math.max(viewportMargin, position.y) }
  const phoneFrameStyle = { width: phoneWidth, minWidth: phoneWidth, maxWidth: phoneWidth, height: phoneHeight, boxSizing: "border-box" as const }
  const controlsFrameStyle = { width: controlsWidth, minWidth: controlsWidth, maxWidth: controlsWidth, height: phoneHeight, boxSizing: "border-box" as const }
  return <div className={`${pinned ? "relative z-20 self-start" : "fixed z-[100]"} flex items-start gap-3 ${controlsLeft ? "flex-row-reverse" : ""}`} style={positionStyle}>
    <div aria-label={`${device.displayName} floating phone frame`} className="flex shrink-0 flex-col overflow-hidden rounded-[22px] border-[3px] border-primary bg-slate-900 text-white" style={phoneFrameStyle}>
      <div className="flex h-8 shrink-0 items-center justify-between bg-slate-950 px-3 text-[9px]"><span>02:16 · {settings.connection}</span><span>{device.batteryPercent}%</span></div>
      <div className="relative flex min-h-0 flex-1 flex-col items-center justify-center bg-emerald-700 p-5 text-center"><div className="absolute inset-x-4 top-4 rounded-full bg-white/15 px-3 py-2 text-left text-[9px] text-white/80">Search</div><Smartphone className="size-10 text-white/80" aria-hidden="true" /><p className="mt-4 text-base font-semibold">{device.displayName}</p><p className="mt-1 text-[10px] text-white/80">{device.workflow}</p><p className="mt-5 text-[10px] leading-4 text-white/70">Interactive phone surface</p><Button size="sm" className="mt-4" onClick={onPreview}><MousePointer2 className="size-3.5" aria-hidden="true" />Start Preview</Button><div className="absolute inset-x-0 bottom-4 flex justify-around text-[10px] text-white/80"><span>Menu</span><span>Home</span><span>Back</span></div></div>
    </div>
    <div aria-label={`${device.displayName} floating device controls`} className="flex min-h-0 shrink-0 flex-col overflow-hidden rounded-[22px] border-[3px] border-primary bg-popover text-foreground" style={controlsFrameStyle}>
      <div className={`flex shrink-0 items-center gap-2 border-b border-primary/30 bg-secondary/50 px-3 py-2 ${pinned ? "" : "cursor-grab active:cursor-grabbing"}`} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp}><span className="min-w-0 flex-1 truncate text-sm font-semibold text-primary">{device.displayName}</span><Button size="icon-sm" variant="ghost" aria-label={pinned ? "Unpin floating device" : "Pin floating device beside frames"} aria-pressed={pinned} onClick={() => onPinChange(!pinned)}><Pin className="size-3.5" /></Button><Button size="icon-sm" variant="ghost" aria-label="Close floating device" onClick={onClose}><X className="size-3.5" /></Button></div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2"><ControlButton icon={Smartphone} label="Change Device" onClick={() => onAction("Change Device")} /><ControlButton icon={Volume2} label="Volume Up" onClick={() => onAction("Volume Up")} /><ControlButton icon={Volume1} label="Volume Down" onClick={() => onAction("Volume Down")} /><ControlButton icon={Image} label="Screenshot" onClick={() => onAction("Screenshot")} /><ControlButton icon={Power} label="Power Button" onClick={() => onAction("Power Button")} /><ControlButton icon={RotateCw} label="Lock Rotate" onClick={() => onAction("Lock Rotate")} /><ControlButton icon={Grid3X3} label="Install APK" onClick={() => onAction("Install APK")} /><ControlButton icon={Upload} label="Import File" onClick={() => onAction("Import File")} /><ControlButton icon={Download} label="Export File" onClick={() => onAction("Export File")} /><ControlButton icon={ClipboardCopy} label="ADB Command" onClick={() => onAction("ADB Command")} /><ControlButton icon={Keyboard} label="Quick Phrase" onClick={() => onAction("Quick Phrase")} /><ControlButton icon={RotateCw} label="Reboot" onClick={() => onAction("Reboot")} /><ControlButton icon={Keyboard} label="Switch Keyboard" onClick={() => onAction("Switch Keyboard")} /></div>
      <div className="shrink-0 border-t border-border px-3 py-2 text-[10px] text-muted-foreground">{followers.length} follower{followers.length === 1 ? "" : "s"} selected</div>
    </div>
  </div>
}
function ControlButton({ icon: Icon, label, onClick }: { icon: typeof Smartphone; label: string; onClick: () => void }) { return <Button variant="ghost" className="h-9 w-full justify-start rounded-none px-2 text-xs" onClick={onClick}><Icon className="size-3.5 text-muted-foreground" aria-hidden="true" />{label}</Button> }
function SettingToggle({ label, description, checked, onChange, onLabel = "On", offLabel = "Off" }: { label: string; description: string; checked: boolean; onChange: (value: boolean) => void; onLabel?: string; offLabel?: string }) { const id = label.toLowerCase().replaceAll(" ", "-"); return <div className="flex items-start gap-3 border-t border-border pt-4"><input id={id} type="checkbox" role="switch" checked={checked} onChange={(event) => onChange(event.target.checked)} className="mt-0.5 size-4 accent-primary focus-visible:ring-2 focus-visible:ring-primary" /><label htmlFor={id} className="min-w-0 flex-1 cursor-pointer"><span className="flex justify-between gap-2 text-xs font-medium"><span>{label}</span><span className="text-muted-foreground">{checked ? onLabel : offLabel}</span></span><span className="mt-1 block text-[11px] leading-4 text-muted-foreground">{description}</span></label></div> }
/**
 * rangeEndIp is the second range: its first three octets are the first range's,
 * so they follow it, and only the final octet is the operator's own. The locked
 * octets are DERIVED rather than stored, which is what makes an edit to one of
 * them impossible rather than merely discouraged.
 */
function rangeEndIp(startIp: string, lastOctet: string): string {
  return [...startIp.split(".").slice(0, 3), lastOctet].join(".")
}

/**
 * ControlExplanation carries the explanation a control would otherwise have as a
 * paragraph beneath it, on the control itself. The trigger is the control, so the
 * explanation opens on keyboard focus as well as on hover — it is reachable
 * without a pointer — and it costs the tab no vertical space.
 */
function ControlExplanation({ explanation, children }: { explanation: string; children: ReactElement }) {
  return <TooltipProvider delay={0}><Tooltip><TooltipTrigger render={children} /><TooltipContent>{explanation}</TooltipContent></Tooltip></TooltipProvider>
}
function Choice({ label, value, options, labels, explanation, onChange }: { label: string; value: string; options: readonly string[]; labels?: Record<string, string>; explanation?: string; onChange: (value: string) => void }) { const display = (option: string) => labels?.[option] ?? option.charAt(0).toUpperCase() + option.slice(1); const trigger = <DropdownMenuTrigger render={<Button type="button" variant="outline" aria-label={label} className="mt-2 h-9 w-full justify-between rounded-none px-2 text-xs font-normal" />}><span className="truncate">{display(value)}</span><ChevronDown className="size-3.5" aria-hidden="true" /></DropdownMenuTrigger>; return <div className="block text-xs font-medium"><span>{label}</span><DropdownMenu>{explanation ? <ControlExplanation explanation={explanation}>{trigger}</ControlExplanation> : trigger}<DropdownMenuContent align="start" className="min-w-[var(--anchor-width)]">{options.map((option) => <DropdownMenuItem key={option} onClick={() => onChange(option)}>{display(option)}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu></div> }
function PositionToggle({ label, value, onChange }: { label: string; value: "left" | "right"; onChange: (value: "left" | "right") => void }) { return <div className="block text-xs font-medium"><span>{label}</span><div role="group" aria-label={label} className="mt-2 grid grid-cols-2 border border-input p-1"><Button type="button" size="sm" variant={value === "left" ? "default" : "ghost"} aria-pressed={value === "left"} onClick={() => onChange("left")} className="rounded-none">Left</Button><Button type="button" size="sm" variant={value === "right" ? "default" : "ghost"} aria-pressed={value === "right"} onClick={() => onChange("right")} className="rounded-none">Right</Button></div></div> }
function IpAddressInput({ label, value, onChange, explanation, lockedOctets = 0 }: { label: string; value: string; onChange: (value: string) => void; explanation?: string; lockedOctets?: number }) { const octets = value.split("."); return <fieldset><legend className="sr-only">{label}</legend><div className="grid grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr] items-center gap-1">{octets.map((octet, index) => { const locked = index < lockedOctets; const field = <Input aria-label={`${label} octet ${index + 1}`} inputMode="numeric" maxLength={3} value={octet} disabled={locked} onChange={(event) => { const next = [...octets]; next[index] = event.target.value.replace(/\D/g, "").slice(0, 3); onChange(next.join(".")) }} className="h-9 min-w-0 px-1 text-center font-mono text-xs" />; return <span key={`${label}-${index}`} className="contents">{explanation && index === Math.min(lockedOctets, octets.length - 1) ? <ControlExplanation explanation={explanation}>{field}</ControlExplanation> : field}{index < 3 ? <span aria-hidden="true" className="text-muted-foreground">.</span> : null}</span> })}</div></fieldset> }
function Slider({ label, value, min, max, unit, step = 1, onChange }: { label: string; value: number; min: number; max: number; step?: number; unit: string; onChange: (value: number) => void }) { const id = label.toLowerCase().replaceAll(" ", "-"); return <label htmlFor={id} className="block text-xs font-medium">{label}<span className="float-right font-mono text-muted-foreground">{value} {unit}</span><input id={id} type="range" min={min} max={max} value={value} step={step} onChange={(event) => onChange(Number(event.target.value))} className="mt-3 w-full accent-primary" /><span className="mt-1 flex justify-between text-[10px] text-muted-foreground"><span>{min} {unit}</span><span>{max} {unit}</span></span></label> }
