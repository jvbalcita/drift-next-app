import { useMemo, useState, type FormEvent } from "react"
import { Check, LockKeyhole, Save, Settings2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { ControlPlaneSnapshot, DispatchIntent } from "@/lib/domain/control-plane"
import { FieldLabel, MockNotice, PageIntro, Panel, StatusBadge } from "./shared"

export function SettingsPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [scopeFilter, setScopeFilter] = useState("all")
  const [selectedSettingId, setSelectedSettingId] = useState(snapshot.settings[0]?.id ?? "")
  const [valueJson, setValueJson] = useState(snapshot.settings[0]?.valueJson ?? "")
  const [feedback, setFeedback] = useState("")
  const settings = useMemo(() => snapshot.settings.filter((setting) => scopeFilter === "all" || setting.scope === scopeFilter), [scopeFilter, snapshot.settings])
  const selectedSetting = snapshot.settings.find((setting) => setting.id === selectedSettingId)

  function selectSetting(id: string) {
    const setting = snapshot.settings.find((candidate) => candidate.id === id)
    setSelectedSettingId(id)
    setValueJson(setting?.valueJson ?? "")
    setFeedback("")
  }

  function saveSetting(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selectedSetting) return
    const mutation = dispatch({ type: "updateSetting", settingId: selectedSetting.id, valueJson, rowVersion: selectedSetting.rowVersion })
    setFeedback(mutation.message)
  }

  return (
    <>
      <PageIntro eyebrow="CONFIGURATION / SCOPED SETTINGS" title="Settings" description="Review explicitly scoped configuration without collapsing workspace, device, edge-host, automation-agent, and operator preferences into one authority." actions={<StatusBadge label={`${snapshot.settings.length} settings`} tone="info" />} />
      <MockNotice>Settings are deterministic mock projections. Safety-critical keys are shown with their typed scope and cannot authorize device control from the browser.</MockNotice>
      <div className="grid gap-6 xl:grid-cols-[minmax(250px,0.7fr)_minmax(0,1.3fr)]"><Panel title="Scoped settings" description="Filter by authority scope, then select one versioned setting."><div className="mb-4"><FieldLabel htmlFor="settings-scope">Scope filter</FieldLabel><select id="settings-scope" value={scopeFilter} onChange={(event) => setScopeFilter(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20"><option value="all">All scopes</option><option value="workspace">Workspace</option><option value="control_plane">Control plane</option><option value="edge_host">Edge host</option><option value="device">Device</option><option value="automation_agent">Automation agent</option><option value="operator_preference">Operator preference</option></select></div><div className="space-y-2">{settings.length === 0 ? <p className="text-xs text-muted-foreground">No settings in this scope.</p> : settings.map((setting) => <button key={setting.id} type="button" onClick={() => selectSetting(setting.id)} className={`w-full border p-3 text-left transition-colors focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30 ${setting.id === selectedSettingId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><div className="flex items-start justify-between gap-2"><div><p className="text-xs font-medium">{setting.key}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{setting.scope} · v{setting.rowVersion}</p></div><StatusBadge label={setting.state} tone={setting.state === "active" ? "healthy" : "neutral"} /></div><p className="mt-2 truncate text-[11px] text-muted-foreground">{setting.valueSummary}</p></button>)}</div></Panel><Panel title="Setting detail" description="Updates use row-version optimistic concurrency and keep the value typed as JSON.">{selectedSetting ? <form className="space-y-4" onSubmit={saveSetting}><div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4"><div><p className="text-sm font-semibold">{selectedSetting.key}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{selectedSetting.id} · target {selectedSetting.targetId}</p></div><div className="flex items-center gap-2">{selectedSetting.safetyCritical ? <StatusBadge label="Safety critical" tone="attention" /> : <StatusBadge label="Low risk preference" tone="neutral" />}<StatusBadge label={`row v${selectedSetting.rowVersion}`} tone="info" /></div></div><dl className="grid gap-3 text-xs sm:grid-cols-2"><div><dt className="text-muted-foreground">Scope</dt><dd className="mt-1 font-medium">{selectedSetting.scope}</dd></div><div><dt className="text-muted-foreground">State</dt><dd className="mt-1 font-medium">{selectedSetting.state}</dd></div></dl><div><FieldLabel htmlFor="setting-value">Value JSON</FieldLabel><textarea id="setting-value" value={valueJson} onChange={(event) => setValueJson(event.target.value)} rows={5} className="mt-1 w-full rounded-none border border-input bg-background p-3 font-mono text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" aria-describedby="setting-value-help" /><p id="setting-value-help" className="mt-1 text-[10px] text-muted-foreground">The mock client validates JSON and rejects a stale row version.</p></div><div className="flex flex-wrap items-center gap-2"><Button type="submit" disabled={selectedSetting.state === "retired"}><Save className="size-3.5" aria-hidden="true" />Save setting</Button><span aria-live="polite" className="text-xs text-muted-foreground">{feedback}</span></div></form> : <div className="py-8 text-sm text-muted-foreground">Select a setting to inspect its scope and version.</div>}</Panel></div>
      <div className="mt-6 grid gap-6 md:grid-cols-3"><ScopeCard icon={Settings2} label="Organization / workspace" detail="Retention and shared defaults" /><ScopeCard icon={LockKeyhole} label="Control plane" detail="Safety-critical runtime policy" /><ScopeCard icon={Check} label="Operator preference" detail="Low-risk presentation choices" /></div>
    </>
  )
}

function ScopeCard({ icon: Icon, label, detail }: { icon: typeof Settings2; label: string; detail: string }) {
  return <div className="border border-border bg-card p-4"><Icon className="size-4 text-primary" aria-hidden="true" /><p className="mt-3 text-xs font-semibold">{label}</p><p className="mt-1 text-[11px] leading-5 text-muted-foreground">{detail}</p></div>
}
