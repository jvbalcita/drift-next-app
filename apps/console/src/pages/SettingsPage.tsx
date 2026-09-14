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
      <div className="grid gap-6 xl:grid-cols-[minmax(250px,0.7fr)_minmax(0,1.3fr)]"><Panel title="Scoped settings" description="Filter by authority scope, then select one versioned setting."><div className="mb-4"><FieldLabel htmlFor="settings-scope">Scope filter</FieldLabel><select id="settings-scope" value={scopeFilter} onChange={(event) => setScopeFilter(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20"><option value="all">All scopes</option><option value="workspace">Workspace</option><option value="control_plane">Control plane</option><option value="edge_host">Edge host</option><option value="device">Device</option><option value="automation_agent">Automation agent</option><option value="operator_preference">Operator preference</option></select></div><div className="space-y-2">{settings.length === 0 ? <p className="text-xs text-muted-foreground">No settings in this scope.</p> : settings.map((setting) => <button key={setting.id} type="button" onClick={() => selectSetting(setting.id)} className={`w-full border p-3 text-left transition-colors focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary/30 ${setting.id === selectedSettingId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><div className="flex items-start justify-between gap-2"><div><p className="text-xs font-medium">{setting.key}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{setting.scope} · v{setting.rowVersion}</p></div><StatusBadge label={setting.state} tone={setting.state === "active" ? "healthy" : "neutral"} /></div><p className="mt-2 truncate text-[11px] text-muted-foreground">{setting.valueSummary}</p></button>)}</div></Panel><Panel title="Setting detail" description="Updates use row-version optimistic concurrency and typed value validation.">{selectedSetting ? <form className="space-y-4" onSubmit={saveSetting}><div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4"><div><p className="text-sm font-semibold">{selectedSetting.key}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{selectedSetting.id} · target {selectedSetting.targetId}</p></div><div className="flex items-center gap-2">{selectedSetting.risk === "safety_critical" ? <StatusBadge label="Safety critical" tone="attention" /> : <StatusBadge label="Low risk preference" tone="neutral" />}<StatusBadge label={`${selectedSetting.valueKind} value`} tone="info" /><StatusBadge label={`row v${selectedSetting.rowVersion}`} tone="neutral" /></div></div><dl className="grid gap-3 text-xs sm:grid-cols-2"><div><dt className="text-muted-foreground">Scope</dt><dd className="mt-1 font-medium">{selectedSetting.scope}</dd></div><div><dt className="text-muted-foreground">State</dt><dd className="mt-1 font-medium">{selectedSetting.state}</dd></div><div><dt className="text-muted-foreground">Value kind</dt><dd className="mt-1 font-medium">{selectedSetting.valueKind}</dd></div><div><dt className="text-muted-foreground">Bounds</dt><dd className="mt-1 font-medium">{selectedSetting.allowedValues?.join(", ") ?? (selectedSetting.minValue !== undefined ? `${selectedSetting.minValue}–${selectedSetting.maxValue}` : "service constrained")}</dd></div></dl><TypedSettingEditor setting={selectedSetting} valueJson={valueJson} onChange={setValueJson} /><p id="setting-value-help" className="mt-1 text-[10px] text-muted-foreground">Safety-critical values use typed controls. The mock client validates JSON, bounds, redaction, and row version.</p><div className="flex flex-wrap items-center gap-2"><Button type="submit" disabled={selectedSetting.state === "retired"}><Save className="size-3.5" aria-hidden="true" />Save setting</Button><span aria-live="polite" className="text-xs text-muted-foreground">{feedback}</span></div></form> : <div className="py-8 text-sm text-muted-foreground">Select a setting to inspect its scope and version.</div>}</Panel></div>
      <div className="mt-6"><Panel title="Setting history" description="Every projection value is retained with its actor and scope."><div className="space-y-2">{snapshot.settingHistory.map((change) => <div key={change.id} className="flex flex-wrap items-center justify-between gap-2 border-b border-border/70 py-2 text-[11px] last:border-0"><span><span className="font-medium">{change.key}</span> · {change.valueJson}</span><span className="drift-data text-[10px] text-muted-foreground">{change.scope} · row v{change.rowVersion} · {change.actorType} · {change.changedAt}</span></div>)}</div></Panel></div>
      <div className="mt-6 grid gap-6 md:grid-cols-3"><ScopeCard icon={Settings2} label="Organization / workspace" detail="Retention and shared defaults" /><ScopeCard icon={LockKeyhole} label="Control plane" detail="Safety-critical runtime policy" /><ScopeCard icon={Check} label="Operator preference" detail="Low-risk presentation choices" /></div>
    </>
  )
}

function ScopeCard({ icon: Icon, label, detail }: { icon: typeof Settings2; label: string; detail: string }) {
  return <div className="border border-border bg-card p-4"><Icon className="size-4 text-primary" aria-hidden="true" /><p className="mt-3 text-xs font-semibold">{label}</p><p className="mt-1 text-[11px] leading-5 text-muted-foreground">{detail}</p></div>
}

function TypedSettingEditor({ setting, valueJson, onChange }: { setting: ControlPlaneSnapshot["settings"][number]; valueJson: string; onChange: (value: string) => void }) {
  if (setting.valueKind === "boolean") {
    return <div><FieldLabel htmlFor="setting-value">Value</FieldLabel><label className="mt-1 flex min-h-9 items-center gap-2 border border-input bg-background px-3 text-xs"><input id="setting-value" type="checkbox" checked={valueJson === "true"} onChange={(event) => onChange(JSON.stringify(event.target.checked))} aria-describedby="setting-value-help" />Enabled</label></div>
  }
  if (setting.valueKind === "integer") {
    return <div><FieldLabel htmlFor="setting-value">Value</FieldLabel><input id="setting-value" type="number" value={numberValue(valueJson)} min={setting.minValue} max={setting.maxValue} step={1} onChange={(event) => onChange(event.target.value === "" ? "" : JSON.stringify(Number(event.target.value)))} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 font-mono text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" aria-describedby="setting-value-help" /></div>
  }
  if (setting.valueKind === "enum") {
    return <div><FieldLabel htmlFor="setting-value">Value</FieldLabel><select id="setting-value" value={stringValue(valueJson)} onChange={(event) => onChange(JSON.stringify(event.target.value))} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" aria-describedby="setting-value-help">{setting.allowedValues?.map((value) => <option key={value} value={value}>{value}</option>)}</select></div>
  }
  return <div><FieldLabel htmlFor="setting-value">Value JSON</FieldLabel><textarea id="setting-value" value={valueJson} onChange={(event) => onChange(event.target.value)} rows={5} className="mt-1 w-full rounded-none border border-input bg-background p-3 font-mono text-xs outline-none focus:border-primary focus:ring-2 focus:ring-primary/20" aria-describedby="setting-value-help" /></div>
}

function numberValue(valueJson: string): string {
  try {
    const value: unknown = JSON.parse(valueJson)
    return typeof value === "number" ? String(value) : ""
  } catch {
    return ""
  }
}

function stringValue(valueJson: string): string {
  try {
    const value: unknown = JSON.parse(valueJson)
    return typeof value === "string" ? value : ""
  } catch {
    return ""
  }
}
