import { useMemo, useState } from "react"
import { RefreshCw, Search, Smartphone } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { LabAdapterIndicator } from "./lab-adapter"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { DataTablePagination, DeviceStatus, EmptyState, PageIntro, StatusBadge } from "./shared"
import { buildInspection, deviceName, endpointAddress } from "./device-inspection"
import { DeviceInspectSheet } from "./DeviceInspectSheet"

type Filter = "all" | "online" | "attention" | "replaced" | "retired"
const filters: { value: Filter; label: string }[] = [{ value: "all", label: "All" }, { value: "online", label: "Online" }, { value: "attention", label: "Needs Attention" }, { value: "replaced", label: "Replaced" }, { value: "retired", label: "Retired" }]

// This registry is read-only: the device service exposes ListDevices and
// GetDevice and nothing else. No control on this page may offer a mutation the
// service cannot execute, and none may open a destructive confirmation for one.
export function DevicesPage({ snapshot, dispatch, view = "all", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: Filter) => void }) {
  const filter: Filter = isFilter(view) ? view : "all"
  const [query, setQuery] = useState(""); const [inspectedId, setInspectedId] = useState<string | null>(null); const [inspectTab, setInspectTab] = useState("identity"); const [message, setMessage] = useState(""); const [page, setPage] = useState(0); const [pageSize, setPageSize] = useState(5)
  const devices = useMemo(() => snapshot.devices.filter((device) => matches(device, filter) && searchable(device).includes(query.trim().toLowerCase())), [snapshot.devices, filter, query])
  const pageCount = Math.max(1, Math.ceil(devices.length / pageSize)); const safePage = Math.min(page, pageCount - 1); const visibleDevices = devices.slice(safePage * pageSize, (safePage + 1) * pageSize); const detail = snapshot.devices.find((device) => device.id === inspectedId)
  const inspection = useMemo(() => (detail ? buildInspection(detail, snapshot) : []), [detail, snapshot])
  // A tab exists only when the snapshot backs it, so the active tab is derived
  // from what the selected device actually has.
  const activeInspectionTab = inspection.some((tab) => tab.id === inspectTab) ? inspectTab : inspection[0]?.id ?? "identity"
  function inspect(device: DeviceView) { setInspectedId(device.id); setInspectTab("identity") }
  function refresh() { void reportDispatch(dispatch, { type: "refresh" }, setMessage) }
  return <><PageIntro eyebrow="INVENTORY / DEVICES" title="Device Registry" description="Stable identities are primary; transport endpoints are mutable projections, inspectable per device." actions={<><LabAdapterIndicator adapter={snapshot.labAdapter} /><Button variant="outline" size="sm" onClick={refresh}><RefreshCw className="size-3.5" aria-hidden="true" />Refresh</Button></>} />
    <Tabs value={filter} onValueChange={(next) => { if (isFilter(next)) { setPage(0); onViewChange?.(next) } }}><div className="flex flex-wrap items-center justify-between gap-3 border-y border-border py-3"><TabsList className="h-auto max-w-full flex-wrap justify-start rounded-none bg-transparent p-0" aria-label="Device Registry Views">{filters.map((item) => <TabsTrigger key={item.value} value={item.value} className="rounded-none">{item.label}</TabsTrigger>)}</TabsList><div className="relative w-full sm:w-64"><Search className="absolute left-2 top-2.5 size-3.5 text-muted-foreground" aria-hidden="true" /><Input aria-label="Search Device Registry" value={query} onChange={(event) => { setQuery(event.target.value); setPage(0) }} placeholder="Search devices" className="h-9 rounded-none pl-7 text-xs" /></div></div>
      <p aria-live="polite" className="mt-2 min-h-4 text-[11px] text-muted-foreground">{message}</p>
      <TabsContent value={filter} className="mt-4">
      {devices.length === 0 ? <EmptyState label="No Devices in This View" detail="Change the active view or search term." /> : <>
        {/* The registry sizes to its rows and caps its own height: a fixed height would reserve an empty box below a short page. */}
        <div role="region" aria-label="Device Registry" tabIndex={0} className="max-h-[min(62vh,680px)] overflow-auto border-x border-t border-border focus-visible:outline-2 focus-visible:outline-primary">
          <table className="w-full min-w-[760px] border-collapse text-left text-xs"><caption className="sr-only">Device Registry</caption><thead className="sticky top-0 bg-background"><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="p-3">Identity</th><th className="p-3">Lifecycle</th><th className="p-3">Health</th><th className="p-3">Endpoint</th><th className="p-3">Last Seen</th><th className="p-3"><span className="sr-only">Actions</span></th></tr></thead><tbody>{visibleDevices.map((device) => <DeviceRow key={device.id} device={device} endpoints={snapshot.endpoints} onInspect={() => inspect(device)} />)}</tbody></table>
        </div>
        <DataTablePagination page={safePage} pageSize={pageSize} total={devices.length} onPageChange={setPage} onPageSizeChange={(nextPageSize) => { setPageSize(nextPageSize); setPage(0) }} /></>}
      </TabsContent></Tabs>
    <DeviceInspectSheet device={detail} tabs={inspection} activeTab={activeInspectionTab} onTabChange={setInspectTab} onClose={() => setInspectedId(null)} snapshot={snapshot} dispatch={dispatch} /></>
}
function DeviceRow({ device, endpoints, onInspect }: { device: DeviceView; endpoints: ControlPlaneSnapshot["endpoints"]; onInspect: () => void }) {
  // Stable identity leads the row. A scanned device's display name is its
  // transport address, which is an endpoint attribute: it stays in the Endpoint
  // column and never titles the device.
  const name = deviceName(device, endpoints)
  const endpoint = endpoints.find((candidate) => candidate.id === device.endpointId)
  return <tr className="border-b border-border/70 hover:bg-muted/50 focus-within:bg-muted/50"><td className="p-3"><p className={`truncate text-sm font-semibold ${name.mono ? "drift-data" : ""}`}>{name.primary}</p>{name.secondary ? <p className="drift-data mt-1 truncate text-[10px] text-muted-foreground">{name.secondary}</p> : null}</td><td className="p-3"><StatusBadge label={device.lifecycle} tone={device.lifecycle === "active" ? "healthy" : "neutral"} /></td><td className="p-3"><DeviceStatus device={device} /></td><td className="drift-data p-3 text-[10px]">{endpoint ? endpointAddress(endpoint) : "—"}</td><td className="p-3 text-muted-foreground">{device.lastSeen}</td><td className="p-3 text-right"><Button size="sm" variant="outline" aria-label={`Inspect ${name.primary}`} onClick={onInspect}><Smartphone className="size-3.5" aria-hidden="true" />Inspect</Button></td></tr> }
function isFilter(value: string): value is Filter { return filters.some((filter) => filter.value === value) }
function searchable(device: DeviceView) { return `${device.displayName} ${device.stableIdentity} ${device.location}`.toLowerCase() }
function matches(device: DeviceView, filter: Filter) { return filter === "all" || filter === "online" && device.status === "online" || filter === "attention" && device.status !== "online" || filter === "replaced" && device.lifecycle === "unavailable" || filter === "retired" && device.lifecycle === "retired" }
