import { useRef, useState } from "react"
import { Activity, Camera, Eraser, ScanSearch, ShieldCheck, Stethoscope, TriangleAlert } from "lucide-react"
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
  LabRegistrationView,
  MutationResult,
  ProvisioningReadinessView,
  RuntimeConnectionState,
  RuntimeConnectionView,
  SpoolHealthView,
} from "@/lib/domain/control-plane"
import { usesMockControlPlane } from "@/lib/api/connect-json"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { EmptyState, FailureBadge, Panel, StatusBadge, type StatusTone } from "./shared"

type DispatchLab = (intent: ControlPlaneIntent) => Promise<MutationResult>

async function runLabIntent(intent: ControlPlaneIntent, dispatch: DispatchIntent, dispatchLab: DispatchLab | undefined, onFeedback: (message: string) => void) {
  const mutation = await (dispatchLab?.(intent) ?? Promise.resolve(dispatch(intent)))
  onFeedback(mutation.message)
}

const readinessTones: Record<LabReadiness, StatusTone> = { ready: "healthy", blocked: "attention", indeterminate: "attention", unavailable: "neutral" }
const readinessLabels: Record<LabReadiness, string> = { ready: "Ready", blocked: "Blocked", indeterminate: "Indeterminate", unavailable: "Unavailable" }
const runtimeLabels: Record<RuntimeConnectionState, string> = { connected: "Connected", reconnecting: "Reconnecting", disconnected: "Disconnected" }
const runtimeTones: Record<RuntimeConnectionState, StatusTone> = { connected: "healthy", reconnecting: "attention", disconnected: "danger" }

const placeholder = "—"
const value = (input: string | undefined) => (input && input.length > 0 ? input : placeholder)

export function LabModeBadges({ adapter }: { adapter: LabAdapterView }) {
  return (
    <>
      <StatusBadge label={adapter.mode === "lab" ? (adapter.readiness === "unavailable" ? "Unavailable" : "Connected") : "Simulator"} tone={adapter.mode === "lab" ? (adapter.readiness === "unavailable" ? "neutral" : "healthy") : "info"} />
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
  provisioningReadiness: ProvisioningReadinessView | null
  indeterminateActions: readonly IndeterminateActionView[]
  labRegistration: LabRegistrationView | null
  dispatch: DispatchIntent
  dispatchLab?: DispatchLab
  onFeedback: (message: string) => void
  notice?: string
}

// LabStatusStrip is the compact lab adapter surface for the control workspace.
// It surfaces observation, runtime, spool, and provisioning state only; it
// dispatches no device input and never presents lab state as a mock frame.
export function LabStatusStrip({
  adapter,
  runtimeConnection,
  spoolHealth,
  provisioningReadiness,
  indeterminateActions,
  labRegistration,
  dispatch,
  dispatchLab,
  onFeedback,
  notice = "",
}: LabRuntimeProps) {
  const confirmed = adapter.confirmedSerial.length > 0
  const canCapture = confirmed && adapter.readiness === "ready"
  return (
    <section className="mt-6 border border-border bg-muted/20" aria-label="Device Adapter Status">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <span className="mr-auto flex items-center gap-2 text-xs font-semibold"><Activity className="size-3.5 text-primary" aria-hidden="true" />Device Adapter</span>
        <LabModeBadges adapter={adapter} />
        <StatusBadge label={runtimeLabels[runtimeConnection.state]} tone={runtimeTones[runtimeConnection.state]} />
        <StatusBadge label={spoolHealth.exhausted ? "Spool Exhausted" : spoolHealth.blocked > 0 ? "Spool Blocked" : "Spool Clear"} tone={spoolHealth.exhausted || spoolHealth.blocked > 0 ? "attention" : "neutral"} />
      </div>
      <dl className="grid gap-3 px-3 py-3 text-[11px] sm:grid-cols-2 xl:grid-cols-4">
        <LabField label="Confirmed Target" detail={confirmed ? adapter.confirmedDisplayName : "No target confirmed"} mono={false} />
        <LabField label="Stable Identity" detail={value(adapter.stableIdentity)} />
        <LabField label="ADB Transport" detail={`${value(adapter.connectionState)} · ${value(adapter.connectionType)}`} />
        <LabField label="Transport ID" detail={value(adapter.transportId)} />
        <LabField label="Runtime Connection" detail={runtimeLabels[runtimeConnection.state]} mono={false} />
        <LabField label="Spool Health" detail={`Pending ${spoolHealth.pending} · Blocked ${spoolHealth.blocked} · Max ${spoolHealth.maxSize}`} mono={false} />
        <LabField label="Spool Fence" detail={`${spoolHealth.fenceToken} (observation, not lease)`} />
        <LabField label="Indeterminate Actions" detail={`${indeterminateActions.length} requiring confirmation`} mono={false} />
      </dl>
      <div className="flex flex-wrap items-center gap-2 border-t border-border px-3 py-2">
        <Button size="sm" variant="outline" onClick={() => void runLabIntent({ type: "discoverLabDevices" }, dispatch, dispatchLab, onFeedback)}><ScanSearch className="size-3.5" aria-hidden="true" />Discover Devices</Button>
        <ConfirmLabTargetDialog adapter={adapter} dispatch={dispatch} dispatchLab={dispatchLab} onFeedback={onFeedback} />
        {canCapture ? <Button size="sm" variant="outline" onClick={() => void runLabIntent({ type: "captureLabObservation", serial: adapter.confirmedSerial }, dispatch, dispatchLab, onFeedback)}><Camera className="size-3.5" aria-hidden="true" />Capture Observation</Button> : null}
        {confirmed ? <Button size="sm" variant="outline" onClick={() => void runLabIntent({ type: "clearLabTarget" }, dispatch, dispatchLab, onFeedback)}><Eraser className="size-3.5" aria-hidden="true" />Clear Target</Button> : null}
        {adapter.mode === "mock" && confirmed ? <Button size="sm" variant="outline" onClick={() => void runLabIntent({ type: "simulateLabCaptureFailure" }, dispatch, dispatchLab, onFeedback)}><TriangleAlert className="size-3.5" aria-hidden="true" />Simulate Indeterminate</Button> : null}
        <LabDiagnosticsSheet adapter={adapter} />
        <RuntimeSpoolSheet runtimeConnection={runtimeConnection} spoolHealth={spoolHealth} indeterminateActions={indeterminateActions} dispatch={dispatch} onFeedback={onFeedback} />
        <LabProvisioningSheet adapter={adapter} provisioningReadiness={provisioningReadiness} labRegistration={labRegistration} dispatch={dispatch} onFeedback={onFeedback} />
      </div>
      <p aria-live="polite" className="border-t border-border px-3 py-2 text-[11px] leading-5 text-muted-foreground">{notice || "Adapter actions are read-only. Discovery lists candidates; Approval and Registration stay separate; spool fence tokens are observations, not leases."}</p>
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

type ConfirmErrors = { serial?: string; displayName?: string; confirmationText?: string; reason?: string; form?: string }

export function ConfirmLabTargetDialog({ adapter, dispatch, dispatchLab, onFeedback }: { adapter: LabAdapterView; dispatch: DispatchIntent; dispatchLab?: DispatchLab; onFeedback: (message: string) => void }) {
  const [open, setOpen] = useState(false)
  const [serial, setSerial] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [confirmationText, setConfirmationText] = useState("")
  const [reason, setReason] = useState("")
  const [errors, setErrors] = useState<ConfirmErrors>({})
  const [pending, setPending] = useState(false)
  const summary = useRef<HTMLDivElement>(null)
  const requiresConfirmationText = adapter.discovered.length > 1
  const messages = Object.values(errors).filter((message): message is string => Boolean(message))

  function reset() {
    setSerial(""); setDisplayName(""); setConfirmationText(""); setReason(""); setErrors({}); setPending(false)
  }

  async function submit() {
    const next: ConfirmErrors = {}
    if (!serial.trim()) next.serial = "Choose the serial of the connected device to confirm."
    if (!displayName.trim()) next.displayName = "Enter an operator-facing display name."
    if (requiresConfirmationText && confirmationText.trim() !== serial.trim()) next.confirmationText = "Confirmation text must match the selected serial exactly."
    if (!reason.trim()) next.reason = "Record why this target is being confirmed."
    if (Object.keys(next).length === 0) {
      setPending(true)
      // With a single candidate the confirmation field is hidden, so the serial
      // itself is submitted: the service accepts the serial or the CONFIRM
      // literal, and an empty string is neither.
      const submittedConfirmation = requiresConfirmationText ? confirmationText.trim() : serial.trim()
      try {
        const intent: ControlPlaneIntent = {
          type: "confirmLabTarget",
          serial: serial.trim(),
          displayName: displayName.trim(),
          confirmationText: submittedConfirmation,
          reason: reason.trim(),
        }
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
      return
    }
    setErrors(next)
    summary.current?.focus()
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) reset() }}>
      <DialogTrigger render={<Button size="sm" variant="outline" disabled={adapter.discovered.length === 0} />}><ShieldCheck className="size-3.5" aria-hidden="true" />Confirm Target</DialogTrigger>
      <DialogContent className="max-w-lg rounded-none">
        <DialogHeader>
          <DialogTitle>Confirm Target</DialogTitle>
          <DialogDescription>Confirmation authorizes read-only observation of one serial. It acquires no lease, grants no approval, and registers no device.</DialogDescription>
        </DialogHeader>
        <div ref={summary} tabIndex={-1} role={messages.length > 0 ? "alert" : undefined} className="focus-visible:outline-2 focus-visible:outline-primary">
          {messages.length > 0 ? (
            <div className="border-l-2 border-red-500 bg-red-50 p-3 text-xs text-red-800">
              <p className="font-semibold">Confirmation was not recorded.</p>
              <ul className="mt-1 list-disc pl-4">{messages.map((message) => <li key={message}>{message}</li>)}</ul>
            </div>
          ) : null}
        </div>
        <div className="space-y-4 border-y border-border py-4">
          <div>
            <label htmlFor="lab-serial" className="text-xs font-semibold">Serial</label>
            <select id="lab-serial" value={serial} onChange={(event) => setSerial(event.target.value)} aria-invalid={Boolean(errors.serial)} aria-describedby={errors.serial ? "lab-serial-error" : undefined} className="drift-data mt-1 block h-9 w-full border border-input bg-background px-2 text-[11px]">
              <option value="">Select a discovered serial</option>
              {adapter.discovered.map((device) => <option key={device.serial} value={device.serial}>{device.serial} · {device.model} · {device.state}</option>)}
            </select>
            {errors.serial ? <p id="lab-serial-error" className="mt-1 text-[11px] text-red-700">{errors.serial}</p> : null}
          </div>
          <LabTextField id="lab-display-name" label="Display Name" description="Shown to operators; it does not become a device identity." value={displayName} onChange={setDisplayName} error={errors.displayName} />
          {requiresConfirmationText ? <LabTextField id="lab-confirmation-text" label="Confirmation Text" description="Retype the serial exactly, because more than one serial was discovered." value={confirmationText} onChange={setConfirmationText} error={errors.confirmationText} mono /> : null}
          <LabTextField id="lab-reason" label="Reason" description="Recorded in the audit ledger with the confirmation event." value={reason} onChange={setReason} error={errors.reason} />
        </div>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="outline" onClick={() => { setOpen(false); reset() }}>Cancel</Button>
          <Button size="sm" disabled={pending} onClick={submit}>Confirm Target</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function LabTextField({ id, label, description, value: fieldValue, onChange, error, mono = false }: { id: string; label: string; description: string; value: string; onChange: (next: string) => void; error?: string; mono?: boolean }) {
  return (
    <div>
      <label htmlFor={id} className="text-xs font-semibold">{label}</label>
      <Input id={id} value={fieldValue} onChange={(event) => onChange(event.target.value)} aria-invalid={Boolean(error)} aria-describedby={error ? `${id}-error` : `${id}-description`} className={`mt-1 rounded-none text-xs ${mono ? "font-mono" : ""}`} />
      <p id={`${id}-description`} className="mt-1 text-[11px] text-muted-foreground">{description}</p>
      {error ? <p id={`${id}-error`} className="mt-1 text-[11px] text-red-700">{error}</p> : null}
    </div>
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
            <LabField label="Mode" detail={adapter.mode === "lab" ? "Connected Adapter" : "Simulator"} mono={false} />
            <LabField label="Readiness" detail={readinessLabels[adapter.readiness]} mono={false} />
            <LabField label="Adapter Version" detail={value(adapter.adapterVersion)} mono={false} />
            <LabField label="Platform Tools" detail={value(adapter.platformToolsVersion)} mono={false} />
            <LabField label="Confirmed Serial" detail={value(adapter.confirmedSerial)} />
            <LabField label="Confirmed Display Name" detail={value(adapter.confirmedDisplayName)} mono={false} />
            <LabField label="Stable Identity" detail={value(adapter.stableIdentity)} />
            <LabField label="Transport ID" detail={value(adapter.transportId)} />
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
            <p className="text-xs font-semibold">Discovered Serials</p>
            <p className="mt-1 text-[11px] text-muted-foreground">Discovery is not registration. These serials are never added to the device registry.</p>
            {adapter.discovered.length === 0
              ? <div className="mt-3"><EmptyState label="No Discovered Serials" detail="Run discovery from the control workspace to list candidate serials." /></div>
              : <table className="mt-3 w-full border-collapse text-left text-[11px]">
                <caption className="sr-only">Discovered serials</caption>
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
// rendered only for a confirmed, ready target with a sanitized preview so a mock
// frame is never presented as live device output.
export function LabObservationFrame({ adapter, height }: { adapter: LabAdapterView; height: number }) {
  if (adapter.readiness !== "ready" || !adapter.confirmedSerial || !adapter.lastScreenshotPreviewDataUrl) return null
  const width = Math.round(height * 9 / 16)
  return (
    <div className="mb-4 flex flex-wrap items-start gap-4 border border-border bg-card p-3" aria-label={`${adapter.confirmedDisplayName} observation frame`}>
      <div className="border border-border bg-muted" style={{ width, height }}>
        <img src={adapter.lastScreenshotPreviewDataUrl} alt={`Sanitized screenshot preview for ${adapter.confirmedDisplayName}`} className="size-full object-contain" />
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-semibold">{adapter.confirmedDisplayName}</span>
          <StatusBadge label="Read Only" tone="info" />
          <StatusBadge label="Sanitized Preview" tone="healthy" />
        </div>
        <p className="text-[11px] leading-5 text-muted-foreground">This frame shows the sanitized observation preview for the confirmed target only. No placeholder frame on this page represents live device output.</p>
        <p className="text-[11px] leading-5 text-muted-foreground">
          Browse fleet media and retention in{" "}
          <a href="#artifacts/media" className="font-medium underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-primary">
            Artifacts · Media
          </a>
          .
        </p>
        <dl className="grid gap-3 text-[11px] sm:grid-cols-2">
          <LabField label="Serial" detail={adapter.confirmedSerial} />
          <LabField label="Captured" detail={value(adapter.lastObservationAt)} />
          <LabField label="Screenshot Hash" detail={value(adapter.lastScreenshotHash)} />
          <LabField label="Hierarchy" detail={value(adapter.lastHierarchySummary)} mono={false} />
        </dl>
      </div>
    </div>
  )
}

// LabAdapterStatusPanel keeps lab adapter state visible in the device registry
// without letting a discovered serial appear as a registered device.
export function LabAdapterStatusPanel({ adapter }: { adapter: LabAdapterView }) {
  const confirmed = adapter.confirmedSerial.length > 0
  return (
    <Panel
      title="Device Adapter Status"
      description="Read-only observation boundary for one confirmed serial. Discovered serials are never registered as devices and never appear in the registry table below."
      action={<div className="flex flex-wrap items-center gap-2"><LabModeBadges adapter={adapter} /></div>}
      className="mb-6"
    >
      <dl className="grid gap-4 text-[11px] sm:grid-cols-2 xl:grid-cols-4">
        <LabField label="Adapter Availability" detail={adapter.readiness === "unavailable" ? "Unavailable" : "Available"} mono={false} />
        <LabField label="Confirmed Target" detail={confirmed ? `${adapter.confirmedDisplayName} · ${adapter.confirmedSerial}` : "No target confirmed"} mono={false} />
        <LabField label="Authorization State" detail={confirmed ? value(adapter.connectionState) : "Operator confirmation required"} mono={false} />
        <LabField label="Transport" detail={confirmed ? `${value(adapter.transportId)} · ${value(adapter.connectionType)}` : placeholder} />
        <LabField label="Last Health" detail={value(adapter.lastHealthAt)} />
        <LabField label="Last Screenshot" detail={adapter.lastScreenshotHash ? `${value(adapter.lastObservationAt)} · ${adapter.lastScreenshotHash}` : "No screenshot captured"} />
        <LabField label="Last UI-Tree" detail={adapter.lastHierarchySummary ? adapter.lastHierarchySummary : "No UI-tree captured"} mono={false} />
        <LabField label="Versions" detail={`${value(adapter.adapterVersion)} · ${value(adapter.platformToolsVersion)}`} mono={false} />
        <LabField label="Discovered Serials" detail={`${adapter.discovered.length} listed, 0 registered`} mono={false} />
        <LabField label="Outcome" detail={adapter.indeterminate ? "Indeterminate outcome recorded" : adapter.failureClass ? adapter.failureClass.replaceAll("_", " ") : "No failure recorded"} mono={false} />
      </dl>
    </Panel>
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
            {usesMockControlPlane() ? (
              <>
                <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "simulateRuntimeDisconnect", reason: "Operator disconnect" }, onFeedback)}>Simulate Disconnect</Button>
                <Button size="sm" variant="outline" disabled={runtimeConnection.state === "connected"} onClick={() => void reportDispatch(dispatch, { type: "beginRuntimeReconnect" }, onFeedback)}>Begin Reconnect</Button>
                <Button size="sm" variant="outline" disabled={runtimeConnection.state !== "reconnecting"} onClick={() => void reportDispatch(dispatch, { type: "completeRuntimeReconnect", transportId: runtimeConnection.transportId || "transport-0", protocol: runtimeConnection.protocol || "adb" }, onFeedback)}>Complete Reconnect</Button>
                <Button size="sm" variant="outline" onClick={() => void reportDispatch(dispatch, { type: "enqueueMockSpoolItem", kind: "observation", risk: "low", idempotencyKey: `spool-${Date.now()}` }, onFeedback)}>Enqueue Spool Item</Button>
              </>
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

function LabProvisioningSheet({
  adapter,
  provisioningReadiness,
  labRegistration,
  dispatch,
  onFeedback,
}: {
  adapter: LabAdapterView
  provisioningReadiness: ProvisioningReadinessView | null
  labRegistration: LabRegistrationView | null
  dispatch: DispatchIntent
  onFeedback: (message: string) => void
}) {
  const serial = adapter.confirmedSerial || provisioningReadiness?.serial || ""
  const canVerify = serial.length > 0
  const approved = Boolean(labRegistration?.approved)
  const registered = labRegistration?.state === "registered"
  return (
    <Sheet>
      <SheetTrigger render={<Button size="sm" variant="outline" />}><ShieldCheck className="size-3.5" aria-hidden="true" />Device Provisioning</SheetTrigger>
      <SheetContent className="w-full rounded-none sm:max-w-xl">
        <SheetHeader className="border-b border-border">
          <SheetTitle>Device Provisioning And Registration</SheetTitle>
          <SheetDescription>Discovery, Approval, Provisioning, and Registration stay independent. Registration is never implied by Discovery or Approval.</SheetDescription>
        </SheetHeader>
        <div className="space-y-4 p-4">
          <div className="flex flex-wrap gap-2" aria-label="Provisioning stage badges">
            <StatusBadge label="Discovery" tone={adapter.discovered.length > 0 ? "healthy" : "neutral"} />
            <StatusBadge label="Approval" tone={approved ? "healthy" : "attention"} />
            <StatusBadge label="Provisioning" tone={provisioningReadiness?.ready ? "healthy" : "attention"} />
            <StatusBadge label="Registration" tone={registered ? "info" : "neutral"} />
            {adapter.mode === "mock" ? <StatusBadge label="Simulator" tone="info" /> : <StatusBadge label={registered ? "Registered" : "Awaiting Approval"} tone={registered ? "info" : "attention"} />}
          </div>
          {provisioningReadiness ? (
            <dl className="grid gap-3 text-[11px] sm:grid-cols-2">
              <LabField label="Serial" detail={provisioningReadiness.serial} />
              <LabField label="Transport ID" detail={provisioningReadiness.transportId} />
              <LabField label="Pairing Authorized" detail={provisioningReadiness.pairingAuthorized ? "Yes" : "No"} mono={false} />
              <LabField label="ADB Ownership" detail={provisioningReadiness.adbServerOwned ? "Yes" : "No"} mono={false} />
              <LabField label="Platform-Tools" detail={provisioningReadiness.platformToolsCompatible ? "Compatible" : "Missing"} mono={false} />
              <LabField label="Port Policy" detail={provisioningReadiness.portPolicyAllowed ? "Allowed" : "Blocked"} mono={false} />
              <LabField label="Rollback Ready" detail={provisioningReadiness.rollbackReady ? "Yes" : "No"} mono={false} />
              <LabField label="State" detail={provisioningReadiness.state.replaceAll("_", " ")} mono={false} />
              <LabField label="Notes" detail={provisioningReadiness.notes.join(" · ") || "—"} mono={false} />
              {provisioningReadiness.errorCode ? <LabField label="Error Code" detail={provisioningReadiness.errorCode.replaceAll("_", " ")} mono={false} /> : null}
            </dl>
          ) : (
            <EmptyState label="No Provisioning Evidence" detail="Confirm a target, then verify Provisioning readiness evidence." />
          )}
          {labRegistration ? (
            <div className="border border-border bg-muted/30 p-3 text-[11px] leading-5">
              <p className="font-semibold">Registration Record</p>
              <p className="mt-1 text-muted-foreground">{labRegistration.displayName} · {labRegistration.serial} · {labRegistration.state.replaceAll("_", " ")}</p>
              <p className="mt-1 text-muted-foreground">{labRegistration.mockLabeled ? "Simulator registration — fleet registry unchanged." : "Registration recorded."}</p>
            </div>
          ) : null}
          <div className="flex flex-wrap gap-2 border-t border-border pt-4">
            <Button
              size="sm"
              disabled={!canVerify}
              onClick={() => void reportDispatch(dispatch, {
                type: "verifyLabProvisioning",
                serial,
                transportId: adapter.transportId || "3",
                endpointHost: "127.0.0.1",
                endpointPort: adapter.connectionType === "tcp" ? 5555 : 0,
                connectionType: adapter.connectionType || "usb",
                pairingAuthorized: true,
                adbServerOwned: true,
                platformToolsCompatible: true,
                portPolicyAllowed: true,
                rollbackReady: true,
                operatorAuthorized: true,
              }, onFeedback)}
            >
              Verify Provisioning
            </Button>
            <AlertDialog>
              <AlertDialogTrigger render={<Button size="sm" variant="outline" disabled={!provisioningReadiness?.ready || registered} />}>Approve Provisioning</AlertDialogTrigger>
              <AlertDialogContent
                title="Approve Provisioning?"
                description="Approval is separate from Registration. This Approval does not register a device."
                confirmLabel="Grant Approval"
                onConfirm={() => void reportDispatch(dispatch, { type: "approveLabProvisioning", serial, reason: "Approval after Provisioning verification" }, onFeedback)}
              />
            </AlertDialog>
            <AlertDialog>
              <AlertDialogTrigger render={<Button size="sm" variant="secondary" disabled={!approved || registered} />}>Register Device</AlertDialogTrigger>
              <AlertDialogContent
                title="Register Device?"
                description="This creates a registration record only. It does not mutate the fleet registry until the control plane accepts it."
                confirmLabel="Confirm Registration"
                onConfirm={() => void reportDispatch(dispatch, { type: "registerLabDevice", serial, displayName: adapter.confirmedDisplayName || serial, approved: true }, onFeedback)}
              />
            </AlertDialog>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
