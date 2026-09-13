import { useMemo, useState } from "react"
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  BatteryCharging,
  Bell,
  Check,
  CircleHelp,
  Clock3,
  Cpu,
  Grid2X2,
  MoreHorizontal,
  Network,
  Play,
  RefreshCw,
  Search,
  Server,
  Settings2,
  Smartphone,
  Wifi,
  XCircle,
} from "lucide-react"
import { Badge } from "./components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "./components/ui/card"
import { Button } from "./components/ui/button"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "./components/ui/tabs"
import { AppSidebar } from "./components/app-sidebar"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "./components/ui/breadcrumb"
import { Separator } from "./components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "./components/ui/sidebar"
import { TooltipProvider } from "./components/ui/tooltip"

type DeviceStatus = "online" | "attention" | "offline"

type Device = {
  id: string
  name: string
  location: string
  status: DeviceStatus
  task: string
  taskProgress: number
  battery: number
  latency: number
  lastSeen: string
  agent: string
  platform: string
}

const devices: Device[] = [
  {
    id: "atlas-04",
    name: "Atlas 04",
    location: "Rack A · Bay 04",
    status: "online",
    task: "Content validation",
    taskProgress: 72,
    battery: 86,
    latency: 42,
    lastSeen: "just now",
    agent: "edge-1.4.2",
    platform: "Android 14",
  },
  {
    id: "atlas-07",
    name: "Atlas 07",
    location: "Rack A · Bay 07",
    status: "online",
    task: "Idle · ready",
    taskProgress: 100,
    battery: 64,
    latency: 58,
    lastSeen: "18 sec ago",
    agent: "edge-1.4.2",
    platform: "Android 14",
  },
  {
    id: "nova-02",
    name: "Nova 02",
    location: "Rack B · Bay 02",
    status: "attention",
    task: "Reconnecting to agent",
    taskProgress: 34,
    battery: 23,
    latency: 188,
    lastSeen: "2 min ago",
    agent: "edge-1.4.1",
    platform: "Android 13",
  },
  {
    id: "nova-05",
    name: "Nova 05",
    location: "Rack B · Bay 05",
    status: "offline",
    task: "No active run",
    taskProgress: 0,
    battery: 9,
    latency: 0,
    lastSeen: "11 min ago",
    agent: "edge-1.4.1",
    platform: "Android 13",
  },
  {
    id: "orion-01",
    name: "Orion 01",
    location: "Rack C · Bay 01",
    status: "online",
    task: "Workflow smoke test",
    taskProgress: 48,
    battery: 91,
    latency: 36,
    lastSeen: "just now",
    agent: "edge-1.4.2",
    platform: "Android 15",
  },
  {
    id: "orion-03",
    name: "Orion 03",
    location: "Rack C · Bay 03",
    status: "online",
    task: "Idle · ready",
    taskProgress: 100,
    battery: 78,
    latency: 51,
    lastSeen: "32 sec ago",
    agent: "edge-1.4.2",
    platform: "Android 15",
  },
]

const activityEvents = [
  { time: "09:42:18", title: "Workflow checkpoint passed", detail: "Content validation · step 08", tone: "success" },
  { time: "09:41:57", title: "Screenshot artifact captured", detail: "Stored in local demo workspace", tone: "info" },
  { time: "09:40:31", title: "Lease renewed", detail: "Operator console · 30 minute lease", tone: "info" },
  { time: "09:38:04", title: "Agent heartbeat received", detail: "edge-1.4.2 · 42 ms", tone: "success" },
]

function statusLabel(status: DeviceStatus) {
  return status === "online" ? "Online" : status === "attention" ? "Attention" : "Offline"
}

function statusClasses(status: DeviceStatus) {
  return status === "online"
    ? "bg-emerald-600"
    : status === "attention"
      ? "bg-amber-600"
      : "bg-slate-500"
}

function statusBadge(status: DeviceStatus) {
  return status === "online" ? "default" : status === "attention" ? "secondary" : "outline"
}

function statusBadgeClass(status: DeviceStatus) {
  return status === "online"
    ? "border-emerald-200 bg-emerald-50 text-emerald-800"
    : status === "attention"
      ? "border-amber-200 bg-amber-50 text-amber-800"
      : "border-slate-300 bg-slate-100 text-slate-700"
}

function App() {
  const [selectedDeviceId, setSelectedDeviceId] = useState(devices[0].id)
  const [query, setQuery] = useState("")
  const [lastRefresh, setLastRefresh] = useState("just now")
  const [activeSection, setActiveSection] = useState("Overview")

  const selectedDevice = devices.find((device) => device.id === selectedDeviceId) ?? devices[0]
  const filteredDevices = useMemo(
    () =>
      devices.filter((device) =>
        `${device.name} ${device.location} ${device.task}`.toLowerCase().includes(query.toLowerCase()),
      ),
    [query],
  )
  const onlineCount = devices.filter((device) => device.status === "online").length
  const attentionCount = devices.filter((device) => device.status === "attention").length
  const activeRuns = devices.filter((device) => device.taskProgress > 0 && device.taskProgress < 100).length

  return (
    <TooltipProvider>
      <SidebarProvider
        defaultOpen
        data-visual-style="swiss-editorial"
        className="drift-theme min-h-svh bg-background text-foreground"
      >
        <AppSidebar activeSection={activeSection} onSectionChange={setActiveSection} />
        <SidebarInset className="min-w-0">
          <header className="flex h-16 shrink-0 items-center gap-2 border-b border-border transition-[width,height] ease-linear group-has-data-[collapsible=icon]/sidebar-wrapper:h-12">
            <div className="flex items-center gap-2 px-4">
              <SidebarTrigger aria-label="Toggle sidebar" className="-ml-1" />
              <Separator
                orientation="vertical"
                className="mr-2 h-4 shrink-0 self-center data-[orientation=vertical]:h-4 data-[orientation=vertical]:self-center"
              />
              <Breadcrumb>
                <BreadcrumbList>
                  <BreadcrumbItem className="hidden md:block">
                    <BreadcrumbLink href="#workspace">Workspace</BreadcrumbLink>
                  </BreadcrumbItem>
                  <BreadcrumbSeparator className="hidden md:block" />
                  <BreadcrumbItem>
                    <BreadcrumbPage>{activeSection}</BreadcrumbPage>
                  </BreadcrumbItem>
                </BreadcrumbList>
              </Breadcrumb>
            </div>
            <div className="ml-auto flex items-center gap-1 px-4">
              <Button variant="ghost" size="icon-sm" aria-label="Notifications">
                <Bell className="size-4" aria-hidden="true" />
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label="Settings">
                <Settings2 className="size-4" aria-hidden="true" />
              </Button>
            </div>
          </header>

          <div className="drift-editorial-grid mx-auto w-full max-w-[1800px] flex-1 p-4 sm:p-6 lg:p-8">
            <div className="mb-8 flex flex-col justify-between gap-6 md:flex-row md:items-end">
              <div>
                <div className="drift-kicker flex items-center gap-3">
                  <span className="h-px w-8 bg-primary" aria-hidden="true" />
                  <span>OPERATIONS / FLEET CONTROL</span>
                </div>
                <h1 className="mt-3 text-3xl font-semibold tracking-[-0.05em] sm:text-5xl">Fleet overview</h1>
                <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">Monitor device health, active work, and operator events from one surface.</p>
              </div>
              <div className="flex flex-col items-start gap-3 md:items-end">
                <span className="font-mono drift-data text-[10px] uppercase tracking-[0.12em] text-muted-foreground">Updated {lastRefresh}</span>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" onClick={() => setLastRefresh("just now")}>
                    <RefreshCw className="size-3.5" aria-hidden="true" />
                    Refresh
                  </Button>
                  <Button size="sm" disabled title="Actions are disabled in demo mode">
                    <Play className="size-3.5" aria-hidden="true" />
                    Run workflow
                  </Button>
                </div>
              </div>
            </div>

          <section aria-label="Fleet metrics" className="mb-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <MetricCard label="Devices online" value={`${onlineCount}/${devices.length}`} detail="All agents reporting" icon={Wifi} accent="cobalt" />
            <MetricCard label="Needs attention" value={String(attentionCount)} detail="1 reconnecting · 1 offline" icon={AlertTriangle} accent="amber" />
            <MetricCard label="Active runs" value={String(activeRuns)} detail="Across 2 workflows" icon={Activity} accent="violet" />
            <MetricCard label="Median latency" value="48 ms" detail="Last 15 minutes" icon={Network} accent="emerald" />
          </section>

          <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
            <Card className="border-border bg-card">
              <CardHeader className="border-b border-border pb-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Grid2X2 className="size-4 text-primary" aria-hidden="true" /><h2>Device fleet</h2></CardTitle>
                    <CardDescription className="mt-2 text-xs">{devices.length} registered endpoints · sorted by status</CardDescription>
                  </div>
                  <div className="relative w-full sm:w-52">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                    <input aria-label="Search devices" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search devices" className="h-8 w-full rounded-none border border-input bg-background px-3 pl-8 text-xs outline-none transition focus:border-primary focus:ring-2 focus:ring-primary/20" />
                  </div>
                </div>
              </CardHeader>
              <CardContent className="p-3 sm:p-4">
                <div className="grid gap-3 md:grid-cols-2">
                  {filteredDevices.map((device) => (
                    <DeviceCard key={device.id} device={device} selected={device.id === selectedDevice.id} onSelect={() => setSelectedDeviceId(device.id)} />
                  ))}
                </div>
                {filteredDevices.length === 0 ? <p className="px-2 py-8 text-center text-sm text-muted-foreground">No devices match “{query}”.</p> : null}
              </CardContent>
            </Card>

            <Card className="border-border bg-card">
              <CardHeader className="border-b border-border pb-4">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Smartphone className="size-4 text-primary" aria-hidden="true" /><h2>Selected device</h2></CardTitle>
                    <CardDescription className="mt-2 text-xs">Live inspector · read-only demo</CardDescription>
                  </div>
                  <Button variant="ghost" size="icon-sm" aria-label="More device options" disabled><MoreHorizontal className="size-4" aria-hidden="true" /></Button>
                </div>
              </CardHeader>
              <CardContent className="p-0">
                <div className="flex items-center gap-3 border-b border-border px-4 py-4">
                  <div className="flex size-11 items-center justify-center rounded-none border-l-2 border-primary bg-secondary text-primary"><Smartphone className="size-5" aria-hidden="true" /></div>
                  <div className="min-w-0 flex-1"><p className="truncate text-sm font-semibold">{selectedDevice.name}</p><p className="truncate text-xs text-muted-foreground">{selectedDevice.location} · {selectedDevice.platform}</p></div>
                  <Badge variant={statusBadge(selectedDevice.status)} className={statusBadgeClass(selectedDevice.status)}><span className={`mr-1.5 size-1.5 rounded-full ${statusClasses(selectedDevice.status)}`} />{statusLabel(selectedDevice.status)}</Badge>
                </div>
                <Tabs defaultValue="overview" className="w-full">
                  <TabsList className="mx-4 mt-4 w-[calc(100%-2rem)]" variant="line">
                    <TabsTrigger value="overview">Overview</TabsTrigger>
                    <TabsTrigger value="activity">Activity</TabsTrigger>
                  </TabsList>
                  <TabsContent value="overview" className="px-4 pb-4 pt-4">
                    <div className="grid grid-cols-2 gap-3">
                      <InspectorMetric icon={Cpu} label="Agent" value={selectedDevice.agent} />
                      <InspectorMetric icon={Clock3} label="Last seen" value={selectedDevice.lastSeen} />
                      <InspectorMetric icon={BatteryCharging} label="Battery" value={`${selectedDevice.battery}%`} />
                      <InspectorMetric icon={Network} label="Latency" value={selectedDevice.latency ? `${selectedDevice.latency} ms` : "—"} />
                    </div>
                    <div className="mt-4 rounded-none border border-border bg-muted p-3">
                      <div className="mb-2 flex items-center justify-between"><span className="text-xs font-semibold uppercase tracking-[0.08em]">Current task</span><span className="drift-data text-[11px] text-muted-foreground">{selectedDevice.taskProgress}%</span></div>
                      <p className="mb-2 text-xs text-muted-foreground">{selectedDevice.task}</p>
                      <div className="h-1.5 overflow-hidden rounded-none bg-background"><div className="h-full rounded-none bg-primary transition-all" style={{ width: `${selectedDevice.taskProgress}%` }} /></div>
                    </div>
                    <div className="mt-4 flex items-start gap-2 rounded-none border-l-2 border-primary bg-secondary/70 p-3 text-[11px] leading-relaxed text-muted-foreground"><CircleHelp className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />This inspector is backed by deterministic mock data until the control-plane API is connected.</div>
                  </TabsContent>
                  <TabsContent value="activity" className="px-4 pb-4 pt-4"><ActivityTimeline compact /></TabsContent>
                </Tabs>
              </CardContent>
            </Card>
          </div>

          <section className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.42fr)]">
            <Card className="border-border bg-card">
              <CardHeader className="border-b border-border pb-4"><CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Activity className="size-4 text-primary" aria-hidden="true" />Recent activity</CardTitle><CardDescription className="mt-2 text-xs">Auditable events from this demo session</CardDescription></CardHeader>
              <CardContent className="pt-4"><ActivityTimeline /></CardContent>
            </Card>
            <Card className="border-border bg-card">
              <CardHeader className="border-b border-border pb-4"><CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.08em]"><Server className="size-4 text-primary" aria-hidden="true" />Control plane</CardTitle><CardDescription className="mt-2 text-xs">Local bootstrap readiness</CardDescription></CardHeader>
              <CardContent className="space-y-3 pt-4"><ReadinessRow label="Web console" state="Ready" /><ReadinessRow label="API contract" state="Planned" muted /><ReadinessRow label="Go services" state="Planned" muted /><ReadinessRow label="Device adapters" state="Disabled" muted /></CardContent>
            </Card>
          </section>

          <footer className="mt-6 flex flex-col gap-2 border-t border-border pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between"><span>Drift Command Center · local bootstrap</span><span className="flex items-center gap-1.5 font-mono uppercase tracking-[0.08em] text-amber-700"><XCircle className="size-3" aria-hidden="true" />Demo mode · actions disabled</span></footer>
          </div>
        </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
  )
}

function MetricCard({
  label,
  value,
  detail,
  icon: Icon,
  accent,
}: {
  label: string
  value: string
  detail: string
  icon: typeof Wifi
  accent: "cobalt" | "amber" | "violet" | "emerald"
}) {
  const accentClass = {
    cobalt: "border-l-2 border-primary bg-secondary text-primary",
    amber: "border-l-2 border-amber-600 bg-amber-50 text-amber-800",
    violet: "border-l-2 border-violet-600 bg-violet-50 text-violet-800",
    emerald: "border-l-2 border-emerald-600 bg-emerald-50 text-emerald-800",
  }[accent]

  return (
    <Card className="border-border bg-card">
      <CardContent className="flex items-start gap-3 p-4">
        <div className={`flex size-9 shrink-0 items-center justify-center rounded-none ${accentClass}`}>
          <Icon className="size-4" aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</p>
          <p className="drift-data mt-1 text-xl font-semibold tracking-tight">{value}</p>
          <p className="mt-1 truncate text-[11px] text-muted-foreground">{detail}</p>
        </div>
      </CardContent>
    </Card>
  )
}

function DeviceCard({ device, selected, onSelect }: { device: Device; selected: boolean; onSelect: () => void }) {
  const selectionClass = selected
    ? "border-primary border-l-4 bg-secondary/80"
    : "border-border bg-card hover:border-primary/60 hover:bg-muted"
  const progressClass = device.status === "attention" ? "bg-amber-600" : device.status === "offline" ? "bg-slate-500" : "bg-primary"

  return (
    <button type="button" aria-pressed={selected} onClick={onSelect} className={`group w-full rounded-none border p-4 text-left transition-colors ${selectionClass}`}>
      <div className="mb-3 flex items-start gap-3">
        <div className={`mt-1 size-2 shrink-0 rounded-full ${statusClasses(device.status)}`} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <p className="truncate text-sm font-medium">{device.name}</p>
            <Badge variant={statusBadge(device.status)} className={`shrink-0 text-[10px] ${statusBadgeClass(device.status)}`}>
              {statusLabel(device.status)}
            </Badge>
          </div>
          <p className="mt-1 truncate text-[11px] text-muted-foreground">{device.location}</p>
        </div>
      </div>
      <div className="mb-3 flex items-center justify-between gap-2 text-[11px]">
        <span className="truncate text-muted-foreground">{device.task}</span>
        <span className="drift-data shrink-0 text-muted-foreground">{device.taskProgress}%</span>
      </div>
      <div className="h-1 overflow-hidden rounded-none bg-muted">
        <div className={`h-full rounded-none ${progressClass}`} style={{ width: `${device.taskProgress}%` }} />
      </div>
      <div className="mt-3 flex items-center justify-between text-[10px] text-muted-foreground">
        <span className="flex items-center gap-1"><BatteryCharging className="size-3" aria-hidden="true" />{device.battery}%</span>
        <span className="flex items-center gap-1"><Clock3 className="size-3" aria-hidden="true" />{device.lastSeen}</span>
        <ArrowUpRight className={`size-3 transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 ${selected ? "text-primary" : ""}`} aria-hidden="true" />
      </div>
    </button>
  )
}

function InspectorMetric({ icon: Icon, label, value }: { icon: typeof Cpu; label: string; value: string }) {
  return (
    <div className="rounded-none border border-border bg-background p-3">
      <div className="mb-2 flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
        <Icon className="size-3 text-primary" aria-hidden="true" />
        {label}
      </div>
      <p className="drift-data truncate text-xs font-medium">{value}</p>
    </div>
  )
}

function ActivityTimeline({ compact = false }: { compact?: boolean }) {
  const events = compact ? activityEvents.slice(0, 3) : activityEvents

  return (
    <div className="space-y-4">
      {events.map((event, index) => (
        <div key={`${event.time}-${event.title}`} className="flex gap-3">
          <div className="flex flex-col items-center">
            <span className={`mt-1.5 flex size-5 items-center justify-center rounded-full border ${event.tone === "success" ? "border-emerald-200 bg-emerald-50 text-emerald-800" : "border-primary/20 bg-secondary text-primary"}`}>
              {event.tone === "success" ? <Check className="size-3" aria-hidden="true" /> : <Activity className="size-3" aria-hidden="true" />}
            </span>
            {index < events.length - 1 ? <span className="mt-1 h-full w-px bg-border" /> : null}
          </div>
          <div className="min-w-0 flex-1 pb-1">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <p className="text-xs font-medium">{event.title}</p>
              <span className="drift-data text-[10px] text-muted-foreground">{event.time}</span>
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">{event.detail}</p>
          </div>
        </div>
      ))}
    </div>
  )
}

function ReadinessRow({ label, state, muted = false }: { label: string; state: string; muted?: boolean }) {
  return (
    <div className="flex items-center justify-between rounded-none border border-border bg-card px-3 py-2.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={`flex items-center gap-1.5 text-[11px] ${muted ? "text-muted-foreground" : "text-emerald-700"}`}>
        <span className={`size-1.5 rounded-full ${muted ? "bg-slate-400" : "bg-emerald-600"}`} />
        {state}
      </span>
    </div>
  )
}

export default App
