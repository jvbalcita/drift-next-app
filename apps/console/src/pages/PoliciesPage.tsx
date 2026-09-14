import { useState, type FormEvent } from "react"
import { FileCheck2, GitBranch, Save, ShieldAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DispatchIntent, PolicyDecisionView } from "@/lib/domain/control-plane"
import { FailureBadge, FieldLabel, MockNotice, PageIntro, Panel, StatusBadge, type StatusTone } from "./shared"

export function PoliciesPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [selectedPolicyId, setSelectedPolicyId] = useState(snapshot.policies[0]?.id ?? "")
  const [ruleJson, setRuleJson] = useState(snapshot.policies[0]?.ruleJson ?? "{}")
  const [feedback, setFeedback] = useState("")
  const selectedPolicy = snapshot.policies.find((policy) => policy.id === selectedPolicyId)

  function selectPolicy(id: string) {
    const policy = snapshot.policies.find((candidate) => candidate.id === id)
    setSelectedPolicyId(id)
    setRuleJson(policy?.ruleJson ?? "{}")
    setFeedback("")
  }

  function createVersion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selectedPolicy) return
    const mutation = dispatch({ type: "createPolicyVersion", basePolicyId: selectedPolicy.id, ruleJson })
    setFeedback(mutation.message)
    if (mutation.ok && mutation.resourceId) setSelectedPolicyId(mutation.resourceId)
  }

  function activatePolicy() {
    if (!selectedPolicy) return
    const mutation = dispatch({ type: "activatePolicy", policyId: selectedPolicy.id, rowVersion: selectedPolicy.rowVersion })
    setFeedback(mutation.message)
  }

  function retirePolicy() {
    if (!selectedPolicy) return
    const mutation = dispatch({ type: "retirePolicy", policyId: selectedPolicy.id, rowVersion: selectedPolicy.rowVersion })
    setFeedback(mutation.message)
  }

  return (
    <>
      <PageIntro eyebrow="SAFETY / POLICY DECISIONS" title="Policies" description="Review versioned policy definitions and the decisions persisted with protected runs and actions." actions={<StatusBadge label="Fail closed" tone="attention" />} />
      <MockNotice>Policy definitions are immutable version records in this phase. Create a draft version, review its bounded rule JSON, then activate it explicitly; the browser cannot approve a real action.</MockNotice>
      <Tabs defaultValue="active" className="mb-6"><TabsList className="rounded-none border border-border bg-background p-0" aria-label="Policy views"><TabsTrigger value="active" className="rounded-none">Active policies</TabsTrigger><TabsTrigger value="versions" className="rounded-none">Versions</TabsTrigger><TabsTrigger value="decisions" className="rounded-none">Decision log</TabsTrigger></TabsList>
      <TabsContent value="active" className="mt-6"><div className="grid gap-6 xl:grid-cols-[minmax(250px,0.7fr)_minmax(0,1.3fr)]">
        <Panel title="Policy catalog" description="Policy state and version are separate from each decision record.">
          <div className="space-y-3">{snapshot.policies.map((policy) => <button key={policy.id} type="button" onClick={() => selectPolicy(policy.id)} className={`w-full border p-3 text-left transition-colors focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30 ${policy.id === selectedPolicyId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><div className="flex items-start justify-between gap-2"><div><p className="text-sm font-medium">{policy.name}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{policy.id} · v{policy.version} · row v{policy.rowVersion}</p></div><StatusBadge label={policy.state} tone={policy.state === "active" ? "healthy" : policy.state === "retired" ? "neutral" : "attention"} /></div><p className="mt-3 text-[11px] leading-5 text-muted-foreground">{policy.ruleSummary}</p></button>)}</div>
        </Panel>
        <Panel title="Policy definition" description="Rule changes create a new draft version and preserve the previous definition.">
          {selectedPolicy ? <form className="space-y-4" onSubmit={createVersion}><div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4"><div><p className="text-sm font-semibold">{selectedPolicy.name}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{selectedPolicy.id}</p></div><div className="flex gap-2"><StatusBadge label={`version ${selectedPolicy.version}`} tone="info" /><StatusBadge label={`row v${selectedPolicy.rowVersion}`} tone="neutral" /></div></div><div><FieldLabel htmlFor="policy-rule-json">Rule JSON</FieldLabel><textarea id="policy-rule-json" value={ruleJson} onChange={(event) => setRuleJson(event.target.value)} rows={7} className="mt-1 w-full rounded-none border border-input bg-background p-3 font-mono text-xs leading-5 outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" aria-describedby="policy-rule-help" /><p id="policy-rule-help" className="mt-1 text-[10px] text-muted-foreground">Bounded JSON is validated for syntax and sensitive keys. Credentials and connector instructions are not accepted.</p></div><div className="flex flex-wrap items-center gap-2"><Button type="submit" disabled={selectedPolicy.state === "retired"}><Save className="size-3.5" aria-hidden="true" />Create draft version</Button><Button type="button" variant="outline" disabled={selectedPolicy.state !== "draft"} onClick={activatePolicy}>Activate selected draft</Button><Button type="button" variant="outline" disabled={selectedPolicy.state === "retired"} onClick={retirePolicy}>Retire version</Button><span aria-live="polite" className="text-xs text-muted-foreground">{feedback}</span></div></form> : <div className="py-8 text-sm text-muted-foreground">Select a policy.</div>}
        </Panel>
      </div></TabsContent>
      <TabsContent value="versions" className="mt-6"><Panel title="Policy versions" description="Every definition is a durable version record; activation remains an explicit action from the active-policy workspace."><div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{snapshot.policies.map((policy) => <button key={policy.id} type="button" onClick={() => selectPolicy(policy.id)} className="border border-border p-4 text-left transition-colors hover:bg-muted focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30"><div className="flex items-start justify-between gap-3"><div><p className="text-sm font-semibold">{policy.name}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{policy.id} · v{policy.version} · row v{policy.rowVersion}</p></div><StatusBadge label={policy.state} tone={policy.state === "active" ? "healthy" : policy.state === "retired" ? "neutral" : "attention"} /></div><p className="mt-4 text-xs leading-5 text-muted-foreground">{policy.ruleSummary}</p></button>)}</div></Panel></TabsContent>
      <TabsContent value="decisions" className="mt-6"><Panel title="Recorded policy decisions" description="Each protected resource keeps its own decision, reason code, actor, and correlation ID."><div className="overflow-x-auto"><table className="w-full min-w-[860px] border-collapse text-left text-xs"><caption className="sr-only">Policy decisions for protected resources</caption><thead><tr className="border-b border-border text-[10px] uppercase tracking-[0.08em] text-muted-foreground"><th className="px-3 pb-3">Decision</th><th className="px-3 pb-3">Resource</th><th className="px-3 pb-3">Action</th><th className="px-3 pb-3">Reason</th><th className="px-3 pb-3">Actor</th><th className="px-3 pb-3">Correlation</th><th className="px-3 pb-3">Decided</th></tr></thead><tbody>{snapshot.policyDecisions.map((decision) => <DecisionRow key={decision.id} decision={decision} />)}</tbody></table></div></Panel></TabsContent>
      </Tabs>
      <div className="mt-6 grid gap-6 md:grid-cols-3"><BoundaryCard icon={ShieldAlert} label="Emergency stop" detail="Denies before dispatch." tone="danger" /><BoundaryCard icon={GitBranch} label="Versioned policy" detail="Decision records retain the policy version context." tone="info" /><BoundaryCard icon={FileCheck2} label="Audit evidence" detail="Reason and correlation remain searchable." tone="healthy" /></div>
    </>
  )
}

function DecisionRow({ decision }: { decision: PolicyDecisionView }) {
  const tone: StatusTone = decision.decision === "allow" ? "healthy" : decision.decision === "deny" ? "danger" : "attention"
  return <tr className="border-b border-border/70 last:border-0"><td className="px-3 py-3"><StatusBadge label={decision.decision} tone={tone} /></td><td className="px-3 py-3"><p className="font-medium">{decision.resourceType}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{decision.resourceId}</p></td><td className="px-3 py-3 font-mono text-[11px]">{decision.action}</td><td className="px-3 py-3"><div className="flex items-center gap-2"><span>{decision.reasonCode.replaceAll("_", " ")}</span>{decision.decision !== "allow" ? <FailureBadge failureClass={decision.reasonCode} /> : null}</div></td><td className="drift-data px-3 py-3 text-[10px]">{decision.actorId}</td><td className="drift-data px-3 py-3 text-[10px]">{decision.correlationId}</td><td className="drift-data px-3 py-3 text-[10px] text-muted-foreground">{decision.decidedAt}</td></tr>
}

function BoundaryCard({ icon: Icon, label, detail, tone }: { icon: typeof ShieldAlert; label: string; detail: string; tone: StatusTone }) {
  return <div className="border border-border bg-card p-4"><div className="flex items-center gap-2"><Icon className={`size-4 ${tone === "danger" ? "text-red-700" : tone === "healthy" ? "text-emerald-700" : "text-primary"}`} aria-hidden="true" /><p className="text-xs font-semibold">{label}</p></div><p className="mt-2 text-[11px] leading-5 text-muted-foreground">{detail}</p></div>
}
