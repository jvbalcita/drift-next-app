import { useMemo, useState, type FormEvent } from "react"
import { Database, Link2, ListChecks, Plus, ShieldOff } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { ControlPlaneSnapshot, DispatchIntent } from "@/lib/domain/control-plane"
import { FieldLabel, MockNotice, PageIntro, Panel, StatusBadge, type StatusTone } from "./shared"

export function AccountsPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [selectedAccountId, setSelectedAccountId] = useState(snapshot.accounts[0]?.id ?? "")
  const [sourceProvider, setSourceProvider] = useState("fixture")
  const [sourceName, setSourceName] = useState("")
  const [sourceReference, setSourceReference] = useState("")
  const [sourceMetadata, setSourceMetadata] = useState(`{"environment":"demo"}`)
  const [accountSourceId, setAccountSourceId] = useState(snapshot.accountSources[0]?.id ?? "")
  const [accountReference, setAccountReference] = useState("")
  const [accountLabel, setAccountLabel] = useState("")
  const [accountMetadata, setAccountMetadata] = useState(`{"tier":"review"}`)
  const [assignmentAccountId, setAssignmentAccountId] = useState(snapshot.accounts[0]?.id ?? "")
  const [assignmentDeviceId, setAssignmentDeviceId] = useState(snapshot.devices[0]?.id ?? "")
  const [feedback, setFeedback] = useState("")
  const selectedAccount = snapshot.accounts.find((account) => account.id === selectedAccountId)
  const selectedSource = snapshot.accountSources.find((source) => source.id === selectedAccount?.sourceId)
  const selectedServiceStates = useMemo(() => snapshot.accountServiceStates.filter((state) => state.accountId === selectedAccountId), [selectedAccountId, snapshot.accountServiceStates])
  const selectedServiceHistory = useMemo(() => snapshot.accountServiceStateHistory.filter((state) => state.accountId === selectedAccountId), [selectedAccountId, snapshot.accountServiceStateHistory])
  const selectedRuns = useMemo(() => snapshot.accountRuns.filter((run) => run.accountId === selectedAccountId), [selectedAccountId, snapshot.accountRuns])
  const selectedRunIds = useMemo(() => new Set(selectedRuns.map((run) => run.id)), [selectedRuns])
  const selectedRunEvents = useMemo(() => snapshot.accountRunEvents.filter((event) => selectedRunIds.has(event.runId)), [selectedRunIds, snapshot.accountRunEvents])
  const accountAssignments = useMemo(() => snapshot.accountDeviceAssignments.filter((assignment) => assignment.accountId === selectedAccountId), [selectedAccountId, snapshot.accountDeviceAssignments])
  const selectedSyncEvents = useMemo(() => snapshot.accountSyncEvents.filter((event) => event.accountId === selectedAccountId || event.sourceId === selectedAccount?.sourceId), [selectedAccountId, selectedAccount?.sourceId, snapshot.accountSyncEvents])

  function createSource(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const mutation = dispatch({ type: "createAccountSource", provider: sourceProvider, displayName: sourceName, externalReference: sourceReference, metadataJson: sourceMetadata })
    setFeedback(mutation.message)
    if (mutation.ok) {
      setSourceName("")
      setSourceReference("")
    }
  }

  function createAccount(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const mutation = dispatch({ type: "createAccount", sourceId: accountSourceId, externalReference: accountReference, label: accountLabel, metadataJson: accountMetadata })
    setFeedback(mutation.message)
    if (mutation.ok) {
      setAccountReference("")
      setAccountLabel("")
    }
  }

  function saveAccount() {
    if (!selectedAccount) return
    const mutation = dispatch({ type: "updateAccount", accountId: selectedAccount.id, externalReference: accountReference || selectedAccount.externalReference, label: accountLabel || selectedAccount.label, metadataJson: accountMetadata || selectedAccount.metadataJson, rowVersion: selectedAccount.rowVersion })
    setFeedback(mutation.message)
  }

  function updateAccountState(state: string) {
    if (!selectedAccount || !isAccountState(state)) return
    const mutation = dispatch({ type: "updateAccountState", accountId: selectedAccount.id, state, rowVersion: selectedAccount.rowVersion })
    setFeedback(mutation.message)
  }

  function assignDevice(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const mutation = dispatch({ type: "assignAccountDevice", accountId: assignmentAccountId, deviceId: assignmentDeviceId })
    setFeedback(mutation.message)
  }

  return (
    <>
      <PageIntro eyebrow="IDENTITY / ACCOUNT REFERENCES" title="Accounts" description="Manage non-secret account references, explicit device assignments, service projections, and bounded run history without enabling a connector." actions={<StatusBadge label={`${snapshot.accounts.length} accounts`} tone="info" />} />
      <MockNotice>Account records contain provider references and sanitized metadata only. The connector is disabled in this phase; sync records retain idempotency, correlation, and outcome metadata without attempting external access.</MockNotice>

      <div className="grid gap-6 xl:grid-cols-[minmax(250px,0.75fr)_minmax(0,1.25fr)]">
        <Panel title="Account sources" description="A source identifies an authority without storing credentials or connector configuration.">
          <div className="space-y-2">
            {snapshot.accountSources.map((source) => <div key={source.id} className="border border-border p-3"><div className="flex items-start justify-between gap-2"><div><p className="text-sm font-medium">{source.displayName}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{source.provider} · {source.id} · row v{source.rowVersion}</p></div><StatusBadge label={source.state} tone={source.state === "active" ? "healthy" : "neutral"} /></div><p className="mt-2 text-[11px] text-muted-foreground">External ref · {source.externalReference || "not set"}</p></div>)}
          </div>
          <form className="mt-5 space-y-3 border-t border-border pt-4" onSubmit={createSource}>
            <div className="flex items-center gap-2"><Plus className="size-4 text-primary" aria-hidden="true" /><p className="text-xs font-semibold uppercase tracking-[0.08em]">Add source reference</p></div>
            <div><FieldLabel htmlFor="source-provider">Provider</FieldLabel><input id="source-provider" value={sourceProvider} onChange={(event) => setSourceProvider(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <div><FieldLabel htmlFor="source-name">Display name</FieldLabel><input id="source-name" value={sourceName} onChange={(event) => setSourceName(event.target.value)} placeholder="Sanitized fixture source" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <div><FieldLabel htmlFor="source-reference">External reference</FieldLabel><input id="source-reference" value={sourceReference} onChange={(event) => setSourceReference(event.target.value)} placeholder="catalog-v1" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <div><FieldLabel htmlFor="source-metadata">Sanitized metadata JSON</FieldLabel><textarea id="source-metadata" value={sourceMetadata} onChange={(event) => setSourceMetadata(event.target.value)} rows={3} className="mt-1 w-full rounded-none border border-input bg-background p-2 font-mono text-[11px] outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <Button type="submit"><Database className="size-3.5" aria-hidden="true" />Save source reference</Button>
          </form>
        </Panel>

        <Panel title="Account catalog" description="Select an account to inspect its current state and immutable reference metadata.">
          <div className="grid gap-2 md:grid-cols-2">
            {snapshot.accounts.map((account) => <button key={account.id} type="button" onClick={() => { setSelectedAccountId(account.id); setAssignmentAccountId(account.id); setAccountReference(account.externalReference); setAccountLabel(account.label); setAccountMetadata(account.metadataJson) }} className={`border p-3 text-left transition-colors focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30 ${account.id === selectedAccountId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><div className="flex items-start justify-between gap-2"><div><p className="text-sm font-medium">{account.label}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{account.id} · {account.sourceProvider} · row v{account.rowVersion}</p></div><StatusBadge label={account.state} tone={account.state === "active" ? "healthy" : account.state === "retired" ? "neutral" : "attention"} /></div><p className="mt-2 text-[11px] text-muted-foreground">{account.externalReference} · service {account.serviceState}</p></button>)}
          </div>
          <form className="mt-5 space-y-3 border-t border-border pt-4" onSubmit={createAccount}>
            <div className="flex items-center gap-2"><Plus className="size-4 text-primary" aria-hidden="true" /><p className="text-xs font-semibold uppercase tracking-[0.08em]">Add account reference</p></div>
            <div className="grid gap-3 sm:grid-cols-2"><div><FieldLabel htmlFor="account-source">Account source</FieldLabel><select id="account-source" value={accountSourceId} onChange={(event) => setAccountSourceId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20">{snapshot.accountSources.map((source) => <option key={source.id} value={source.id}>{source.displayName}</option>)}</select></div><div><FieldLabel htmlFor="account-reference">External reference</FieldLabel><input id="account-reference" value={accountReference} onChange={(event) => setAccountReference(event.target.value)} placeholder="account-reference" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div></div>
            <div><FieldLabel htmlFor="account-label">Account label</FieldLabel><input id="account-label" value={accountLabel} onChange={(event) => setAccountLabel(event.target.value)} placeholder="Review fixture" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <div><FieldLabel htmlFor="account-metadata">Sanitized metadata JSON</FieldLabel><textarea id="account-metadata" value={accountMetadata} onChange={(event) => setAccountMetadata(event.target.value)} rows={3} className="mt-1 w-full rounded-none border border-input bg-background p-2 font-mono text-[11px] outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div>
            <div className="flex flex-wrap gap-2"><Button type="submit"><Plus className="size-3.5" aria-hidden="true" />Create account</Button>{selectedAccount ? <Button type="button" variant="outline" onClick={saveAccount}>Save selected account</Button> : null}</div>
          </form>
          <span aria-live="polite" className="mt-3 block text-xs text-muted-foreground">{feedback}</span>
        </Panel>
      </div>

      {selectedAccount ? <>
        <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1.1fr)_minmax(280px,0.9fr)]">
          <Panel title="Selected account" description="State changes use an explicit row version. Display names are resolved from the device registry only for presentation.">
            <div className="grid gap-4 sm:grid-cols-2"><dl className="space-y-3 text-xs"><div><dt className="text-muted-foreground">Reference</dt><dd className="drift-data mt-1">{selectedAccount.externalReference}</dd></div><div><dt className="text-muted-foreground">Source</dt><dd className="mt-1">{selectedSource?.displayName ?? selectedAccount.sourceId}</dd></div><div><dt className="text-muted-foreground">Metadata</dt><dd className="mt-1 break-all font-mono text-[10px]">{selectedAccount.metadataJson}</dd></div></dl><div className="space-y-3"><div><FieldLabel htmlFor="account-state">Account state</FieldLabel><select id="account-state" value={selectedAccount.state} onChange={(event) => updateAccountState(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20"><option value="draft">Draft</option><option value="active">Active</option><option value="inactive">Inactive</option><option value="retired">Retired</option></select></div><div className="border border-border bg-muted/40 p-3"><p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Service projection</p><div className="mt-2 flex items-center gap-2"><StatusBadge label={selectedAccount.serviceState} tone={serviceTone(selectedAccount.serviceState)} /><span className="text-xs">{selectedAccount.lastRun}</span></div></div></div></div>
          </Panel>
          <Panel title="Explicit device assignment" description="Assignments store account and stable device IDs. A connector or device command is never implied.">
            <form className="space-y-3" onSubmit={assignDevice}><div><FieldLabel htmlFor="assignment-account">Account</FieldLabel><select id="assignment-account" value={assignmentAccountId} onChange={(event) => setAssignmentAccountId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20">{snapshot.accounts.map((account) => <option key={account.id} value={account.id}>{account.label}</option>)}</select></div><div><FieldLabel htmlFor="assignment-device">Device</FieldLabel><select id="assignment-device" value={assignmentDeviceId} onChange={(event) => setAssignmentDeviceId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20">{snapshot.devices.map((device) => <option key={device.id} value={device.id}>{device.displayName} · {device.id}</option>)}</select></div><Button type="submit"><Link2 className="size-3.5" aria-hidden="true" />Assign by ID</Button></form>
            <div className="mt-5 space-y-2 border-t border-border pt-4">{accountAssignments.length === 0 ? <p className="text-xs text-muted-foreground">No assignments for this account.</p> : accountAssignments.map((assignment) => { const device = snapshot.devices.find((candidate) => candidate.id === assignment.deviceId); return <div key={assignment.id} className="flex items-center justify-between gap-3 border-b border-border/70 pb-2 text-xs last:border-0"><div><p className="font-medium">{device?.displayName ?? "Unknown device"}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{assignment.deviceId} · {assignment.state} · row v{assignment.rowVersion}</p></div>{assignment.state === "active" ? <Button type="button" variant="outline" size="sm" onClick={() => setFeedback(dispatch({ type: "endAccountDeviceAssignment", assignmentId: assignment.id, rowVersion: assignment.rowVersion }).message)}>End</Button> : null}</div> })}</div>
          </Panel>
        </div>

        <div className="mt-6 grid gap-6 xl:grid-cols-2">
          <Panel title="Service stages and history" description="The current projection is separate from append-only observations."><div className="space-y-3">{selectedServiceStates.map((state) => <div key={state.id} className="border border-border p-3"><div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-medium">{state.serviceName}</span><div className="flex gap-2"><StatusBadge label={state.stage} tone="info" /><StatusBadge label={state.state} tone={serviceTone(state.state)} /></div></div><p className="drift-data mt-2 text-[10px] text-muted-foreground">observed {state.observedAt} · row v{state.rowVersion}{state.failureClass ? ` · ${state.failureClass}` : ""}</p></div>)}{selectedServiceHistory.map((state) => <div key={state.id} className="flex items-center justify-between border-b border-border/70 py-2 text-[11px]"><span>{state.serviceName} · {state.stage}</span><span className="drift-data text-[10px] text-muted-foreground">{state.observedAt} · recorded {state.recordedAt} · {state.state}</span></div>)}</div></Panel>
          <Panel title="Account runs and sync history" description="Runs and connector outcomes are independently visible, including disabled attempts."><div className="space-y-3">{selectedRuns.map((run) => <div key={run.id} className="border border-border p-3"><div className="flex items-center justify-between gap-2"><div><p className="text-sm font-medium">{run.id}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{run.correlationId} · row v{run.rowVersion}</p></div><StatusBadge label={run.state} tone={run.state === "completed" ? "healthy" : run.state === "failed" ? "danger" : "attention"} /></div>{run.startedAt ? <p className="mt-2 text-[11px] text-muted-foreground">Started {run.startedAt}</p> : null}{run.failureClass ? <p className="mt-2 text-[11px] text-red-700">{run.failureClass}</p> : null}</div>)}{selectedRunEvents.map((event) => <div key={event.id} className="flex items-center justify-between border-b border-border/70 py-2 text-[11px]"><span>{event.state} · {event.actorType}</span><span className="drift-data text-[10px] text-muted-foreground">{event.occurredAt} · {event.correlationId}</span></div>)}{selectedSyncEvents.map((event) => <div key={event.id} className="border-t border-border pt-3 text-[11px]"><div className="flex items-center gap-2"><ShieldOff className="size-3.5 text-primary" aria-hidden="true" /><span className="font-medium">{event.eventName} · connector {event.outcome}</span><span className="drift-data text-[10px] text-muted-foreground">{event.idempotencyKey}</span></div><p className="mt-1 text-muted-foreground">No external account access was attempted.</p></div>)}</div></Panel>
        </div>
        <div className="mt-6 grid gap-4 md:grid-cols-3"><SummaryCard icon={Database} label="Metadata only" detail="References are bounded and redaction-checked." /><SummaryCard icon={Link2} label="Explicit assignment" detail="Account-to-device links use stable IDs." /><SummaryCard icon={ListChecks} label="Auditable history" detail="Stages, runs, and sync outcomes stay separate." /></div>
      </> : null}
    </>
  )
}

function isAccountState(value: string): value is "draft" | "active" | "inactive" | "retired" {
  return value === "draft" || value === "active" || value === "inactive" || value === "retired"
}

function serviceTone(state: "unknown" | "healthy" | "degraded" | "failed" | "disabled"): StatusTone {
  return state === "healthy" ? "healthy" : state === "failed" ? "danger" : state === "degraded" ? "attention" : "neutral"
}

function SummaryCard({ icon: Icon, label, detail }: { icon: typeof Database; label: string; detail: string }) {
  return <div className="border border-border bg-card p-4"><Icon className="size-4 text-primary" aria-hidden="true" /><p className="mt-3 text-xs font-semibold">{label}</p><p className="mt-1 text-[11px] leading-5 text-muted-foreground">{detail}</p></div>
}
