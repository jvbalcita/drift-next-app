import { useRef, useState, type FormEvent } from "react"
import { Check, Plus, Radar, Save, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DispatchIntent, NetworkProfileView } from "@/lib/domain/control-plane"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { DataTablePagination, EmptyState, FieldLabel, FailureBadge, OperatorNotice, PageIntro, Panel, StatusBadge } from "./shared"

export function NetworkProfilesPage({ snapshot, dispatch, view = "profiles", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const [selectedProfileId, setSelectedProfileId] = useState(snapshot.networkProfiles[0]?.id ?? "new")
  const [name, setName] = useState(snapshot.networkProfiles[0]?.name ?? "")
  const [addressStart, setAddressStart] = useState(parseAddressPolicy(snapshot.networkProfiles[0]?.addressPolicy ?? "").start)
  const [addressEnd, setAddressEnd] = useState(parseAddressPolicy(snapshot.networkProfiles[0]?.addressPolicy ?? "").end)
  const [ports, setPorts] = useState(snapshot.networkProfiles[0]?.ports.join(", ") ?? "5555")
  const [isDefault, setIsDefault] = useState(snapshot.networkProfiles[0]?.isDefault ?? false)
  const [feedback, setFeedback] = useState("")
  const [profileErrors, setProfileErrors] = useState<{ name?: string; addressPolicy?: string; ports?: string }>({})
  const profileErrorSummaryRef = useRef<HTMLDivElement>(null)
  const [profileDialogOpen, setProfileDialogOpen] = useState(false)
  const [scanDialogOpen, setScanDialogOpen] = useState(false)
  const selectedProfile = snapshot.networkProfiles.find((profile) => profile.id === selectedProfileId)

  function selectProfile(profile: NetworkProfileView) { const range = parseAddressPolicy(profile.addressPolicy); setSelectedProfileId(profile.id); setName(profile.name); setAddressStart(range.start); setAddressEnd(range.end); setPorts(profile.ports.join(", ")); setIsDefault(profile.isDefault); setProfileErrors({}); setFeedback("") }
  function beginNewProfile() { setSelectedProfileId("new"); setName(""); setAddressStart(["192", "168", "1", "1"]); setAddressEnd(["192", "168", "1", "254"]); setPorts("5555"); setIsDefault(false); setProfileErrors({}); setFeedback(""); setProfileDialogOpen(true) }
  function parsedPorts() { return ports.split(",").map((value) => Number(value.trim())).filter((value) => Number.isInteger(value) && value > 0 && value <= 65535) }
  async function saveProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const parsed = parsedPorts()
    const start = addressStart.join(".")
    const end = addressEnd.join(".")
    const range = `${start}-${end}`
    const errors = {
      name: name.trim() ? undefined : "Enter a profile name.",
      addressPolicy: validIPv4Range(addressStart, addressEnd) ? undefined : "Enter a valid IPv4 range. Each octet must be 0 through 255, and the start must not exceed the end.",
      ports: parsed.length > 0 ? undefined : "Enter one or more ports from 1 through 65535.",
    }
    setProfileErrors(errors)
    if (Object.values(errors).some(Boolean)) {
      requestAnimationFrame(() => profileErrorSummaryRef.current?.focus())
      return
    }
    const mutation = selectedProfileId === "new"
      ? await dispatch({ type: "createNetworkProfile", name, addressPolicy: range, ports: parsed, isDefault })
      : await dispatch({ type: "updateNetworkProfile", profileId: selectedProfileId, name, addressPolicy: range, ports: parsed, isDefault, rowVersion: selectedProfile?.rowVersion ?? 0 })
    setFeedback(mutation.message)
    if (mutation.ok) setProfileDialogOpen(false)
  }
  function retireProfile() {
    if (!selectedProfile) return
    void reportDispatch(dispatch, { type: "retireNetworkProfile", profileId: selectedProfile.id, rowVersion: selectedProfile.rowVersion }, setFeedback)
  }
  async function startScan() {
    const result = await reportDispatch(dispatch, { type: "startScan", profileId: selectedProfileId }, setFeedback)
    if (result.ok) setScanDialogOpen(false)
  }

  return <>
    <PageIntro eyebrow="DISCOVERY / NETWORK PROFILES" title="Network Profiles" description="Define bounded, non-authoritative discovery policy separately from scan execution." actions={<div className="flex gap-2"><Button variant="outline" size="sm" onClick={beginNewProfile}><Plus className="size-3.5" aria-hidden="true" />New Profile</Button><Button size="sm" onClick={() => setScanDialogOpen(true)} disabled={!selectedProfile || selectedProfile.state !== "active"}><Radar className="size-3.5" aria-hidden="true" />Configure Scan</Button></div>} />
    <OperatorNotice>Enter the IPv4 range used by your devices. Saving or scanning does not add a device; a scan records what it observed against the devices already known to this workspace.</OperatorNotice>
    <Tabs value={view} onValueChange={onViewChange} className="mt-6"><TabsList className="h-auto flex-wrap rounded-none border border-border bg-background p-0" aria-label="Network profile views"><TabsTrigger value="profiles" className="rounded-none">Profiles</TabsTrigger><TabsTrigger value="scans" className="rounded-none">Discovery Scans</TabsTrigger><TabsTrigger value="endpoints" className="rounded-none">Registered Endpoints</TabsTrigger><TabsTrigger value="history" className="rounded-none">History</TabsTrigger></TabsList>
      <TabsContent value="profiles" className="mt-6"><Panel title="Profile Catalog" description="Profile definitions constrain discovery; they do not establish an endpoint."><ProfileTable profiles={snapshot.networkProfiles} onEdit={(profile) => { selectProfile(profile); setProfileDialogOpen(true) }} /></Panel></TabsContent>
      <TabsContent value="scans" className="mt-6"><Panel title="Discovery Scans" description="A scan is an explicit intent against an active profile; it records the devices it observed." action={<Button size="sm" onClick={() => setScanDialogOpen(true)} disabled={!selectedProfile || selectedProfile.state !== "active"}><Radar className="size-3.5" aria-hidden="true" />Configure Scan</Button>}><ScanTable runs={snapshot.scanRuns} /></Panel></TabsContent>
      <TabsContent value="endpoints" className="mt-6"><Panel title="Registered Endpoints" description="Endpoint records are the mutable transport identity of the devices a scan observed."><EndpointTable endpoints={snapshot.endpoints} /></Panel></TabsContent>
      <TabsContent value="history" className="mt-6"><Panel title="Scan History" description="Every attempted scan retains its profile reference and terminal outcome."><ScanTable runs={snapshot.scanRuns} /></Panel></TabsContent>
    </Tabs>
    <p aria-live="polite" className="mt-6 border-l-2 border-primary bg-secondary/60 p-3 text-xs text-muted-foreground">{feedback || "Discovery status feedback appears here. No external discovery is active."}</p>
    <Dialog open={profileDialogOpen} onOpenChange={setProfileDialogOpen}><DialogContent className="rounded-none sm:max-w-2xl"><DialogHeader><DialogTitle>{selectedProfileId === "new" ? "Create Network Profile" : "Edit Network Profile"}</DialogTitle><DialogDescription>Define the IPv4 range and ports used when discovering device endpoints.</DialogDescription></DialogHeader><form className="grid gap-4 sm:grid-cols-2" noValidate onSubmit={saveProfile}>{Object.values(profileErrors).some(Boolean) ? <div ref={profileErrorSummaryRef} tabIndex={-1} role="alert" className="border border-destructive p-3 text-xs text-destructive sm:col-span-2"><p className="font-medium">Correct the highlighted fields before saving.</p><ul className="mt-1 list-disc pl-4">{Object.values(profileErrors).filter(Boolean).map((error) => <li key={error}>{error}</li>)}</ul></div> : null}<div className="sm:col-span-2"><FieldLabel htmlFor="profile-name">Profile Name</FieldLabel><Input id="profile-name" value={name} onChange={(event) => { setName(event.target.value); setProfileErrors((current) => ({ ...current, name: undefined })) }} aria-invalid={Boolean(profileErrors.name)} aria-describedby={profileErrors.name ? "profile-name-error" : undefined} />{profileErrors.name ? <p id="profile-name-error" className="mt-1 text-xs text-destructive">{profileErrors.name}</p> : null}</div><IPv4RangeFields start={addressStart} end={addressEnd} error={profileErrors.addressPolicy} onStartChange={(next) => { setAddressStart(next); setProfileErrors((current) => ({ ...current, addressPolicy: undefined })) }} onEndChange={(next) => { setAddressEnd(next); setProfileErrors((current) => ({ ...current, addressPolicy: undefined })) }} /><div><FieldLabel htmlFor="profile-ports">Ports</FieldLabel><Input id="profile-ports" value={ports} onChange={(event) => { setPorts(event.target.value); setProfileErrors((current) => ({ ...current, ports: undefined })) }} aria-invalid={Boolean(profileErrors.ports)} aria-describedby={profileErrors.ports ? "profile-ports-error" : undefined} className="font-mono" />{profileErrors.ports ? <p id="profile-ports-error" className="mt-1 text-xs text-destructive">{profileErrors.ports}</p> : null}</div><label className="flex items-center gap-2 text-xs sm:col-span-2"><input type="checkbox" checked={isDefault} onChange={(event) => setIsDefault(event.target.checked)} className="size-4 accent-[var(--primary)]" />Use as Default Discovery Policy</label><DialogFooter className="sm:col-span-2"><Button type="submit"><Save className="size-3.5" aria-hidden="true" />Save Profile</Button>{selectedProfile ? <Button type="button" variant="destructive" onClick={retireProfile} disabled={selectedProfile.state === "retired"}><Trash2 className="size-3.5" aria-hidden="true" />Retire</Button> : null}</DialogFooter></form></DialogContent></Dialog>
    <Dialog open={scanDialogOpen} onOpenChange={setScanDialogOpen}><DialogContent className="rounded-none"><DialogHeader><DialogTitle>Configure discovery scan</DialogTitle><DialogDescription>Runs only against the selected bounded profile. No external discovery or endpoint registration occurs.</DialogDescription></DialogHeader><dl className="border-y border-border py-3 text-xs"><div className="flex justify-between gap-4"><dt className="text-muted-foreground">Profile</dt><dd>{selectedProfile?.name ?? "None selected"}</dd></div><div className="mt-2 flex justify-between gap-4"><dt className="text-muted-foreground">Address policy</dt><dd className="font-mono">{selectedProfile?.addressPolicy ?? "—"}</dd></div></dl><DialogFooter><Button onClick={startScan} disabled={!selectedProfile || selectedProfile.state !== "active"}><Radar className="size-3.5" aria-hidden="true" />Start scan</Button></DialogFooter></DialogContent></Dialog>
  </>
}

function ProfileTable({ profiles, onEdit }: { profiles: ControlPlaneSnapshot["networkProfiles"]; onEdit: (profile: NetworkProfileView) => void }) { const [page, setPage] = useState(0); const [pageSize, setPageSize] = useState(10); const visible = profiles.slice(page * pageSize, (page + 1) * pageSize); return <>{profiles.length === 0 ? <EmptyState label="No Network Profiles" detail="Create a bounded profile to continue." /> : <div className="overflow-x-auto border border-border"><table className="w-full min-w-[720px] text-left text-xs"><caption className="sr-only">Network profile catalog</caption><thead><tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground"><th className="p-3">Name</th><th className="p-3">Address Policy</th><th className="p-3">Ports</th><th className="p-3">State</th><th className="p-3">Default</th><th className="p-3"><span className="sr-only">Actions</span></th></tr></thead><tbody>{visible.map((profile) => <tr key={profile.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50"><td className="p-3"><p className="font-medium">{profile.name}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{profile.id} · row v{profile.rowVersion}</p></td><td className="p-3 font-mono text-[11px]">{profile.addressPolicy}</td><td className="p-3">{profile.ports.join(", ")}</td><td className="p-3"><StatusBadge label={profile.state} tone={profile.state === "active" ? "healthy" : profile.state === "retired" ? "neutral" : "attention"} /></td><td className="p-3">{profile.isDefault ? <Check className="size-4 text-primary" aria-label="Default profile" /> : "—"}</td><td className="p-3 text-right"><Button variant="outline" size="sm" onClick={() => onEdit(profile)}>Edit Profile</Button></td></tr>)}</tbody></table></div>}<DataTablePagination page={page} pageSize={pageSize} total={profiles.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} /></> }
function EndpointTable({ endpoints }: { endpoints: ControlPlaneSnapshot["endpoints"] }) { const [page, setPage] = useState(0); const [pageSize, setPageSize] = useState(10); const visible = endpoints.slice(page * pageSize, (page + 1) * pageSize); return <>{endpoints.length === 0 ? <EmptyState label="No Registered Endpoints" detail="Endpoints appear here once a scan has observed a device." /> : <div className="overflow-x-auto border border-border"><table className="w-full min-w-[720px] text-left text-xs"><caption className="sr-only">Registered network endpoints</caption><thead><tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground"><th className="p-3">Endpoint</th><th className="p-3">Device</th><th className="p-3">Address</th><th className="p-3">Observed</th><th className="p-3">State</th></tr></thead><tbody>{visible.map((endpoint) => <tr key={endpoint.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50"><td className="p-3"><p className="font-medium">{endpoint.endpointType}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{endpoint.id} · {endpoint.serial}</p></td><td className="drift-data p-3 text-[10px]">{endpoint.deviceId}</td><td className="p-3 font-mono text-[11px]">{endpoint.host}:{endpoint.port}</td><td className="p-3">{endpoint.observedAt}</td><td className="p-3"><StatusBadge label={endpoint.state} tone={endpoint.state === "current" ? "healthy" : endpoint.state === "retired" ? "neutral" : "attention"} /></td></tr>)}</tbody></table></div>}<DataTablePagination page={page} pageSize={pageSize} total={endpoints.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} /></> }
function ScanTable({ runs }: { runs: ControlPlaneSnapshot["scanRuns"] }) { const [page, setPage] = useState(0); const [pageSize, setPageSize] = useState(10); const visible = runs.slice(page * pageSize, (page + 1) * pageSize); return <>{runs.length === 0 ? <EmptyState label="No Discovery Scans" detail="Run a scan to populate discovery history." /> : <div className="overflow-x-auto border border-border"><table className="w-full min-w-[650px] text-left text-xs"><caption className="sr-only">Discovery scan history</caption><thead><tr className="border-b border-border text-[10px] tracking-[0.08em] text-muted-foreground"><th className="p-3">Run</th><th className="p-3">Profile</th><th className="p-3">Requested</th><th className="p-3">Completed</th><th className="p-3">State</th></tr></thead><tbody>{visible.map((run) => <tr key={run.id} className="border-b border-border/70 last:border-0 hover:bg-muted/50"><td className="drift-data p-3 text-[10px]">{run.id}</td><td className="drift-data p-3 text-[10px]">{run.networkProfileId}</td><td className="p-3">{run.requestedAt}</td><td className="p-3"><span>{run.finishedAt ?? "—"}</span>{run.failureClass ? <FailureBadge failureClass={run.failureClass} /> : null}</td><td className="p-3"><StatusBadge label={run.state} tone={run.state === "completed" ? "healthy" : run.state === "failed" ? "danger" : "info"} /></td></tr>)}</tbody></table></div>}<DataTablePagination page={page} pageSize={pageSize} total={runs.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} /></> }
type IPv4Octets = [string, string, string, string]

const defaultIPv4Range = { start: ["192", "168", "1", "1"] as IPv4Octets, end: ["192", "168", "1", "254"] as IPv4Octets }

function parseIPv4(value: string): IPv4Octets | null {
  const parts = value.trim().split(".")
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part) || Number(part) > 255)) return null
  return parts as IPv4Octets
}

function octetsToNumber(octets: IPv4Octets) { return octets.reduce((value, octet) => value * 256 + Number(octet), 0) }

function numberToOctets(value: number): IPv4Octets { return [String((value >>> 24) & 255), String((value >>> 16) & 255), String((value >>> 8) & 255), String(value & 255)] }

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
  const renderAddress = (label: string, prefix: string, value: IPv4Octets, onChange: (next: IPv4Octets) => void) => <div><FieldLabel htmlFor={`${prefix}-1`}>{label}</FieldLabel><div className="mt-1 grid grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr] items-center gap-1"><span className="sr-only">{label}</span>{value.map((octet, index) => <span key={`${prefix}-${index}`} className="contents"><Input id={`${prefix}-${index + 1}`} aria-label={`${label} octet ${index + 1}`} inputMode="numeric" maxLength={3} value={octet} onChange={(event) => { const next = [...value] as IPv4Octets; next[index] = event.target.value.replace(/\D/g, "").slice(0, 3); onChange(next) }} className="text-center font-mono" />{index < 3 ? <span aria-hidden="true" className="text-center text-muted-foreground">.</span> : null}</span>)}</div></div>
  return <div className="space-y-3 sm:col-span-2"><div className="grid gap-4 rounded-none border border-border bg-muted/20 p-3 sm:grid-cols-2">{renderAddress("Start Address", "profile-address-start", start, onStartChange)}{renderAddress("End Address", "profile-address-end", end, onEndChange)}</div>{error ? <p id="profile-address-policy-error" className="text-xs text-destructive">{error}</p> : <p className="text-[11px] text-muted-foreground">Enter the first and last IPv4 addresses allowed for discovery.</p>}</div>
}
