import { useMemo, useState, type ReactNode } from "react"
import { Activity, AlertTriangle, Check, Network, RefreshCw, Search, Server, Smartphone, Wifi, XCircle } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CardContent } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { reportDispatch } from "@/lib/api/report-dispatch"
import type { ControlPlaneSnapshot, DeviceStatus as DeviceStatusType, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { DataTablePagination, DeviceStatus, EmptyState, PageIntro, Panel } from "./shared"

export function OverviewPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [selectedDeviceId, setSelectedDeviceId] = useState(snapshot.devices[0]?.id ?? "")
  const [inspectorOpen, setInspectorOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [message, setMessage] = useState("")
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const selectedDevice = snapshot.devices.find((device) => device.id === selectedDeviceId) ?? snapshot.devices[0]
  const filteredDevices = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return snapshot.devices.filter((device) => `${device.displayName} ${device.stableIdentity} ${device.location} ${device.workflow}`.toLowerCase().includes(normalized))
  }, [query, snapshot.devices])
  const pageCount = Math.max(1, Math.ceil(filteredDevices.length / pageSize))
  const safePage = Math.min(page, pageCount - 1)
  const visibleDevices = filteredDevices.slice(safePage * pageSize, (safePage + 1) * pageSize)
  const onlineCount = snapshot.devices.filter((device) => device.status === "online").length
  const attentionCount = snapshot.devices.filter((device) => device.status === "attention").length
  const activeRuns = snapshot.runs.filter((run) => run.state === "running" || run.state === "paused").length
  const measuredLatencies = snapshot.devices.map((device) => device.latencyMs).filter((latency) => latency > 0)
  const medianLatency = median(measuredLatencies)

  function refresh() {
    void reportDispatch(dispatch, { type: "refresh" }, setMessage)
  }

  return <>
    <PageIntro eyebrow="OPERATIONS / FLEET CONTROL" title="Fleet overview" description="Monitor device health, active work, and operator events from one surface." actions={<div className="flex flex-col items-start gap-2 sm:items-end"><span className="drift-data font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">{message || "Updated just now"}</span><Button variant="outline" size="sm" onClick={refresh}><RefreshCw className="size-3.5" aria-hidden="true" />Refresh</Button></div>} />

    <section aria-label="Fleet metrics" className="mb-8 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <MetricCard label="Devices online" value={`${onlineCount}/${snapshot.devices.length}`} detail={`${onlineCount} currently observed`} icon={Wifi} accent="cobalt" />
      <MetricCard label="Needs attention" value={String(attentionCount)} detail="Attention observations" icon={AlertTriangle} accent="amber" />
      <MetricCard label="Active runs" value={String(activeRuns)} detail="Running or paused targets" icon={Activity} accent="violet" />
      <MetricCard label="Median latency" value={medianLatency === null ? "Not measured" : `${medianLatency} ms`} detail={medianLatency === null ? "No device observations yet" : `${measuredLatencies.length} measured devices`} icon={Network} accent="emerald" />
    </section>

    <h2 className="sr-only">Device fleet</h2><Panel title="Device fleet" description={`${snapshot.devices.length} registered identities · transport state shown separately`} action={<div className="relative w-full sm:w-56"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input aria-label="Search devices" value={query} onChange={(event) => { setQuery(event.target.value); setPage(0) }} placeholder="Search devices" className="h-9 rounded-none pl-8 text-xs" /></div>}>
      {filteredDevices.length === 0 ? <EmptyState label="No devices match this search" detail={query ? `No registered identity contains “${query}”.` : "The control plane has not registered a device yet."} /> : <>
        <div role="region" aria-label="Fleet device table" tabIndex={0} className="overflow-x-auto border border-border focus-visible:outline-2 focus-visible:outline-primary"><table className="w-full min-w-[700px] text-left text-xs"><caption className="sr-only">Fleet devices</caption><thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="p-3">Device</th><th className="p-3">Location</th><th className="p-3">Health</th><th className="p-3">Current work</th><th className="p-3">Battery</th><th className="p-3">Last seen</th></tr></thead><tbody>{visibleDevices.map((device) => <DeviceRow key={device.id} device={device} selected={device.id === selectedDevice?.id} onSelect={() => { setSelectedDeviceId(device.id); setInspectorOpen(true) }} />)}</tbody></table></div>
        <DataTablePagination page={safePage} pageSize={pageSize} total={filteredDevices.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} />
      </>}
    </Panel>

    {selectedDevice ? <section aria-labelledby="selected-device-heading" className="mt-6 border border-border bg-card"><div className="flex flex-wrap items-center justify-between gap-3 border-b border-border p-4"><div><h2 id="selected-device-heading" className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Smartphone className="size-4 text-primary" aria-hidden="true" />Selected device</h2><p className="mt-2 text-xs text-muted-foreground">Current device projection · inspect for activity and health</p></div><DeviceStatus device={selectedDevice} /></div><div className="grid gap-3 p-4 sm:grid-cols-2"><InspectorMetric label="Identity" value={selectedDevice.stableIdentity} /><InspectorMetric label="Current work" value={selectedDevice.workflow || "No active work"} /></div></section> : null}

    <section className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.42fr)]">
      <DisclosurePanel icon={Activity} title="Recent activity" description="Auditable events from this workspace"><ActivityTimeline events={snapshot.events.slice(0, 4)} /></DisclosurePanel>
      <DisclosurePanel icon={Server} title="Runtime readiness" description="Local bootstrap readiness"><ReadinessRow label="Web console" state="Ready" /><ReadinessRow label="Control plane" state={snapshot.runtimeConnection.state === "connected" ? "Ready" : "Unavailable"} muted={snapshot.runtimeConnection.state !== "connected"} /><ReadinessRow label="Go services" state={snapshot.runtimeConnection.state === "connected" ? "Ready" : "Unavailable"} muted={snapshot.runtimeConnection.state !== "connected"} /><ReadinessRow label="Device adapters" state={snapshot.labAdapter.readiness === "unavailable" ? "Unavailable" : "Ready"} muted={snapshot.labAdapter.readiness === "unavailable"} /></DisclosurePanel>
    </section>

    <footer className="mt-6 flex flex-col gap-2 border-t border-border pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between"><span>Drift Command Center</span><span className={`flex items-center gap-1.5 font-mono uppercase tracking-[0.08em] ${snapshot.runtimeConnection.state === "connected" ? "text-emerald-700" : "text-amber-700"}`}>{snapshot.runtimeConnection.state === "connected" ? <Check className="size-3" aria-hidden="true" /> : <XCircle className="size-3" aria-hidden="true" />}{snapshot.runtimeConnection.state === "connected" ? "Connected" : snapshot.runtimeConnection.state === "reconnecting" ? "Reconnecting" : "Disconnected"}</span></footer>

    <Sheet modal={false} open={inspectorOpen} onOpenChange={setInspectorOpen}><SheetContent className="w-full rounded-none sm:max-w-xl"><SheetHeader className="border-b border-border"><SheetTitle>{selectedDevice?.displayName ?? "Device Inspector"}</SheetTitle><SheetDescription>Current device, activity, and health projections.</SheetDescription></SheetHeader>{selectedDevice ? <Tabs defaultValue="overview" className="p-4"><TabsList aria-label="Device inspector sections"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="activity">Activity</TabsTrigger><TabsTrigger value="health">Health</TabsTrigger></TabsList><TabsContent value="overview" className="space-y-3 pt-4 text-xs"><DeviceStatus device={selectedDevice} /><p>{selectedDevice.location}</p><p>{selectedDevice.platformVersion || "Platform not reported"}</p><p>Edge agent {selectedDevice.agentId || "not reported"} · last seen {selectedDevice.lastSeen}</p></TabsContent><TabsContent value="activity" className="pt-4"><ActivityTimeline events={snapshot.events.slice(0, 4)} compact /></TabsContent><TabsContent value="health" className="space-y-3 pt-4 text-xs"><p>Battery {selectedDevice.batteryPercent}%</p><p>Latency {selectedDevice.latencyMs > 0 ? `${selectedDevice.latencyMs} ms` : "Not measured"}</p><p>{selectedDevice.workflow}</p></TabsContent></Tabs> : null}</SheetContent></Sheet>
  </>
}

function median(values: number[]): number | null {
  if (values.length === 0) return null
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 === 0 ? Math.round((sorted[middle - 1] + sorted[middle]) / 2) : sorted[middle]
}

function MetricCard({ label, value, detail, icon: Icon, accent }: { label: string; value: string; detail: string; icon: typeof Wifi; accent: "cobalt" | "amber" | "violet" | "emerald" }) {
  const accentClass = { cobalt: "border-l-2 border-primary bg-secondary text-primary", amber: "border-l-2 border-amber-600 bg-amber-50 text-amber-800", violet: "border-l-2 border-violet-600 bg-violet-50 text-violet-800", emerald: "border-l-2 border-emerald-600 bg-emerald-50 text-emerald-800" }[accent]
  return <article aria-labelledby={`metric-${label.replaceAll(" ", "-").toLowerCase()}`} className="rounded-none border border-border bg-card"><CardContent className="flex items-start gap-3 p-4"><div className={`flex size-9 shrink-0 items-center justify-center rounded-none ${accentClass}`}><Icon className="size-4" aria-hidden="true" /></div><div className="min-w-0"><h2 id={`metric-${label.replaceAll(" ", "-").toLowerCase()}`} className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</h2><p className="drift-data mt-1 text-xl font-semibold tracking-tight">{value}</p><p className="mt-1 truncate text-[11px] text-muted-foreground">{detail}</p></div></CardContent></article>
}

function DeviceRow({ device, selected, onSelect }: { device: DeviceView; selected: boolean; onSelect: () => void }) {
  return <tr className={`border-b border-border/70 transition-colors hover:bg-muted/60 focus-within:bg-muted/60 ${selected ? "bg-secondary/60" : ""}`}><td className="p-3"><button type="button" aria-pressed={selected} onClick={onSelect} className="flex min-h-11 items-center gap-2 text-left font-medium focus-visible:outline-2 focus-visible:outline-primary"><span className={`size-2 rounded-full ${statusDotClass(device.status)}`} aria-hidden="true" /><span>{device.displayName}</span></button><p className="drift-data mt-1 text-[10px] text-muted-foreground">stable · {device.stableIdentity}</p></td><td className="p-3 text-muted-foreground">{device.location || "Not reported"}</td><td className="p-3"><DeviceStatus device={device} /></td><td className="p-3"><p>{device.workflow || "No active work"}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{device.taskProgress}%</p></td><td className="p-3">{device.batteryPercent}%</td><td className="p-3 text-muted-foreground">{device.lastSeen}</td></tr>
}

function statusDotClass(status: DeviceStatusType) { return status === "online" ? "bg-emerald-600" : status === "attention" ? "bg-amber-600" : status === "unobserved" ? "bg-slate-300" : "bg-slate-500" }

function InspectorMetric({ label, value }: { label: string; value: string }) {
  return <div className="border border-border bg-background p-3"><p className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">{label}</p><p className="mt-2 truncate text-xs font-medium">{value}</p></div>
}

function DisclosurePanel({ icon: Icon, title, description, children }: { icon: typeof Activity; title: string; description: string; children: ReactNode }) {
  return <Collapsible className="border border-border bg-card"><CollapsibleTrigger className="flex min-h-14 w-full items-center justify-between p-4 text-left"><span><span className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Icon className="size-4 text-primary" aria-hidden="true" />{title}</span><span className="mt-2 block text-xs text-muted-foreground">{description}</span></span><span className="text-xs text-muted-foreground">Show</span></CollapsibleTrigger><CollapsibleContent className="border-t border-border p-4">{children}</CollapsibleContent></Collapsible>
}

function ActivityTimeline({ events, compact = false }: { events: readonly { id: string; name: string; actor: string; occurredAt: string; payloadSummary: string }[]; compact?: boolean }) {
  const visibleEvents = compact ? events.slice(0, 3) : events
  if (visibleEvents.length === 0) return <p className="text-xs text-muted-foreground">No auditable events yet.</p>
  return <div className="space-y-4">{visibleEvents.map((event, index) => <div key={event.id} className="flex gap-3"><div className="flex flex-col items-center"><span className="mt-1.5 flex size-5 items-center justify-center rounded-full border border-emerald-200 bg-emerald-50 text-emerald-800"><Check className="size-3" aria-hidden="true" /></span>{index < visibleEvents.length - 1 ? <span className="mt-1 h-full w-px bg-border" aria-hidden="true" /> : null}</div><div className="min-w-0 flex-1 pb-1"><div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1"><p className="text-xs font-medium">{event.name}</p><span className="drift-data text-[10px] text-muted-foreground">{event.occurredAt}</span></div><p className="mt-1 text-[11px] text-muted-foreground">{event.actor} · {event.payloadSummary}</p></div></div>)}</div>
}

function ReadinessRow({ label, state, muted = false }: { label: string; state: string; muted?: boolean }) {
  return <div className="flex items-center justify-between rounded-none border border-border bg-card px-3 py-2.5"><span className="text-xs text-muted-foreground">{label}</span><span className={`flex items-center gap-1.5 text-[11px] ${muted ? "text-muted-foreground" : "text-emerald-700"}`}><span className={`size-1.5 rounded-full ${muted ? "bg-slate-400" : "bg-emerald-600"}`} aria-hidden="true" />{state}</span></div>
}
