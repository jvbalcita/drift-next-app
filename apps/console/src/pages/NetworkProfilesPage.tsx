import { useRef, useState, type FormEvent, type RefObject } from "react"
import { AlertTriangle, Check, Pencil, Plus, Radar, Save, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { AlertDialog, AlertDialogContent, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { ControlPlaneSnapshot, DispatchIntent, NetworkProfileView, ObservedDeviceView, ScanRunView } from "@/lib/domain/control-plane"
import { DataTablePagination, EmptyState, FieldLabel, FailureBadge, FormSelect, OperatorNotice, PageIntro, Panel, StatusBadge } from "./shared"

const newProfileId = "new"

type ProfileForm = {
  name: string
  start: IPv4Octets
  end: IPv4Octets
  ports: string
  isDefault: boolean
}

export function NetworkProfilesPage({ snapshot, dispatch, view = "profiles", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const profiles = snapshot.networkProfiles
  // The catalog owns the selection: an explicit choice holds while that profile
  // still exists, then the default profile, then the first one. Nothing is
  // frozen at mount, so a profile added or deleted in this session stays
  // scannable without a refresh, and a stale selection cannot disable a scan.
  const [chosenProfileId, setChosenProfileId] = useState<string | null>(null)
  const [creatingNew, setCreatingNew] = useState(false)
  const selectedProfile = profiles.find((profile) => profile.id === chosenProfileId) ?? profiles.find((profile) => profile.isDefault) ?? profiles[0]

  // The form follows the profile the dialog is editing, including when the
  // catalog changes underneath it. Adjusting state during render is deliberate:
  // an effect would paint a frame of the previous profile's values.
  const formTargetId = creatingNew ? newProfileId : (selectedProfile?.id ?? newProfileId)
  const [form, setForm] = useState<ProfileForm>(() => profileForm(creatingNew ? undefined : selectedProfile))
  const [loadedFormId, setLoadedFormId] = useState(formTargetId)
  if (loadedFormId !== formTargetId) {
    setLoadedFormId(formTargetId)
    setForm(profileForm(creatingNew ? undefined : selectedProfile))
  }

  const [saveError, setSaveError] = useState("")
  const [profileErrors, setProfileErrors] = useState<{
    name?: string
    addressPolicy?: string
    ports?: string
  }>({})
  const profileErrorSummaryRef = useRef<HTMLDivElement>(null)
  const saveErrorRef = useRef<HTMLDivElement>(null)
  const [profileDialogOpen, setProfileDialogOpen] = useState(false)
  const [scanDialogOpen, setScanDialogOpen] = useState(false)
  const [focusedRunId, setFocusedRunId] = useState<string | null>(null)

  const observationsByRun = new Map<string, ObservedDeviceView[]>()
  for (const observation of snapshot.scanObservations) {
    observationsByRun.set(observation.scanRunId, [...(observationsByRun.get(observation.scanRunId) ?? []), observation])
  }
  const observedRuns = snapshot.scanRuns.filter((run) => observationsByRun.has(run.id))
  const focusedRun = snapshot.scanRuns.find((run) => run.id === focusedRunId) ?? mostRecentRun(observedRuns) ?? mostRecentRun(snapshot.scanRuns)
  const observedDevices = focusedRun ? (observationsByRun.get(focusedRun.id) ?? []) : []

  function selectProfile(profile: NetworkProfileView) {
    setCreatingNew(false)
    setChosenProfileId(profile.id)
    setProfileErrors({})
    setSaveError("")
  }
  function chooseProfile(profileId: string) {
    setCreatingNew(false)
    setChosenProfileId(profileId)
    setProfileErrors({})
    setSaveError("")
  }
  function beginNewProfile() {
    setCreatingNew(true)
    setProfileErrors({})
    setSaveError("")
    setProfileDialogOpen(true)
  }
  function parsedPorts() {
    return form.ports
      .split(",")
      .map((value) => Number(value.trim()))
      .filter((value) => Number.isInteger(value) && value > 0 && value <= 65535)
  }
  async function saveProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaveError("")
    const parsed = parsedPorts()
    const range = `${form.start.join(".")}-${form.end.join(".")}`
    const errors = {
      name: form.name.trim() ? undefined : "Enter a profile name.",
      addressPolicy: validIPv4Range(form.start, form.end) ? undefined : "Enter a valid IPv4 range. Each octet must be 0 through 255, and the start must not exceed the end.",
      ports: parsed.length > 0 ? undefined : "Enter one or more ports from 1 through 65535.",
    }
    setProfileErrors(errors)
    if (Object.values(errors).some(Boolean)) {
      requestAnimationFrame(() => profileErrorSummaryRef.current?.focus())
      return
    }
    const updating = creatingNew ? undefined : selectedProfile
    const mutation = updating
      ? await dispatch({
          type: "updateNetworkProfile",
          profileId: updating.id,
          name: form.name,
          addressPolicy: range,
          ports: parsed,
          isDefault: form.isDefault,
        })
      : await dispatch({
          type: "createNetworkProfile",
          name: form.name,
          addressPolicy: range,
          ports: parsed,
          isDefault: form.isDefault,
        })
    if (mutation.ok) {
      toast.success(updating ? "Network profile updated" : "Network profile created", { description: mutation.message })
      setProfileDialogOpen(false)
      setCreatingNew(false)
      setChosenProfileId(mutation.resourceId ?? selectedProfile?.id ?? null)
      return
    }
    setSaveError(mutation.message)
    requestAnimationFrame(() => saveErrorRef.current?.focus())
  }
  async function deleteProfile(profile: NetworkProfileView) {
    const result = await dispatch({ type: "deleteNetworkProfile", profileId: profile.id, confirmed: true })
    if (result.ok) toast.success("Network profile deleted", { description: result.message })
    else toast.error("Profile deletion failed", { description: result.message })
  }
  async function startScan() {
    if (!selectedProfile) return
    const result = await dispatch({ type: "startScan", profileId: selectedProfile.id })
    if (!result.ok) { toast.error("Discovery scan failed", { description: result.message }); return }
    toast.success("Discovery scan started", { description: result.message })
    setScanDialogOpen(false)
    // Focus the run this scan created; the catalog reorders as it completes.
    setFocusedRunId(result.resourceId ?? null)
  }

  return (
    <>
      <PageIntro
        eyebrow="DISCOVERY / NETWORK PROFILES"
        title="Network Profiles"
        description="Define bounded, non-authoritative discovery policy separately from scan execution."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" onClick={beginNewProfile}>
              <Plus className="size-3.5" aria-hidden="true" />
              New Profile
            </Button>
            <Button size="sm" onClick={() => setScanDialogOpen(true)} disabled={!selectedProfile}>
              <Radar className="size-3.5" aria-hidden="true" />
              Configure Scan
            </Button>
          </div>
        }
      />
      <OperatorNotice>Enter the IPv4 range used by your devices. Saving or scanning does not add a device; a scan records what it observed against the devices already known to this workspace.</OperatorNotice>
      <Tabs value={view} onValueChange={onViewChange} className="mt-6">
        <TabsList aria-label="Network profile views">
          <TabsTrigger value="profiles">Profiles</TabsTrigger>
          <TabsTrigger value="scans">Discovery Scans</TabsTrigger>
          <TabsTrigger value="endpoints">Registered Endpoints</TabsTrigger>
        </TabsList>
        <TabsContent value="profiles" className="mt-6">
          <Panel title="Profile Catalog" description="Profile definitions constrain discovery; they do not establish an endpoint.">
            <ProfileTable
              profiles={profiles}
              onEdit={(profile) => {
                selectProfile(profile)
                setProfileDialogOpen(true)
              }}
              onDelete={deleteProfile}
            />
          </Panel>
        </TabsContent>
        <TabsContent value="scans" className="mt-6">
          <Panel
            title="Discovery Scans"
            description="A scan is an explicit intent against a saved profile; it records the devices it observed. Every attempted scan keeps its terminal outcome. Deleting a profile clears the profile reference without removing the run."
            action={
              <Button size="sm" onClick={() => setScanDialogOpen(true)} disabled={!selectedProfile}>
                <Radar className="size-3.5" aria-hidden="true" />
                Configure Scan
              </Button>
            }
          >
            <ScanTable runs={snapshot.scanRuns} />
            {focusedRun ? <ObservedDevicePanel run={focusedRun} observations={observedDevices} devices={snapshot.devices} /> : null}
          </Panel>
        </TabsContent>
        <TabsContent value="endpoints" className="mt-6">
          <Panel title="Registered Endpoints" description="Each device's current endpoint. The endpoint is the mutable half of a device: the transport a device left stays as its own endpoint history, dated with when it was superseded, and is not listed or counted here.">
            <EndpointTable endpoints={snapshot.endpoints} devices={snapshot.devices} />
          </Panel>
        </TabsContent>
      </Tabs>
      <Dialog open={profileDialogOpen} onOpenChange={setProfileDialogOpen}>
        <DialogContent className="rounded-none sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{formTargetId === newProfileId ? "Create Network Profile" : "Edit Network Profile"}</DialogTitle>
            <DialogDescription>Define the IPv4 range and ports used when discovering device endpoints.</DialogDescription>
          </DialogHeader>
          <form className="grid gap-4 sm:grid-cols-2" noValidate onSubmit={saveProfile}>
            {saveError ? <SaveFailureNotice noticeRef={saveErrorRef} message={saveError} /> : null}
            {Object.values(profileErrors).some(Boolean) ? (
              <div ref={profileErrorSummaryRef} tabIndex={-1} role="alert" className="border border-destructive p-3 text-xs text-destructive sm:col-span-2">
                <p className="font-medium">Correct the highlighted fields before saving.</p>
                <ul className="mt-1 list-disc pl-4">
                  {Object.values(profileErrors)
                    .filter(Boolean)
                    .map((error) => (
                      <li key={error}>{error}</li>
                    ))}
                </ul>
              </div>
            ) : null}
            <div className="sm:col-span-2">
              <FieldLabel htmlFor="profile-name">Profile Name</FieldLabel>
              <Input
                id="profile-name"
                value={form.name}
                onChange={(event) => {
                  setForm((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                  setProfileErrors((current) => ({
                    ...current,
                    name: undefined,
                  }))
                }}
                aria-invalid={Boolean(profileErrors.name)}
                aria-describedby={profileErrors.name ? "profile-name-error" : undefined}
              />
              {profileErrors.name ? (
                <p id="profile-name-error" className="mt-1 text-xs text-destructive">
                  {profileErrors.name}
                </p>
              ) : null}
            </div>
            <IPv4RangeFields
              start={form.start}
              end={form.end}
              error={profileErrors.addressPolicy}
              onStartChange={(next) => {
                setForm((current) => ({ ...current, start: next }))
                setProfileErrors((current) => ({
                  ...current,
                  addressPolicy: undefined,
                }))
              }}
              onEndChange={(next) => {
                setForm((current) => ({ ...current, end: next }))
                setProfileErrors((current) => ({
                  ...current,
                  addressPolicy: undefined,
                }))
              }}
            />
            <div>
              <FieldLabel htmlFor="profile-ports">Ports</FieldLabel>
              <Input
                id="profile-ports"
                value={form.ports}
                onChange={(event) => {
                  setForm((current) => ({
                    ...current,
                    ports: event.target.value,
                  }))
                  setProfileErrors((current) => ({
                    ...current,
                    ports: undefined,
                  }))
                }}
                aria-invalid={Boolean(profileErrors.ports)}
                aria-describedby={profileErrors.ports ? "profile-ports-error" : undefined}
                className="font-mono"
              />
              {profileErrors.ports ? (
                <p id="profile-ports-error" className="mt-1 text-xs text-destructive">
                  {profileErrors.ports}
                </p>
              ) : null}
            </div>
            <label className="flex items-center gap-2 text-xs sm:col-span-2">
              <Checkbox
                checked={form.isDefault}
                onCheckedChange={(checked) =>
                  setForm((current) => ({
                    ...current,
                    isDefault: checked,
                  }))
                }
              />
              Use as Default Discovery Policy
            </label>
            <DialogFooter className="sm:col-span-2">
              <Button type="submit">
                <Save className="size-3.5" aria-hidden="true" />
                Save Profile
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={scanDialogOpen} onOpenChange={setScanDialogOpen}>
        <DialogContent className="rounded-none">
          <DialogHeader>
            <DialogTitle>Configure discovery scan</DialogTitle>
            <DialogDescription>Runs only against the selected bounded profile. No external discovery or endpoint registration occurs.</DialogDescription>
          </DialogHeader>
          <div>
            <FieldLabel htmlFor="scan-profile">Discovery profile</FieldLabel>
            <FormSelect id="scan-profile" ariaLabel="Discovery profile" value={selectedProfile?.id ?? ""} disabled={profiles.length === 0} onValueChange={chooseProfile} className="mt-1" options={profiles.length === 0 ? [{ value: "", label: "No saved profile", disabled: true }] : profiles.map((profile) => ({ value: profile.id, label: profile.isDefault ? `${profile.name} (default)` : profile.name }))} />
          </div>
          <dl className="border-y border-border py-3 text-xs">
            <div className="flex justify-between gap-4">
              <dt className="text-muted-foreground">Profile</dt>
              <dd>{selectedProfile?.name ?? "None selected"}</dd>
            </div>
            <div className="mt-2 flex justify-between gap-4">
              <dt className="text-muted-foreground">Address policy</dt>
              <dd className="font-mono">{selectedProfile?.addressPolicy ?? "—"}</dd>
            </div>
          </dl>
          <DialogFooter>
            <Button onClick={startScan} disabled={!selectedProfile}>
              <Radar className="size-3.5" aria-hidden="true" />
              Start scan
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function profileForm(profile: NetworkProfileView | undefined): ProfileForm {
  if (!profile)
    return {
      name: "",
      start: defaultIPv4Range.start,
      end: defaultIPv4Range.end,
      ports: "5555",
      isDefault: false,
    }
  const range = parseAddressPolicy(profile.addressPolicy)
  return {
    name: profile.name,
    start: range.start,
    end: range.end,
    ports: profile.ports.join(", "),
    isDefault: profile.isDefault,
  }
}

/** The run an operator means by "the latest scan" without relying on list order. */
function mostRecentRun(runs: readonly ScanRunView[]): ScanRunView | undefined {
  return [...runs].sort((left, right) => right.requestedAt.localeCompare(left.requestedAt))[0]
}

function SaveFailureNotice({ noticeRef, message }: { noticeRef: RefObject<HTMLDivElement | null>; message: string }) {
  return (
    <div ref={noticeRef} tabIndex={-1} role="alert" aria-labelledby="profile-save-failure-title" className="flex items-start gap-2 border-l-2 border-destructive bg-destructive/10 p-3 text-xs text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 sm:col-span-2">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
      <div>
        <p id="profile-save-failure-title" className="font-semibold">
          Save failed. The profile was not saved.
        </p>
        {message ? <p className="mt-1">{message}</p> : null}
      </div>
    </div>
  )
}

function ProfileTable({ profiles, onEdit, onDelete }: { profiles: ControlPlaneSnapshot["networkProfiles"]; onEdit: (profile: NetworkProfileView) => void; onDelete: (profile: NetworkProfileView) => void }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = profiles.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      {profiles.length === 0 ? (
        <EmptyState label="No Network Profiles" detail="Create a bounded profile to continue." />
      ) : (
        <div className="overflow-x-auto border border-border">
          <table className="w-full min-w-[720px] text-left text-xs">
            <caption className="sr-only">Network profile catalog</caption>
            <thead>
              <tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground">
                <th className="p-3">Name</th>
                <th className="p-3">Address Policy</th>
                <th className="p-3">Ports</th>
                <th className="p-3">Default</th>
                <th className="p-3">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {visible.map((profile) => (
                <tr key={profile.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                  <td className="p-3">
                    <p className="font-medium">{profile.name}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">{profile.id}</p>
                  </td>
                  <td className="p-3 font-mono text-[11px]">{profile.addressPolicy}</td>
                  <td className="p-3">{profile.ports.join(", ")}</td>
                  <td className="p-3">{profile.isDefault ? <Check className="size-4 text-primary" aria-label="Default profile" /> : "—"}</td>
                  <td className="p-3">
                    <TooltipProvider><div className="flex justify-end gap-1">
                      <Tooltip>
                        <TooltipTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Edit ${profile.name}`} onClick={() => onEdit(profile)} />}><Pencil className="size-3.5" aria-hidden="true" /></TooltipTrigger>
                        <TooltipContent>Edit profile</TooltipContent>
                      </Tooltip>
                      <AlertDialog>
                        <Tooltip>
                          <AlertDialogTrigger render={<TooltipTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Delete ${profile.name}`} />} />}><Trash2 className="size-3.5" aria-hidden="true" /></AlertDialogTrigger>
                          <TooltipContent>Delete profile</TooltipContent>
                        </Tooltip>
                        <AlertDialogContent title="Delete Network Profile?" description={`Deletes ${profile.name}. Scans that used it stay in history with no profile reference. Network profiles cannot be restored; a scan needs a saved profile.`} confirmLabel="Confirm Delete" onConfirm={() => onDelete(profile)} />
                      </AlertDialog>
                    </div></TooltipProvider>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <DataTablePagination
        page={page}
        pageSize={pageSize}
        total={profiles.length}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next)
          setPage(0)
        }}
      />
    </>
  )
}

// The board shows each device's CURRENT endpoint, never every endpoint record:
// an endpoint change is not an identity change, so a device activated onto a new
// port would otherwise be listed - and counted - twice, once at the transport it
// left. The records a device left stay readable as that device's own endpoint
// history, each dated with the moment it was superseded.
function EndpointTable({ endpoints, devices }: { endpoints: ControlPlaneSnapshot["endpoints"]; devices: ControlPlaneSnapshot["devices"] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const current = endpoints.filter((endpoint) => endpoint.state === "current")
  const visible = current.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      {current.length === 0 ? (
        <EmptyState label="No Registered Endpoints" detail="An endpoint is recorded against the canonical device a scan observed. Run a scan against a saved profile; if this list stays empty, confirm the profile range, Wi-Fi LAN, and client isolation settings." />
      ) : (
        <div className="overflow-x-auto border border-border">
          <table className="w-full min-w-[720px] text-left text-xs">
            <caption className="sr-only">Registered network endpoints</caption>
            <thead>
              <tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground">
                <th className="p-3">Endpoint</th>
                <th className="p-3">Device</th>
                <th className="p-3">Address</th>
                <th className="p-3">Observed</th>
                <th className="p-3">State</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((endpoint) => (
                <tr key={endpoint.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                  <td className="p-3">
                    <p className="font-medium">{endpoint.endpointType}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">
                      {endpoint.id} · {endpoint.serial}
                    </p>
                  </td>
                  <td className="p-3">
                    <p>{devices.find((device) => device.id === endpoint.deviceId)?.displayName ?? "Unknown device"}</p>
                    <p className="drift-data mt-1 text-[10px] text-muted-foreground">{endpoint.deviceId}</p>
                  </td>
                  <td className="p-3 font-mono text-[11px]">
                    {endpoint.host}:{endpoint.port}
                  </td>
                  <td className="p-3">{endpoint.observedAt}</td>
                  <td className="p-3">
                    <StatusBadge label={endpoint.state} tone="healthy" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <DataTablePagination
        page={page}
        pageSize={pageSize}
        total={current.length}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next)
          setPage(0)
        }}
      />
    </>
  )
}

function ScanTable({ runs }: { runs: ControlPlaneSnapshot["scanRuns"] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = runs.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <>
      {runs.length === 0 ? (
        <EmptyState label="No Discovery Scans" detail="Run a scan to populate discovery history." />
      ) : (
        <div className="overflow-x-auto border border-border">
          <table className="w-full min-w-[650px] text-left text-xs">
            <caption className="sr-only">Discovery scan history</caption>
            <thead>
              <tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground">
                <th className="p-3">Run</th>
                <th className="p-3">Profile</th>
                <th className="p-3">Requested</th>
                <th className="p-3">Completed</th>
                <th className="p-3">State</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((run) => (
                <tr key={run.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                  <td className="drift-data p-3 text-[10px]">{run.id}</td>
                  <td className="drift-data p-3 text-[10px]">{run.networkProfileId || "Profile deleted"}</td>
                  <td className="p-3">{run.requestedAt}</td>
                  <td className="p-3">
                    <span>{run.finishedAt ?? "—"}</span>
                    {run.failureClass ? <FailureBadge failureClass={run.failureClass} /> : null}
                  </td>
                  <td className="p-3">
                    <StatusBadge label={run.state} tone={run.state === "completed" ? "healthy" : run.state === "failed" ? "danger" : "info"} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <DataTablePagination
        page={page}
        pageSize={pageSize}
        total={runs.length}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next)
          setPage(0)
        }}
      />
    </>
  )
}

function ObservedDevicePanel({ run, observations, devices }: { run: ScanRunView; observations: readonly ObservedDeviceView[]; devices: ControlPlaneSnapshot["devices"] }) {
  const online = observations.filter((observation) => observation.state === "online").length
  const offline = observations.filter((observation) => observation.state === "offline").length
  const unauthorized = observations.filter((observation) => observation.state === "unauthorized").length
  return (
    <section aria-labelledby="scan-observations-title" className="mt-4 border-t border-border pt-4">
      <h3 id="scan-observations-title" className="text-xs font-semibold">
        Devices observed by {run.id}
      </h3>
      {run.state === "failed" ? <p className="mt-2 text-xs text-muted-foreground">This scan failed before it could observe a device. Its terminal outcome is recorded above.</p> : null}
      {run.state !== "failed" && observations.length === 0 ? (
        <div className="mt-2">
          <EmptyState label="No devices observed by this scan" detail="No ADB endpoints responded. Confirm the profile range, Wi-Fi LAN, and client isolation settings." />
        </div>
      ) : null}
      {observations.length > 0 ? (
        <>
          <p className="mt-1 text-xs text-muted-foreground">
            {observations.length} devices observed: {online} online, {offline} offline, {unauthorized} unauthorized.
          </p>
          <div className="mt-3 overflow-x-auto border border-border">
            <table className="w-full min-w-[720px] text-left text-xs">
              <caption className="sr-only">Devices observed by this scan</caption>
              <thead>
                <tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground">
                  <th className="p-3">Address</th>
                  <th className="p-3">Serial</th>
                  <th className="p-3">Model</th>
                  <th className="p-3">Device</th>
                  <th className="p-3">Link State</th>
                </tr>
              </thead>
              <tbody>
                {observations.map((observation) => (
                  <tr key={`${observation.scanRunId}-${observation.host}-${observation.port}-${observation.serial}`} className="border-b border-border/70 last:border-0 hover:bg-muted/50">
                    <td className="p-3 font-mono text-[11px]">
                      {observation.host}:{observation.port}
                    </td>
                    <td className="drift-data p-3 text-[10px]">{observation.serial}</td>
                    <td className="p-3">{observation.model || "—"}</td>
                    <td className="p-3">
                      {observation.known ? (
                        <>
                          <p>{devices.find((device) => device.id === observation.deviceId)?.displayName ?? "Known device"}</p>
                          <p className="drift-data mt-1 text-[10px] text-muted-foreground">{observation.deviceId}</p>
                        </>
                      ) : (
                        <p>New device</p>
                      )}
                    </td>
                    <td className="p-3">
                      <StatusBadge label={observation.state} tone={observation.state === "online" ? "healthy" : observation.state === "unauthorized" ? "attention" : "neutral"} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}
      {unauthorized > 0 ? <p className="mt-2 text-xs text-muted-foreground">A device answered but is not authorized for debugging. Accept the debugging prompt on the device and scan again; until then its endpoint stays unknown.</p> : null}
    </section>
  )
}

type IPv4Octets = [string, string, string, string]

const defaultIPv4Range = {
  start: ["192", "168", "1", "1"] as IPv4Octets,
  end: ["192", "168", "1", "254"] as IPv4Octets,
}

function parseIPv4(value: string): IPv4Octets | null {
  const parts = value.trim().split(".")
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part) || Number(part) > 255)) return null
  return parts as IPv4Octets
}

function octetsToNumber(octets: IPv4Octets) {
  return octets.reduce((value, octet) => value * 256 + Number(octet), 0)
}

function numberToOctets(value: number): IPv4Octets {
  return [String((value >>> 24) & 255), String((value >>> 16) & 255), String((value >>> 8) & 255), String(value & 255)]
}

function parseAddressPolicy(policy: string) {
  const trimmed = policy.trim()
  const rangeParts = trimmed.split("-")
  if (rangeParts.length === 2) {
    const start = parseIPv4(rangeParts[0])
    const end = parseIPv4(rangeParts[1])
    if (start && end) return { start, end }
  }
  const cidrParts = trimmed.split("/")
  const address = parseIPv4(cidrParts[0] ?? "")
  const prefix = Number(cidrParts[1])
  if (address && Number.isInteger(prefix) && prefix >= 0 && prefix <= 32) {
    const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
    const network = octetsToNumber(address) & mask
    const broadcast = network | (~mask >>> 0)
    return { start: numberToOctets(network), end: numberToOctets(broadcast) }
  }
  return defaultIPv4Range
}

function validIPv4Range(start: IPv4Octets, end: IPv4Octets) {
  const valid = (octets: IPv4Octets) => octets.every((octet) => /^\d{1,3}$/.test(octet) && Number(octet) <= 255)
  return valid(start) && valid(end) && octetsToNumber(start) <= octetsToNumber(end)
}

function IPv4RangeFields({ start, end, error, onStartChange, onEndChange }: { start: IPv4Octets; end: IPv4Octets; error?: string; onStartChange: (value: IPv4Octets) => void; onEndChange: (value: IPv4Octets) => void }) {
  const renderAddress = (label: string, prefix: string, value: IPv4Octets, onChange: (next: IPv4Octets) => void) => (
    <div>
      <FieldLabel htmlFor={`${prefix}-1`}>{label}</FieldLabel>
      <div className="mt-1 grid grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr] items-center gap-1">
        <span className="sr-only">{label}</span>
        {value.map((octet, index) => (
          <span key={`${prefix}-${index}`} className="contents">
            <Input
              id={`${prefix}-${index + 1}`}
              aria-label={`${label} octet ${index + 1}`}
              inputMode="numeric"
              maxLength={3}
              value={octet}
              onChange={(event) => {
                const next = [...value] as IPv4Octets
                next[index] = event.target.value.replace(/\D/g, "").slice(0, 3)
                onChange(next)
              }}
              className="text-center font-mono"
            />
            {index < 3 ? (
              <span aria-hidden="true" className="text-center text-muted-foreground">
                .
              </span>
            ) : null}
          </span>
        ))}
      </div>
    </div>
  )
  return (
    <div className="space-y-3 sm:col-span-2">
      <div className="grid gap-4 rounded-none border border-border bg-muted/20 p-3 sm:grid-cols-2">
        {renderAddress("Start Address", "profile-address-start", start, onStartChange)}
        {renderAddress("End Address", "profile-address-end", end, onEndChange)}
      </div>
      {error ? (
        <p id="profile-address-policy-error" className="text-xs text-destructive">
          {error}
        </p>
      ) : (
        <p className="text-[11px] text-muted-foreground">Enter the first and last IPv4 addresses allowed for discovery.</p>
      )}
    </div>
  )
}
