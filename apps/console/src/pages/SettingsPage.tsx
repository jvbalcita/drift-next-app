import { useMemo, useRef, useState, type FormEvent } from "react"
import { Save } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DispatchIntent, SettingScope, SettingView } from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, FieldLabel, OperatorNotice, PageIntro, StatusBadge } from "./shared"

const scopes: readonly { value: SettingScope; label: string }[] = [
  { value: "workspace", label: "Workspace" },
  { value: "control_plane", label: "Control Plane" },
  { value: "edge_host", label: "Edge Host" },
  { value: "device", label: "Device" },
  { value: "automation_agent", label: "Automation Agent" },
  { value: "operator_preference", label: "Operator Preference" },
]

function scopeForView(view: string): SettingScope | null {
  const candidate = view.replaceAll("-", "_") as SettingScope
  return scopes.some((scope) => scope.value === candidate) ? candidate : null
}

export function SettingsPage({
  snapshot,
  dispatch,
  view = "workspace",
  onViewChange,
}: {
  snapshot: ControlPlaneSnapshot
  dispatch: DispatchIntent
  view?: string
  onViewChange?: (view: string) => void
}) {
  const activeScope = scopeForView(view) ?? "workspace"
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [valueJson, setValueJson] = useState("")
  const [error, setError] = useState("")
  const [feedback, setFeedback] = useState("")
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const errorSummaryRef = useRef<HTMLDivElement>(null)
  const selected = snapshot.settings.find((setting) => setting.id === selectedId)
  const settings = useMemo(
    () => snapshot.settings.filter((setting) => setting.scope === activeScope),
    [activeScope, snapshot.settings],
  )
  const pagedSettings = settings.slice(page * pageSize, (page + 1) * pageSize)

  function open(setting: SettingView) {
    setSelectedId(setting.id)
    setValueJson(setting.valueJson)
    setError("")
    setFeedback("")
  }

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selected) return
    const validationError = validateSettingValue(selected, valueJson)
    if (validationError) {
      setError(validationError)
      requestAnimationFrame(() => errorSummaryRef.current?.focus())
      return
    }
    const result = dispatch({ type: "updateSetting", settingId: selected.id, valueJson, rowVersion: selected.rowVersion })
    if (result.ok) {
      setFeedback("Setting saved. The control plane updated the bounded projection.")
      setError("")
      return
    }
    setError(result.message)
    requestAnimationFrame(() => errorSummaryRef.current?.focus())
  }

  return (
    <>
      <PageIntro
        eyebrow="CONFIGURATION / SCOPED SETTINGS"
        title="Settings"
        description="Settings are separated by authority scope. Workspace display preferences are never conflated with safety-critical policy."
        actions={<StatusBadge label={`${snapshot.settings.length} Settings`} tone="info" />}
      />
      <OperatorNotice>All settings are local projections. Browser display preferences cannot authorize device control.</OperatorNotice>

      <Tabs value={view === "history" ? "history" : "settings"} onValueChange={(next) => onViewChange?.(next === "history" ? "history" : activeScope.replaceAll("_", "-"))} className="mt-6">
        <TabsList className="rounded-none border border-border bg-background p-0" aria-label="Settings Views">
          <TabsTrigger value="settings" className="rounded-none">Settings</TabsTrigger>
          <TabsTrigger value="history" className="rounded-none">History</TabsTrigger>
        </TabsList>
        <TabsContent value="settings" className="mt-4">
          <Tabs value={activeScope} onValueChange={(next) => { setPage(0); onViewChange?.(next.replaceAll("_", "-")) }}>
            <TabsList className="h-auto max-w-full flex-wrap justify-start rounded-none border border-border bg-background p-0" aria-label="Setting Scopes">
              {scopes.map((scope) => <TabsTrigger key={scope.value} value={scope.value} className="rounded-none text-xs">{scope.label}</TabsTrigger>)}
            </TabsList>
            <TabsContent value={activeScope} className="mt-4">
              {settings.length === 0 ? <EmptyState label="No Settings" detail="No settings are available for this scope." /> : <SettingsTable settings={pagedSettings} onOpen={open} />}
              <DataTablePagination page={page} pageSize={pageSize} total={settings.length} onPageChange={setPage} onPageSizeChange={(nextSize) => { setPageSize(nextSize); setPage(0) }} />
            </TabsContent>
          </Tabs>
        </TabsContent>
        <TabsContent value="history" className="mt-4">
          <HistoryTable entries={snapshot.settingHistory} />
        </TabsContent>
      </Tabs>

      <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelectedId(null)}>
        <SheetContent className="w-full rounded-none sm:max-w-xl">
          <SheetHeader className="border-b border-border">
            <SheetTitle>{selected?.key ?? "Setting"}</SheetTitle>
            <SheetDescription>Scope: {selected?.scope ?? "—"}. Updates retain typed validation and optimistic row versioning.</SheetDescription>
          </SheetHeader>
          {selected ? <form onSubmit={save} className="space-y-4 p-4">
            {error ? <div ref={errorSummaryRef} tabIndex={-1} role="alert" className="border-l-2 border-destructive bg-destructive/10 p-3 text-xs text-foreground">{error}</div> : null}
            <div className="flex flex-wrap gap-2"><StatusBadge label={selected.risk === "safety_critical" ? "Safety Critical" : "Low-Risk Preference"} tone={selected.risk === "safety_critical" ? "attention" : "neutral"} /><StatusBadge label={selected.valueKind} tone="info" /></div>
            <div>
              <FieldLabel htmlFor="setting-value">Value JSON</FieldLabel>
              <textarea id="setting-value" value={valueJson} onChange={(event) => { setValueJson(event.target.value); setError("") }} aria-invalid={Boolean(error)} aria-describedby={error ? "setting-value-error setting-value-help" : "setting-value-help"} rows={6} className="mt-1 w-full rounded-none border border-input bg-background p-3 font-mono text-xs" />
              {error ? <p id="setting-value-error" className="mt-1 text-xs text-destructive">{error}</p> : null}
              <p id="setting-value-help" className="mt-1 text-[11px] text-muted-foreground">Allowed values: {settingConstraints(selected)}</p>
            </div>
            <Button type="submit" disabled={selected.state === "retired"}><Save className="size-3.5" aria-hidden="true" />Save Setting</Button>
            {feedback ? <p role="status" className="text-xs text-muted-foreground">{feedback}</p> : null}
          </form> : null}
        </SheetContent>
      </Sheet>
    </>
  )
}

function SettingsTable({ settings, onOpen }: { settings: readonly SettingView[]; onOpen: (setting: SettingView) => void }) {
  return <ScrollArea className="border border-border"><table className="w-full min-w-[760px] text-left text-xs"><caption className="sr-only">Settings for the selected scope</caption><thead className="sticky top-0 bg-background"><tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground"><th className="p-3">Setting</th><th className="p-3">State</th><th className="p-3">Risk</th><th className="p-3">Value</th><th className="p-3">Version</th><th className="p-3"><span className="sr-only">Actions</span></th></tr></thead><tbody>{settings.map((setting) => <tr key={setting.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50"><td className="p-3 font-medium">{setting.key}</td><td className="p-3"><StatusBadge label={setting.state} tone={setting.state === "active" ? "healthy" : "neutral"} /></td><td className="p-3"><StatusBadge label={setting.risk === "safety_critical" ? "Safety Critical" : "Preference"} tone={setting.risk === "safety_critical" ? "attention" : "neutral"} /></td><td className="p-3 text-muted-foreground">{setting.valueSummary}</td><td className="drift-data p-3 text-[10px]">v{setting.rowVersion}</td><td className="p-3 text-right"><Button size="sm" variant="outline" onClick={() => onOpen(setting)}>Edit</Button></td></tr>)}</tbody></table></ScrollArea>
}

function HistoryTable({ entries }: { entries: ControlPlaneSnapshot["settingHistory"] }) {
  return <ScrollArea className="border border-border"><table className="w-full min-w-[760px] text-left text-xs"><caption className="sr-only">Setting change history</caption><thead><tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground"><th className="p-3">Setting</th><th className="p-3">Value</th><th className="p-3">Scope</th><th className="p-3">Actor</th><th className="p-3">Changed</th></tr></thead><tbody>{entries.map((entry) => <tr key={entry.id} className="border-b border-border/70 last:border-0"><td className="p-3 font-medium">{entry.key}</td><td className="p-3 font-mono text-[11px]">{entry.valueJson}</td><td className="p-3">{entry.scope.replaceAll("_", " ")}</td><td className="p-3">{entry.actorType}</td><td className="p-3">{entry.changedAt}</td></tr>)}</tbody></table></ScrollArea>
}

function settingConstraints(setting: SettingView) {
  if (setting.allowedValues) return setting.allowedValues.join(", ")
  if (setting.minValue !== undefined && setting.maxValue !== undefined) return `${setting.minValue}–${setting.maxValue}`
  return "Valid JSON required"
}

function validateSettingValue(setting: SettingView, valueJson: string) {
  let parsed: unknown
  try { parsed = JSON.parse(valueJson) } catch { return "Enter valid JSON before saving." }
  if (setting.valueKind === "boolean" && typeof parsed !== "boolean") return "Enter a JSON boolean: true or false."
  if (setting.valueKind === "integer" && (typeof parsed !== "number" || !Number.isInteger(parsed) || (setting.minValue !== undefined && parsed < setting.minValue) || (setting.maxValue !== undefined && parsed > setting.maxValue))) return `Enter an integer from ${setting.minValue ?? "the allowed minimum"} to ${setting.maxValue ?? "the allowed maximum"}.`
  if (setting.valueKind === "enum" && (typeof parsed !== "string" || !setting.allowedValues?.includes(parsed))) return `Choose one of: ${setting.allowedValues?.join(", ") ?? "the allowed values"}.`
  return ""
}
