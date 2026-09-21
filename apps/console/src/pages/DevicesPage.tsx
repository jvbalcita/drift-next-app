import { useMemo, useState, type KeyboardEvent } from "react"
import { Check, Copy, Loader2, RefreshCw, Search, TriangleAlert } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { LabAdapterIndicator } from "./lab-adapter"
import { isOnlineDevice } from "@/lib/device-status"
import { DataTablePagination, DeviceStatus, EmptyState, PageIntro } from "./shared"
import { buildInspection, deviceName, endpointHost, endpointPort, humanTimestamp } from "./device-inspection"
import { DeviceInspectSheet } from "./DeviceInspectSheet"

type Filter = "all" | "online" | "attention" | "replaced" | "retired"
type DeviceRefreshState = { deviceId: string; status: "refreshing" | "success" | "error"; message: string }

const filters: { value: Filter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "online", label: "Online" },
  { value: "attention", label: "Needs Attention" },
  { value: "replaced", label: "Replaced" },
  { value: "retired", label: "Retired" },
]

// Registry mutations stay in the inspection sheet, where the selected device
// identity and the confirmation context are visible. The table itself remains
// a projection and never invents a row-level mutation.
export function DevicesPage({ snapshot, dispatch, view = "all", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: Filter) => void }) {
  const filter: Filter = isFilter(view) ? view : "all"
  const [query, setQuery] = useState("")
  const [inspectedId, setInspectedId] = useState<string | null>(null)
  const [deviceRefresh, setDeviceRefresh] = useState<DeviceRefreshState | undefined>()
  const [refreshing, setRefreshing] = useState(false)
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const normalizedQuery = query.trim().toLowerCase()
  const devices = useMemo(
    () => snapshot.devices.filter((device) => matches(device, filter) && searchable(device, snapshot.endpoints).includes(normalizedQuery)),
    [normalizedQuery, snapshot.devices, snapshot.endpoints, filter],
  )
  const pageCount = Math.max(1, Math.ceil(devices.length / pageSize))
  const safePage = Math.min(page, pageCount - 1)
  const visibleDevices = devices.slice(safePage * pageSize, (safePage + 1) * pageSize)
  const selectedDeviceIds = devices.filter((device) => selectedIds.has(device.id)).map((device) => device.id)
  const allDevicesSelected = devices.length > 0 && selectedDeviceIds.length === devices.length
  const someDevicesSelected = selectedDeviceIds.length > 0 && !allDevicesSelected
  const detail = snapshot.devices.find((device) => device.id === inspectedId)
  const inspection = useMemo(() => (detail ? buildInspection(detail, snapshot) : []), [detail, snapshot])

  function inspect(device: DeviceView) {
    setInspectedId(device.id)
    setDeviceRefresh({ deviceId: device.id, status: "refreshing", message: "Refreshing device facts…" })
    void dispatch({ type: "refresh", deviceId: device.id }).then((outcome) => {
      setDeviceRefresh({ deviceId: device.id, status: outcome.ok ? "success" : "error", message: outcome.message })
    }).catch(() => {
      setDeviceRefresh({ deviceId: device.id, status: "error", message: "The device facts could not be refreshed." })
    })
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
    <Tabs value={filter} onValueChange={(next) => { if (isFilter(next)) { setPage(0); setSelectedIds(new Set()); onViewChange?.(next) } }}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-y border-border py-3">
        <TabsList aria-label="Device Registry Views">
          {filters.map((item) => <TabsTrigger key={item.value} value={item.value}>{item.label}</TabsTrigger>)}
        </TabsList>
        <div className="flex w-full flex-wrap items-center justify-end gap-3 sm:w-auto">
          <BulkDeviceLifecycleControls devices={devices} selectedDeviceIds={selectedDeviceIds} dispatch={dispatch} onSelectionChange={setSelectedIds} />
          <div className="relative w-full sm:w-64">
            <Search className="absolute left-2 top-2.5 size-3.5 text-muted-foreground" aria-hidden="true" />
            <Input aria-label="Search Device Registry" value={query} onChange={(event) => { setQuery(event.target.value); setPage(0) }} placeholder="Search devices" className="h-9 rounded-none pl-7 text-xs" />
          </div>
        </div>
      </div>
      <TabsContent value={filter} className="mt-4">
        {devices.length === 0 ? <EmptyState label="No Devices in This View" detail="Change the active view or search term." /> : <>
          {/* The registry sizes to its rows and caps its own height: a fixed height would reserve an empty box below a short page. */}
          <div role="region" aria-label="Device Registry" tabIndex={0} className="max-h-[min(62vh,680px)] overflow-x-auto overflow-y-auto border-x border-t border-border focus-visible:outline-2 focus-visible:outline-primary">
            <table className="w-full min-w-[980px] border-collapse text-left text-xs">
              <caption className="sr-only">Device Registry</caption>
              <thead className="sticky top-0 z-10 bg-background">
                <tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground">
                  <th scope="col" className="w-12 p-3"><Checkbox aria-label={`Select all ${devices.length} devices in this view`} checked={allDevicesSelected} indeterminate={someDevicesSelected} onCheckedChange={(checked) => setSelectedIds(checked === true ? new Set(devices.map((device) => device.id)) : new Set())} /></th>
                  <th scope="col" className="min-w-44 p-3">Device Name</th>
                  <th scope="col" className="min-w-32 p-3">Phone Model</th>
                  <th scope="col" className="min-w-44 p-3">Serial Number</th>
                  <th scope="col" className="min-w-32 p-3">Endpoint</th>
                  <th scope="col" className="w-20 p-3">Port</th>
                  <th scope="col" className="min-w-32 p-3">Status</th>
                  <th scope="col" className="min-w-40 p-3">Last Seen</th>
                </tr>
              </thead>
              <tbody>{visibleDevices.map((device) => <DeviceRow key={device.id} device={device} endpoints={snapshot.endpoints} selected={selectedIds.has(device.id)} onSelected={(checked) => setSelectedIds((current) => { const next = new Set(current); if (checked) next.add(device.id); else next.delete(device.id); return next })} onInspect={() => inspect(device)} />)}</tbody>
            </table>
          </div>
          <DataTablePagination page={safePage} pageSize={pageSize} total={devices.length} onPageChange={setPage} onPageSizeChange={(nextPageSize) => { setPageSize(nextPageSize); setPage(0) }} />
        </>}
      </TabsContent>
    </Tabs>
    <DeviceInspectSheet device={detail} tabs={inspection} refreshState={detail && deviceRefresh?.deviceId === detail.id ? deviceRefresh : undefined} onClose={() => { setInspectedId(null); setDeviceRefresh(undefined) }} snapshot={snapshot} dispatch={dispatch} />
  </>
}

function BulkDeviceLifecycleControls({ devices, selectedDeviceIds, dispatch, onSelectionChange }: { devices: readonly DeviceView[]; selectedDeviceIds: readonly string[]; dispatch: DispatchIntent; onSelectionChange: (ids: Set<string>) => void }) {
  const [action, setAction] = useState<"retire" | "delete" | null>(null)
  const [reason, setReason] = useState("")
  const [confirmation, setConfirmation] = useState("")
  const [copiedConfirmation, setCopiedConfirmation] = useState(false)
  const [copyError, setCopyError] = useState("")
  const [status, setStatus] = useState("")
  const [outcomes, setOutcomes] = useState<readonly { deviceId: string; ok: boolean; message: string }[]>([])
  const [pending, setPending] = useState(false)
  const selected = devices.filter((device) => selectedDeviceIds.includes(device.id))
  const allSelected = devices.length > 0 && selected.length === devices.length
  const requiredConfirmation = `DELETE ${selected.length} DEVICES`

  function open(next: "retire" | "delete") {
    setAction(next)
    setReason("")
    setConfirmation("")
    setCopiedConfirmation(false)
    setCopyError("")
    setStatus("")
    setOutcomes([])
  }

  function close() {
    if (pending) return
    setAction(null)
    setReason("")
    setConfirmation("")
    setCopiedConfirmation(false)
    setCopyError("")
  }

  async function submit() {
    if (!action || selected.length === 0 || !reason.trim() || pending) return
    setPending(true)
    const ids = selected.map((device) => device.id)
    const result = action === "retire"
      ? await dispatch({ type: "bulkRetireDevices", deviceIds: ids, reason: reason.trim() })
      : await dispatch({ type: "bulkDeleteDevices", deviceIds: ids, confirmationDeviceIds: ids, reason: reason.trim() })
    setPending(false)
    setStatus(result.message)
    setOutcomes(result.deviceLifecycleBatch?.outcomes ?? [])
    onSelectionChange(new Set())
    setAction(null)
    setReason("")
    setConfirmation("")
    setCopiedConfirmation(false)
    setCopyError("")
  }

  async function copyConfirmation() {
    if (!navigator.clipboard) {
      setCopyError("Copy is unavailable in this browser.")
      return
    }
    try {
      await navigator.clipboard.writeText(requiredConfirmation)
      setCopiedConfirmation(true)
      setCopyError("")
    } catch {
      setCopyError("The confirmation text could not be copied.")
    }
  }

  return <>
    <div role="region" aria-label="Bulk device actions" className="flex flex-wrap items-center gap-2">
      <Button type="button" size="sm" variant="ghost" className="border border-border" onClick={() => onSelectionChange(allSelected ? new Set() : new Set(devices.map((device) => device.id)))} disabled={devices.length === 0}>{allSelected ? "Clear selection" : `Select all ${devices.length}`}</Button>
      <span className="text-xs text-muted-foreground">{selected.length} selected</span>
      {selected.length > 0 ? <>
        <Button type="button" size="sm" variant="outline" onClick={() => open("retire")} disabled={pending}>Retire selected</Button>
        <Button type="button" size="sm" variant="destructive" onClick={() => open("delete")} disabled={pending}>Delete selected</Button>
      </> : null}
      {status ? <p role="status" aria-live="polite" className="basis-full text-xs">{status}</p> : null}
    </div>
    {outcomes.length > 0 ? <details className="mb-4 border border-border p-3 text-xs">
      <summary className="cursor-pointer font-medium">Review {outcomes.length} device outcomes</summary>
      <ul aria-label="Bulk device outcomes" className="mt-2 max-h-48 space-y-1 overflow-y-auto">
        {outcomes.map((outcome) => <li key={outcome.deviceId} className={outcome.ok ? "text-muted-foreground" : "text-destructive"}><span className="drift-data">{outcome.deviceId}</span>: {outcome.message}</li>)}
      </ul>
    </details> : null}
    {action ? <Dialog open onOpenChange={(open) => { if (!open) close() }}>
      <DialogContent size="lg" className="rounded-none">
        <DialogHeader>
          <DialogTitle>{action === "retire" ? `Retire ${selected.length} devices?` : `Delete ${selected.length} devices permanently?`}</DialogTitle>
          <DialogDescription>{action === "retire" ? "This records one reversible expectation decision per selected device and preserves every device history record." : "Review the exact selected identities below. Each device is checked independently; attached devices and devices with active work are refused and reported."}</DialogDescription>
        </DialogHeader>
        {action === "delete" ? <>
          <div aria-label="Selected device identities" className="max-h-40 overflow-y-auto border border-border bg-background p-2 text-xs"><ul className="space-y-1">{selected.map((device) => <li key={device.id}><span className="drift-data">{device.id}</span> · {device.displayName}</li>)}</ul></div>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <label htmlFor="bulk-device-confirmation" className="text-xs font-medium">Type <span className="drift-data">{requiredConfirmation}</span> to confirm</label>
              <Button type="button" size="sm" variant="outline" onClick={() => void copyConfirmation()} aria-label={copiedConfirmation ? "Copied deletion confirmation" : "Copy deletion confirmation"}>
                {copiedConfirmation ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
                {copiedConfirmation ? "Copied" : "Copy"}
              </Button>
            </div>
            <code className="block border border-border bg-muted/40 px-3 py-2 text-xs font-semibold tracking-wide">{requiredConfirmation}</code>
            <Input id="bulk-device-confirmation" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="off" autoFocus />
            {copyError ? <p role="status" className="text-xs text-destructive">{copyError}</p> : null}
          </div>
        </> : null}
        <div className="space-y-2"><label htmlFor="bulk-device-reason" className="text-xs font-medium">Reason</label><Input id="bulk-device-reason" value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Why is this registry decision being made?" autoFocus={action === "retire"} /></div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={close} disabled={pending}>Cancel</Button>
          <Button type="button" variant={action === "delete" ? "destructive" : "default"} onClick={() => void submit()} disabled={pending || !reason.trim() || action === "delete" && confirmation !== requiredConfirmation}>{pending ? "Saving…" : action === "retire" ? "Retire selected" : "Delete selected"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog> : null}
  </>
}

function DeviceRow({ device, endpoints, selected, onSelected, onInspect }: { device: DeviceView; endpoints: ControlPlaneSnapshot["endpoints"]; selected: boolean; onSelected: (checked: boolean) => void; onInspect: () => void }) {
  const name = deviceName(device, endpoints)
  // The row only uses the current endpoint projection. Historical endpoints
  // stay available in the inspect sheet and must not masquerade as reachable.
  const endpoint = device.endpointId
    ? endpoints.find((candidate) => candidate.id === device.endpointId && candidate.state === "current")
    : endpoints.find((candidate) => candidate.deviceId === device.id && candidate.state === "current")
  const lastSeen = humanTimestamp(device.lastSeen)
  function activate(event: KeyboardEvent<HTMLTableRowElement>) {
    if (event.key !== "Enter" && event.key !== " ") return
    event.preventDefault()
    onInspect()
  }
  return <tr tabIndex={0} aria-label={`Open details for ${name.primary}`} onClick={onInspect} onKeyDown={activate} className="cursor-pointer border-b border-border/70 outline-none hover:bg-muted/50 focus:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary">
    <td className="p-3" onClick={(event) => event.stopPropagation()}><Checkbox aria-label={`Select ${name.primary}`} checked={selected} onCheckedChange={(checked) => onSelected(checked === true)} /></td>
    <td className="min-w-0 p-3"><p className={`truncate text-sm font-semibold ${name.mono ? "drift-data" : ""}`} title={name.primary}>{name.primary}</p></td>
    <td className="drift-data p-3 text-[11px]">{device.phoneModel?.trim() || "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint?.serial.trim() || "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint ? endpointHost(endpoint) || "—" : "—"}</td>
    <td className="drift-data p-3 text-[11px]">{endpoint ? endpointPort(endpoint) || "—" : "—"}</td>
    <td className="p-3"><DeviceStatus device={device} /></td>
    <td className="p-3 text-muted-foreground">{lastSeen.label ? lastSeen.exact ? <time dateTime={lastSeen.exact} title={lastSeen.exact}>{lastSeen.label}</time> : lastSeen.label : "—"}</td>
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
  // Online is asked of the ONE online reading rather than tested here, so this
  // view and every action-candidate set resolve the same fleet (AGENTS.md §2).
  return filter === "all" || filter === "online" && isOnlineDevice(device) || filter === "attention" && !isOnlineDevice(device) || filter === "replaced" && device.observedAgainAfterRetirement || filter === "retired" && device.expectation === "retired"
}
