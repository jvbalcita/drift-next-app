import { useState, type FormEvent } from "react"
import { Cpu, ShieldCheck } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { reportDispatch } from "@/lib/api/report-dispatch"
import type { AutomationAgentProfileView, ControlPlaneSnapshot, DispatchIntent } from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, FieldLabel, OperatorNotice, PageIntro, StatusBadge, type StatusTone } from "./shared"

export function AgentsPage({ snapshot, dispatch, view = "runtimes", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [agentName, setAgentName] = useState("")
  const [assignAgentId, setAssignAgentId] = useState(snapshot.automationAgents[0]?.id ?? "")
  const [assignDeviceId, setAssignDeviceId] = useState(snapshot.devices[0]?.id ?? "")
  const [feedback, setFeedback] = useState("")
  const selected = snapshot.edgeAgents.find((agent) => agent.id === selectedId)
  function createAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void reportDispatch(dispatch, { type: "createAutomationAgent", name: agentName }, setFeedback).then((outcome) => {
      if (outcome.ok) setAgentName("")
    })
  }
  function assignAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void reportDispatch(dispatch, { type: "assignAutomationAgentDevice", agentId: assignAgentId, deviceId: assignDeviceId }, setFeedback)
  }
  return <><PageIntro eyebrow="AUTOMATION / AGENTS" title="Agent Profiles" description="Edge runtime health is separate from logical profiles, their assignments, and declarative capabilities." actions={<StatusBadge label="No Profile Bypass" tone="info" />} /><OperatorNotice>Profiles are declarative metadata. They cannot access device protocols, credentials, arbitrary scripts, or bypass policy and leases.</OperatorNotice>
    <Tabs value={view} onValueChange={onViewChange}><TabsList className="rounded-none border border-border bg-background p-0" aria-label="Agent views"><TabsTrigger value="runtimes" className="rounded-none">Edge Runtimes</TabsTrigger><TabsTrigger value="profiles" className="rounded-none">Logical Profiles</TabsTrigger><TabsTrigger value="assignments" className="rounded-none">Assignments</TabsTrigger><TabsTrigger value="capabilities" className="rounded-none">Capabilities</TabsTrigger></TabsList>
      <TabsContent value="runtimes"><RuntimeTable snapshot={snapshot} onOpen={setSelectedId} /></TabsContent>
      <TabsContent value="profiles">
        <form onSubmit={createAgent} className="mt-4 flex flex-wrap items-end gap-3 border border-border p-4">
          <div>
            <FieldLabel htmlFor="new-agent-name">Agent Name</FieldLabel>
            <input id="new-agent-name" value={agentName} onChange={(event) => setAgentName(event.target.value)} className="mt-1 h-9 rounded-none border border-input bg-background px-2 text-xs" />
          </div>
          <Button type="submit" size="sm" variant="outline" disabled={!agentName.trim()}>Create Agent</Button>
          <p aria-live="polite" className="text-[11px] text-muted-foreground">{feedback}</p>
        </form>
        {snapshot.automationAgentProfiles.length > 0
          ? <ProfileList profiles={snapshot.automationAgentProfiles} />
          : snapshot.automationAgents.length === 0
            ? <EmptyState label="No Automation Agents" detail="Create an agent, then assign a registered device. Agents cannot use shell, credentials, or unrestricted filesystem access." />
            : <div className="mt-4 border border-border"><table className="w-full text-left text-xs"><thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="p-3">Agent</th><th className="p-3">State</th></tr></thead><tbody>{snapshot.automationAgents.map((agent) => <tr key={agent.id} className="border-b border-border/70"><td className="p-3">{agent.name}</td><td className="p-3"><StatusBadge label={agent.state} tone={agent.state === "active" ? "healthy" : "attention"} /></td></tr>)}</tbody></table></div>}
      </TabsContent>
      <TabsContent value="assignments">
        <form onSubmit={assignAgent} className="mt-4 grid gap-3 border border-border p-4 md:grid-cols-3">
          <div>
            <FieldLabel htmlFor="assign-agent">Automation Agent</FieldLabel>
            <select id="assign-agent" value={assignAgentId} onChange={(event) => setAssignAgentId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs">
              {snapshot.automationAgents.length === 0 ? <option value="">No Automation Agents</option> : null}
              {snapshot.automationAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
            </select>
          </div>
          <div>
            <FieldLabel htmlFor="assign-device">Device</FieldLabel>
            <select id="assign-device" value={assignDeviceId} onChange={(event) => setAssignDeviceId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs">
              {snapshot.devices.length === 0 ? <option value="">No Registered Devices</option> : null}
              {snapshot.devices.map((device) => <option key={device.id} value={device.id}>{device.displayName}</option>)}
            </select>
          </div>
          <Button type="submit" size="sm" variant="outline" className="self-end" disabled={!assignAgentId || !assignDeviceId}>Assign Device</Button>
          <p aria-live="polite" className="text-[11px] text-muted-foreground md:col-span-3">{feedback}</p>
        </form>
        <div className="mt-4 border border-border"><table className="w-full text-left text-xs"><thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="p-3">Profile</th><th className="p-3">Assignment</th><th className="p-3">Trust</th></tr></thead><tbody>{snapshot.automationAgentProfiles.map((profile) => <tr key={profile.id} className="border-b border-border/70"><td className="p-3">{profile.id}</td><td className="p-3">{profile.assignmentSummary}</td><td className="p-3"><StatusBadge label={profile.trust} tone={trustTone(profile.trust)} /></td></tr>)}</tbody></table></div>
      </TabsContent>
      <TabsContent value="capabilities"><div className="mt-4 grid gap-3 md:grid-cols-2">{snapshot.automationAgentProfiles.map((profile) => <div key={profile.id} className="border border-border p-4"><p className="text-xs font-semibold">{profile.id}</p><p className="mt-2 text-xs text-muted-foreground">{profile.capabilities.join(", ") || "No capabilities declared"}</p></div>)}</div></TabsContent>
    </Tabs>
    <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelectedId(null)}><SheetContent className="w-full rounded-none sm:max-w-xl"><SheetHeader className="border-b border-border"><SheetTitle>{selected?.displayName ?? "Runtime Detail"}</SheetTitle><SheetDescription>Runtime state, health, and device bindings.</SheetDescription></SheetHeader>{selected ? <div className="space-y-4 p-4 text-xs"><div className="flex justify-between"><span>{selected.version}</span><StatusBadge label={selected.state} tone={selected.state === "active" ? "healthy" : "attention"} /></div><p>Last seen {selected.lastSeen}</p><div className="border-t border-border pt-3"><p className="font-semibold">Bound devices</p><p className="mt-1 text-muted-foreground">{selected.deviceIds.map((id) => snapshot.devices.find((device) => device.id === id)?.displayName ?? id).join(", ")}</p></div></div> : null}</SheetContent></Sheet>
  </> }
function RuntimeTable({ snapshot, onOpen }: { snapshot: ControlPlaneSnapshot; onOpen: (id: string) => void }) { const [page, setPage] = useState(0); const [pageSize, setPageSize] = useState(10); const visible = snapshot.edgeAgents.slice(page * pageSize, (page + 1) * pageSize); return <>{snapshot.edgeAgents.length === 0 ? <EmptyState label="No Edge Runtimes" detail="No edge runtime projections are available." /> : <ScrollArea className="mt-4 h-[min(65vh,700px)] border border-border"><table className="w-full min-w-[700px] text-left text-xs"><caption className="sr-only">Edge runtimes</caption><thead className="sticky top-0 bg-background"><tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground"><th className="p-3">Runtime</th><th className="p-3">State</th><th className="p-3">Version</th><th className="p-3">Devices</th><th className="p-3">Last Seen</th></tr></thead><tbody>{visible.map((agent) => <tr key={agent.id} className="border-b border-border/70 hover:bg-muted/50"><td className="p-3"><button onClick={() => onOpen(agent.id)} className="flex items-center gap-2 font-medium hover:underline"><Cpu className="size-3.5 text-primary" aria-hidden="true" />{agent.displayName}</button></td><td className="p-3"><StatusBadge label={agent.state} tone={agent.state === "active" ? "healthy" : "attention"} /></td><td className="drift-data p-3 text-[10px]">{agent.version}</td><td className="p-3">{agent.deviceIds.length}</td><td className="p-3 text-muted-foreground">{agent.lastSeen}</td></tr>)}</tbody></table></ScrollArea>}<DataTablePagination page={page} pageSize={pageSize} total={snapshot.edgeAgents.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} /></> }
function ProfileList({ profiles }: { profiles: readonly AutomationAgentProfileView[] }) { return <div className="mt-4 grid gap-3 md:grid-cols-2">{profiles.map((profile) => <div key={profile.id} className="border border-border p-4"><div className="flex justify-between gap-2"><p className="text-xs font-semibold">{profile.id}</p><StatusBadge label={profile.state} tone={profile.state === "published" ? "healthy" : "attention"} /></div><p className="mt-2 text-xs text-muted-foreground">{profile.personality}</p><div className="mt-3 flex gap-2"><ShieldCheck className="size-3.5 text-primary" aria-hidden="true" /><span className="text-[11px]">Trust: {profile.trust}</span></div></div>)}</div> }
function trustTone(value: AutomationAgentProfileView["trust"]): StatusTone { return value === "approved" ? "healthy" : value === "revoked" ? "danger" : "attention" }
