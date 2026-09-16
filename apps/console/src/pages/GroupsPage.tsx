import { useMemo, useState, type FormEvent } from "react"
import { ArrowDown, ArrowUp, MoveRight, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import type { ControlPlaneIntent, ControlPlaneSnapshot, DeviceView, DispatchIntent, GroupView } from "@/lib/domain/control-plane"
import { reportDispatch } from "@/lib/api/report-dispatch"
import { EmptyState, FieldLabel, PageIntro, StatusBadge } from "./shared"
import { activeMemberships, orderedGroups, textForDevice, ungroupedDevices } from "./page-utils"

/**
 * Groups is one surface. Membership and ordering were only ever the same
 * relation viewed twice, so both are edited in place on the group they belong
 * to and there is no tab to hold a stale copy of either.
 *
 * Feedback is addressed by the scope of the action that produced it, so a
 * message never surfaces against a control the operator did not touch.
 */
export function GroupsPage({ snapshot, dispatch }: { snapshot: ControlPlaneSnapshot; dispatch: DispatchIntent }) {
  const [groupName, setGroupName] = useState("")
  const [renameDrafts, setRenameDrafts] = useState<Record<string, string>>({})
  const [assignDrafts, setAssignDrafts] = useState<Record<string, string>>({})
  const [ungroupedDrafts, setUngroupedDrafts] = useState<Record<string, string>>({})
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<Record<string, string>>({})

  const memberships = useMemo(() => activeMemberships(snapshot.memberships), [snapshot.memberships])
  const groups = useMemo(() => orderedGroups(snapshot.groups), [snapshot.groups])
  const ungrouped = useMemo(() => ungroupedDevices(snapshot.devices, snapshot.memberships), [snapshot.devices, snapshot.memberships])

  function announce(scope: string) {
    return (message: string) => setFeedback((previous) => ({ ...previous, [scope]: message }))
  }

  function report(scope: string, intent: ControlPlaneIntent) {
    return reportDispatch(dispatch, intent, announce(scope))
  }

  function membersOf(groupId: string) {
    return memberships.filter((membership) => membership.groupId === groupId).sort((left, right) => left.position - right.position)
  }

  function createGroup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void report("create", { type: "createDeviceGroup", name: groupName }).then((outcome) => {
      if (outcome.ok) setGroupName("")
    })
  }

  function renameGroup(event: FormEvent<HTMLFormElement>, group: GroupView) {
    event.preventDefault()
    const name = (renameDrafts[group.id] ?? group.name).trim()
    if (!name) return
    void report(`group:${group.id}`, { type: "renameDeviceGroup", groupId: group.id, name, rowVersion: group.rowVersion })
  }

  function deleteGroup(group: GroupView, confirmed: boolean) {
    void report(`group:${group.id}`, { type: "deleteDeviceGroup", groupId: group.id, rowVersion: group.rowVersion, confirmed }).then((outcome) => {
      if (outcome.ok) setPendingDelete(null)
    })
  }

  function moveGroup(index: number, delta: number) {
    const target = index + delta
    if (target < 0 || target >= groups.length) return
    const ids = groups.map((group) => group.id)
    const reordered = [...ids]
    const moved = reordered[index]
    const displaced = reordered[target]
    if (moved === undefined || displaced === undefined) return
    reordered[index] = displaced
    reordered[target] = moved
    void report("groups", { type: "reorderDeviceGroups", groupIds: reordered })
  }

  function moveWithinGroup(deviceId: string, groupId: string, position: number) {
    void report(`group:${groupId}`, { type: "moveDeviceToGroup", deviceId, groupId, position })
  }

  function removeFromGroup(deviceId: string, groupId: string) {
    void report(`group:${groupId}`, { type: "removeDeviceFromGroup", deviceId })
  }

  function assignToGroup(event: FormEvent<HTMLFormElement>, group: GroupView) {
    event.preventDefault()
    const deviceId = assignDrafts[group.id] ?? ""
    if (!deviceId) return
    const position = membersOf(group.id).reduce((highest, membership) => Math.max(highest, membership.position), 0) + 1
    void report(`group:${group.id}`, { type: "moveDeviceToGroup", deviceId, groupId: group.id, position })
  }

  function assignUngrouped(event: FormEvent<HTMLFormElement>, device: DeviceView) {
    event.preventDefault()
    const groupId = ungroupedDrafts[device.id] ?? ""
    if (!groupId) return
    const position = membersOf(groupId).reduce((highest, membership) => Math.max(highest, membership.position), 0) + 1
    void report("ungrouped", { type: "moveDeviceToGroup", deviceId: device.id, groupId, position })
  }

  const activeGroups = groups.filter((group) => group.state === "active")

  return (
    <>
      <PageIntro
        eyebrow="INVENTORY / GROUPS"
        title="Groups and Membership"
        description="Membership and ordering are edited in place on the group they belong to. Ungrouped is a computed view, never a persisted authority."
        actions={<StatusBadge label={`${snapshot.groups.length} groups`} tone="info" />}
      />

      <form onSubmit={createGroup} className="mt-4 flex flex-wrap items-end gap-3 border border-border p-4">
        <div>
          <FieldLabel htmlFor="new-group-name">Group Name</FieldLabel>
          <input id="new-group-name" value={groupName} onChange={(event) => setGroupName(event.target.value)} className="mt-1 h-9 rounded-none border border-input bg-background px-2 text-xs" />
        </div>
        <Button type="submit" size="sm" variant="outline" disabled={!groupName.trim()}>Create Group</Button>
        <p aria-live="polite" data-feedback-scope="create" className="text-[11px] text-muted-foreground">{feedback["create"] ?? ""}</p>
      </form>

      <p aria-live="polite" data-feedback-scope="groups" className="mt-2 text-[11px] text-muted-foreground">{feedback["groups"] ?? ""}</p>

      <div className="mt-4 space-y-4">
        {groups.length === 0 ? <EmptyState label="No groups yet" detail="Create a group to place devices." /> : null}
        {groups.map((group, index) => {
          const members = membersOf(group.id)
          const retired = group.state !== "active"
          const outside = snapshot.devices.filter((device) => !members.some((membership) => membership.deviceId === device.id))
          return (
            <section key={group.id} aria-label={group.name} className="border border-border p-4">
              <div className="flex flex-wrap items-center gap-3">
                <h2 className="text-sm font-semibold">{group.name}</h2>
                <StatusBadge label={group.state} tone={retired ? "neutral" : "healthy"} />
                <span className="text-[11px] text-muted-foreground">{members.length} placed · position {group.position} · row version {group.rowVersion}</span>
                <div className="ml-auto flex items-center gap-1">
                  <Button size="icon-sm" variant="outline" aria-label={`Move ${group.name} up`} disabled={index === 0 || retired} onClick={() => moveGroup(index, -1)}><ArrowUp className="size-3.5" /></Button>
                  <Button size="icon-sm" variant="outline" aria-label={`Move ${group.name} down`} disabled={index === groups.length - 1 || retired} onClick={() => moveGroup(index, 1)}><ArrowDown className="size-3.5" /></Button>
                </div>
              </div>

              <div className="mt-3 flex flex-wrap items-end gap-3 border-t border-border pt-3">
                <form onSubmit={(event) => renameGroup(event, group)} className="flex flex-wrap items-end gap-2">
                  <div>
                    <FieldLabel htmlFor={`rename-${group.id}`}>Rename {group.name}</FieldLabel>
                    <input
                      id={`rename-${group.id}`}
                      aria-label={`New name for ${group.name}`}
                      value={renameDrafts[group.id] ?? group.name}
                      disabled={retired}
                      onChange={(event) => setRenameDrafts((previous) => ({ ...previous, [group.id]: event.target.value }))}
                      className="mt-1 h-9 rounded-none border border-input bg-background px-2 text-xs"
                    />
                  </div>
                  <Button type="submit" size="sm" variant="outline" aria-label={`Rename ${group.name}`} disabled={retired || !(renameDrafts[group.id] ?? group.name).trim()}>Rename</Button>
                </form>
                {pendingDelete === group.id ? (
                  <div className="ml-auto flex items-center gap-2">
                    <Button size="sm" aria-label={`Confirm delete ${group.name}`} onClick={() => deleteGroup(group, true)}>Confirm delete</Button>
                    <Button size="sm" variant="outline" aria-label={`Cancel delete ${group.name}`} onClick={() => setPendingDelete(null)}>Cancel</Button>
                  </div>
                ) : (
                  <Button size="sm" variant="outline" aria-label={`Delete ${group.name}`} disabled={retired} onClick={() => setPendingDelete(group.id)} className="ml-auto">
                    <X className="size-3.5" aria-hidden="true" />Delete group
                  </Button>
                )}
              </div>

              <ol className="mt-3 space-y-2">
                {members.map((membership, memberIndex) => (
                  <li key={membership.id} className="flex items-center gap-3 border-t border-border pt-2 text-xs">
                    <span className="drift-data">{membership.position}</span>
                    <span className="flex-1">{textForDevice(snapshot.devices, membership.deviceId)}</span>
                    <Button size="icon-sm" variant="outline" aria-label={`Move ${textForDevice(snapshot.devices, membership.deviceId)} up`} disabled={retired || memberIndex === 0} onClick={() => moveWithinGroup(membership.deviceId, group.id, membership.position - 1)}><ArrowUp className="size-3.5" /></Button>
                    <Button size="icon-sm" variant="outline" aria-label={`Move ${textForDevice(snapshot.devices, membership.deviceId)} down`} disabled={retired || memberIndex === members.length - 1} onClick={() => moveWithinGroup(membership.deviceId, group.id, membership.position + 1)}><ArrowDown className="size-3.5" /></Button>
                    <Button size="icon-sm" variant="outline" aria-label={`Remove ${textForDevice(snapshot.devices, membership.deviceId)} from ${group.name}`} disabled={retired} onClick={() => removeFromGroup(membership.deviceId, group.id)}><X className="size-3.5" /></Button>
                  </li>
                ))}
                {members.length === 0 ? <li className="border-t border-border pt-2 text-xs text-muted-foreground">No active placements.</li> : null}
              </ol>

              <form onSubmit={(event) => assignToGroup(event, group)} className="mt-3 flex flex-wrap items-end gap-3 border-t border-border pt-3">
                <div>
                  <FieldLabel htmlFor={`assign-${group.id}`}>Device to assign to {group.name}</FieldLabel>
                  <select
                    id={`assign-${group.id}`}
                    aria-label={`Device to assign to ${group.name}`}
                    value={assignDrafts[group.id] ?? ""}
                    disabled={retired}
                    onChange={(event) => setAssignDrafts((previous) => ({ ...previous, [group.id]: event.target.value }))}
                    className="mt-1 h-9 rounded-none border border-input bg-background px-2 text-xs"
                  >
                    <option value="">Select Device</option>
                    {outside.map((device) => <option key={device.id} value={device.id}>{device.displayName}</option>)}
                  </select>
                </div>
                <Button type="submit" size="sm" className="rounded-none" aria-label={`Assign to ${group.name}`} disabled={retired || !assignDrafts[group.id]}><MoveRight className="size-3.5" aria-hidden="true" />Assign</Button>
                <p aria-live="polite" data-feedback-scope={`group:${group.id}`} className="text-[11px] text-muted-foreground">{feedback[`group:${group.id}`] ?? ""}</p>
              </form>
            </section>
          )
        })}
      </div>

      <section aria-label="Ungrouped" className="mt-4 border border-border p-4">
        <div className="flex items-center gap-3">
          <h2 className="text-sm font-semibold">Ungrouped</h2>
          <StatusBadge label="computed" tone="neutral" />
          <span className="text-[11px] text-muted-foreground">Devices with no active placement. Not a stored group.</span>
        </div>
        {ungrouped.length === 0 ? (
          <div className="mt-3"><EmptyState label="Every device is placed" detail="Ungrouped is empty because each device holds an active membership." /></div>
        ) : (
          <ol className="mt-3 space-y-2">
            {ungrouped.map((device) => (
              <li key={device.id} className="flex flex-wrap items-center gap-3 border-t border-border pt-2 text-xs">
                <span className="flex-1">{device.displayName}</span>
                <form onSubmit={(event) => assignUngrouped(event, device)} className="flex items-end gap-2">
                  <select
                    aria-label={`Group for ${device.displayName}`}
                    value={ungroupedDrafts[device.id] ?? ""}
                    onChange={(event) => setUngroupedDrafts((previous) => ({ ...previous, [device.id]: event.target.value }))}
                    className="h-9 rounded-none border border-input bg-background px-2 text-xs"
                  >
                    <option value="">Select Group</option>
                    {activeGroups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}
                  </select>
                  <Button type="submit" size="sm" variant="outline" aria-label={`Add ${device.displayName} to group`} disabled={!ungroupedDrafts[device.id]}>Add to group</Button>
                </form>
              </li>
            ))}
          </ol>
        )}
        <p aria-live="polite" data-feedback-scope="ungrouped" className="mt-3 text-[11px] text-muted-foreground">{feedback["ungrouped"] ?? ""}</p>
      </section>
    </>
  )
}
