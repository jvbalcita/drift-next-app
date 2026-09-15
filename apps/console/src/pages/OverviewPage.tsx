import { useMemo, useState } from "react"
import {
  Activity,
  AlertTriangle,
  BatteryCharging,
  Check,
  CircleHelp,
  Clock3,
  Cpu,
  Grid2X2,
  Network,
  Play,
  RefreshCw,
  Search,
  Server,
  Smartphone,
  Wifi,
  XCircle,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { DataTablePagination } from "./shared"
import type {
  ControlPlaneSnapshot,
  DeviceStatus,
  DeviceView,
  DispatchIntent,
} from "@/lib/domain/control-plane"

export function OverviewPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [selectedDeviceId, setSelectedDeviceId] = useState(snapshot.devices[0]?.id ?? "")
  const [inspectorOpen, setInspectorOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [lastRefresh, setLastRefresh] = useState("just now")
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const selectedDevice = snapshot.devices.find((device) => device.id === selectedDeviceId) ?? snapshot.devices[0]
  const filteredDevices = useMemo(
    () => snapshot.devices.filter((device) => `${device.displayName} ${device.location} ${device.workflow}`.toLowerCase().includes(query.toLowerCase())),
    [query, snapshot.devices],
  )
  const visibleDevices = filteredDevices.slice(page * pageSize, (page + 1) * pageSize)
  const onlineCount = snapshot.devices.filter((device) => device.status === "online").length
  const attentionCount = snapshot.devices.filter((device) => device.status === "attention").length
  const activeRuns = snapshot.runs.filter((run) => run.state === "running" || run.state === "paused").length

  return (
    <>
      <div className="mb-8 flex flex-col justify-between gap-6 md:flex-row md:items-end">
        <div>
          <div className="drift-kicker flex items-center gap-3"><span className="h-px w-8 bg-primary" aria-hidden="true" /><span>OPERATIONS / FLEET CONTROL</span></div>
          <h1 className="mt-3 text-3xl font-semibold tracking-[-0.05em] sm:text-5xl">Fleet overview</h1>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">Monitor device health, active work, and operator events from one surface.</p>
        </div>
        <div className="flex flex-col items-start gap-3 md:items-end">
          <span className="font-mono drift-data text-[10px] uppercase tracking-[0.12em] text-muted-foreground">Updated {lastRefresh}</span>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => { void dispatch({ type: "refresh" }).then(() => setLastRefresh("just now")) }}>
              <RefreshCw className="size-3.5" aria-hidden="true" />
              Refresh
            </Button>
            <Button size="sm" disabled title="Requires Confirmation">
              <Play className="size-3.5" aria-hidden="true" />
              Run workflow
            </Button>
          </div>
        </div>
      </div>

      <section aria-label="Fleet metrics" className="mb-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard label="Devices online" value={`${onlineCount}/${snapshot.devices.length}`} detail="All agents reporting" icon={Wifi} accent="cobalt" />
        <MetricCard label="Needs attention" value={String(attentionCount)} detail="Reconnecting or offline" icon={AlertTriangle} accent="amber" />
        <MetricCard label="Active runs" value={String(activeRuns)} detail="Independent target state" icon={Activity} accent="violet" />
        <MetricCard label="Median latency" value="48 ms" detail="Observation window" icon={Network} accent="emerald" />
      </section>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <Card className="rounded-none border-border bg-card">
          <CardHeader className="border-b border-border pb-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Grid2X2 className="size-4 text-primary" aria-hidden="true" /><h2>Device fleet</h2></CardTitle>
                <CardDescription className="mt-2 text-xs">{snapshot.devices.length} registered identities · transport state shown separately</CardDescription>
              </div>
              <div className="relative w-full sm:w-52">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                <input aria-label="Search devices" value={query} onChange={(event) => { setQuery(event.target.value); setPage(0) }} placeholder="Search devices" className="h-8 w-full rounded-none border border-input bg-background px-3 pl-8 text-xs outline-none transition focus:border-primary focus:ring-2 focus:ring-primary/20" />
              </div>
            </div>
          </CardHeader>
          <CardContent className="p-3 sm:p-4">
            <div className="overflow-x-auto"><table className="w-full min-w-[700px] text-left text-xs"><caption className="sr-only">Fleet devices</caption><thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="pb-3">Device</th><th className="pb-3">Location</th><th className="pb-3">Health</th><th className="pb-3">Current work</th><th className="pb-3">Battery</th><th className="pb-3">Last seen</th></tr></thead><tbody>{visibleDevices.map((device) => <DeviceCard key={device.id} device={device} selected={device.id === selectedDevice?.id} onSelect={() => { setSelectedDeviceId(device.id); setInspectorOpen(true) }} />)}</tbody></table></div>
            {filteredDevices.length === 0 ? <p className="px-2 py-8 text-center text-sm text-muted-foreground">No devices match “{query}”.</p> : <DataTablePagination page={page} pageSize={pageSize} total={filteredDevices.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} />}
          </CardContent>
        </Card>

        {selectedDevice ? <Card className="hidden rounded-none border-border bg-card">
          <CardHeader className="border-b border-border pb-4">
            <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Smartphone className="size-4 text-primary" aria-hidden="true" /><h2>Selected device</h2></CardTitle>
            <CardDescription className="mt-2 text-xs">Current device, edge-agent, and observation projections</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <div className="flex items-center gap-3 border-b border-border px-4 py-4">
              <div className="flex size-11 items-center justify-center rounded-none border-l-2 border-primary bg-secondary text-primary"><Smartphone className="size-5" aria-hidden="true" /></div>
              <div className="min-w-0 flex-1"><p className="truncate text-sm font-semibold">{selectedDevice.displayName}</p><p className="truncate text-xs text-muted-foreground">{selectedDevice.location} · {selectedDevice.platformVersion}</p></div>
              <DeviceStatusBadge status={selectedDevice.status} />
            </div>
            <Tabs defaultValue="overview" className="w-full">
              <TabsList className="mx-4 mt-4 w-[calc(100%-2rem)]" variant="line">
                <TabsTrigger value="overview">Overview</TabsTrigger>
                <TabsTrigger value="activity">Activity</TabsTrigger>
              </TabsList>
              <TabsContent value="overview" className="px-4 pb-4 pt-4">
                <div className="grid grid-cols-2 gap-3">
                  <InspectorMetric icon={Cpu} label="Edge agent" value={selectedDevice.agentId} />
                  <InspectorMetric icon={Clock3} label="Last seen" value={selectedDevice.lastSeen} />
                  <InspectorMetric icon={BatteryCharging} label="Battery" value={`${selectedDevice.batteryPercent}%`} />
                  <InspectorMetric icon={Network} label="Latency" value={selectedDevice.latencyMs ? `${selectedDevice.latencyMs} ms` : "—"} />
                </div>
                <div className="mt-4 rounded-none border border-border bg-muted p-3">
                  <div className="mb-2 flex items-center justify-between"><span className="text-xs font-semibold uppercase tracking-[0.08em]">Current workflow</span><span className="drift-data text-[11px] text-muted-foreground">{selectedDevice.taskProgress}%</span></div>
                  <p className="mb-2 text-xs text-muted-foreground">{selectedDevice.workflow}</p>
                  <div className="h-1.5 overflow-hidden rounded-none bg-background"><div className="h-full rounded-none bg-primary transition-all" style={{ width: `${selectedDevice.taskProgress}%` }} /></div>
                </div>
                <div className="mt-4 flex items-start gap-2 rounded-none border-l-2 border-primary bg-secondary/70 p-3 text-[11px] leading-relaxed text-muted-foreground"><CircleHelp className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />This inspector shows current projections. It does not send device commands.</div>
              </TabsContent>
              <TabsContent value="activity" className="px-4 pb-4 pt-4"><ActivityTimeline events={snapshot.events.slice(0, 4)} compact /></TabsContent>
            </Tabs>
          </CardContent>
        </Card> : null}
      </div>
      <Sheet modal={false} open={inspectorOpen} onOpenChange={setInspectorOpen}><SheetContent className="w-full rounded-none sm:max-w-xl"><SheetHeader className="border-b border-border"><SheetTitle>{selectedDevice?.displayName ?? "Device Inspector"}</SheetTitle><SheetDescription>Current device, activity, and health projections.</SheetDescription></SheetHeader>{selectedDevice ? <Tabs defaultValue="overview" className="p-4"><TabsList className="rounded-none border border-border bg-background p-0"><TabsTrigger value="overview" className="rounded-none">Overview</TabsTrigger><TabsTrigger value="activity" className="rounded-none">Activity</TabsTrigger><TabsTrigger value="health" className="rounded-none">Health</TabsTrigger></TabsList><TabsContent value="overview" className="space-y-3 text-xs"><DeviceStatusBadge status={selectedDevice.status} /><p>{selectedDevice.location}</p><p>Edge agent {selectedDevice.agentId} · last seen {selectedDevice.lastSeen}</p></TabsContent><TabsContent value="activity"><ActivityTimeline events={snapshot.events.slice(0, 4)} compact /></TabsContent><TabsContent value="health" className="text-xs">Battery {selectedDevice.batteryPercent}% · latency {selectedDevice.latencyMs} ms · {selectedDevice.workflow}</TabsContent></Tabs> : null}</SheetContent></Sheet>

      <section className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.42fr)]">
        <Collapsible className="border border-border bg-card"><CollapsibleTrigger className="flex w-full items-center justify-between p-4 text-left"><span><span className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Activity className="size-4 text-primary" aria-hidden="true" />Recent activity</span><span className="mt-2 block text-xs text-muted-foreground">Auditable events from this workspace</span></span><span className="text-xs text-muted-foreground">Show</span></CollapsibleTrigger><CollapsibleContent className="border-t border-border p-4"><ActivityTimeline events={snapshot.events.slice(0, 4)} /></CollapsibleContent></Collapsible>
        <Collapsible className="border border-border bg-card"><CollapsibleTrigger className="flex w-full items-center justify-between p-4 text-left"><span><span className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Server className="size-4 text-primary" aria-hidden="true" />Runtime readiness</span><span className="mt-2 block text-xs text-muted-foreground">Local bootstrap readiness</span></span><span className="text-xs text-muted-foreground">Show</span></CollapsibleTrigger><CollapsibleContent className="space-y-3 border-t border-border p-4"><ReadinessRow label="Web console" state="Ready" /><ReadinessRow label="Control plane" state={snapshot.runtimeConnection.state === "connected" ? "Ready" : "Unavailable"} /><ReadinessRow label="Go services" state={snapshot.runtimeConnection.state === "connected" ? "Ready" : "Unavailable"} muted={snapshot.runtimeConnection.state !== "connected"} /><ReadinessRow label="Device adapters" state={snapshot.labAdapter.readiness === "unavailable" ? "Unavailable" : "Ready"} muted={snapshot.labAdapter.readiness === "unavailable"} /></CollapsibleContent></Collapsible>
      </section>

      <footer className="mt-6 flex flex-col gap-2 border-t border-border pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between"><span>Drift Command Center</span><span className={`flex items-center gap-1.5 font-mono uppercase tracking-[0.08em] ${snapshot.runtimeConnection.state === "connected" ? "text-emerald-700" : "text-amber-700"}`}>{snapshot.runtimeConnection.state === "connected" ? <Check className="size-3" aria-hidden="true" /> : <XCircle className="size-3" aria-hidden="true" />}{snapshot.runtimeConnection.state === "connected" ? "Connected" : snapshot.runtimeConnection.state === "reconnecting" ? "Reconnecting" : "Disconnected"}</span></footer>
    </>
  )
}

function MetricCard({ label, value, detail, icon: Icon, accent }: { label: string; value: string; detail: string; icon: typeof Wifi; accent: "cobalt" | "amber" | "violet" | "emerald" }) {
  const accentClass = { cobalt: "border-l-2 border-primary bg-secondary text-primary", amber: "border-l-2 border-amber-600 bg-amber-50 text-amber-800", violet: "border-l-2 border-violet-600 bg-violet-50 text-violet-800", emerald: "border-l-2 border-emerald-600 bg-emerald-50 text-emerald-800" }[accent]
  return <Card className="rounded-none border-border bg-card"><CardContent className="flex items-start gap-3 p-4"><div className={`flex size-9 shrink-0 items-center justify-center rounded-none ${accentClass}`}><Icon className="size-4" aria-hidden="true" /></div><div className="min-w-0"><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</p><p className="drift-data mt-1 text-xl font-semibold tracking-tight">{value}</p><p className="mt-1 truncate text-[11px] text-muted-foreground">{detail}</p></div></CardContent></Card>
}

function DeviceCard({ device, selected, onSelect }: { device: DeviceView; selected: boolean; onSelect: () => void }) {
  return <tr className={`border-b border-border/70 transition-colors hover:bg-muted/60 ${selected ? "bg-secondary/60" : ""}`}><td className="py-3 pr-3"><button type="button" aria-pressed={selected} onClick={onSelect} className="flex items-center gap-2 text-left font-medium focus-visible:outline-2 focus-visible:outline-primary"><span className={`size-2 rounded-full ${statusDotClass(device.status)}`} /><span>{device.displayName}</span></button><p className="drift-data mt-1 text-[10px] text-muted-foreground">{device.stableIdentity}</p></td><td className="py-3 pr-3 text-muted-foreground">{device.location}</td><td className="py-3 pr-3"><DeviceStatusBadge status={device.status} /></td><td className="py-3 pr-3"><p>{device.workflow}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{device.taskProgress}%</p></td><td className="py-3 pr-3">{device.batteryPercent}%</td><td className="py-3 text-muted-foreground">{device.lastSeen}</td></tr>
}

function DeviceStatusBadge({ status }: { status: DeviceStatus }) {
  const label = status === "online" ? "Online" : status === "attention" ? "Attention" : "Offline"
  const classes = status === "online" ? "border-emerald-200 bg-emerald-50 text-emerald-800" : status === "attention" ? "border-amber-200 bg-amber-50 text-amber-800" : "border-slate-300 bg-slate-100 text-slate-700"
  return <Badge variant="outline" className={`shrink-0 rounded-none text-[10px] ${classes}`}><span className={`mr-1.5 size-1.5 rounded-full ${statusDotClass(status)}`} />{label}</Badge>
}

function statusDotClass(status: DeviceStatus) {
  return status === "online" ? "bg-emerald-600" : status === "attention" ? "bg-amber-600" : "bg-slate-500"
}

function InspectorMetric({ icon: Icon, label, value }: { icon: typeof Cpu; label: string; value: string }) {
  return <div className="rounded-none border border-border bg-background p-3"><div className="mb-2 flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground"><Icon className="size-3 text-primary" aria-hidden="true" />{label}</div><p className="drift-data truncate text-xs font-medium">{value}</p></div>
}

function ActivityTimeline({ events, compact = false }: { events: readonly { id: string; name: string; actor: string; occurredAt: string; payloadSummary: string }[]; compact?: boolean }) {
  const visibleEvents = compact ? events.slice(0, 3) : events
  return <div className="space-y-4">{visibleEvents.map((event, index) => <div key={event.id} className="flex gap-3"><div className="flex flex-col items-center"><span className="mt-1.5 flex size-5 items-center justify-center rounded-full border border-emerald-200 bg-emerald-50 text-emerald-800"><Check className="size-3" aria-hidden="true" /></span>{index < visibleEvents.length - 1 ? <span className="mt-1 h-full w-px bg-border" /> : null}</div><div className="min-w-0 flex-1 pb-1"><div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1"><p className="text-xs font-medium">{event.name}</p><span className="drift-data text-[10px] text-muted-foreground">{event.occurredAt}</span></div><p className="mt-1 text-[11px] text-muted-foreground">{event.actor} · {event.payloadSummary}</p></div></div>)}</div>
}

function ReadinessRow({ label, state, muted = false }: { label: string; state: string; muted?: boolean }) {
  return <div className="flex items-center justify-between rounded-none border border-border bg-card px-3 py-2.5"><span className="text-xs text-muted-foreground">{label}</span><span className={`flex items-center gap-1.5 text-[11px] ${muted ? "text-muted-foreground" : "text-emerald-700"}`}><span className={`size-1.5 rounded-full ${muted ? "bg-slate-400" : "bg-emerald-600"}`} />{state}</span></div>
}
