import { useState } from "react"
import { Ban, FileText, ListChecks, ShieldCheck } from "lucide-react"
import { AlertDialog, AlertDialogContent, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DispatchIntent, RunState } from "@/lib/domain/control-plane"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { DataTablePagination, EmptyState, FailureBadge, FieldLabel, OperatorNotice, PageIntro, Panel, StatusBadge, type StatusTone } from "./shared"
import { textForDevice } from "./page-utils"

export function RunsPage({ snapshot, dispatch, view = "active", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  const [feedback, setFeedback] = useState("")
  const publishedWorkflows = snapshot.workflows.filter((workflow) => workflow.state === "published" || Boolean(workflow.publishedVersionId))
  const [workflowId, setWorkflowId] = useState(publishedWorkflows[0]?.id ?? "")
  const [selectedDeviceIds, setSelectedDeviceIds] = useState<string[]>(snapshot.devices[0] ? [snapshot.devices[0].id] : [])
  const selectedRun = snapshot.runs.find((run) => run.id === selectedRunId)
  const activeRuns = snapshot.runs.filter((run) => !isTerminal(run.state))
  const historicalRuns = snapshot.runs.filter((run) => isTerminal(run.state))
  const failedRuns = snapshot.runs.filter((run) => run.state === "failed" || run.state === "paused" || run.failureClass)
  function inspect(id: string) { setSelectedRunId(id) }
  function cancel(id: string) { void reportDispatch(dispatch, { type: "cancelRun", runId: id }, setFeedback) }
  function toggleDevice(deviceId: string) {
    setSelectedDeviceIds((current) => current.includes(deviceId) ? current.filter((id) => id !== deviceId) : [...current, deviceId])
  }
  function startRun() {
    void reportDispatch(dispatch, { type: "startWorkflowRun", workflowId, deviceIds: selectedDeviceIds, confirmed: true }, setFeedback)
  }
  return <>
    <PageIntro eyebrow="EXECUTION / RUNS" title="Runs and Targets" description="Inspect parent runs and independent per-device outcomes without conflating target state with aggregate state." actions={<StatusBadge label="Independent Targets" tone="info" />} />
    <OperatorNotice>Parent runs and per-device targets are independent. Starting a run requires a published workflow, explicit device selection, and confirmation. Cancel requests the control plane; blocked, stale, or unauthorized targets stay unexecuted.</OperatorNotice>
    <form className="mt-6 grid gap-3 border border-border p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto]" onSubmit={(event) => event.preventDefault()}>
      <div>
        <FieldLabel htmlFor="start-run-workflow">Published Workflow</FieldLabel>
        <select id="start-run-workflow" value={workflowId} onChange={(event) => setWorkflowId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs">
          {publishedWorkflows.length === 0 ? <option value="">No Published Workflows</option> : null}
          {publishedWorkflows.map((workflow) => <option key={workflow.id} value={workflow.id}>{workflow.name} · v{workflow.version}</option>)}
        </select>
      </div>
      <fieldset className="min-w-0">
        <legend className="text-[10px] font-semibold uppercase tracking-[.08em] text-muted-foreground">Devices</legend>
        <div className="mt-1 max-h-28 overflow-y-auto border border-border p-2">
          {snapshot.devices.length === 0 ? <p className="text-xs text-muted-foreground">No Registered Devices</p> : snapshot.devices.map((device) => (
            <label key={device.id} className="flex items-center gap-2 py-1 text-xs">
              <input type="checkbox" checked={selectedDeviceIds.includes(device.id)} onChange={() => toggleDevice(device.id)} />
              {device.displayName}
            </label>
          ))}
        </div>
      </fieldset>
      <AlertDialog>
        <AlertDialogTrigger render={<Button type="button" size="sm" variant="outline" className="self-end" disabled={!workflowId || selectedDeviceIds.length === 0}>Start Run</Button>} />
        <AlertDialogContent
          title="Start Workflow Run?"
          description="Creates a parent run for the selected devices only. Each device keeps an independent target, lease, fencing token, and outcome. Actions are not replayed after disconnect or timeout."
          confirmLabel="Confirm Start Run"
          onConfirm={startRun}
        />
      </AlertDialog>
    </form>
    <Tabs value={view} onValueChange={onViewChange} className="mt-6"><TabsList className="rounded-none border border-border bg-background p-0" aria-label="Run views"><TabsTrigger value="active" className="rounded-none">Active</TabsTrigger><TabsTrigger value="history" className="rounded-none">History</TabsTrigger><TabsTrigger value="failed" className="rounded-none">Failed / Indeterminate</TabsTrigger></TabsList>
      <TabsContent value="active" className="mt-6"><Panel title="Active Runs" description="Admission and aggregate state are visible here; inspect a row for targets, steps, evidence, and events."><RunTable runs={activeRuns} onInspect={inspect} /></Panel></TabsContent>
      <TabsContent value="history" className="mt-6"><Panel title="Run History" description="Completed, failed, and cancelled parent runs retain their immutable target snapshot."><RunTable runs={historicalRuns} onInspect={inspect} /></Panel></TabsContent>
      <TabsContent value="failed" className="mt-6"><Panel title="Failed or Indeterminate Runs" description="Failure classification is kept alongside independent target results, not hidden by aggregate status."><RunTable runs={failedRuns} onInspect={inspect} /></Panel></TabsContent>
    </Tabs>
    <div className="mt-6 grid gap-4 md:grid-cols-3"><Posture icon={ShieldCheck} label="Admission facts" detail="Approval, policy, lease, fencing, and observation are independent." /><Posture icon={ListChecks} label="Target independence" detail="Each target retains retries, cleanup, and failure facts." /><Posture icon={FileText} label="Operator Boundary" detail="Cancel does not replay blocked or stale target actions." /></div>
    <p aria-live="polite" className="mt-6 border-l-2 border-primary bg-secondary/60 p-3 text-xs text-muted-foreground">{feedback || "Run feedback appears here. Independent target outcomes stay visible after cancel."}</p>
    <Sheet open={selectedRunId !== null} onOpenChange={(open) => { if (!open) setSelectedRunId(null) }}><SheetContent className="w-full rounded-none sm:max-w-2xl"><SheetHeader><SheetTitle>{selectedRun?.workflowName ?? "Run Detail"}</SheetTitle><SheetDescription>Parent summary, independent targets, steps, evidence, and events are separated below.</SheetDescription></SheetHeader>{selectedRun ? <Tabs defaultValue="summary" className="flex min-h-0 flex-1 flex-col px-4 pb-4"><TabsList className="h-auto flex-wrap rounded-none border border-border bg-background p-0"><TabsTrigger value="summary" className="rounded-none">Summary</TabsTrigger><TabsTrigger value="targets" className="rounded-none">Targets</TabsTrigger><TabsTrigger value="steps" className="rounded-none">Steps</TabsTrigger><TabsTrigger value="evidence" className="rounded-none">Evidence</TabsTrigger><TabsTrigger value="events" className="rounded-none">Events</TabsTrigger></TabsList><TabsContent value="summary" className="mt-4"><dl className="grid gap-3 border-y border-border py-4 text-xs sm:grid-cols-2"><Fact label="Run ID" value={selectedRun.id} mono /><Fact label="State" value={selectedRun.state} /><Fact label="Target snapshot" value={selectedRun.targetSnapshotId} mono /><Fact label="Selector" value={selectedRun.selector} /><Fact label="Concurrency" value={String(selectedRun.concurrencyLimit)} /><Fact label="Retry budget" value={String(selectedRun.retryBudget)} /></dl><div className="mt-4 flex flex-wrap gap-2"><StatusBadge label={`approval ${selectedRun.approval}`} tone={selectedRun.approval === "approved" ? "healthy" : "attention"} />{!isTerminal(selectedRun.state) ? <Button size="sm" variant="outline" onClick={() => cancel(selectedRun.id)}><Ban className="size-3.5" aria-hidden="true" />Cancel Run</Button> : null}</div></TabsContent><TabsContent value="targets" className="mt-4"><div className="space-y-2">{snapshot.runTargets.filter((target) => target.runId === selectedRun.id).map((target) => <div key={target.id} className="flex flex-wrap items-center justify-between gap-3 border border-border p-3 text-xs"><div><p className="font-medium">{textForDevice(snapshot.devices, target.deviceId)}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{target.id} · attempts {target.attemptCount}{target.leaseId ? ` · lease ${target.leaseId}` : ""}</p>{target.failureClass ? <FailureBadge failureClass={target.failureClass} /> : null}</div><StatusBadge label={target.state.replaceAll("_", " ")} tone={targetTone(target.state)} /></div>)}</div></TabsContent><TabsContent value="steps" className="mt-4"><p className="border border-border bg-muted/40 p-3 text-xs text-muted-foreground">Step traces are not listed here. The workflow definition retains {selectedRun.workflowName} version {selectedRun.workflowVersion}. Independent target state remains on the Targets tab.</p></TabsContent><TabsContent value="evidence" className="mt-4"><p className="border border-border bg-muted/40 p-3 text-xs text-muted-foreground">Evidence references are retained through target observation IDs. Open Artifacts for authorized media access; raw device payloads are not shown here.</p></TabsContent><TabsContent value="events" className="mt-4"><div className="space-y-2">{snapshot.events.filter((event) => event.correlationId === selectedRun.id || event.resourceId === selectedRun.id).map((event) => <div key={event.id} className="border-b border-border/70 py-2 text-xs"><p className="font-medium">{event.name}</p><p className="mt-1 text-muted-foreground">{event.actor} · {event.occurredAt} · {event.payloadSummary}</p></div>)}{snapshot.events.filter((event) => event.correlationId === selectedRun.id || event.resourceId === selectedRun.id).length === 0 ? <p className="text-xs text-muted-foreground">No events linked to this run.</p> : null}</div></TabsContent></Tabs> : null}</SheetContent></Sheet>
  </>
}

function RunTable({ runs, onInspect }: { runs: readonly ControlPlaneSnapshot["runs"][number][]; onInspect: (id: string) => void }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(5)
  const visibleRuns = runs.slice(page * pageSize, (page + 1) * pageSize)
  return <>{runs.length === 0 ? <EmptyState label="No Runs" detail="No runs match this view." /> : <div className="overflow-x-auto border border-border"><table className="w-full min-w-[820px] text-left text-xs"><caption className="sr-only">Runs for the selected state</caption><thead><tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground"><th className="p-3">Run</th><th className="p-3">Workflow</th><th className="p-3">Targets</th><th className="p-3">Created</th><th className="p-3">State</th><th className="p-3"><span className="sr-only">Actions</span></th></tr></thead><tbody>{visibleRuns.map((run) => <tr key={run.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50"><td className="drift-data p-3 text-[10px]">{run.id}</td><td className="p-3">{run.workflowName} · v{run.workflowVersion}</td><td className="p-3 font-mono text-[11px]">{run.targetSnapshotId}</td><td className="p-3">{run.createdAt}</td><td className="p-3"><div className="flex items-center gap-2"><StatusBadge label={run.state.replaceAll("_", " ")} tone={runTone(run.state)} /><FailureBadge failureClass={run.failureClass} /></div></td><td className="p-3 text-right"><Button size="sm" variant="outline" onClick={() => onInspect(run.id)}>Inspect</Button></td></tr>)}</tbody></table></div>}<DataTablePagination page={page} pageSize={pageSize} total={runs.length} onPageChange={setPage} onPageSizeChange={(nextSize) => { setPageSize(nextSize); setPage(0) }} /></>
}
function Fact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt className="text-muted-foreground">{label}</dt><dd className={`mt-1 ${mono ? "font-mono text-[10px]" : ""}`}>{value}</dd></div> }
function Posture({ icon: Icon, label, detail }: { icon: typeof ShieldCheck; label: string; detail: string }) { return <div className="border border-border p-4"><Icon className="size-4 text-primary" aria-hidden="true" /><p className="mt-3 text-xs font-semibold">{label}</p><p className="mt-1 text-[11px] leading-5 text-muted-foreground">{detail}</p></div> }
function isTerminal(state: RunState) { return state === "completed" || state === "failed" || state === "cancelled" }
function runTone(state: RunState): StatusTone { return state === "completed" ? "healthy" : state === "failed" || state === "cancelled" ? "danger" : state === "paused" ? "attention" : "info" }
function targetTone(state: ControlPlaneSnapshot["runTargets"][number]["state"]): StatusTone { return state === "succeeded" ? "healthy" : state === "failed" || state === "cleanup_failed" ? "danger" : state === "cancelled" ? "neutral" : "info" }
