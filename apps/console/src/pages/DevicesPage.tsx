import { useMemo, useState } from "react"
import { Loader2, RefreshCw, Search, Smartphone, TriangleAlert } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { LabAdapterIndicator } from "./lab-adapter"
import { DataTablePagination, DeviceStatus, EmptyState, PageIntro } from "./shared"
import { buildInspection, deviceName, endpointHost, endpointPort, humanTimestamp } from "./device-inspection"
import { DeviceInspectSheet } from "./DeviceInspectSheet"

type Filter = "all" | "online" | "attention" | "replaced" | "retired"

const filters: { value: Filter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "online", label: "Online" },
  { value: "attention", label: "Needs Attention" },
  { value: "replaced", label: "Replaced" },
  { value: "retired", label: "Retired" },
]

// This registry is read-only: the device service exposes ListDevices and
// GetDevice and nothing else. No control on this page may offer a mutation the
// service cannot execute, and none may open a destructive confirmation for one.
export function DevicesPage({ snapshot, dispatch, view = "all", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: Filter) => void }) {
  const filter: Filter = isFilter(view) ? view : "all"
  const [query, setQuery] = useState("")
  const [inspectedId, setInspectedId] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(5)
  const normalizedQuery = query.trim().toLowerCase()
  const devices = useMemo(
    () => snapshot.devices.filter((device) => matches(device, filter) && searchable(device, snapshot.endpoints).includes(normalizedQuery)),
    [normalizedQuery, snapshot.devices, snapshot.endpoints, filter],
  )
  const pageCount = Math.max(1, Math.ceil(devices.length / pageSize))
  const safePage = Math.min(page, pageCount - 1)
  const visibleDevices = devices.slice(safePage * pageSize, (safePage + 1) * pageSize)
  const detail = snapshot.devices.find((device) => device.id === inspectedId)
  const inspection = useMemo(() => (detail ? buildInspection(detail, snapshot) : []), [detail, snapshot])

  function inspect(device: DeviceView) {
    setInspectedId(device.id)
  }
  async function refresh() {
    if (refreshing) return
    setRefreshing(true)
    try {
      const outcome = await dispatch({ type: "refresh" })
      if (outcome.ok) toast.success("Device registry refreshed", { description: outcome.message })
      else toast.error("Device registry refresh failed", { description: outcome.message })
    } catch {
      toast.error("Device registry refresh failed", { description: "The control plane could not refresh the registry projection." })
    } finally {
      setRefreshing(false)
    }
  }

  return <>
    <PageIntro
      eyebrow="INVENTORY / DEVICES"
      title="Device Registry"
      description="Names come from the captured Android device name when available. Status and endpoint values come from the current transport observation."
      actions={<><LabAdapterIndicator adapter={snapshot.labAdapter} /><Button variant="outline" size="sm" disabled={refreshing} onClick={() => void refresh()}>{refreshing ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> : <RefreshCw className="size-3.5" aria-hidden="true" />}{refreshing ? "Refreshing…" : "Refresh"}</Button></>}
    />
    {snapshot.projectionWarnings.length > 0 ? <div role="alert" className="mb-5 flex items-start gap-2 border-l-2 border-amber-600 bg-amber-50 p-3 text-xs leading-5 text-amber-900">
      <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <div><p className="font-semibold">Registry read needs attention</p><ul className="mt-1 list-disc pl-4">{snapshot.projectionWarnings.map((warning) => <li key={`${warning.source}-${warning.message}`}>{warning.message}</li>)}</ul></div>
    </div> : null}
    <Tabs value={filter} onValueChange={(next) => { if (isFilter(next)) { setPage(0); onViewChange?.(next) } }}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-y border-border py-3">
        <TabsList aria-label="Device Registry Views">
          {filters.map((item) => <TabsTrigger key={item.value} value={item.value}>{item.label}</TabsTrigger>)}
        </TabsList>
        <div className="relative w-full sm:w-64">
          <Search className="absolute left-2 top-2.5 size-3.5 text-muted-foreground" aria-hidden="true" />
          <Input aria-label="Search Device Registry" value={query} onChange={(event) => { setQuery(event.target.value); setPage(0) }} placeholder="Search devices" className="h-9 rounded-none pl-7 text-xs" />
        </div>
      </div>
      <TabsContent value={filter} className="mt-4">
        {devices.length === 0 ? <EmptyState label="No Devices in This View" detail="Change the active view or search term." /> : <>
          {/* The registry sizes to its rows and caps its own height: a fixed height would reserve an empty box below a short page. */}
          <div role="region" aria-label="Device Registry" tabIndex={0} className="max-h-[min(62vh,680px)] overflow-x-auto overflow-y-auto border-x border-t border-border focus-visible:outline-2 focus-visible:outline-primary">
            <table className="w-full min-w-[1080px] border-collapse text-left text-xs">
              <caption className="sr-only">Device Registry</caption>
              <thead className="sticky top-0 z-10 bg-background">
                <tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground">
                  <th scope="col" className="min-w-44 p-3">Device Name</th>
                  <th scope="col" className="min-w-32 p-3">Phone Model</th>
                  <th scope="col" className="min-w-44 p-3">Serial Number</th>
                  <th scope="col" className="min-w-32 p-3">Endpoint</th>
                  <th scope="col" className="w-20 p-3">Port</th>
                  <th scope="col" className="min-w-32 p-3">Status</th>
                  <th scope="col" className="min-w-40 p-3">Last Seen</th>
                  <th scope="col" className="p-3"><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>{visibleDevices.map((device) => <DeviceRow key={device.id} device={device} endpoints={snapshot.endpoints} onInspect={() => inspect(device)} />)}</tbody>
            </table>
          </div>
          <DataTablePagination page={safePage} pageSize={pageSize} total={devices.length} onPageChange={setPage} onPageSizeChange={(nextPageSize) => { setPageSize(nextPageSize); setPage(0) }} />
        </>}
      </TabsContent>
    </Tabs>
    <DeviceInspectSheet device={detail} tabs={inspection} onClose={() => setInspectedId(null)} snapshot={snapshot} dispatch={dispatch} />
  </>
}

function DeviceRow({ device, endpoints, onInspect }: { device: DeviceView; endpoints: ControlPlaneSnapshot["endpoints"]; onInspect: () => void }) {
  const name = deviceName(device, endpoints)
  // The row only uses the current endpoint projection. Historical endpoints
  // stay available in the inspect sheet and must not masquerade as reachable.
  const endpoint = device.endpointId
    ? endpoints.find((candidate) => candidate.id === device.endpointId && candidate.state === "current")
    : endpoints.find((candidate) => candidate.deviceId === device.id && candidate.state === "current")
  const lastSeen = humanTimestamp(device.lastSeen)
  return <tr className="border-b border-border/70 hover:bg-muted/50 focus-within:bg-muted/50">
    <td className="min-w-0 p-3"><p className={`truncate text-sm font-semibold ${name.mono ? "drift-data" : ""}`} title={name.primary}>{name.primary}</p></td>
    <td className="drift-data p-3 text-[11px]">{device.phoneModel?.trim() || "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint?.serial.trim() || "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint ? endpointHost(endpoint) || "—" : "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint ? endpointPort(endpoint) || "—" : "—"}</td>
    <td className="p-3"><DeviceStatus device={device} /></td>
    <td className="p-3 text-muted-foreground">{lastSeen.label ? lastSeen.exact ? <time dateTime={lastSeen.exact} title={lastSeen.exact}>{lastSeen.label}</time> : lastSeen.label : "—"}</td>
    <td className="p-3 text-right"><Button size="sm" variant="outline" aria-label={`Inspect ${name.primary}`} onClick={onInspect}><Smartphone className="size-3.5" aria-hidden="true" />Inspect</Button></td>
  </tr>
}

function isFilter(value: string): value is Filter {
  return filters.some((filter) => filter.value === value)
}

function searchable(device: DeviceView, endpoints: ControlPlaneSnapshot["endpoints"]) {
  const endpointText = endpoints.filter((endpoint) => endpoint.deviceId === device.id).flatMap((endpoint) => [endpoint.serial, endpoint.host, String(endpoint.port)]).join(" ")
  return `${device.displayName} ${device.phoneModel ?? ""} ${device.stableIdentity} ${device.location} ${endpointText}`.toLowerCase()
}

function matches(device: DeviceView, filter: Filter) {
  return filter === "all" || filter === "online" && device.status === "online" || filter === "attention" && device.status !== "online" || filter === "replaced" && device.lifecycle === "unavailable" || filter === "retired" && device.lifecycle === "retired"
}
