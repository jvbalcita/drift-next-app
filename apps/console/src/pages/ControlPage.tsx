import { useMemo, useState } from "react"
import { CircleHelp, MonitorPlay, Pause, Play, Square, Users } from "lucide-react"
import { Button } from "@/components/ui/button"
import type {
  ControlEligibility,
  ControlPlaneSnapshot,
  DeviceView,
  DispatchIntent,
  MirrorSessionView,
} from "@/lib/domain/control-plane"
import { DeviceStatus, MockNotice, PageIntro, Panel, StatusBadge, type StatusTone } from "./shared"
import { textForDevice } from "./page-utils"

export function ControlPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const initialSource = snapshot.devices.find((device) => device.status === "online")?.id ?? ""
  const [sourceDeviceId, setSourceDeviceId] = useState(initialSource)
  const [followerDeviceIds, setFollowerDeviceIds] = useState<string[]>([])
  const [feedback, setFeedback] = useState("")
  const source = snapshot.devices.find((device) => device.id === sourceDeviceId)
  const followers = useMemo(() => snapshot.devices.filter((device) => device.id !== sourceDeviceId), [sourceDeviceId, snapshot.devices])
  const eligibleFollowers = followers.filter((device) => device.controlEligibility === "eligible")
  const selectedFollowers = followerDeviceIds.filter((id) => eligibleFollowers.some((device) => device.id === id))
  const allEligibleSelected = eligibleFollowers.length > 0 && eligibleFollowers.every((device) => selectedFollowers.includes(device.id))
  const sourceReady = source?.status === "online" && source.controlEligibility === "eligible"
  const activeSession = snapshot.mirrorSessions.find((session) => session.state === "active")
  const latestSession = snapshot.mirrorSessions[0]

  function toggleFollower(deviceId: string) {
    setFollowerDeviceIds((current) => current.includes(deviceId) ? current.filter((id) => id !== deviceId) : [...current, deviceId])
    setFeedback("")
  }

  function selectAllEligible() {
    setFollowerDeviceIds(allEligibleSelected ? [] : eligibleFollowers.map((device) => device.id))
    setFeedback("")
  }

  function startPreview() {
    const mutation = dispatch({ type: "startMirrorPreview", sourceDeviceId, followerDeviceIds: selectedFollowers })
    setFeedback(mutation.message)
  }

  function stopPreview(session: MirrorSessionView) {
    const mutation = dispatch({ type: "stopMirrorPreview", sessionId: session.id })
    setFeedback(mutation.message)
  }

  return (
    <>
      <PageIntro eyebrow="CONTROL / SOURCE + FOLLOWERS" title="Mirror control" description="Choose one source observation and a bounded set of eligible followers. The current slice is a browser-only interaction prototype." actions={<StatusBadge label="Preview only" tone="info" />} />
      <MockNotice>This page is deliberately non-operative. It acquires no lease, sends no command, connects to no device, and records only mock result metadata.</MockNotice>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,0.82fr)_minmax(0,1.18fr)]">
        <Panel title="Source device" description="Exactly one source is selected for this preview.">
          <fieldset>
            <legend className="sr-only">Select source device</legend>
            <div className="space-y-3">{snapshot.devices.map((device) => <label key={device.id} className={`flex cursor-pointer items-start gap-3 border p-3 transition-colors focus-within:border-primary focus-within:ring-2 focus-within:ring-primary/25 ${device.id === sourceDeviceId ? "border-primary bg-secondary/70" : "border-border hover:bg-muted"}`}><input type="radio" name="source-device" value={device.id} checked={device.id === sourceDeviceId} onChange={() => { setSourceDeviceId(device.id); setFollowerDeviceIds((current) => current.filter((id) => id !== device.id)); setFeedback("") }} className="mt-1 size-4 accent-[var(--primary)]" aria-label={`Source device ${device.displayName}`} /><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-medium">{device.displayName}</span><DeviceStatus device={device} /></div><p className="mt-1 text-[11px] text-muted-foreground">{device.location} · {device.platformVersion}</p><p className="drift-data mt-2 text-[10px] text-muted-foreground">{device.stableIdentity} · {device.lastSeen}</p></div></label>)}</div>
          </fieldset>
          <div className="mt-4 border-l-2 border-primary bg-secondary/60 p-3 text-xs"><div className="flex items-center gap-2 font-semibold"><MonitorPlay className="size-4 text-primary" aria-hidden="true" />Source readiness</div><p className="mt-1 text-muted-foreground">{source ? sourceReady ? "Ready for a mock preview." : `Not ready: ${eligibilityLabel(source.controlEligibility)}.` : "Select a source device."}</p></div>
        </Panel>

        <Panel title="Follower devices" description="Followers are selected independently; the source is excluded from this set." action={<Button variant="outline" size="sm" onClick={selectAllEligible} disabled={eligibleFollowers.length === 0}>{allEligibleSelected ? "Clear eligible" : "Select all eligible"}</Button>}>
          <fieldset>
            <legend className="sr-only">Select eligible follower devices</legend>
            <div className="grid gap-3 sm:grid-cols-2">{followers.map((device) => { const eligible = device.controlEligibility === "eligible"; const selected = selectedFollowers.includes(device.id); return <label key={device.id} className={`flex min-h-28 items-start gap-3 border p-3 transition-colors ${eligible ? "cursor-pointer hover:bg-muted focus-within:border-primary focus-within:ring-2 focus-within:ring-primary/25" : "cursor-not-allowed bg-muted/50 opacity-80"} ${selected ? "border-primary bg-secondary/70" : "border-border"}`}><input type="checkbox" checked={selected} disabled={!eligible} onChange={() => toggleFollower(device.id)} className="mt-1 size-4 accent-[var(--primary)]" aria-label={`Follower device ${device.displayName}`} /><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-medium">{device.displayName}</span><StatusBadge label={eligibilityLabel(device.controlEligibility)} tone={eligibilityTone(device.controlEligibility)} /></div><p className="mt-1 text-[11px] text-muted-foreground">{device.location}</p><p className="mt-2 text-[10px] leading-4 text-muted-foreground">{eligibilityDetail(device.controlEligibility)}</p></div></label> })}</div>
          </fieldset>
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4"><p className="text-xs"><span className="drift-data font-semibold">{selectedFollowers.length}</span> follower{selectedFollowers.length === 1 ? "" : "s"} selected</p><p className="text-[11px] text-muted-foreground">Eligible {eligibleFollowers.length} · excluded source {source?.displayName ?? "—"}</p></div>
        </Panel>
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(280px,0.42fr)]">
        <Panel title="Preview frames" description="Frames are fixed mock placeholders, not live video or device screenshots.">
          <div className="grid gap-4 lg:grid-cols-[minmax(220px,0.8fr)_minmax(0,1.2fr)]">
            <MockDeviceFrame device={source} label="Selected source mock frame" prominent />
            <div><div className="mb-3 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.08em]"><Users className="size-4 text-primary" aria-hidden="true" />Follower preview grid</div><div className="grid gap-3 sm:grid-cols-2">{selectedFollowers.length === 0 ? <div className="border border-dashed border-border px-4 py-8 text-center text-xs text-muted-foreground sm:col-span-2">Select at least one eligible follower to populate the preview.</div> : selectedFollowers.map((id) => <MockDeviceFrame key={id} device={snapshot.devices.find((candidate) => candidate.id === id)} label={`${textForDevice(snapshot.devices, id)} follower mock frame`} />)}</div></div>
          </div>
        </Panel>
        <Panel title="Session action" description="The primary action is intentionally simulated until the typed control-plane runtime is authorized and connected.">
          <div className="space-y-4"><div className="flex items-start gap-3 border border-border bg-muted/50 p-3"><Pause className="mt-0.5 size-4 text-amber-700" aria-hidden="true" /><div><p className="text-xs font-semibold">No command path attached</p><p className="mt-1 text-[11px] leading-5 text-muted-foreground">The source result and every follower result remain separate. A source success never implies follower success.</p></div></div><Button className="w-full rounded-none" onClick={startPreview} disabled={!sourceReady || selectedFollowers.length === 0}><Play className="size-3.5" aria-hidden="true" />Start mirror preview</Button>{activeSession ? <Button variant="outline" className="w-full rounded-none" onClick={() => stopPreview(activeSession)}><Square className="size-3.5" aria-hidden="true" />Stop mirror preview</Button> : null}<p aria-live="polite" className="min-h-10 border-l-2 border-primary bg-secondary/60 p-3 text-[11px] leading-5 text-muted-foreground">{feedback || "Simulation status will appear here. No command will be sent."}</p></div>
        </Panel>
      </div>

      <div className="mt-6"><Panel title="Session results" description="Source and follower outcomes are rendered as independent typed results.">{latestSession ? <SessionResult session={latestSession} snapshot={snapshot} /> : <div className="flex items-start gap-2 text-xs text-muted-foreground"><CircleHelp className="mt-0.5 size-4 text-primary" aria-hidden="true" />No preview session has been started in this mock workspace.</div>}<div className="mt-4 border-t border-border pt-4"><p className="mb-2 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">Typed result vocabulary</p><div className="flex flex-wrap gap-2"><StatusBadge label="Offline" tone="attention" /><StatusBadge label="Incompatible" tone="attention" /><StatusBadge label="Policy denied" tone="danger" /><StatusBadge label="Lease conflict" tone="danger" /><StatusBadge label="Target resolution failed" tone="danger" /></div></div></Panel></div>
      <div className="mt-6 border-t border-border pt-4 text-[11px] leading-5 text-muted-foreground"><strong className="font-semibold text-foreground">Legend:</strong> Ready means the mock projection satisfies preview eligibility; Offline, Incompatible, and Policy denied are explicit non-success states. Colour is supplementary.</div>
    </>
  )
}

function MockDeviceFrame({ device, label, prominent = false }: { device: DeviceView | undefined; label: string; prominent?: boolean }) {
  return <div aria-label={label} className={`flex aspect-[9/13] min-h-40 flex-col border border-border bg-foreground p-3 text-white ${prominent ? "mx-auto w-full max-w-[300px]" : "w-full"}`}><div className="flex items-center justify-between border-b border-white/20 pb-2 text-[10px] uppercase tracking-[0.08em]"><span>Mock / preview</span><span className="drift-data">1080×1920</span></div><div className="flex flex-1 flex-col items-center justify-center gap-3 text-center"><div className="flex size-12 items-center justify-center border border-white/40"><MonitorPlay className="size-5" aria-hidden="true" /></div><p className="text-sm font-semibold">{device?.displayName ?? "No source selected"}</p><p className="text-[10px] text-white/70">No live feed attached</p></div><div className="border-t border-white/20 pt-2 text-[10px] text-white/70">{device?.workflow ?? "Awaiting selection"}</div></div>
}

function SessionResult({ session, snapshot }: { session: MirrorSessionView; snapshot: ControlPlaneSnapshot }) {
  return <div className="space-y-4"><div className="border border-border p-3"><div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-xs font-semibold">Source · {textForDevice(snapshot.devices, session.sourceDeviceId)}</p><p className="drift-data mt-1 text-[10px] text-muted-foreground">{session.id} · {session.startedAt}</p></div><StatusBadge label={session.state} tone={session.state === "active" ? "info" : "neutral"} /></div><p className="mt-2 text-xs text-muted-foreground">{session.sourceResult}</p></div><div className="grid gap-3 md:grid-cols-2">{session.followerResults.map((result) => <div key={result.deviceId} className="border border-border p-3"><div className="flex items-center justify-between gap-2"><p className="text-xs font-semibold">Follower · {textForDevice(snapshot.devices, result.deviceId)}</p><StatusBadge label={result.outcome.replaceAll("_", " ")} tone={result.outcome === "simulated_success" ? "healthy" : "attention"} /></div><p className="mt-2 text-[11px] leading-5 text-muted-foreground">{result.detail}</p></div>)}</div></div>
}

function eligibilityLabel(value: ControlEligibility): string {
  return value === "policy_denied" ? "Policy denied" : value.replaceAll("_", " ")
}

function eligibilityTone(value: ControlEligibility): StatusTone {
  return value === "eligible" ? "healthy" : value === "policy_denied" ? "danger" : "attention"
}

function eligibilityDetail(value: ControlEligibility): string {
  switch (value) {
    case "eligible": return "Fresh mock observation and required capability present."
    case "offline": return "No current transport; action is withheld."
    case "incompatible": return "Capability projection does not match this preview."
    case "policy_denied": return "Active policy denies this target."
  }
}
