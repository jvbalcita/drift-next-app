import { useRef, useState } from "react"
import { ArrowLeft, ShieldCheck } from "lucide-react"
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { ControlPlaneSnapshot, DeviceView, DispatchIntent } from "@/lib/domain/control-plane"
import { deviceName, endpointsFor, type InspectionSection, type InspectionTab } from "./device-inspection"

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
}: {
  device?: DeviceView
  tabs: readonly InspectionTab[]
  onClose: () => void
  snapshot: ControlPlaneSnapshot
  dispatch: DispatchIntent
}) {
  const endpoints = device ? endpointsFor(device.id, snapshot.endpoints) : []
  const name = device ? deviceName(device, endpoints) : undefined
  const [accessOpen, setAccessOpen] = useState(false)
  const accessTriggerRef = useRef<HTMLButtonElement>(null)
  const mainTabs = tabs.filter((tab) => tab.id !== "access")
  const accessTab = tabs.find((tab) => tab.id === "access")

  function returnToDetails() {
    setAccessOpen(false)
    requestAnimationFrame(() => accessTriggerRef.current?.focus())
  }
  return (
    <>
    <Sheet open={Boolean(device) && !accessOpen} onOpenChange={(open) => { if (!open && !accessOpen) onClose() }}>
      <SheetContent className="w-full rounded-none bg-popover p-0 sm:max-w-xl lg:max-w-2xl">
        <SheetHeader className="border-b border-border px-4 py-4 pr-12 sm:px-5">
          <SheetTitle className="break-words text-lg tracking-[-0.025em]">{name?.primary ?? "Device Inspection"}</SheetTitle>
          <SheetDescription className="sr-only">Inspect the observed details and history for this device.</SheetDescription>
        </SheetHeader>
        {device ? (
          <SheetBody>
            <div>
              {mainTabs.flatMap((tab) => tab.sections.map((section, index) => <InspectionSectionView key={`${tab.id}-${section.title ?? index}`} section={section} />))}
              {accessTab ? <div className="border-b border-border p-3"><Button ref={accessTriggerRef} type="button" variant="outline" className="w-full justify-between" onClick={() => setAccessOpen(true)}><span className="flex items-center gap-2"><ShieldCheck className="size-4" aria-hidden="true" />Access &amp; Control</span><span className="text-xs text-muted-foreground">Leases and assignments</span></Button></div> : null}
              <div className="px-4 pb-5 sm:px-5">
                <DeviceInputControls device={device} snapshot={snapshot} dispatch={dispatch} />
              </div>
            </div>
          </SheetBody>
        ) : null}
      </SheetContent>
    </Sheet>
    <Sheet open={Boolean(device) && accessOpen} onOpenChange={(open) => { if (!open) returnToDetails() }}>
      <SheetContent className="w-full rounded-none bg-popover p-0 sm:max-w-xl lg:max-w-2xl" showCloseButton={false}>
        <SheetHeader className="border-b border-border px-3 py-3">
          <Button type="button" size="sm" variant="ghost" className="mb-2 w-fit px-0" onClick={returnToDetails}><ArrowLeft className="size-4" aria-hidden="true" />Back to device details</Button>
          <SheetTitle>Access &amp; Control</SheetTitle>
          <SheetDescription>Control leases and account assignments for {name?.primary ?? "this device"}.</SheetDescription>
        </SheetHeader>
        <SheetBody>{accessTab?.sections.map((section, index) => <InspectionSectionView key={`${accessTab.id}-${section.title ?? index}`} section={section} />)}</SheetBody>
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
