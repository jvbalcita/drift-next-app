import { useMemo, useState, type ReactNode } from "react"
import { Filter, Search } from "lucide-react"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { ControlPlaneSnapshot, EventKind, EventView } from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, FailureBadge, OperatorNotice, PageIntro, StatusBadge } from "./shared"

export function EventsPage({ snapshot }: { snapshot: ControlPlaneSnapshot }) {
  const [kind, setKind] = useState<EventKind | "all">("all")
  const [actor, setActor] = useState("")
  const [resource, setResource] = useState("")
  const [correlation, setCorrelation] = useState("")
  const [time, setTime] = useState("")
  const [severity, setSeverity] = useState<"all" | "normal" | "failure">("all")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const events = useMemo(() => snapshot.events.filter((event) => {
    const includes = (value: string, query: string) => value.toLowerCase().includes(query.trim().toLowerCase())
    return (kind === "all" || event.kind === kind)
      && includes(event.actor, actor)
      && includes(`${event.resourceType} ${event.resourceId}`, resource)
      && includes(event.correlationId, correlation)
      && includes(event.occurredAt, time)
      && (severity === "all" || (severity === "normal" ? !event.failureClass : Boolean(event.failureClass)))
  }), [actor, correlation, kind, resource, severity, snapshot.events, time])
  const visibleEvents = events.slice(page * pageSize, (page + 1) * pageSize)
  const selected = snapshot.events.find((event) => event.id === selectedId)
  const resetPage = () => setPage(0)

  return <>
    <PageIntro eyebrow="OBSERVABILITY / EVENTS" title="Events and Audit" description="A dense, searchable event ledger. Bounded metadata stays visible while raw payloads and sensitive evidence remain unavailable." actions={<StatusBadge label="Redacted View" tone="info" />} />
    <OperatorNotice>Payload bodies, screenshots, credentials, and protocol data are omitted. This is a control-plane ledger only.</OperatorNotice>
    <div className="flex flex-wrap items-end gap-3 border-y border-border py-3" aria-label="Event Filters">
      <FilterSelect id="event-kind" label="Event Type" value={kind} onChange={(next) => { setKind(next as EventKind | "all"); resetPage() }}><option value="all">All Events</option><option value="operational">Operational</option><option value="audit">Audit</option></FilterSelect>
      <FilterInput id="event-actor" label="Actor" value={actor} onChange={(next) => { setActor(next); resetPage() }} />
      <FilterInput id="event-resource" label="Device or Resource" value={resource} onChange={(next) => { setResource(next); resetPage() }} />
      <FilterInput id="event-correlation" label="Correlation ID" value={correlation} onChange={(next) => { setCorrelation(next); resetPage() }} />
      <FilterSelect id="event-severity" label="Severity" value={severity} onChange={(next) => { setSeverity(next as "all" | "normal" | "failure"); resetPage() }}><option value="all">All Severities</option><option value="normal">Normal</option><option value="failure">Failure</option></FilterSelect>
      <FilterInput id="event-time" label="Time" value={time} onChange={(next) => { setTime(next); resetPage() }} />
      <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground"><Filter className="size-3.5" aria-hidden="true" />{events.length} Results</span>
    </div>
    <div className="mt-4">
      {events.length === 0 ? <EmptyState label="No Events Match" detail="Adjust one or more filters to see event ledger entries." /> : <EventTable events={visibleEvents} onOpen={setSelectedId} />}
      <DataTablePagination page={page} pageSize={pageSize} total={events.length} onPageChange={setPage} onPageSizeChange={(nextSize) => { setPageSize(nextSize); setPage(0) }} />
    </div>
    <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelectedId(null)}><SheetContent className="w-full rounded-none sm:max-w-xl"><SheetHeader className="border-b border-border"><SheetTitle>{selected?.name ?? "Event Detail"}</SheetTitle><SheetDescription>Sanitized, bounded event metadata.</SheetDescription></SheetHeader>{selected ? <dl className="grid gap-4 p-4 text-xs"><Detail label="Occurred" value={selected.occurredAt} /><Detail label="Actor" value={selected.actor} /><Detail label="Resource" value={`${selected.resourceType} / ${selected.resourceId}`} /><Detail label="Correlation ID" value={selected.correlationId} mono /><Detail label="Payload Summary" value={selected.payloadSummary} /><div><dt className="text-muted-foreground">Failure Classification</dt><dd className="mt-1"><FailureBadge failureClass={selected.failureClass} />{!selected.failureClass ? "None" : null}</dd></div></dl> : null}</SheetContent></Sheet>
  </>
}

function FilterSelect({ id, label, value, onChange, children }: { id: string; label: string; value: string; onChange: (value: string) => void; children: ReactNode }) {
  return <label htmlFor={id} className="text-xs font-semibold text-foreground">{label}<select id={id} value={value} onChange={(event) => onChange(event.target.value)} className="mt-1 block h-9 rounded-none border border-input bg-background px-2 text-xs text-foreground">{children}</select></label>
}

function FilterInput({ id, label, value, onChange }: { id: string; label: string; value: string; onChange: (value: string) => void }) {
  return <label htmlFor={id} className="text-xs font-semibold text-foreground">{label}<span className="relative mt-1 block"><Search className="absolute left-2 top-2.5 size-3.5 text-muted-foreground" aria-hidden="true" /><input id={id} value={value} onChange={(event) => onChange(event.target.value)} placeholder={`Filter ${label.toLowerCase()}`} className="h-9 rounded-none border border-input bg-background pl-7 pr-2 text-xs text-foreground" /></span></label>
}

function EventTable({ events, onOpen }: { events: readonly EventView[]; onOpen: (id: string) => void }) {
  return <ScrollArea className="h-[min(68vh,760px)] border border-border"><table className="w-full min-w-[940px] border-collapse text-left text-xs"><caption className="sr-only">Operational and audit event history</caption><thead className="sticky top-0 bg-background"><tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground"><th className="p-3">Time</th><th className="p-3">Event</th><th className="p-3">Type</th><th className="p-3">Actor</th><th className="p-3">Device or Resource</th><th className="p-3">Correlation</th><th className="p-3">Severity</th></tr></thead><tbody>{events.map((event) => <EventRow key={event.id} event={event} onOpen={() => onOpen(event.id)} />)}</tbody></table></ScrollArea>
}

function EventRow({ event, onOpen }: { event: EventView; onOpen: () => void }) { return <tr className="border-b border-border/70 hover:bg-muted/50"><td className="p-3 text-muted-foreground">{event.occurredAt}</td><td className="p-3"><button onClick={onOpen} className="text-left font-medium underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-primary">{event.name}</button></td><td className="p-3"><StatusBadge label={event.kind} tone={event.kind === "audit" ? "info" : "healthy"} /></td><td className="p-3">{event.actor}</td><td className="p-3">{event.resourceType} · {event.resourceId}</td><td className="drift-data p-3 text-[10px]">{event.correlationId}</td><td className="p-3"><FailureBadge failureClass={event.failureClass} />{!event.failureClass ? <span className="text-muted-foreground">Normal</span> : null}</td></tr> }
function Detail({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt className="text-muted-foreground">{label}</dt><dd className={`mt-1 ${mono ? "drift-data text-[10px]" : ""}`}>{value}</dd></div> }
