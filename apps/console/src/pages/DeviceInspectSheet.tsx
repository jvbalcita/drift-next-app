import { useRef, useState } from "react"
import { ArrowLeft, ChevronDown, ChevronRight, Network, ShieldCheck } from "lucide-react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { ControlPlaneSnapshot, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { deviceName, endpointsFor, type InspectionItem, type InspectionSection, type InspectionTab } from "./device-inspection"

type DeviceRefreshState = { deviceId: string; status: "refreshing" | "success" | "error"; message: string }

/**
 * DeviceInspectSheet renders the inspection surface for one device.
 *
 * The title is the device name or adapter model: the transport address is a
 * mutable endpoint attribute and never names the device. The body follows the
 * old Drift inspection pattern: one compact, scrollable stream of sections,
 * with no secondary metadata band or tab navigation.
 */
export function DeviceInspectSheet({
  device,
  tabs,
  onClose,
  snapshot,
  dispatch,
  refreshState,
}: {
  device?: DeviceView
  tabs: readonly InspectionTab[]
  onClose: () => void
  snapshot: ControlPlaneSnapshot
  dispatch: DispatchIntent
  refreshState?: DeviceRefreshState
}) {
  const endpoints = device ? endpointsFor(device.id, snapshot.endpoints) : []
  const name = device ? deviceName(device, endpoints) : undefined
  const [accessOpen, setAccessOpen] = useState(false)
  const [connectionOpen, setConnectionOpen] = useState(false)
  const accessTriggerRef = useRef<HTMLButtonElement>(null)
  const connectionTriggerRef = useRef<HTMLButtonElement>(null)
  const mainTabs = tabs.filter((tab) => tab.id !== "access")
  const accessTab = tabs.find((tab) => tab.id === "access")
  const connectionTab = tabs.find((tab) => tab.id === "endpoint")
  const endpointItems = connectionTab?.sections.flatMap((section) => section.items ?? []) ?? []

  function returnToDetails(sheet: "access" | "connection") {
    setAccessOpen(false)
    setConnectionOpen(false)
    const triggerRef = sheet === "access" ? accessTriggerRef : connectionTriggerRef
    requestAnimationFrame(() => triggerRef.current?.focus())
  }
  return (
    <>
    <Sheet open={Boolean(device) && !accessOpen && !connectionOpen} onOpenChange={(open) => { if (!open && !accessOpen && !connectionOpen) onClose() }}>
      <SheetContent className="w-full rounded-none bg-popover p-0 sm:max-w-xl lg:max-w-2xl">
        <SheetHeader className="border-b border-border px-4 py-4 pr-12 sm:px-5">
          <SheetTitle className="break-words text-lg tracking-[-0.025em]">{name?.primary ?? "Device Inspection"}</SheetTitle>
          <SheetDescription className="sr-only">Inspect the observed details and history for this device.</SheetDescription>
          {refreshState ? <p aria-live="polite" aria-busy={refreshState.status === "refreshing"} className={`text-[11px] leading-5 ${refreshState.status === "error" ? "text-destructive" : "text-muted-foreground"}`}>{refreshState.message}</p> : null}
        </SheetHeader>
        {device ? (
          <SheetBody>
            <div>
              {mainTabs.filter((tab) => tab.id !== "endpoint").flatMap((tab) => tab.sections.map((section, index) => <InspectionSectionView key={`${tab.id}-${section.title ?? index}`} section={section} />))}
              {accessTab || connectionTab ? <div className="grid gap-2 border-b border-border p-3 lg:grid-cols-2">
                {connectionTab ? <Button ref={connectionTriggerRef} type="button" variant="ghost" className="h-auto min-w-0 justify-between gap-3 border border-border bg-popover px-3 py-2.5 text-left text-foreground hover:bg-muted/50" onClick={() => { setAccessOpen(false); setConnectionOpen(true) }}>
                  <span className="min-w-0 flex-1"><span className="flex min-w-0 items-center gap-2 font-medium"><Network className="size-4 shrink-0" aria-hidden="true" /><span className="truncate">Connection &amp; Endpoints</span></span><span className="mt-1 block text-[10px] font-normal text-muted-foreground">{endpointItems.length} record{endpointItems.length === 1 ? "" : "s"}</span></span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                </Button> : null}
                {accessTab ? <Button ref={accessTriggerRef} type="button" variant="ghost" className="h-auto min-w-0 justify-between gap-3 border border-border bg-popover px-3 py-2.5 text-left text-foreground hover:bg-muted/50" onClick={() => { setConnectionOpen(false); setAccessOpen(true) }}>
                  <span className="min-w-0 flex-1"><span className="flex min-w-0 items-center gap-2 font-medium"><ShieldCheck className="size-4 shrink-0" aria-hidden="true" /><span className="truncate">Access &amp; Control</span></span><span className="mt-1 block text-[10px] font-normal text-muted-foreground">Leases and assignments</span></span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                </Button> : null}
              </div> : null}
              <div className="px-4 pb-5 sm:px-5">
                <DeviceInputControls device={device} snapshot={snapshot} dispatch={dispatch} />
              </div>
            </div>
          </SheetBody>
        ) : null}
      </SheetContent>
    </Sheet>
    <Sheet open={Boolean(device) && accessOpen} onOpenChange={(open) => { if (!open) returnToDetails("access") }}>
      <SheetContent className="w-full rounded-none bg-popover p-0 sm:max-w-xl lg:max-w-2xl" showCloseButton={false}>
        <SheetHeader className="border-b border-border px-3 py-3">
          <Button type="button" size="sm" variant="ghost" className="mb-2 w-fit px-0" onClick={() => returnToDetails("access")}><ArrowLeft className="size-4" aria-hidden="true" />Back to device details</Button>
          <SheetTitle>Access &amp; Control</SheetTitle>
          <SheetDescription>Control leases and account assignments for {name?.primary ?? "this device"}.</SheetDescription>
        </SheetHeader>
        <SheetBody>{accessTab?.sections.map((section, index) => <InspectionSectionView key={`${accessTab.id}-${section.title ?? index}`} section={section} />)}</SheetBody>
      </SheetContent>
    </Sheet>
    <Sheet open={Boolean(device) && connectionOpen} onOpenChange={(open) => { if (!open) returnToDetails("connection") }}>
      <SheetContent className="w-full rounded-none bg-popover p-0 sm:max-w-xl lg:max-w-2xl" showCloseButton={false}>
        <SheetHeader className="border-b border-border px-3 py-3">
          <Button type="button" size="sm" variant="ghost" className="mb-2 w-fit px-0" onClick={() => returnToDetails("connection")}><ArrowLeft className="size-4" aria-hidden="true" />Back to device details</Button>
          <SheetTitle>Connection &amp; Endpoints</SheetTitle>
          <SheetDescription>Current and historical transport records for {name?.primary ?? "this device"}, grouped by observation date.</SheetDescription>
        </SheetHeader>
        <SheetBody><EndpointHistoryView items={endpointItems} /></SheetBody>
      </SheetContent>
    </Sheet>
    </>
  )
}

function DeviceInputControls({ device, snapshot, dispatch }: { device: DeviceView; snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [mode, setMode] = useState<"tap" | "swipe" | "key">("tap")
  const [values, setValues] = useState({ x: "0", y: "0", startX: "0", startY: "0", endX: "0", endY: "0", durationMs: "300", width: "1080", height: "1920", keyCode: "4", observationToken: "" })
  const [status, setStatus] = useState("")
  const lease = snapshot.leases.find((candidate) => candidate.deviceId === device.id && candidate.state === "active")
  const observation = snapshot.observations.find((candidate) => candidate.deviceId === device.id && candidate.freshnessToken)
  if (device.lifecycle === "retired" || device.controlEligibility !== "eligible" || !lease || !observation) return null
  const token = observation.freshnessToken
  const set = (key: keyof typeof values, value: string) => setValues((current) => ({ ...current, [key]: value }))

  async function submit() {
    const number = (key: keyof typeof values) => Number(values[key])
    const common = { deviceId: device.id, confirmed: true }
    const result = mode === "tap"
      ? await dispatch({ type: "submitDeviceTap", ...common, x: number("x"), y: number("y"), renderWidth: number("width"), renderHeight: number("height"), observationToken: values.observationToken || token })
      : mode === "swipe"
        ? await dispatch({ type: "submitDeviceSwipe", ...common, startX: number("startX"), startY: number("startY"), endX: number("endX"), endY: number("endY"), durationMs: number("durationMs"), renderWidth: number("width"), renderHeight: number("height"), observationToken: values.observationToken || token })
        : await dispatch({ type: "submitDeviceKeyEvent", ...common, keyCode: number("keyCode"), observationToken: values.observationToken || token })
    setStatus(`${result.ok ? "Kernel outcome" : "Kernel refusal"}: ${result.message}`)
  }

  return (
    <section aria-labelledby="device-input-title" className="mt-6 space-y-3 border-t border-border pt-5">
      <div>
        <h3 id="device-input-title" className="text-[10px] font-semibold uppercase tracking-[.08em]">Device input</h3>
        <p className="text-[11px] leading-5 text-muted-foreground">Lab/fake-device path only. Actions require this device's active lease and latest observation.</p>
      </div>
      <div className="flex flex-wrap gap-1" role="group" aria-label="Device input type">
        {(["tap", "swipe", "key"] as const).map((item) => (
          <Button key={item} type="button" size="sm" variant={mode === item ? "default" : "outline"} onClick={() => setMode(item)}>
            {item === "key" ? "Key event" : item[0].toUpperCase() + item.slice(1)}
          </Button>
        ))}
      </div>
      {mode === "tap" ? (
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="Tap X" inputMode="numeric" value={values.x} onChange={(event) => set("x", event.target.value)} />
          <Input aria-label="Tap Y" inputMode="numeric" value={values.y} onChange={(event) => set("y", event.target.value)} />
        </div>
      ) : null}
      {mode === "swipe" ? (
        <div className="grid grid-cols-2 gap-2">
          {(["startX", "startY", "endX", "endY", "durationMs"] as const).map((key) => (
            <Input key={key} aria-label={key} inputMode="numeric" value={values[key]} onChange={(event) => set(key, event.target.value)} />
          ))}
        </div>
      ) : null}
      {mode === "key" ? <Input aria-label="Key code" inputMode="numeric" value={values.keyCode} onChange={(event) => set("keyCode", event.target.value)} /> : null}
      {mode !== "key" ? (
        <div className="grid grid-cols-2 gap-2">
          <Input aria-label="Render width" inputMode="numeric" value={values.width} onChange={(event) => set("width", event.target.value)} />
          <Input aria-label="Render height" inputMode="numeric" value={values.height} onChange={(event) => set("height", event.target.value)} />
          <Input className="col-span-2" aria-label="Observation token" value={values.observationToken || token} onChange={(event) => set("observationToken", event.target.value)} />
        </div>
      ) : null}
      <Button type="button" onClick={() => void submit()}>Submit to kernel</Button>
      <p role="status" aria-live="polite" className="min-h-5 text-xs">{status}</p>
    </section>
  )
}

function InspectionSectionView({ section }: { section: InspectionSection }) {
  return (
    <section className="border-b border-border last:border-b-0">
      {section.title ? <h3 className="border-b border-border px-3 pt-4 pb-2 text-[10px] font-semibold uppercase tracking-[.1em] text-primary">{section.title}</h3> : null}
      {section.description ? <p className="border-b border-border px-3 pb-3 text-[11px] leading-5 text-muted-foreground">{section.description}</p> : null}
      {section.rows ? (
        <dl className="grid text-xs sm:grid-cols-2">
          {section.rows.map((entry) => <InspectionField key={entry.label} label={entry.label} value={entry.value} mono={entry.mono} exact={entry.exact} />)}
        </dl>
      ) : null}
      {section.items ? (
        <div>
          {section.items.map((item) => (
            <article key={item.key} className="border-t border-border/70 first:border-t-0">
              <dl className="grid text-xs sm:grid-cols-2">
                {item.rows.map((entry) => <InspectionField key={entry.label} label={entry.label} value={entry.value} mono={entry.mono} exact={entry.exact} />)}
              </dl>
            </article>
          ))}
        </div>
      ) : null}
    </section>
  )
}

function InspectionField({ label, value, mono = false, exact }: { label: string; value: string; mono?: boolean; exact?: string }) {
  return (
    <div className="min-w-0 border-b border-border/70 p-3 last:border-b-0 sm:border-r sm:border-border/70 sm:[&:nth-child(even)]:border-r-0">
      <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{label}</dt>
      <dd title={exact} className={`mt-1 break-words font-medium ${mono ? "drift-data text-[11px]" : "text-xs"}`}>{value}</dd>
    </div>
  )
}

function EndpointHistoryView({ items }: { items: readonly InspectionItem[] }) {
  const groups = groupEndpointItems(items)
  if (groups.length === 0) return <p className="p-4 text-xs text-muted-foreground">No endpoint history is available for this device.</p>

  return <div>{groups.map((group, index) => (
    <Collapsible key={group.key} defaultOpen={index === 0} className="border-b border-border last:border-b-0">
      <CollapsibleTrigger className="group flex w-full items-center justify-between gap-3 px-3 py-3 text-left text-xs font-semibold hover:bg-muted/50 focus-visible:bg-muted/50">
        <span>{group.label}</span>
        <span className="flex items-center gap-2 text-[10px] font-normal uppercase tracking-[.08em] text-muted-foreground">
          {group.items.length} record{group.items.length === 1 ? "" : "s"}
          <ChevronDown className="size-4 transition-transform group-data-[panel-open]:rotate-180" aria-hidden="true" />
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="border-t border-border/70">
          {group.items.map((item, index) => <article key={item.key} className="border-b border-border/70 p-3 last:border-b-0">
            <div className="mb-2 flex items-start justify-between gap-3 border-b border-border/70 pb-2">
              <div className="min-w-0"><p className="text-[10px] font-semibold uppercase tracking-[.08em] text-primary">Record {index + 1}</p><p className="mt-0.5 text-[10px] text-muted-foreground">{endpointObservedLabel(item)}</p></div>
              <span className="shrink-0 text-[10px] font-semibold uppercase tracking-[.08em] text-muted-foreground">{endpointStateLabel(item)}</span>
            </div>
            <dl className="grid text-xs">{item.rows.map((entry) => <InspectionField key={entry.label} label={entry.label} value={entry.value} mono={entry.mono} exact={entry.exact} />)}</dl>
          </article>)}
        </div>
      </CollapsibleContent>
    </Collapsible>
  ))}</div>
}

type EndpointHistoryItem = InspectionItem

function groupEndpointItems(items: readonly EndpointHistoryItem[]) {
  const groups = new Map<string, { key: string; label: string; sort: number; items: EndpointHistoryItem[] }>()
  for (const item of items) {
    const observedAt = item.rows.find((row) => row.label === "Observed At")?.value ?? ""
    const timestamp = Date.parse(observedAt)
    const key = Number.isFinite(timestamp) ? new Date(timestamp).toISOString().slice(0, 10) : "unknown"
    const existing = groups.get(key)
    if (existing) {
      existing.items.push(item)
      existing.sort = Math.max(existing.sort, Number.isFinite(timestamp) ? timestamp : -1)
      continue
    }
    groups.set(key, { key, label: endpointDateLabel(key), sort: Number.isFinite(timestamp) ? timestamp : -1, items: [item] })
  }
  return [...groups.values()]
    .map((group) => ({ ...group, items: [...group.items].sort((left, right) => endpointTimestamp(right) - endpointTimestamp(left)) }))
    .sort((left, right) => right.sort - left.sort)
}

function endpointTimestamp(item: EndpointHistoryItem) {
  const value = Date.parse(item.rows.find((row) => row.label === "Observed At")?.value ?? "")
  return Number.isFinite(value) ? value : -1
}

function endpointDateLabel(key: string) {
  if (key === "unknown") return "Date not reported"
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${key}T00:00:00Z`))
}

function endpointStateLabel(item: EndpointHistoryItem) {
  const state = item.rows.find((row) => row.label === "State")?.value.trim().toLowerCase()
  if (state === "current") return "Current"
  if (state === "superseded") return "Historical"
  return state ? state[0].toUpperCase() + state.slice(1) : "Unspecified"
}

function endpointObservedLabel(item: EndpointHistoryItem) {
  const value = item.rows.find((row) => row.label === "Observed At")?.value ?? ""
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return "Observation time not reported"
  return `Observed ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" }).format(new Date(timestamp))} UTC`
}
