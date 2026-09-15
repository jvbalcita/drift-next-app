import { useMemo, useState, type ReactNode } from "react"
import { Archive, Filter, HardDrive, RefreshCw, Search, Trash2 } from "lucide-react"
import { AlertDialog, AlertDialogContent, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type {
  ArtifactCategory,
  ArtifactLifecycleState,
  ArtifactRetentionClass,
  ArtifactView,
  ControlPlaneSnapshot,
  DispatchIntent,
  RecordingMediaView,
} from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, FailureBadge, MockNotice, PageIntro, Panel, StatusBadge, type StatusTone } from "./shared"

type ArtifactLoadState = "ready" | "loading" | "error"

function categoryLabel(category: ArtifactCategory): string {
  switch (category) {
    case "screenshot":
      return "Screenshot"
    case "ui_tree":
      return "UI Tree"
    case "recording":
      return "Recording"
    case "structured_evidence":
      return "Structured Evidence"
    case "other":
      return "Other"
    default: {
      const _exhaustive: never = category
      return _exhaustive
    }
  }
}

function lifecycleLabel(state: ArtifactLifecycleState): string {
  switch (state) {
    case "admitted":
      return "Admitted"
    case "active":
      return "Active"
    case "eligible_for_deletion":
      return "Eligible For Deletion"
    case "deleted":
      return "Deleted"
    case "cleanup_failed":
      return "Cleanup Failure"
    case "omitted":
      return "Omitted"
    case "redacted":
      return "Redacted"
    case "partial":
      return "Partial"
    case "unauthorized":
      return "Unauthorized"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function retentionLabel(retention: ArtifactRetentionClass): string {
  switch (retention) {
    case "execution_evidence":
      return "Execution Evidence"
    case "audit_security":
      return "Audit Security"
    case "disposable":
      return "Disposable"
    default: {
      const _exhaustive: never = retention
      return _exhaustive
    }
  }
}

function lifecycleTone(state: ArtifactLifecycleState): StatusTone {
  switch (state) {
    case "active":
    case "admitted":
      return "healthy"
    case "eligible_for_deletion":
    case "partial":
    case "cleanup_failed":
      return "attention"
    case "unauthorized":
    case "redacted":
      return "danger"
    case "deleted":
    case "omitted":
      return "neutral"
    default: {
      const _exhaustive: never = state
      return _exhaustive
    }
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function ArtifactsPage({
  snapshot,
  dispatch,
  view = "library",
  onViewChange,
}: {
  snapshot: ControlPlaneSnapshot
  dispatch: DispatchIntent
  view?: string
  onViewChange?: (view: string) => void
}) {
  const [loadState, setLoadState] = useState<ArtifactLoadState>("ready")
  const [feedback, setFeedback] = useState("")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedDeviceId, setSelectedDeviceId] = useState(snapshot.devices.find((device) => device.status === "online")?.id ?? snapshot.devices[0]?.id ?? "")
  const [search, setSearch] = useState("")
  const [category, setCategory] = useState<ArtifactCategory | "all">("all")
  const [lifecycle, setLifecycle] = useState<ArtifactLifecycleState | "all">("all")
  const [retention, setRetention] = useState<ArtifactRetentionClass | "all">("all")
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)

  const filtered = useMemo(() => {
    const includes = (value: string, query: string) => value.toLowerCase().includes(query.trim().toLowerCase())
    return snapshot.artifacts.filter((artifact) => {
      if (category !== "all" && artifact.category !== category) return false
      if (lifecycle !== "all" && artifact.lifecycleState !== lifecycle) return false
      if (retention !== "all" && artifact.retentionClass !== retention) return false
      return includes(
        `${artifact.id} ${artifact.contentHash} ${artifact.ownerId} ${artifact.referenceId} ${artifact.sanitizedPreviewLabel}`,
        search,
      )
    })
  }, [category, lifecycle, retention, search, snapshot.artifacts])

  const visible = filtered.slice(page * pageSize, (page + 1) * pageSize)
  const selected = snapshot.artifacts.find((artifact) => artifact.id === selectedId)
  const storage = snapshot.storageHealth
  const deviceArtifacts = snapshot.artifacts.filter(
    (artifact) => artifact.deviceId === selectedDeviceId && artifact.previewKind === "screenshot" && artifact.visibility === "authorized",
  )
  const selectedDevice = snapshot.devices.find((device) => device.id === selectedDeviceId)

  function resetPage() {
    setPage(0)
  }

  function retryLoad() {
    setLoadState("loading")
    window.setTimeout(() => {
      const refresh = dispatch({ type: "refresh" })
      setLoadState(refresh.ok ? "ready" : "error")
      setFeedback(refresh.message)
    }, 120)
  }

  function openArtifact(artifactId: string) {
    const read = dispatch({ type: "readArtifact", artifactId })
    setFeedback(read.message)
    if (read.ok || read.errorCode === "unauthorized") setSelectedId(artifactId)
  }

  return (
    <>
      <PageIntro
        eyebrow="STORAGE / ARTIFACTS"
        title="Artifacts"
        description="Content-addressed artifact metadata, sanitized media previews, retention, and cleanup. Raw filesystem paths and secrets are never shown."
        actions={
          <>
            <StatusBadge label="Sanitized View" tone="info" />
            {storage.quotaWarning ? <StatusBadge label="Quota Warning" tone="attention" /> : <StatusBadge label="Storage Healthy" tone="healthy" />}
          </>
        }
      />
      <MockNotice>
        Artifact bytes stay in the mock CAS projection. Screenshots are sanitized placeholders, UI trees are bounded summaries, and unauthorized content is withheld.
      </MockNotice>

      {storage.quotaWarning ? (
        <div role="status" className="mb-4 border border-border bg-muted/40 p-3 text-xs" aria-label="Storage Quota Warning">
          <p className="font-semibold">Storage Quota Warning</p>
          <p className="mt-1 text-muted-foreground">
            {storage.warningSummary} · {formatBytes(storage.usedBytes)} of {formatBytes(storage.budgetBytes)} · Cleanup Failures {storage.cleanupFailures}
          </p>
        </div>
      ) : null}

      {feedback ? (
        <p role="status" className="mb-4 border border-border bg-muted/30 px-3 py-2 text-xs" aria-live="polite">
          {feedback}
        </p>
      ) : null}

      {loadState === "loading" ? (
        <div role="status" className="border border-border bg-muted/40 px-4 py-8 text-center" aria-label="Loading Artifacts">
          <p className="text-sm font-medium">Loading Artifacts</p>
          <p className="mt-1 text-xs text-muted-foreground">Refreshing the mock artifact projection.</p>
        </div>
      ) : null}

      {loadState === "error" ? (
        <div role="alert" className="border border-border bg-muted/40 px-4 py-8 text-center">
          <p className="text-sm font-medium">Artifacts Unavailable</p>
          <p className="mt-1 text-xs text-muted-foreground">The mock projection failed to refresh.</p>
          <Button size="sm" variant="outline" className="mt-4" onClick={retryLoad}>
            <RefreshCw className="size-3.5" aria-hidden="true" />
            Retry
          </Button>
        </div>
      ) : null}

      {loadState === "ready" ? (
        <Tabs value={view} onValueChange={onViewChange} className="mt-2">
          <TabsList className="h-auto flex-wrap rounded-none border border-border bg-background p-0" aria-label="Artifact Views">
            <TabsTrigger value="library" className="rounded-none">
              Library
            </TabsTrigger>
            <TabsTrigger value="media" className="rounded-none">
              Media
            </TabsTrigger>
            <TabsTrigger value="recordings" className="rounded-none">
              Recordings
            </TabsTrigger>
            <TabsTrigger value="storage" className="rounded-none">
              Storage
            </TabsTrigger>
            <TabsTrigger value="audit" className="rounded-none">
              Audit
            </TabsTrigger>
          </TabsList>

          <TabsContent value="library" className="mt-4">
            <div className="flex flex-wrap items-end gap-3 border-y border-border py-3" aria-label="Artifact Filters">
              <FilterInput id="artifact-search" label="Search" value={search} onChange={(next) => { setSearch(next); resetPage() }} />
              <FilterSelect id="artifact-category" label="Type / Category" value={category} onChange={(next) => { setCategory(next as ArtifactCategory | "all"); resetPage() }}>
                <option value="all">All Categories</option>
                <option value="screenshot">Screenshot</option>
                <option value="ui_tree">UI Tree</option>
                <option value="recording">Recording</option>
                <option value="structured_evidence">Structured Evidence</option>
                <option value="other">Other</option>
              </FilterSelect>
              <FilterSelect id="artifact-lifecycle" label="Lifecycle State" value={lifecycle} onChange={(next) => { setLifecycle(next as ArtifactLifecycleState | "all"); resetPage() }}>
                <option value="all">All States</option>
                <option value="admitted">Admitted</option>
                <option value="active">Active</option>
                <option value="eligible_for_deletion">Eligible For Deletion</option>
                <option value="deleted">Deleted</option>
                <option value="cleanup_failed">Cleanup Failure</option>
                <option value="omitted">Omitted</option>
                <option value="redacted">Redacted</option>
                <option value="partial">Partial</option>
                <option value="unauthorized">Unauthorized</option>
              </FilterSelect>
              <FilterSelect id="artifact-retention" label="Retention Class" value={retention} onChange={(next) => { setRetention(next as ArtifactRetentionClass | "all"); resetPage() }}>
                <option value="all">All Retention Classes</option>
                <option value="execution_evidence">Execution Evidence</option>
                <option value="audit_security">Audit Security</option>
                <option value="disposable">Disposable</option>
              </FilterSelect>
              <Button size="sm" variant="outline" onClick={retryLoad}>
                <RefreshCw className="size-3.5" aria-hidden="true" />
                Refresh
              </Button>
              <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
                <Filter className="size-3.5" aria-hidden="true" />
                {filtered.length} Results
              </span>
            </div>
            <div className="mt-4">
              {filtered.length === 0 ? (
                <EmptyState label="No Artifacts Match" detail="Adjust filters or refresh the mock artifact projection." />
              ) : (
                <ArtifactTable artifacts={visible} onOpen={openArtifact} />
              )}
              <DataTablePagination
                page={page}
                pageSize={pageSize}
                total={filtered.length}
                onPageChange={setPage}
                onPageSizeChange={(nextSize) => {
                  setPageSize(nextSize)
                  setPage(0)
                }}
              />
            </div>
          </TabsContent>

          <TabsContent value="media" className="mt-4 space-y-4">
            <div className="flex flex-wrap items-end gap-3 border-y border-border py-3">
              <label htmlFor="artifact-media-device" className="text-xs font-semibold text-foreground">
                Selected Device
                <select
                  id="artifact-media-device"
                  value={selectedDeviceId}
                  onChange={(event) => setSelectedDeviceId(event.target.value)}
                  className="mt-1 block h-9 rounded-none border border-input bg-background px-2 text-xs"
                >
                  {snapshot.devices.map((device) => (
                    <option key={device.id} value={device.id}>
                      {device.displayName}
                    </option>
                  ))}
                </select>
              </label>
              <StatusBadge label={selectedDevice?.controlEligibility === "eligible" ? "Authorized Device" : "Limited Access"} tone={selectedDevice?.controlEligibility === "eligible" ? "healthy" : "attention"} />
            </div>
            <p className="text-xs text-muted-foreground">
              Grid views use low-resolution sanitized previews. Full-resolution media is limited to the selected authorized device.
            </p>
            {deviceArtifacts.length === 0 ? (
              <EmptyState label="No Media Previews" detail="No authorized screenshot artifacts are available for this device." />
            ) : (
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-label="Low Resolution Media Grid">
                {deviceArtifacts.map((artifact) => (
                  <button
                    key={artifact.id}
                    type="button"
                    onClick={() => openArtifact(artifact.id)}
                    className="border border-border bg-card p-3 text-left focus-visible:outline-2 focus-visible:outline-primary"
                  >
                    <div className="mb-2 aspect-video border border-border bg-muted motion-safe:transition-opacity">
                      {artifact.sanitizedPreviewDataUrl ? (
                        <img src={artifact.sanitizedPreviewDataUrl} alt="" className="size-full object-contain opacity-80" />
                      ) : (
                        <div className="flex size-full items-center justify-center text-[10px] text-muted-foreground">Preview Withheld</div>
                      )}
                    </div>
                    <p className="text-xs font-medium">{artifact.sanitizedPreviewLabel}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">{artifact.contentHash}</p>
                    <div className="mt-2 flex flex-wrap gap-2">
                      <StatusBadge label="Low Res" tone="info" />
                      <StatusBadge label={lifecycleLabel(artifact.lifecycleState)} tone={lifecycleTone(artifact.lifecycleState)} />
                    </div>
                  </button>
                ))}
              </div>
            )}
            {selectedDevice?.controlEligibility === "eligible" && deviceArtifacts[0]?.sanitizedPreviewDataUrl ? (
              <Panel title="Full-Resolution Authorized Preview" description="Shown only for the selected authorized device. Placeholder pixels only — no raw capture.">
                <div className="mx-auto max-w-md border border-border bg-muted p-2">
                  <img
                    src={deviceArtifacts[0].sanitizedPreviewDataUrl}
                    alt={`Full-resolution sanitized preview for ${selectedDevice.displayName}`}
                    className="w-full object-contain"
                  />
                </div>
              </Panel>
            ) : (
              <EmptyState label="Full-Resolution Withheld" detail="Select an authorized online device with an eligible screenshot artifact." />
            )}
          </TabsContent>

          <TabsContent value="recordings" className="mt-4">
            {snapshot.recordingMedia.length === 0 ? (
              <EmptyState label="No Recording Sessions" detail="Recording sessions appear here with bounded lifecycle and cleanup state." />
            ) : (
              <RecordingTable recordings={snapshot.recordingMedia} onOpenArtifact={openArtifact} />
            )}
            {snapshot.recordingMedia.length > 0 ? (
              <p className="mt-3 text-xs text-muted-foreground" aria-live="polite">
                {snapshot.recordingMedia.length} Recording Session{snapshot.recordingMedia.length === 1 ? "" : "s"} In Workspace
              </p>
            ) : null}
          </TabsContent>

          <TabsContent value="storage" className="mt-4 space-y-4">
            <div className="grid gap-3 md:grid-cols-3">
              <Panel title="Used Capacity" description="Local workspace byte budget.">
                <p className="text-2xl font-semibold tabular-nums">{formatBytes(storage.usedBytes)}</p>
                <p className="mt-1 text-xs text-muted-foreground">Of {formatBytes(storage.budgetBytes)}</p>
              </Panel>
              <Panel title="Object Count" description="Metadata rows in the mock projection.">
                <p className="text-2xl font-semibold tabular-nums">{storage.objectCount}</p>
                <p className="mt-1 text-xs text-muted-foreground">Orphan Metadata {storage.orphanMetadataCount} · Orphan Bytes {storage.orphanBytesCount}</p>
              </Panel>
              <Panel title="Cleanup Failures" description="Retention worker outcomes.">
                <p className="text-2xl font-semibold tabular-nums">{storage.cleanupFailures}</p>
                <div className="mt-2">
                  <StatusBadge label={storage.quotaWarning ? "Quota Warning" : "Within Budget"} tone={storage.quotaWarning ? "attention" : "healthy"} />
                </div>
              </Panel>
            </div>
            <div className="flex items-start gap-2 border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
              <HardDrive className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <p>{storage.warningSummary}. Paths and store roots are never exposed in this console.</p>
            </div>
          </TabsContent>

          <TabsContent value="audit" className="mt-4">
            {snapshot.artifactAudits.length === 0 ? (
              <EmptyState label="No Artifact Audit Entries" detail="Reads, deletes, rejected admissions, and cleanup failures appear here." />
            ) : (
              <AuditTable audits={snapshot.artifactAudits} />
            )}
          </TabsContent>
        </Tabs>
      ) : null}

      <Sheet open={Boolean(selected)} onOpenChange={(open) => !open && setSelectedId(null)}>
        <SheetContent className="w-full rounded-none sm:max-w-xl">
          <SheetHeader className="border-b border-border">
            <SheetTitle>{selected ? categoryLabel(selected.category) : "Artifact Detail"}</SheetTitle>
            <SheetDescription>Sanitized metadata, ownership, retention, and preview states.</SheetDescription>
          </SheetHeader>
          {selected ? (
            <div className="space-y-4 p-4 text-xs">
              <div className="flex flex-wrap gap-2">
                <StatusBadge label={lifecycleLabel(selected.lifecycleState)} tone={lifecycleTone(selected.lifecycleState)} />
                <StatusBadge label={retentionLabel(selected.retentionClass)} tone="info" />
                <StatusBadge label={selected.deletionEligible ? "Deletion Eligible" : "Protected"} tone={selected.deletionEligible ? "attention" : "neutral"} />
                <FailureBadge failureClass={selected.failureClass} />
              </div>
              <dl className="grid gap-3 sm:grid-cols-2">
                <Detail label="Artifact ID" value={selected.id} mono />
                <Detail label="Content Hash" value={selected.contentHash} mono />
                <Detail label="Owner" value={`${selected.ownerType} / ${selected.ownerId}`} />
                <Detail label="Reference" value={`${selected.referenceType} / ${selected.referenceId}`} />
                <Detail label="Device" value={selected.deviceId ?? "None"} />
                <Detail label="Size" value={formatBytes(selected.sizeBytes)} />
                <Detail label="Created" value={selected.createdAt} />
                <Detail label="Visibility" value={selected.visibility.replaceAll("_", " ")} />
              </dl>
              {selected.protectedReason ? <p className="border border-border bg-muted/40 p-3 text-muted-foreground">{selected.protectedReason}</p> : null}

              <section aria-label="Sanitized Preview">
                <h3 className="mb-2 text-xs font-semibold">Sanitized Preview</h3>
                {selected.visibility === "unauthorized" ? (
                  <EmptyState label="Unauthorized" detail="Artifact content is withheld for this operator." />
                ) : selected.visibility === "redacted" ? (
                  <EmptyState label="Redacted" detail="Preview pixels and tree text were redacted at admission." />
                ) : selected.visibility === "omitted" || selected.lifecycleState === "omitted" || selected.lifecycleState === "deleted" ? (
                  <EmptyState label="Omitted" detail="Bytes are omitted from the console projection." />
                ) : selected.previewKind === "ui_tree" ? (
                  <div className="border border-border bg-muted/40 p-3">
                    <p className="font-medium">UI Tree / Structured Evidence</p>
                    <p className="drift-data mt-2 text-[10px] text-muted-foreground">{selected.uiTreeSummary ?? "Bounded node summary retained; raw hierarchy omitted."}</p>
                  </div>
                ) : selected.sanitizedPreviewDataUrl ? (
                  <div className="border border-border bg-muted p-2">
                    <img src={selected.sanitizedPreviewDataUrl} alt={selected.sanitizedPreviewLabel} className="mx-auto max-h-48 object-contain" />
                    <p className="mt-2 text-center text-[10px] text-muted-foreground">{selected.sanitizedPreviewLabel}</p>
                  </div>
                ) : (
                  <EmptyState label="No Preview" detail={selected.sanitizedPreviewLabel} />
                )}
              </section>

              {selected.recordingSessionId ? (
                <section aria-label="Recording Session Metadata">
                  <h3 className="mb-2 text-xs font-semibold">Recording Session</h3>
                  <Detail label="Session ID" value={selected.recordingSessionId} mono />
                </section>
              ) : null}

              <div className="flex flex-wrap gap-2">
                <AlertDialog>
                  <AlertDialogTrigger
                    render={
                      <Button size="sm" variant="outline" disabled={!selected.deletionEligible}>
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        Delete Artifact
                      </Button>
                    }
                  />
                  <AlertDialogContent
                    title="Delete Artifact?"
                    description="Deletes eligible artifact metadata after confirmation. Protected retention classes and active references remain blocked. No filesystem paths are exposed."
                    confirmLabel="Confirm Delete"
                    onConfirm={() => {
                      const outcome = dispatch({ type: "deleteArtifact", artifactId: selected.id, confirmed: true })
                      setFeedback(outcome.message)
                      if (outcome.ok) setSelectedId(null)
                    }}
                  />
                </AlertDialog>
                <AlertDialog>
                  <AlertDialogTrigger
                    render={
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={selected.lifecycleState !== "cleanup_failed" && selected.lifecycleState !== "eligible_for_deletion"}
                      >
                        <Archive className="size-3.5" aria-hidden="true" />
                        Retry Cleanup
                      </Button>
                    }
                  />
                  <AlertDialogContent
                    title="Retry Cleanup?"
                    description="Runs retention cleanup for an eligible or failed-cleanup artifact after confirmation. Failures remain visible in audit."
                    confirmLabel="Confirm Cleanup"
                    onConfirm={() => {
                      const outcome = dispatch({ type: "cleanupArtifact", artifactId: selected.id, confirmed: true })
                      setFeedback(outcome.message)
                      if (outcome.ok) setSelectedId(null)
                    }}
                  />
                </AlertDialog>
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </>
  )
}

function FilterSelect({
  id,
  label,
  value,
  onChange,
  children,
}: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  children: ReactNode
}) {
  return (
    <label htmlFor={id} className="text-xs font-semibold text-foreground">
      {label}
      <select id={id} value={value} onChange={(event) => onChange(event.target.value)} className="mt-1 block h-9 rounded-none border border-input bg-background px-2 text-xs text-foreground">
        {children}
      </select>
    </label>
  )
}

function FilterInput({ id, label, value, onChange }: { id: string; label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label htmlFor={id} className="text-xs font-semibold text-foreground">
      {label}
      <span className="relative mt-1 block">
        <Search className="absolute left-2 top-2.5 size-3.5 text-muted-foreground" aria-hidden="true" />
        <input
          id={id}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={`Filter ${label.toLowerCase()}`}
          className="h-9 rounded-none border border-input bg-background pl-7 pr-2 text-xs text-foreground"
        />
      </span>
    </label>
  )
}

function ArtifactTable({ artifacts, onOpen }: { artifacts: readonly ArtifactView[]; onOpen: (id: string) => void }) {
  return (
    <div className="overflow-x-auto border border-border">
      <table className="w-full min-w-[960px] text-left text-xs">
        <caption className="sr-only">Artifact library</caption>
        <thead>
          <tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground">
            <th className="p-3">Artifact</th>
            <th className="p-3">Category</th>
            <th className="p-3">Lifecycle</th>
            <th className="p-3">Retention</th>
            <th className="p-3">Hash</th>
            <th className="p-3">Ownership</th>
            <th className="p-3">Eligibility</th>
          </tr>
        </thead>
        <tbody>
          {artifacts.map((artifact) => (
            <tr key={artifact.id} className="border-b border-border/70 hover:bg-muted/50">
              <td className="p-3">
                <button type="button" onClick={() => onOpen(artifact.id)} className="text-left font-medium underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-primary">
                  {artifact.sanitizedPreviewLabel}
                </button>
                <p className="drift-data mt-1 text-[10px] text-muted-foreground">{artifact.id}</p>
              </td>
              <td className="p-3">{categoryLabel(artifact.category)}</td>
              <td className="p-3">
                <StatusBadge label={lifecycleLabel(artifact.lifecycleState)} tone={lifecycleTone(artifact.lifecycleState)} />
              </td>
              <td className="p-3">{retentionLabel(artifact.retentionClass)}</td>
              <td className="drift-data p-3 text-[10px]">{artifact.contentHash}</td>
              <td className="p-3">
                <p>{artifact.ownerType}</p>
                <p className="drift-data mt-1 text-[10px] text-muted-foreground">{artifact.referenceType} · {artifact.referenceId}</p>
              </td>
              <td className="p-3">
                <StatusBadge label={artifact.deletionEligible ? "Eligible" : "Protected"} tone={artifact.deletionEligible ? "attention" : "neutral"} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function RecordingTable({
  recordings,
  onOpenArtifact,
}: {
  recordings: readonly RecordingMediaView[]
  onOpenArtifact: (id: string) => void
}) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = recordings.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      <div className="overflow-x-auto border border-border">
        <table className="w-full min-w-[820px] text-left text-xs">
          <caption className="sr-only">Recording session metadata</caption>
          <thead>
            <tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground">
              <th className="p-3">Session</th>
              <th className="p-3">Device</th>
              <th className="p-3">State</th>
              <th className="p-3">Duration</th>
              <th className="p-3">Preview</th>
              <th className="p-3">Full Res</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((recording) => (
              <tr key={recording.id} className="border-b border-border/70">
                <td className="p-3">
                  {recording.artifactId ? (
                    <button type="button" className="font-medium underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-primary" onClick={() => onOpenArtifact(recording.artifactId!)}>
                      {recording.sessionId}
                    </button>
                  ) : (
                    <span className="font-medium">{recording.sessionId}</span>
                  )}
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {recording.startedAt}
                    {recording.endedAt ? ` → ${recording.endedAt}` : ""}
                  </p>
                </td>
                <td className="p-3">{recording.deviceId}</td>
                <td className="p-3">
                  <StatusBadge label={recording.state.replaceAll("_", " ")} tone={recording.state === "completed" ? "healthy" : recording.state === "recording" ? "info" : "attention"} />
                  <FailureBadge failureClass={recording.failureClass} />
                </td>
                <td className="p-3">{recording.durationMs !== undefined ? `${Math.round(recording.durationMs / 1000)} s` : "—"}</td>
                <td className="p-3">{recording.lowResPreviewLabel}</td>
                <td className="p-3">
                  <StatusBadge label={recording.fullResAuthorized ? "Authorized" : "Withheld"} tone={recording.fullResAuthorized ? "healthy" : "neutral"} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <DataTablePagination
        page={page}
        pageSize={pageSize}
        total={recordings.length}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next)
          setPage(0)
        }}
      />
    </>
  )
}

function AuditTable({ audits }: { audits: ControlPlaneSnapshot["artifactAudits"] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = audits.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      <div className="overflow-x-auto border border-border">
        <table className="w-full min-w-[820px] text-left text-xs">
          <caption className="sr-only">Artifact audit ledger</caption>
          <thead>
            <tr className="border-b border-border text-[10px] tracking-[.08em] text-muted-foreground">
              <th className="p-3">Time</th>
              <th className="p-3">Action</th>
              <th className="p-3">Artifact</th>
              <th className="p-3">Actor</th>
              <th className="p-3">Outcome</th>
              <th className="p-3">Summary</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((audit) => (
              <tr key={audit.id} className="border-b border-border/70">
                <td className="p-3 text-muted-foreground">{audit.occurredAt}</td>
                <td className="p-3">{audit.action.replaceAll("_", " ")}</td>
                <td className="drift-data p-3 text-[10px]">{audit.artifactId}</td>
                <td className="p-3">{audit.actor}</td>
                <td className="p-3">
                  <StatusBadge label={audit.outcome} tone={audit.outcome === "accepted" ? "healthy" : audit.outcome === "rejected" ? "attention" : "danger"} />
                  <FailureBadge failureClass={audit.failureClass} />
                </td>
                <td className="p-3 text-muted-foreground">{audit.summary}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <DataTablePagination
        page={page}
        pageSize={pageSize}
        total={audits.length}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next)
          setPage(0)
        }}
      />
    </>
  )
}

function Detail({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={`mt-1 ${mono ? "drift-data text-[10px]" : ""}`}>{value}</dd>
    </div>
  )
}
