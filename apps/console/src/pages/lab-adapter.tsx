import { useRef, useState } from "react"
import { Activity, Camera, ShieldCheck, Stethoscope, TriangleAlert } from "lucide-react"
import { AlertDialog, AlertDialogContent, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import type {
  ControlPlaneIntent,
  DispatchIntent,
  IndeterminateActionView,
  LabAdapterView,
  LabReadiness,
  MutationResult,
  RuntimeConnectionState,
  RuntimeConnectionView,
  SpoolHealthView,
} from "@/lib/domain/control-plane"
import { usesMockControlPlane } from "@/lib/api/connect-json"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { EmptyState, FailureBadge, StatusBadge, type StatusTone } from "./shared"

type DispatchLab = (intent: ControlPlaneIntent) => Promise<MutationResult>

const readinessTones: Record<LabReadiness, StatusTone> = { ready: "healthy", blocked: "attention", indeterminate: "attention", unavailable: "neutral" }
const readinessLabels: Record<LabReadiness, string> = { ready: "Ready", blocked: "Blocked", indeterminate: "Indeterminate", unavailable: "Unavailable" }
const runtimeLabels: Record<RuntimeConnectionState, string> = { connected: "Connected", reconnecting: "Reconnecting", disconnected: "Disconnected" }
const runtimeTones: Record<RuntimeConnectionState, StatusTone> = { connected: "healthy", reconnecting: "attention", disconnected: "danger" }

const placeholder = "—"
const value = (input: string | undefined) => (input && input.length > 0 ? input : placeholder)

export function LabModeBadges({ adapter }: { adapter: LabAdapterView }) {
  return (
    <>
      <StatusBadge label={adapter.mode === "lab" && adapter.readiness !== "unavailable" ? "Connected" : "Unavailable"} tone={adapter.mode === "lab" && adapter.readiness !== "unavailable" ? "healthy" : "neutral"} />
      <StatusBadge label={readinessLabels[adapter.readiness]} tone={readinessTones[adapter.readiness]} />
      {adapter.indeterminate ? <StatusBadge label="Indeterminate Outcome" tone="attention" /> : null}
      <FailureBadge failureClass={adapter.failureClass} />
    </>
  )
}

type LabRuntimeProps = {
  adapter: LabAdapterView
  runtimeConnection: RuntimeConnectionView
  spoolHealth: SpoolHealthView
  indeterminateActions: readonly IndeterminateActionView[]
  dispatch: DispatchIntent
  dispatchLab?: DispatchLab
  onFeedback: (message: string) => void
  notice?: string
}

// LabStatusStrip is the compact lab adapter surface for the control workspace.
// It surfaces observation, runtime, and spool state only; it dispatches no
// device input, and every capture names its own target explicitly.
export function LabStatusStrip({
  adapter,
  runtimeConnection,
  spoolHealth,
  indeterminateActions,
  dispatch,
  dispatchLab,
  onFeedback,
  notice = "",
}: LabRuntimeProps) {
  const observed = adapter.lastObservedSerial.length > 0
  return (
    <section className="mt-6 border border-border bg-muted/20" aria-label="Device Adapter Status">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <span className="mr-auto flex items-center gap-2 text-xs font-semibold"><Activity className="size-3.5 text-primary" aria-hidden="true" />Device Adapter</span>
        <LabModeBadges adapter={adapter} />
        <StatusBadge label={runtimeLabels[runtimeConnection.state]} tone={runtimeTones[runtimeConnection.state]} />
        <StatusBadge label={spoolHealth.exhausted ? "Spool Exhausted" : spoolHealth.blocked > 0 ? "Spool Blocked" : "Spool Clear"} tone={spoolHealth.exhausted || spoolHealth.blocked > 0 ? "attention" : "neutral"} />
      </div>
      <dl className="grid gap-3 px-3 py-3 text-[11px] sm:grid-cols-2 xl:grid-cols-4">
        <LabField label="Last Observed" detail={observed ? adapter.lastObservedSerial : "Nothing observed yet"} />
        <LabField label="ADB Transport" detail={`${value(adapter.connectionState)} · ${value(adapter.connectionType)}`} />
        <LabField label="Runtime Connection" detail={runtimeLabels[runtimeConnection.state]} mono={false} />
        <LabField label="Spool Health" detail={`Pending ${spoolHealth.pending} · Blocked ${spoolHealth.blocked} · Max ${spoolHealth.maxSize}`} mono={false} />
        <LabField label="Spool Fence" detail={`${spoolHealth.fenceToken} (observation, not lease)`} />
        <LabField label="Indeterminate Actions" detail={`${indeterminateActions.length} requiring confirmation`} mono={false} />
      </dl>
      <div className="flex flex-wrap items-center gap-2 border-t border-border px-3 py-2">
        <CaptureObservationDialog dispatch={dispatch} dispatchLab={dispatchLab} onFeedback={onFeedback} />
        {adapter.mode === "mock" ? <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "simulateLabCaptureFailure" }, onFeedback)}><TriangleAlert className="size-3.5" aria-hidden="true" />Simulate Indeterminate</Button> : null}
        <LabDiagnosticsSheet adapter={adapter} />
        <RuntimeSpoolSheet runtimeConnection={runtimeConnection} spoolHealth={spoolHealth} indeterminateActions={indeterminateActions} dispatch={dispatch} onFeedback={onFeedback} />
      </div>
      <p aria-live="polite" className="border-t border-border px-3 py-2 text-[11px] leading-5 text-muted-foreground">{notice || "Adapter actions are read-only. A capture names exactly one attached serial; spool fence tokens are observations, not leases."}</p>
    </section>
  )
}

function LabField({ label, detail, mono = true }: { label: string; detail: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">{label}</dt>
      <dd className={`mt-1 truncate ${mono ? "drift-data text-[10px]" : "font-medium"}`}>{detail}</dd>
    </div>
  )
}

type CaptureErrors = { serial?: string; form?: string }

// CaptureObservationDialog asks for the exact serial to observe. The serial is
// submitted in the same call that performs the observation: the adapter is never
// asked to infer a target from session state, list order, or a display name.
export function CaptureObservationDialog({ dispatch, dispatchLab, onFeedback, disabled = false }: { dispatch: DispatchIntent; dispatchLab?: DispatchLab; onFeedback: (message: string) => void; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const [serial, setSerial] = useState("")
  const [errors, setErrors] = useState<CaptureErrors>({})
  const [pending, setPending] = useState(false)
  const summary = useRef<HTMLDivElement>(null)
  const messages = Object.values(errors).filter((message): message is string => Boolean(message))

  function reset() {
    setSerial(""); setErrors({}); setPending(false)
  }

  async function submit() {
    const next: CaptureErrors = {}
    if (!serial.trim()) next.serial = "Enter the exact serial of the device to observe."
    if (Object.keys(next).length > 0) {
      setErrors(next)
      summary.current?.focus()
      return
    }
    setPending(true)
    try {
      const intent: ControlPlaneIntent = { type: "captureLabObservation", serial: serial.trim() }
      const mutation = await (dispatchLab?.(intent) ?? Promise.resolve(dispatch(intent)))
      onFeedback(mutation.message)
      if (mutation.ok) {
        setOpen(false)
        reset()
        return
      }
      setErrors({ form: mutation.message })
      summary.current?.focus()
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) reset() }}>
      <DialogTrigger render={<Button size="sm" variant="outline" disabled={disabled} />}><Camera className="size-3.5" aria-hidden="true" />Capture Observation</DialogTrigger>
      <DialogContent className="max-w-lg rounded-none">
        <DialogHeader>
          <DialogTitle>Capture Observation</DialogTitle>
          <DialogDescription>One read-only observation of the serial you name here. It acquires no lease, grants no approval, changes no device state, and registers no device.</DialogDescription>
        </DialogHeader>
        <div ref={summary} tabIndex={-1} role={messages.length > 0 ? "alert" : undefined} className="focus-visible:outline-2 focus-visible:outline-primary">
          {messages.length > 0 ? (
            <div className="border-l-2 border-red-500 bg-red-50 p-3 text-xs text-red-800">
              <p className="font-semibold">The observation was not captured.</p>
              <ul className="mt-1 list-disc pl-4">{messages.map((message) => <li key={message}>{message}</li>)}</ul>
            </div>
          ) : null}
        </div>
        <div className="space-y-4 border-y border-border py-4">
          <div>
            <label htmlFor="lab-capture-serial" className="text-xs font-semibold">Target Serial</label>
            <Input id="lab-capture-serial" value={serial} onChange={(event) => setSerial(event.target.value)} aria-invalid={Boolean(errors.serial)} aria-describedby={errors.serial ? "lab-capture-serial-error" : "lab-capture-serial-description"} className="drift-data mt-1 rounded-none text-xs font-mono" />
            <p id="lab-capture-serial-description" className="mt-1 text-[11px] text-muted-foreground">Retype the serial exactly. A target is never inferred from a list, a default, or a previous session.</p>
            {errors.serial ? <p id="lab-capture-serial-error" className="mt-1 text-[11px] text-red-700">{errors.serial}</p> : null}
          </div>
        </div>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="outline" onClick={() => { setOpen(false); reset() }}>Cancel</Button>
          <Button size="sm" disabled={pending} onClick={() => void submit()}>Capture Observation</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function LabDiagnosticsSheet({ adapter }: { adapter: LabAdapterView }) {
  return (
    <Sheet>
      <SheetTrigger render={<Button size="sm" variant="outline" />}><Stethoscope className="size-3.5" aria-hidden="true" />Adapter Diagnostics</SheetTrigger>
      <SheetContent className="w-full rounded-none sm:max-w-xl">
        <SheetHeader className="border-b border-border">
          <SheetTitle>Device Adapter Diagnostics</SheetTitle>
          <SheetDescription>Bounded adapter metadata. Raw hierarchies, screenshot bytes, and protocol output stay out of this view.</SheetDescription>
        </SheetHeader>
        <div className="space-y-4 p-4">
          <dl className="grid gap-4 text-xs sm:grid-cols-2">
            <LabField label="Mode" detail={adapter.mode === "lab" && adapter.readiness !== "unavailable" ? "Connected Adapter" : "Unavailable"} mono={false} />
            <LabField label="Readiness" detail={readinessLabels[adapter.readiness]} mono={false} />
            <LabField label="Adapter Version" detail={value(adapter.adapterVersion)} mono={false} />
            <LabField label="Platform Tools" detail={value(adapter.platformToolsVersion)} mono={false} />
            <LabField label="Last Observed Serial" detail={value(adapter.lastObservedSerial)} />
            <LabField label="Connection State" detail={value(adapter.connectionState)} mono={false} />
            <LabField label="Connection Type" detail={value(adapter.connectionType)} mono={false} />
            <LabField label="Last Health" detail={value(adapter.lastHealthAt)} />
            <LabField label="Last Observation" detail={value(adapter.lastObservationAt)} />
            <LabField label="Screenshot Hash" detail={value(adapter.lastScreenshotHash)} />
            <LabField label="Hierarchy Summary" detail={value(adapter.lastHierarchySummary)} mono={false} />
            <LabField label="Observation Latency" detail={adapter.observationLatencyMs > 0 ? `${adapter.observationLatencyMs} ms` : placeholder} mono={false} />
            <LabField label="Correlation ID" detail={value(adapter.correlationId)} />
          </dl>
          <div className="border-t border-border pt-4">
            <p className="text-xs font-semibold">Attached Transports Observed</p>
            <p className="mt-1 text-[11px] text-muted-foreground">These serials were observed while resolving a capture target. Observation is not registration: none of them is added to the device registry.</p>
            {adapter.discovered.length === 0
              ? <div className="mt-3"><EmptyState label="No Attached Transports Observed" detail="Capture a named serial to observe the attached transports." /></div>
              : <table className="mt-3 w-full border-collapse text-left text-[11px]">
                <caption className="sr-only">Attached transports observed</caption>
                <thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="py-2">Serial</th><th className="py-2">State</th><th className="py-2">Model</th><th className="py-2">Transport</th></tr></thead>
                <tbody>{adapter.discovered.map((device) => <tr key={device.serial} className="border-b border-border/70"><td className="drift-data py-2 text-[10px]">{device.serial}</td><td className="py-2">{device.state}</td><td className="py-2">{device.model}</td><td className="drift-data py-2 text-[10px]">{device.transportId} · {device.connectionType}</td></tr>)}</tbody>
              </table>}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}

// LabObservationFrame renders the sanitized lab preview in its own frame. It is
// rendered only for a completed observation with a sanitized preview, so a mock
// frame is never presented as live device output.
export function LabObservationFrame({ adapter, height }: { adapter: LabAdapterView; height: number }) {
  if (adapter.readiness === "unavailable" || !adapter.lastObservedSerial || !adapter.lastScreenshotPreviewDataUrl) return null
  const width = Math.round(height * 9 / 16)
  return (
    <div className="mb-4 flex flex-wrap items-start gap-4 border border-border bg-card p-3" aria-label={`${adapter.lastObservedSerial} observation frame`}>
      <div className="border border-border bg-muted" style={{ width, height }}>
        <img src={adapter.lastScreenshotPreviewDataUrl} alt={`Sanitized screenshot preview for ${adapter.lastObservedSerial}`} className="size-full object-contain" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="drift-data text-sm font-semibold">{adapter.lastObservedSerial}</span>
          <StatusBadge label="Read Only" tone="info" />
          <StatusBadge label="Sanitized Preview" tone="healthy" />
        </div>
        <p className="text-[11px] leading-5 text-muted-foreground">This frame shows the sanitized observation preview for the serial that was explicitly named in the capture call. No placeholder frame on this page represents live device output.</p>
        <p className="text-[11px] leading-5 text-muted-foreground">
          Browse fleet media and retention in{" "}
          <a href="#artifacts/media" className="font-medium underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-primary">
            Artifacts · Media
          </a>
          .
        </p>
        <dl className="grid gap-3 text-[11px] sm:grid-cols-2">
          <LabField label="Serial" detail={adapter.lastObservedSerial} />
          <LabField label="Captured" detail={value(adapter.lastObservationAt)} />
          <LabField label="Screenshot Hash" detail={value(adapter.lastScreenshotHash)} />
          <LabField label="Hierarchy" detail={value(adapter.lastHierarchySummary)} mono={false} />
        </dl>
      </div>
    </div>
  )
}

// LabAdapterIndicator is the compact adapter signal for surfaces that are not
// the control workspace. It keeps the observation boundary visible ("observed"
// is not "registered") in one line and exposes the detail on demand, so the
// adapter never spends permanent header space. It dispatches nothing.
export function LabAdapterIndicator({ adapter }: { adapter: LabAdapterView }) {
  return (
    <div role="group" aria-label="Device Adapter" className="flex flex-wrap items-center gap-2">
      <span className="text-[11px] font-medium text-muted-foreground">Device Adapter</span>
      <LabModeBadges adapter={adapter} />
      <span className="text-[11px] text-muted-foreground">{`${adapter.discovered.length} observed · 0 registered`}</span>
      <LabDiagnosticsSheet adapter={adapter} />
    </div>
  )
}

function RuntimeSpoolSheet({
  runtimeConnection,
  spoolHealth,
  indeterminateActions,
  dispatch,
  onFeedback,
}: {
  runtimeConnection: RuntimeConnectionView
  spoolHealth: SpoolHealthView
  indeterminateActions: readonly IndeterminateActionView[]
  dispatch: DispatchIntent
  onFeedback: (message: string) => void
}) {
  const blockedSequence = spoolHealth.blockedSequences[0] ?? 0
  const hasBlockedSequence = blockedSequence > 0
  return (
    <Sheet>
      <SheetTrigger render={<Button size="sm" variant="outline" />}><Activity className="size-3.5" aria-hidden="true" />Runtime And Spool</SheetTrigger>
      <SheetContent className="w-full rounded-none sm:max-w-xl">
        <SheetHeader className="border-b border-border">
          <SheetTitle>Runtime Connection And Spool</SheetTitle>
          <SheetDescription>Connection state and spool fence tokens are observations. They are never control-plane leases, and blocked items never auto-replay.</SheetDescription>
        </SheetHeader>
        <div className="space-y-4 p-4">
          <div className="flex flex-wrap gap-2">
            <StatusBadge label={runtimeLabels[runtimeConnection.state]} tone={runtimeTones[runtimeConnection.state]} />
            <StatusBadge label={runtimeConnection.helperTokenIsLease ? "Lease (invalid)" : "Helper Token Not A Lease"} tone="info" />
            <StatusBadge label={spoolHealth.fenceIsLease ? "Lease (invalid)" : "Fence Not A Lease"} tone="info" />
          </div>
          <dl className="grid gap-3 text-[11px] sm:grid-cols-2">
            <LabField label="Transport ID" detail={value(runtimeConnection.transportId)} />
            <LabField label="Protocol" detail={value(runtimeConnection.protocol)} mono={false} />
            <LabField label="Disconnect Reason" detail={value(runtimeConnection.disconnectedReason) || "—"} mono={false} />
            <LabField label="Pending Indeterminate" detail={`${runtimeConnection.pendingIndeterminate}`} mono={false} />
            <LabField label="Spool Pending" detail={`${spoolHealth.pending}`} mono={false} />
            <LabField label="Spool Blocked" detail={`${spoolHealth.blocked}`} mono={false} />
            <LabField label="Blocked Sequences" detail={spoolHealth.blockedSequences.length > 0 ? spoolHealth.blockedSequences.join(", ") : "—"} mono={false} />
            <LabField label="Spool Max Size" detail={`${spoolHealth.maxSize}`} mono={false} />
            <LabField label="Retention Ms" detail={`${spoolHealth.retentionMs}`} mono={false} />
            <LabField label="Fence Token" detail={`${spoolHealth.fenceToken}`} />
            <LabField label="Exhausted" detail={spoolHealth.exhausted ? "Yes" : "No"} mono={false} />
          </dl>
          <div className="flex flex-wrap gap-2 border-t border-border pt-4">
            <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "simulateRuntimeDisconnect", reason: "Operator disconnect" }, onFeedback)}>Disconnect Runtime</Button>
            <Button size="sm" variant="outline" disabled={runtimeConnection.state === "connected"} onClick={() => void reportDispatch(dispatch, { type: "beginRuntimeReconnect" }, onFeedback)}>Begin Reconnect</Button>
            <Button size="sm" variant="outline" disabled={runtimeConnection.state !== "reconnecting"} onClick={() => void reportDispatch(dispatch, { type: "completeRuntimeReconnect", transportId: runtimeConnection.transportId || "transport-0", protocol: runtimeConnection.protocol || "adb" }, onFeedback)}>Complete Reconnect</Button>
            {usesMockControlPlane() ? (
              <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "enqueueMockSpoolItem", kind: "observation", risk: "low", idempotencyKey: `spool-${Date.now()}` }, onFeedback)}>Enqueue Spool Item</Button>
            ) : null}
            <AlertDialog>
              <AlertDialogTrigger render={<Button size="sm" variant="outline" disabled={!hasBlockedSequence || runtimeConnection.state !== "connected"} />}>Confirm Spool Replay</AlertDialogTrigger>
              <AlertDialogContent
                title="Confirm Spool Replay?"
                description={`Blocked spool items never auto-replay. Confirming records operator intent for sequence ${blockedSequence || "—"}; it does not blind-retry indeterminate actions.`}
                confirmLabel="Confirm Replay"
                onConfirm={() => void reportDispatch(dispatch, { type: "confirmSpoolReplay", sequence: blockedSequence, confirm: true }, onFeedback)}
              />
            </AlertDialog>
          </div>
          <div className="border-t border-border pt-4">
            <p className="text-xs font-semibold">Indeterminate Actions</p>
            <p className="mt-1 text-[11px] text-muted-foreground">Each item requires operator confirmation. Blind replay is refused.</p>
            {indeterminateActions.length === 0
              ? <div className="mt-3"><EmptyState label="No Indeterminate Actions" detail="Disconnected or changed-transport outcomes appear here until an operator confirms them." /></div>
              : <ul className="mt-3 space-y-2">
                {indeterminateActions.map((action) => (
                  <li key={action.actionId} className="border border-border p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="drift-data text-[10px]">{action.actionId}</span>
                      <StatusBadge label={action.requiresOperatorConfirmation ? "Confirmation Required" : "Resolved"} tone="attention" />
                      <StatusBadge label={action.risk} tone="neutral" />
                    </div>
                    <p className="mt-2 text-[11px] leading-5 text-muted-foreground">{action.summary}</p>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <AlertDialog>
                        <AlertDialogTrigger render={<Button size="sm" />}><ShieldCheck className="size-3.5" aria-hidden="true" />Confirm Outcome</AlertDialogTrigger>
                        <AlertDialogContent
                          title="Confirm Indeterminate Outcome?"
                          description="Confirmation records operator judgment after a fresh observation path. It never blind-replays the original action."
                          confirmLabel="Confirm Without Replay"
                          onConfirm={() => void reportDispatch(dispatch, { type: "confirmIndeterminateAction", actionId: action.actionId, confirm: true, resolution: "operator_confirmed" }, onFeedback)}
                        />
                      </AlertDialog>
                      <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "confirmIndeterminateAction", actionId: action.actionId, confirm: false, resolution: "fresh_observation" }, onFeedback)}>Drop Without Replay</Button>
                    </div>
                  </li>
                ))}
              </ul>}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
