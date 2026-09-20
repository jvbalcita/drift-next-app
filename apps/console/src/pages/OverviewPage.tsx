import { useMemo, useState } from "react"
import { Activity, AlertTriangle, Check, Network, RefreshCw, Server, ShieldAlert, Wifi, XCircle } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { CardContent } from "@/components/ui/card"
import type { ControlPlaneSnapshot, DispatchIntent, EventView } from "@/lib/domain/control-plane"
import { EmptyState, PageIntro, Panel } from "./shared"
import { humanTimestamp } from "./device-inspection"

const actionableEventPattern = /fail|error|timeout|recover|reconnect|disconnect|arriv|depart|scan|creat|updat|renam|delet|assign|move|retir|halt|stop/i

export function OverviewPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [refreshing, setRefreshing] = useState(false)
  const onlineCount = snapshot.devices.filter((device) => device.status === "online").length
  const attentionCount = snapshot.devices.filter((device) => device.status !== "online").length
  const activeRuns = snapshot.runs.filter((run) => run.state === "running" || run.state === "paused").length
  const measuredLatencies = snapshot.devices.map((device) => device.latencyMs).filter((latency) => latency > 0)
  const medianLatency = median(measuredLatencies)
  const actionableEvents = useMemo(() => [...snapshot.events]
    .filter((event) => Boolean(event.failureClass) || actionableEventPattern.test(`${event.name} ${event.payloadSummary}`))
    .sort((left, right) => timestamp(right.occurredAt) - timestamp(left.occurredAt))
    .slice(0, 8), [snapshot.events])

  async function refresh() {
    if (refreshing) return
    setRefreshing(true)
    try {
      const outcome = await dispatch({ type: "refresh" })
      if (outcome.ok) toast.success("Fleet overview refreshed", { description: outcome.message })
      else toast.error("Fleet refresh failed", { description: outcome.message })
    } catch {
      toast.error("Fleet refresh failed", { description: "The control plane could not refresh the fleet projection." })
    } finally {
      setRefreshing(false)
    }
  }

  return <>
    <PageIntro eyebrow="OPERATIONS / FLEET CONTROL" title="Fleet overview" description="Monitor device health, active work, and operator events from one surface." actions={<Button variant="outline" size="sm" disabled={refreshing} onClick={() => void refresh()}><RefreshCw className={`size-3.5 ${refreshing ? "animate-spin" : ""}`} aria-hidden="true" />{refreshing ? "Refreshing…" : "Refresh"}</Button>} />
    <section aria-label="Fleet metrics" className="mb-8 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <MetricCard label="Devices online" value={`${onlineCount}/${snapshot.devices.length}`} detail={`${onlineCount} currently observed`} icon={Wifi} accent="cobalt" />
      <MetricCard label="Needs attention" value={String(attentionCount)} detail="Offline, unauthorized, or unobserved" icon={AlertTriangle} accent="amber" />
      <MetricCard label="Active runs" value={String(activeRuns)} detail="Running or paused targets" icon={Activity} accent="violet" />
      <MetricCard label="Median latency" value={medianLatency === null ? "Not measured" : `${medianLatency} ms`} detail={medianLatency === null ? "No device observations yet" : `${measuredLatencies.length} measured devices`} icon={Network} accent="emerald" />
    </section>
    <section className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(300px,0.42fr)]">
      <Panel title="Recent activity" description="Fleet changes and outcomes that may need an operator's attention."><ActivityFeed events={actionableEvents} /></Panel>
      <Panel title="Runtime readiness" description="Observed control-plane safety and execution state."><div className="space-y-2">
        <ReadinessRow label="Control-plane connection" state={runtimeConnectionLabel(snapshot)} detail={snapshot.runtimeConnection.updatedAt ? `Updated ${humanTimestamp(snapshot.runtimeConnection.updatedAt).label}` : undefined} tone={snapshot.runtimeConnection.state === "connected" ? "healthy" : "attention"} />
        <ReadinessRow label="Device adapter" state={adapterLabel(snapshot)} detail={snapshot.labAdapter.failureClass || (snapshot.labAdapter.lastHealthAt ? `Checked ${humanTimestamp(snapshot.labAdapter.lastHealthAt).label}` : undefined)} tone={snapshot.labAdapter.readiness === "ready" ? "healthy" : "attention"} />
        <ReadinessRow label="Command spool" state={snapshot.spoolHealth.exhausted ? "Exhausted" : snapshot.spoolHealth.blocked > 0 ? `${snapshot.spoolHealth.blocked} blocked` : snapshot.spoolHealth.pending > 0 ? `${snapshot.spoolHealth.pending} pending` : "Clear"} detail={`Capacity ${snapshot.spoolHealth.pending}/${snapshot.spoolHealth.maxSize}`} tone={snapshot.spoolHealth.exhausted || snapshot.spoolHealth.blocked > 0 ? "attention" : "healthy"} />
        <ReadinessRow label="Emergency stop" state={snapshot.halt.state === "clear" ? "Clear" : "Active"} detail={snapshot.halt.state === "emergency_stop" ? snapshot.halt.reason : undefined} tone={snapshot.halt.state === "clear" ? "healthy" : "danger"} />
      </div></Panel>
    </section>
    <footer className="mt-6 flex flex-col gap-2 border-t border-border pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between"><span>Drift Command Center</span><span className={`flex items-center gap-1.5 font-mono uppercase tracking-[0.08em] ${snapshot.runtimeConnection.state === "connected" ? "text-emerald-700" : "text-amber-700"}`}>{snapshot.runtimeConnection.state === "connected" ? <Check className="size-3" aria-hidden="true" /> : <XCircle className="size-3" aria-hidden="true" />}{snapshot.runtimeConnection.state}</span></footer>
  </>
}

function timestamp(value: string): number { const parsed = Date.parse(value); return Number.isFinite(parsed) ? parsed : 0 }
function median(values: number[]): number | null { if (values.length === 0) return null; const sorted = [...values].sort((a, b) => a - b); const middle = Math.floor(sorted.length / 2); return sorted.length % 2 === 0 ? Math.round((sorted[middle - 1] + sorted[middle]) / 2) : sorted[middle] }

function MetricCard({ label, value, detail, icon: Icon, accent }: { label: string; value: string; detail: string; icon: typeof Wifi; accent: "cobalt" | "amber" | "violet" | "emerald" }) {
  const accentClass = { cobalt: "border-l-2 border-primary bg-secondary text-primary", amber: "border-l-2 border-amber-600 bg-amber-50 text-amber-800", violet: "border-l-2 border-violet-600 bg-violet-50 text-violet-800", emerald: "border-l-2 border-emerald-600 bg-emerald-50 text-emerald-800" }[accent]
  return <article aria-labelledby={`metric-${label.replaceAll(" ", "-").toLowerCase()}`} className="rounded-none border border-border bg-card"><CardContent className="flex items-start gap-3 p-4"><div className={`flex size-9 shrink-0 items-center justify-center rounded-none ${accentClass}`}><Icon className="size-4" aria-hidden="true" /></div><div className="min-w-0"><h2 id={`metric-${label.replaceAll(" ", "-").toLowerCase()}`} className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</h2><p className="drift-data mt-1 text-xl font-semibold tracking-tight">{value}</p><p className="mt-1 text-[11px] text-muted-foreground">{detail}</p></div></CardContent></article>
}

function activityHref(event: EventView): string | undefined {
  const resource = event.resourceType.toLowerCase()
  if (resource.includes("device") || resource.includes("endpoint")) return "#devices/all"
  if (resource.includes("scan") || resource.includes("profile")) return "#network-profiles/scans"
  if (resource.includes("run") || resource.includes("target")) return "#runs/all"
  if (resource.includes("group") || resource.includes("membership")) return "#groups/membership"
  if (resource.includes("account")) return "#accounts/accounts"
  if (resource.includes("policy")) return "#policies/policies"
  return undefined
}

function ActivityFeed({ events }: { events: readonly EventView[] }) {
  if (events.length === 0) return <EmptyState label="No actionable activity" detail="Failures, recoveries, fleet changes, scans, and operator mutations will appear here." />
  return <ol className="divide-y divide-border">{events.map((event) => { const relative = humanTimestamp(event.occurredAt); const failed = Boolean(event.failureClass) || /fail|error|timeout/i.test(event.name); const recovered = /recover|reconnect/i.test(event.name); const href = activityHref(event); return <li key={event.id} className="grid gap-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-start"><span className={`mt-1.5 size-2 ${failed ? "bg-destructive" : recovered ? "bg-emerald-600" : "bg-primary"}`} aria-hidden="true" /><div className="min-w-0"><div className="flex flex-wrap items-baseline gap-2">{href ? <a className="text-xs font-medium underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring" href={href}>{event.name}</a> : <p className="text-xs font-medium">{event.name}</p>}<span className="font-mono text-[10px] uppercase tracking-[.08em] text-muted-foreground">{event.resourceType || event.kind}{event.resourceId ? ` · ${event.resourceId}` : ""}</span></div><p className="mt-1 text-[11px] leading-5 text-muted-foreground">{event.payloadSummary || `${event.actor} recorded this event.`}</p>{event.failureClass ? <p className="mt-1 text-[10px] font-medium text-destructive">{event.failureClass.replaceAll("_", " ")}</p> : null}</div><time className="drift-data text-[10px] text-muted-foreground" dateTime={relative.exact ?? event.occurredAt} title={relative.exact}>{relative.label || event.occurredAt}</time></li> })}</ol>
}

function runtimeConnectionLabel(snapshot: ControlPlaneSnapshot): string { if (snapshot.runtimeConnection.state === "connected") return `Connected${snapshot.runtimeConnection.protocol ? ` · ${snapshot.runtimeConnection.protocol}` : ""}`; if (snapshot.runtimeConnection.state === "reconnecting") return "Reconnecting"; return snapshot.runtimeConnection.disconnectedReason || "Disconnected" }
function adapterLabel(snapshot: ControlPlaneSnapshot): string { if (snapshot.labAdapter.indeterminate) return "Indeterminate"; return snapshot.labAdapter.readiness[0].toUpperCase() + snapshot.labAdapter.readiness.slice(1) }
function ReadinessRow({ label, state, detail, tone }: { label: string; state: string; detail?: string; tone: "healthy" | "attention" | "danger" }) { const color = tone === "healthy" ? "text-emerald-700" : tone === "danger" ? "text-destructive" : "text-amber-700"; const dot = tone === "healthy" ? "bg-emerald-600" : tone === "danger" ? "bg-destructive" : "bg-amber-600"; const Icon = tone === "danger" ? ShieldAlert : Server; return <div className="border border-border bg-card px-3 py-2.5"><div className="flex items-center justify-between gap-3"><span className="flex items-center gap-2 text-xs text-muted-foreground"><Icon className="size-3.5" aria-hidden="true" />{label}</span><span className={`flex items-center gap-1.5 text-right text-[11px] ${color}`}><span className={`size-1.5 shrink-0 rounded-full ${dot}`} aria-hidden="true" />{state}</span></div>{detail ? <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">{detail}</p> : null}</div> }
