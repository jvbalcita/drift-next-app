import { useState, type FormEvent } from "react"
import { Check, Edit3, Globe2, Plus, Radar, Save, ShieldCheck, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { ControlPlaneSnapshot, DispatchIntent, NetworkProfileView } from "@/lib/domain/control-plane"
import { EmptyState, FieldLabel, FailureBadge, MockNotice, PageIntro, Panel, StatusBadge } from "./shared"

export function NetworkProfilesPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [selectedProfileId, setSelectedProfileId] = useState(snapshot.networkProfiles[0]?.id ?? "new")
  const [name, setName] = useState(snapshot.networkProfiles[0]?.name ?? "")
  const [addressPolicy, setAddressPolicy] = useState(snapshot.networkProfiles[0]?.addressPolicy ?? "")
  const [ports, setPorts] = useState(snapshot.networkProfiles[0]?.ports.join(", ") ?? "5555")
  const [isDefault, setIsDefault] = useState(snapshot.networkProfiles[0]?.isDefault ?? false)
  const [candidateReason, setCandidateReason] = useState("")
  const [feedback, setFeedback] = useState("")

  function selectProfile(profile: NetworkProfileView) {
    setSelectedProfileId(profile.id)
    setName(profile.name)
    setAddressPolicy(profile.addressPolicy)
    setPorts(profile.ports.join(", "))
    setIsDefault(profile.isDefault)
    setFeedback("")
  }

  function newProfile() {
    setSelectedProfileId("new")
    setName("")
    setAddressPolicy("192.0.2.0/24")
    setPorts("5555")
    setIsDefault(false)
    setFeedback("")
  }

  function parsedPorts(): number[] {
    return ports.split(",").map((value) => Number(value.trim())).filter((value) => Number.isInteger(value) && value > 0 && value <= 65535)
  }

  function saveProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const values = parsedPorts()
    const mutation = selectedProfileId === "new"
      ? dispatch({ type: "createNetworkProfile", name, addressPolicy, ports: values, isDefault })
      : dispatch({ type: "updateNetworkProfile", profileId: selectedProfileId, name, addressPolicy, ports: values, isDefault, rowVersion: snapshot.networkProfiles.find((profile) => profile.id === selectedProfileId)?.rowVersion ?? 0 })
    setFeedback(mutation.message)
  }

  function retireProfile() {
    const profile = snapshot.networkProfiles.find((candidate) => candidate.id === selectedProfileId)
    if (!profile) return
    const mutation = dispatch({ type: "retireNetworkProfile", profileId: profile.id, rowVersion: profile.rowVersion })
    setFeedback(mutation.message)
  }

  function startScan() {
    const mutation = dispatch({ type: "startScan", profileId: selectedProfileId })
    setFeedback(mutation.message)
  }

  return (
    <>
      <PageIntro eyebrow="DISCOVERY / NETWORK PROFILES" title="Network Profiles" description="Define bounded, non-authoritative discovery policy separately from scan execution and candidate approval." actions={<Button variant="outline" size="sm" onClick={newProfile}><Plus className="size-3.5" aria-hidden="true" />New profile</Button>} />
      <MockNotice>Address policies use documentation ranges and the transport is a deterministic mock. Saving or scanning opens no network socket and registers no real endpoint.</MockNotice>
      <div className="grid gap-6 xl:grid-cols-[minmax(260px,0.65fr)_minmax(0,1.35fr)]">
        <Panel title="Profile catalog" description="Draft, active, disabled, and retired definitions remain distinct.">
          <div className="space-y-3">{snapshot.networkProfiles.length === 0 ? <EmptyState label="No Network Profiles" detail="Create a bounded profile to continue." /> : snapshot.networkProfiles.map((profile) => <button key={profile.id} type="button" onClick={() => selectProfile(profile)} className={`w-full border p-3 text-left transition-colors focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30 ${profile.id === selectedProfileId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><div className="flex items-start justify-between gap-2"><div><p className="text-sm font-medium">{profile.name}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{profile.id} · v{profile.rowVersion}</p></div><StatusBadge label={profile.state} tone={profile.state === "active" ? "healthy" : profile.state === "retired" ? "neutral" : "attention"} /></div><p className="mt-3 text-xs text-muted-foreground">{profile.addressPolicy} · ports {profile.ports.join(", ")}</p>{profile.isDefault ? <p className="mt-2 flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-primary"><Check className="size-3" aria-hidden="true" />Default profile</p> : null}</button>)}</div>
        </Panel>
        <Panel title={selectedProfileId === "new" ? "Create Network Profile" : "Edit Network Profile"} description="Profile definition is separate from the scan action below.">
          <form className="grid gap-4 sm:grid-cols-2" onSubmit={saveProfile}><div className="sm:col-span-2"><FieldLabel htmlFor="profile-name">Profile name</FieldLabel><input id="profile-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="Lab C staging" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-3 text-sm outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" required /></div><div><FieldLabel htmlFor="profile-address-policy">Address policy</FieldLabel><input id="profile-address-policy" value={addressPolicy} onChange={(event) => setAddressPolicy(event.target.value)} placeholder="192.0.2.0/24" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-3 font-mono text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" required /><p className="mt-1 text-[10px] text-muted-foreground">CIDR or inclusive IP range; validation is mock-only.</p></div><div><FieldLabel htmlFor="profile-ports">Ports</FieldLabel><input id="profile-ports" value={ports} onChange={(event) => setPorts(event.target.value)} placeholder="5555, 5037" className="mt-1 h-9 w-full rounded-none border border-input bg-background px-3 font-mono text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" required /><p className="mt-1 text-[10px] text-muted-foreground">Comma-separated numeric ports.</p></div><label className="flex items-center gap-2 text-xs sm:col-span-2"><input type="checkbox" checked={isDefault} onChange={(event) => setIsDefault(event.target.checked)} className="size-4 accent-[var(--primary)]" />Use as default discovery policy</label><div className="flex flex-wrap items-center gap-2 sm:col-span-2"><Button type="submit"><Save className="size-3.5" aria-hidden="true" />Save profile</Button>{selectedProfileId !== "new" ? <Button type="button" variant="destructive" onClick={retireProfile} disabled={snapshot.networkProfiles.find((profile) => profile.id === selectedProfileId)?.state === "retired"}><Trash2 className="size-3.5" aria-hidden="true" />Retire</Button> : null}<span aria-live="polite" className="text-xs text-muted-foreground">{feedback}</span></div></form>
        </Panel>
      </div>
      <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,0.72fr)_minmax(0,1.28fr)]">
        <Panel title="Scan action" description="A scan is an explicit mock intent against an active profile; it does not approve or register candidates." action={<Button size="sm" onClick={startScan} disabled={selectedProfileId === "new" || snapshot.networkProfiles.find((profile) => profile.id === selectedProfileId)?.state !== "active"}><Radar className="size-3.5" aria-hidden="true" />Start mock scan</Button>}>
          <div className="space-y-3">{snapshot.scanRuns.map((run) => <div key={run.id} className="border-b border-border pb-3 last:border-0 last:pb-0"><div className="flex items-center justify-between gap-2"><p className="drift-data text-xs font-medium">{run.id}</p><StatusBadge label={run.state} tone={run.state === "completed" ? "healthy" : run.state === "failed" ? "danger" : "info"} /></div><p className="mt-1 text-[11px] text-muted-foreground">profile {run.networkProfileId} · requested {run.requestedAt}</p><div className="mt-2 flex items-center gap-2"><FailureBadge failureClass={run.failureClass} />{run.finishedAt ? <span className="text-[10px] text-muted-foreground">finished {run.finishedAt}</span> : <span className="text-[10px] text-muted-foreground">awaiting mock completion</span>}</div></div>)}</div>
        </Panel>
        <Panel title="Candidate review" description="Discovery, approval, and canonical registration are separate state transitions.">
          <div className="mb-4 flex items-start gap-2 border border-border bg-muted/40 p-3 text-[11px] leading-5 text-muted-foreground"><ShieldCheck className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />Approval is required before the mock registration action can be offered. Candidate evidence is summarized and sanitized.</div><div className="mb-4"><FieldLabel htmlFor="candidate-reason">Decision rationale (optional)</FieldLabel><input id="candidate-reason" value={candidateReason} onChange={(event) => setCandidateReason(event.target.value)} placeholder="Bounded operator rationale" className="mt-1 h-8 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" /></div><div className="space-y-3">{snapshot.scanCandidates.length === 0 ? <EmptyState label="No scan candidates" detail="Run a mock scan to populate the review queue." /> : snapshot.scanCandidates.map((candidate) => <div key={candidate.id} className="border border-border p-3"><div className="flex flex-wrap items-start justify-between gap-3"><div><p className="text-sm font-medium">{candidate.host}:{candidate.port}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{candidate.id} · {candidate.serial} · {candidate.fingerprint}</p></div><StatusBadge label={candidate.state.replaceAll("_", " ")} tone={candidate.state === "approved" || candidate.state === "registered" ? "healthy" : candidate.state === "rejected" || candidate.state === "expired" ? "danger" : "attention"} /></div><p className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground"><Globe2 className="size-3.5 text-primary" aria-hidden="true" />{candidate.evidenceSummary} · discovered {candidate.discoveredAt}</p><div className="mt-3 flex flex-wrap gap-2">{candidate.state === "pending_approval" ? <><Button size="sm" onClick={() => setFeedback(dispatch({ type: "decideScanCandidate", candidateId: candidate.id, approve: true, reason: candidateReason }).message)}><Check className="size-3.5" aria-hidden="true" />Approve</Button><Button size="sm" variant="outline" onClick={() => setFeedback(dispatch({ type: "decideScanCandidate", candidateId: candidate.id, approve: false, reason: candidateReason }).message)}><Edit3 className="size-3.5" aria-hidden="true" />Reject</Button></> : null}{candidate.state === "approved" ? <Button size="sm" variant="secondary" onClick={() => setFeedback(dispatch({ type: "registerScanCandidate", candidateId: candidate.id, displayName: `Mock ${candidate.host}` }).message)}><ShieldCheck className="size-3.5" aria-hidden="true" />Register mock candidate</Button> : null}</div></div>)}</div>
        </Panel>
      </div>
      <div aria-live="polite" className="mt-6 border-l-2 border-primary bg-secondary/60 p-3 text-xs text-muted-foreground">{feedback || "Discovery status feedback appears here. No external discovery is active."}</div>
    </>
  )
}
