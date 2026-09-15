import { useMemo, useState, type FormEvent } from "react"
import { ArrowDown, ArrowUp, MoveRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { ControlPlaneSnapshot, DispatchIntent } from "@/lib/domain/control-plane"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { DataTablePagination, FieldLabel, PageIntro, StatusBadge } from "./shared"
import { activeMemberships, textForDevice } from "./page-utils"

export function GroupsPage({ snapshot, dispatch, view = "groups", onViewChange }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent; view?: string; onViewChange?: (view: string) => void }) {
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null); const [deviceId, setDeviceId] = useState(snapshot.devices[0]?.id ?? ""); const [targetGroup, setTargetGroup] = useState(snapshot.groups[0]?.id ?? ""); const [feedback, setFeedback] = useState(""); const [groupName, setGroupName] = useState("")
  const memberships = useMemo(() => activeMemberships(snapshot.memberships), [snapshot.memberships]); const group = snapshot.groups.find((candidate) => candidate.id === selectedGroup)
  function move(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void reportDispatch(dispatch, { type: "moveDeviceToGroup", deviceId, groupId: targetGroup, position: 1 }, setFeedback)
  }
  function moveWithinGroup(deviceId: string, groupId: string, position: number) {
    void reportDispatch(dispatch, { type: "moveDeviceToGroup", deviceId, groupId, position }, setFeedback)
  }
  function createGroup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void reportDispatch(dispatch, { type: "createDeviceGroup", name: groupName }, setFeedback).then((outcome) => {
      if (outcome.ok) setGroupName("")
    })
  }
  return <><PageIntro eyebrow="INVENTORY / GROUPS" title="Groups and Membership" description="Group membership and ordering remain explicit. Ungrouped is a computed view, never a persisted authority." actions={<StatusBadge label={`${snapshot.groups.length} groups`} tone="info" />} />
    <Tabs value={view} onValueChange={onViewChange}><TabsList className="rounded-none border border-border bg-background p-0" aria-label="Group views"><TabsTrigger value="groups" className="rounded-none">Groups</TabsTrigger><TabsTrigger value="membership" className="rounded-none">Membership</TabsTrigger><TabsTrigger value="ordering" className="rounded-none">Ordering</TabsTrigger></TabsList>
      <TabsContent value="groups">
        <form onSubmit={createGroup} className="mt-4 flex flex-wrap items-end gap-3 border border-border p-4">
          <div>
            <FieldLabel htmlFor="new-group-name">Group Name</FieldLabel>
            <input id="new-group-name" value={groupName} onChange={(event) => setGroupName(event.target.value)} className="mt-1 h-9 rounded-none border border-input bg-background px-2 text-xs" />
          </div>
          <Button type="submit" size="sm" variant="outline" disabled={!groupName.trim()}>Create Group</Button>
          <p aria-live="polite" className="text-[11px] text-muted-foreground">{feedback}</p>
        </form>
        <div className="mt-4 grid gap-3 md:grid-cols-3">{snapshot.groups.map((item) => <button key={item.id} onClick={() => setSelectedGroup(item.id)} className="border border-border p-4 text-left hover:bg-muted/50"><div className="flex justify-between"><p className="text-sm font-semibold">{item.name}</p><StatusBadge label={item.state} tone={item.state === "active" ? "healthy" : "neutral"} /></div><p className="mt-3 text-xs text-muted-foreground">{memberships.filter((membership) => membership.groupId === item.id).length} active members</p></button>)}</div>
      </TabsContent>
      <TabsContent value="membership"><MembershipTable snapshot={snapshot} memberships={memberships} deviceId={deviceId} targetGroup={targetGroup} feedback={feedback} onDeviceId={setDeviceId} onTargetGroup={setTargetGroup} onMove={move} /></TabsContent>
      <TabsContent value="ordering"><div className="mt-4 space-y-3">{snapshot.groups.map((item) => <div key={item.id} className="border border-border p-4"><p className="text-xs font-semibold">{item.name}</p><ol className="mt-3 space-y-2">{memberships.filter((membership) => membership.groupId === item.id).sort((left, right) => left.position - right.position).map((membership) => <li key={membership.id} className="flex items-center gap-3 border-t border-border pt-2 text-xs"><span className="drift-data">{membership.position}</span><span className="flex-1">{textForDevice(snapshot.devices, membership.deviceId)}</span><Button size="icon-sm" variant="outline" aria-label={`Move ${textForDevice(snapshot.devices, membership.deviceId)} up`} disabled={membership.position <= 1} onClick={() => moveWithinGroup(membership.deviceId, item.id, membership.position - 1)}><ArrowUp className="size-3.5" /></Button><Button size="icon-sm" variant="outline" aria-label={`Move ${textForDevice(snapshot.devices, membership.deviceId)} down`} onClick={() => moveWithinGroup(membership.deviceId, item.id, membership.position + 1)}><ArrowDown className="size-3.5" /></Button></li>)}</ol></div>)}</div></TabsContent>
    </Tabs><Sheet open={Boolean(group)} onOpenChange={(open) => !open && setSelectedGroup(null)}><SheetContent className="rounded-none"><SheetHeader className="border-b border-border"><SheetTitle>{group?.name ?? "Group"}</SheetTitle><SheetDescription>Membership operations and ordering are explicit.</SheetDescription></SheetHeader>{group ? <div className="p-4 text-xs">{memberships.filter((membership) => membership.groupId === group.id).length} active members · row version {group.rowVersion}</div> : null}</SheetContent></Sheet>
  </> }

function MembershipTable({
  snapshot,
  memberships,
  deviceId,
  targetGroup,
  feedback,
  onDeviceId,
  onTargetGroup,
  onMove,
}: {
  snapshot: ControlPlaneSnapshot
  memberships: ControlPlaneSnapshot["memberships"]
  deviceId: string
  targetGroup: string
  feedback: string
  onDeviceId: (value: string) => void
  onTargetGroup: (value: string) => void
  onMove: (event: FormEvent<HTMLFormElement>) => void
}) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(10)
  const visible = memberships.slice(page * pageSize, (page + 1) * pageSize)
  return (
    <div className="mt-4 grid gap-4 lg:grid-cols-[1fr_320px]">
      <div>
        <table className="w-full border border-border text-left text-xs">
          <thead><tr className="border-b border-border text-[10px] uppercase tracking-[.08em] text-muted-foreground"><th className="p-3">Device</th><th className="p-3">Group</th><th className="p-3">Position</th></tr></thead>
          <tbody>{visible.map((membership) => <tr key={membership.id} className="border-b border-border/70"><td className="p-3">{textForDevice(snapshot.devices, membership.deviceId)}</td><td className="p-3">{snapshot.groups.find((item) => item.id === membership.groupId)?.name ?? membership.groupId}</td><td className="p-3">{membership.position}</td></tr>)}</tbody>
        </table>
        <DataTablePagination page={page} pageSize={pageSize} total={memberships.length} onPageChange={setPage} onPageSizeChange={(next) => { setPageSize(next); setPage(0) }} />
      </div>
      <form onSubmit={onMove} className="border border-border p-4">
        <p className="text-xs font-semibold uppercase tracking-[.08em]">Bulk move</p>
        <p className="mt-1 text-[11px] text-muted-foreground">One clear typed move action.</p>
        <div className="mt-4"><FieldLabel htmlFor="group-device">Device</FieldLabel><select id="group-device" value={deviceId} onChange={(event) => onDeviceId(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs">{snapshot.devices.map((device) => <option key={device.id} value={device.id}>{device.displayName}</option>)}</select></div>
        <div className="mt-3"><FieldLabel htmlFor="target-group">Target group</FieldLabel><select id="target-group" value={targetGroup} onChange={(event) => onTargetGroup(event.target.value)} className="mt-1 h-9 w-full rounded-none border border-input bg-background px-2 text-xs">{snapshot.groups.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></div>
        <Button type="submit" className="mt-4 w-full rounded-none"><MoveRight className="size-3.5" aria-hidden="true" />Move device</Button>
        <p aria-live="polite" className="mt-2 text-[11px] text-muted-foreground">{feedback}</p>
      </form>
    </div>
  )
}
