import { useMemo, useState } from "react"
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  BatteryCharging,
  Bell,
  Check,
  ChevronRight,
  CircleHelp,
  Clock3,
  Command,
  Cpu,
  Grid2X2,
  LayoutDashboard,
  MoreHorizontal,
  Network,
  Play,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
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

const navigation = [
  { label: "Overview", icon: LayoutDashboard },
  { label: "Devices", icon: Smartphone, count: devices.length },
  { label: "Workflows", icon: Command },
  { label: "Runs", icon: Activity },
  { label: "Policies", icon: ShieldCheck },
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
    ? "bg-emerald-400 shadow-[0_0_12px_rgba(52,211,153,0.85)]"
    : status === "attention"
      ? "bg-amber-300 shadow-[0_0_12px_rgba(252,211,77,0.8)]"
      : "bg-slate-500"
}

function statusBadge(status: DeviceStatus) {
  return status === "online" ? "default" : status === "attention" ? "secondary" : "outline"
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
    <div className="drift-theme min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b border-border/70 bg-sidebar/95 px-5 backdrop-blur-xl lg:px-7">
        <div className="flex items-center gap-3">
          <div className="flex size-8 items-center justify-center rounded-lg border border-primary/40 bg-primary/10 text-primary shadow-[0_0_20px_rgba(0,255,255,0.12)]">
            <Command className="size-4" aria-hidden="true" />
          </div>
          <div>
            <p className="text-sm font-semibold tracking-[0.22em] text-foreground">DRIFT</p>
            <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">Command Center</p>
          </div>
          <span className="hidden h-5 w-px bg-border sm:block" />
          <Badge variant="outline" className="hidden border-primary/30 bg-primary/5 text-primary sm:inline-flex">
            <span className="mr-1.5 size-1.5 rounded-full bg-primary shadow-[0_0_8px_rgba(0,255,255,0.9)]" />
            Demo control plane
          </Badge>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon-sm" aria-label="Notifications">
            <Bell className="size-4" aria-hidden="true" />
          </Button>
          <Button variant="ghost" size="icon-sm" aria-label="Settings">
            <Settings2 className="size-4" aria-hidden="true" />
          </Button>
          <div className="ml-1 hidden items-center gap-2 border-l border-border pl-3 sm:flex">
            <div className="flex size-7 items-center justify-center rounded-full bg-cyan-300/15 text-xs font-semibold text-primary">VC</div>
            <div className="leading-tight">
              <p className="text-xs font-medium">Operator</p>
              <p className="text-[10px] text-muted-foreground">Local session</p>
            </div>
          </div>
        </div>
      </header>

      <div className="mx-auto flex max-w-[1800px]">
        <aside className="hidden min-h-[calc(100vh-4rem)] w-56 shrink-0 border-r border-border/60 bg-sidebar/40 p-3 lg:block">
          <p className="px-3 pb-2 pt-3 text-[10px] font-semibold uppercase tracking-[0.18em] text-muted-foreground">Workspace</p>
          <nav aria-label="Primary navigation" className="space-y-1">
            {navigation.map(({ label, icon: Icon, count }) => (
              <button
                key={label}
                type="button"
                aria-current={activeSection === label ? "page" : undefined}
                onClick={() => setActiveSection(label)}
                className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition-colors ${
                  activeSection === label ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
                }`}
              >
                <Icon className="size-4" aria-hidden="true" />
                <span className="flex-1">{label}</span>
                {count ? <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{count}</span> : null}
              </button>
            ))}
          </nav>
          <div className="mt-auto pt-8">
            <div className="rounded-xl border border-border/70 bg-card/60 p-3">
              <div className="mb-2 flex items-center gap-2 text-xs font-medium text-foreground">
                <Network className="size-3.5 text-primary" aria-hidden="true" />
                Transport health
              </div>
              <div className="mb-2 flex items-center justify-between text-[11px]">
                <span className="text-muted-foreground">Control plane</span>
                <span className="text-emerald-300">Connected</span>
              </div>
              <div className="h-1 overflow-hidden rounded-full bg-muted"><div className="h-full w-[94%] rounded-full bg-primary" /></div>
              <p className="mt-2 text-[10px] leading-relaxed text-muted-foreground">Mock telemetry only. No device commands are enabled.</p>
            </div>
          </div>
        </aside>

        <main className="min-w-0 flex-1 p-4 sm:p-6 lg:p-8">
          <div className="mb-6 flex flex-col justify-between gap-4 md:flex-row md:items-end">
            <div>
              <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground"><span>Workspace</span><ChevronRight className="size-3" aria-hidden="true" /><span className="text-primary">Overview</span></div>
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">Fleet overview</h1>
              <p className="mt-1 text-sm text-muted-foreground">Monitor device health, active work, and operator events from one surface.</p>
            </div>
            <div className="flex items-center gap-2">
              <span className="mr-1 hidden text-xs text-muted-foreground sm:inline">Updated {lastRefresh}</span>
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

          <section aria-label="Fleet metrics" className="mb-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <MetricCard label="Devices online" value={`${onlineCount}/${devices.length}`} detail="All agents reporting" icon={Wifi} accent="cyan" />
            <MetricCard label="Needs attention" value={String(attentionCount)} detail="1 reconnecting · 1 offline" icon={AlertTriangle} accent="amber" />
            <MetricCard label="Active runs" value={String(activeRuns)} detail="Across 2 workflows" icon={Activity} accent="violet" />
            <MetricCard label="Median latency" value="48 ms" detail="Last 15 minutes" icon={Network} accent="emerald" />
          </section>

          <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
            <Card className="border-border/70 bg-card/70">
              <CardHeader className="border-b border-border/60 pb-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <CardTitle className="flex items-center gap-2 text-base"><Grid2X2 className="size-4 text-primary" aria-hidden="true" /><h2>Device fleet</h2></CardTitle>
                    <CardDescription className="mt-1">{devices.length} registered endpoints · sorted by status</CardDescription>
                  </div>
                  <div className="relative w-full sm:w-52">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                    <input aria-label="Search devices" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search devices" className="h-8 w-full rounded-lg border border-border bg-background/60 pl-8 pr-3 text-xs outline-none transition focus:border-primary/60 focus:ring-2 focus:ring-primary/20" />
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

            <Card className="border-border/70 bg-card/70">
              <CardHeader className="border-b border-border/60 pb-4">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <CardTitle className="flex items-center gap-2 text-base"><Smartphone className="size-4 text-primary" aria-hidden="true" /><h2>Selected device</h2></CardTitle>
                    <CardDescription className="mt-1">Live inspector · read-only demo</CardDescription>
                  </div>
                  <Button variant="ghost" size="icon-sm" aria-label="More device options" disabled><MoreHorizontal className="size-4" aria-hidden="true" /></Button>
                </div>
              </CardHeader>
              <CardContent className="p-0">
                <div className="flex items-center gap-3 border-b border-border/60 px-4 py-4">
                  <div className="flex size-11 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 text-primary"><Smartphone className="size-5" aria-hidden="true" /></div>
                  <div className="min-w-0 flex-1"><p className="truncate text-sm font-semibold">{selectedDevice.name}</p><p className="truncate text-xs text-muted-foreground">{selectedDevice.location} · {selectedDevice.platform}</p></div>
                  <Badge variant={statusBadge(selectedDevice.status)}><span className={`mr-1.5 size-1.5 rounded-full ${statusClasses(selectedDevice.status)}`} />{statusLabel(selectedDevice.status)}</Badge>
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
                    <div className="mt-4 rounded-lg border border-border/60 bg-background/50 p-3">
                      <div className="mb-2 flex items-center justify-between"><span className="text-xs font-medium">Current task</span><span className="text-[11px] text-muted-foreground">{selectedDevice.taskProgress}%</span></div>
                      <p className="mb-2 text-xs text-muted-foreground">{selectedDevice.task}</p>
                      <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all" style={{ width: `${selectedDevice.taskProgress}%` }} /></div>
                    </div>
                    <div className="mt-4 flex items-start gap-2 rounded-lg border border-primary/15 bg-primary/5 p-3 text-[11px] leading-relaxed text-muted-foreground"><CircleHelp className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />This inspector is backed by deterministic mock data until the control-plane API is connected.</div>
                  </TabsContent>
                  <TabsContent value="activity" className="px-4 pb-4 pt-4"><ActivityTimeline compact /></TabsContent>
                </Tabs>
              </CardContent>
            </Card>
          </div>

          <section className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.42fr)]">
            <Card className="border-border/70 bg-card/70">
              <CardHeader className="border-b border-border/60 pb-4"><CardTitle className="flex items-center gap-2 text-base"><Activity className="size-4 text-primary" aria-hidden="true" />Recent activity</CardTitle><CardDescription className="mt-1">Auditable events from this demo session</CardDescription></CardHeader>
              <CardContent className="pt-4"><ActivityTimeline /></CardContent>
            </Card>
            <Card className="border-border/70 bg-card/70">
              <CardHeader className="border-b border-border/60 pb-4"><CardTitle className="flex items-center gap-2 text-base"><Server className="size-4 text-primary" aria-hidden="true" />Control plane</CardTitle><CardDescription className="mt-1">Local bootstrap readiness</CardDescription></CardHeader>
              <CardContent className="space-y-3 pt-4"><ReadinessRow label="Web console" state="Ready" /><ReadinessRow label="API contract" state="Planned" muted /><ReadinessRow label="Go services" state="Planned" muted /><ReadinessRow label="Device adapters" state="Disabled" muted /></CardContent>
            </Card>
          </section>

          <footer className="mt-6 flex flex-col gap-2 border-t border-border/60 pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between"><span>Drift Command Center · local bootstrap</span><span className="flex items-center gap-1.5 text-amber-200/80"><XCircle className="size-3" aria-hidden="true" />Demo mode · actions disabled</span></footer>
        </main>
      </div>
    </div>
  )
}

function MetricCard({ label, value, detail, icon: Icon, accent }: { label: string; value: string; detail: string; icon: typeof Wifi; accent: "cyan" | "amber" | "violet" | "emerald" }) {
  const accentClass = { cyan: "text-primary bg-primary/10", amber: "text-amber-300 bg-amber-300/10", violet: "text-violet-300 bg-violet-300/10", emerald: "text-emerald-300 bg-emerald-300/10" }[accent]
  return <Card className="border-border/70 bg-card/70"><CardContent className="flex items-start gap-3 p-4"><div className={`flex size-9 shrink-0 items-center justify-center rounded-lg ${accentClass}`}><Icon className="size-4" aria-hidden="true" /></div><div className="min-w-0"><p className="text-xs text-muted-foreground">{label}</p><p className="mt-1 text-xl font-semibold tracking-tight">{value}</p><p className="mt-1 truncate text-[11px] text-muted-foreground">{detail}</p></div></CardContent></Card>
}

function DeviceCard({ device, selected, onSelect }: { device: Device; selected: boolean; onSelect: () => void }) {
  return <button type="button" aria-pressed={selected} onClick={onSelect} className={`group w-full rounded-xl border p-3 text-left transition-all ${selected ? "border-primary/55 bg-primary/[0.07] shadow-[0_0_24px_rgba(0,255,255,0.08)]" : "border-border/60 bg-background/30 hover:border-primary/30 hover:bg-muted/40"}`}><div className="mb-3 flex items-start gap-3"><div className={`mt-1 size-2 shrink-0 rounded-full ${statusClasses(device.status)}`} /><div className="min-w-0 flex-1"><div className="flex items-center justify-between gap-2"><p className="truncate text-sm font-medium">{device.name}</p><Badge variant={statusBadge(device.status)} className="shrink-0 text-[10px]">{statusLabel(device.status)}</Badge></div><p className="mt-1 truncate text-[11px] text-muted-foreground">{device.location}</p></div></div><div className="mb-3 flex items-center justify-between gap-2 text-[11px]"><span className="truncate text-muted-foreground">{device.task}</span><span className="shrink-0 text-muted-foreground">{device.taskProgress}%</span></div><div className="h-1 overflow-hidden rounded-full bg-muted"><div className={`h-full rounded-full ${device.status === "attention" ? "bg-amber-300" : device.status === "offline" ? "bg-slate-500" : "bg-primary"}`} style={{ width: `${device.taskProgress}%` }} /></div><div className="mt-3 flex items-center justify-between text-[10px] text-muted-foreground"><span className="flex items-center gap-1"><BatteryCharging className="size-3" aria-hidden="true" />{device.battery}%</span><span className="flex items-center gap-1"><Clock3 className="size-3" aria-hidden="true" />{device.lastSeen}</span><ArrowUpRight className={`size-3 transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 ${selected ? "text-primary" : ""}`} aria-hidden="true" /></div></button>
}

function InspectorMetric({ icon: Icon, label, value }: { icon: typeof Cpu; label: string; value: string }) {
  return <div className="rounded-lg border border-border/60 bg-background/40 p-3"><div className="mb-2 flex items-center gap-1.5 text-[10px] uppercase tracking-[0.12em] text-muted-foreground"><Icon className="size-3 text-primary" aria-hidden="true" />{label}</div><p className="truncate text-xs font-medium">{value}</p></div>
}

function ActivityTimeline({ compact = false }: { compact?: boolean }) {
  const events = compact ? activityEvents.slice(0, 3) : activityEvents
  return <div className="space-y-4">{events.map((event, index) => <div key={`${event.time}-${event.title}`} className="flex gap-3"><div className="flex flex-col items-center"><span className={`mt-1.5 flex size-5 items-center justify-center rounded-full ${event.tone === "success" ? "bg-emerald-400/15 text-emerald-300" : "bg-primary/10 text-primary"}`}>{event.tone === "success" ? <Check className="size-3" aria-hidden="true" /> : <Activity className="size-3" aria-hidden="true" />}</span>{index < events.length - 1 ? <span className="mt-1 h-full w-px bg-border/70" /> : null}</div><div className="min-w-0 flex-1 pb-1"><div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1"><p className="text-xs font-medium">{event.title}</p><span className="font-mono text-[10px] text-muted-foreground">{event.time}</span></div><p className="mt-1 text-[11px] text-muted-foreground">{event.detail}</p></div></div>)}</div>
}

function ReadinessRow({ label, state, muted = false }: { label: string; state: string; muted?: boolean }) {
  return <div className="flex items-center justify-between rounded-lg border border-border/50 bg-background/30 px-3 py-2.5"><span className="text-xs text-muted-foreground">{label}</span><span className={`flex items-center gap-1.5 text-[11px] ${muted ? "text-muted-foreground" : "text-emerald-300"}`}><span className={`size-1.5 rounded-full ${muted ? "bg-muted-foreground/50" : "bg-emerald-300 shadow-[0_0_8px_rgba(110,231,183,0.8)]"}`} />{state}</span></div>
}

export default App
