import { useMemo, useState, type FormEvent } from "react";
import { toast } from "sonner";
import {
  ArrowDown,
  ArrowUp,
  FolderPlus,
  Pencil,
  Plus,
  Search,
  Trash2,
  UserPlus,
  Users,
  X,
} from "lucide-react";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import type {
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  DispatchIntent,
  GroupView,
} from "@/lib/domain/control-plane";
import {
  EmptyState,
  FieldLabel,
  FormSelect,
  PageIntro,
  StatusBadge,
} from "./shared";
import {
  activeMemberships,
  orderedGroups,
  textForDevice,
  ungroupedDevices,
} from "./page-utils";

type AssignmentMode =
  | { kind: "group"; group: GroupView }
  | { kind: "ungrouped" }
  | null;

function groupMutationError(message: string): string {
  const normalized = message.toLowerCase();
  if (
    normalized.includes("database constraint") ||
    normalized.includes("unique constraint")
  ) {
    return "A group with this name already exists. Choose a different name.";
  }
  if (normalized.includes("changed since it was loaded")) {
    return "This group changed while you were editing it. Close the dialog, review the latest group, and try again.";
  }
  if (normalized.includes("not found")) {
    return "This group no longer exists. Refresh the page and try again.";
  }
  return message.trim() || "The group could not be updated. Please try again.";
}

/** Groups is one surface: order is persisted, while Ungrouped is computed. */
export function GroupsPage({
  snapshot,
  dispatch,
}: {
  snapshot: ControlPlaneSnapshot;
  dispatch: DispatchIntent;
}) {
  const [createOpen, setCreateOpen] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [renameTarget, setRenameTarget] = useState<GroupView | null>(null);
  const [renameName, setRenameName] = useState("");
  const [assignment, setAssignment] = useState<AssignmentMode>(null);
  const [assignmentGroupId, setAssignmentGroupId] = useState("");
  const [selectedDeviceIds, setSelectedDeviceIds] = useState<string[]>([]);
  const [createError, setCreateError] = useState("");
  const [renameError, setRenameError] = useState("");
  const [assignmentError, setAssignmentError] = useState("");
  const [assignmentQuery, setAssignmentQuery] = useState("");

  const memberships = useMemo(
    () => activeMemberships(snapshot.memberships),
    [snapshot.memberships],
  );
  const groups = useMemo(
    () => orderedGroups(snapshot.groups),
    [snapshot.groups],
  );
  const activeGroups = useMemo(
    () => groups.filter((group) => group.state === "active"),
    [groups],
  );
  const ungrouped = useMemo(
    () => ungroupedDevices(snapshot.devices, snapshot.memberships).filter((device) => device.lifecycle !== "retired"),
    [snapshot.devices, snapshot.memberships],
  );

  function membersOf(groupId: string) {
    return memberships
      .filter((membership) => membership.groupId === groupId)
      .sort((left, right) => left.position - right.position);
  }
  async function notifyMutation(intent: ControlPlaneIntent) {
    const outcome = await dispatch(intent);
    if (outcome.ok) toast.success(outcome.message);
    else
      toast.error("Action failed", {
        description: groupMutationError(outcome.message),
      });
    return outcome;
  }
  async function createGroup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCreateError("");
    const outcome = await dispatch({
      type: "createDeviceGroup",
      name: groupName.trim(),
    });
    if (outcome.ok) {
      toast.success(outcome.message);
      setGroupName("");
      setCreateOpen(false);
    } else {
      setCreateError(groupMutationError(outcome.message));
    }
  }
  function openRename(group: GroupView) {
    setRenameError("");
    setRenameTarget(group);
    setRenameName(group.name);
  }
  async function renameGroup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!renameTarget || !renameName.trim()) return;
    setRenameError("");
    const outcome = await dispatch({
      type: "renameDeviceGroup",
      groupId: renameTarget.id,
      name: renameName.trim(),
      rowVersion: renameTarget.rowVersion,
    });
    if (outcome.ok) {
      toast.success(outcome.message);
      setRenameTarget(null);
    } else {
      setRenameError(groupMutationError(outcome.message));
    }
  }
  function moveGroup(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= groups.length) return;
    const reordered = groups.map((group) => group.id);
    const moved = reordered[index];
    const displaced = reordered[target];
    if (!moved || !displaced) return;
    reordered[index] = displaced;
    reordered[target] = moved;
    void notifyMutation({ type: "reorderDeviceGroups", groupIds: reordered });
  }
  function openGroupAssignment(group: GroupView) {
    setAssignmentError("");
    setSelectedDeviceIds([]);
    setAssignmentQuery("");
    setAssignmentGroupId(group.id);
    setAssignment({ kind: "group", group });
  }
  function openUngroupedAssignment() {
    setAssignmentError("");
    setSelectedDeviceIds([]);
    setAssignmentQuery("");
    setAssignmentGroupId(activeGroups[0]?.id ?? "");
    setAssignment({ kind: "ungrouped" });
  }
  function toggleSelectedDevice(deviceId: string, checked: boolean) {
    setSelectedDeviceIds((current) =>
      checked
        ? [...current, deviceId]
        : current.filter((id) => id !== deviceId),
    );
  }
  async function assignSelected(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const groupId =
      assignment?.kind === "group" ? assignment.group.id : assignmentGroupId;
    if (!groupId || selectedDeviceIds.length === 0) return;
    setAssignmentError("");
    let position =
      membersOf(groupId).reduce(
        (highest, membership) => Math.max(highest, membership.position),
        0,
      ) + 1;
    for (const deviceId of selectedDeviceIds) {
      const outcome = await dispatch({
        type: "moveDeviceToGroup",
        deviceId,
        groupId,
        position,
      });
      if (!outcome.ok) {
        setAssignmentError(groupMutationError(outcome.message));
        return;
      }
      position += 1;
    }
    toast.success(
      selectedDeviceIds.length === 1
        ? "Device assigned to group."
        : `${selectedDeviceIds.length} devices assigned to group.`,
    );
    setAssignment(null);
    setSelectedDeviceIds([]);
  }

  const eligibleAssignmentCandidates =
    assignment?.kind === "group"
      ? snapshot.devices.filter(
          (device) =>
            device.lifecycle !== "retired" &&
            !membersOf(assignment.group.id).some(
              (membership) => membership.deviceId === device.id,
            ),
        )
      : ungrouped;
  const normalizedAssignmentQuery = assignmentQuery.trim().toLowerCase();
  const assignmentCandidates = eligibleAssignmentCandidates.filter((device) => {
    const endpointText = snapshot.endpoints
      .filter((endpoint) => endpoint.deviceId === device.id && endpoint.state === "current")
      .flatMap((endpoint) => [endpoint.host, endpoint.serial, String(endpoint.port)])
      .join(" ");
    return `${device.displayName} ${device.status} ${endpointText}`.toLowerCase().includes(normalizedAssignmentQuery);
  });
  const visibleCandidateIds = assignmentCandidates.map((device) => device.id);
  const selectedVisibleCount = visibleCandidateIds.filter((id) => selectedDeviceIds.includes(id)).length;
  const allVisibleSelected = visibleCandidateIds.length > 0 && selectedVisibleCount === visibleCandidateIds.length;
  const someVisibleSelected = selectedVisibleCount > 0 && !allVisibleSelected;

  function toggleAllVisible(checked: boolean) {
    const visible = new Set(visibleCandidateIds);
    setSelectedDeviceIds((current) => checked
      ? [...new Set([...current, ...visibleCandidateIds])]
      : current.filter((id) => !visible.has(id)));
  }

  return (
    <>
      <PageIntro
        eyebrow="INVENTORY / GROUPS"
        title="Groups and Membership"
        description="Organize devices into ordered operational groups. Unassigned devices remain visible until placed."
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <StatusBadge
              label={`${activeGroups.length} active groups`}
              tone="info"
            />
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <FolderPlus className="size-3.5" aria-hidden="true" />
              New group
            </Button>
          </div>
        }
      />
      <div className="mt-3 grid min-w-0 gap-5 xl:grid-cols-[minmax(0,1fr)_20rem] xl:items-start">
        <div className="min-w-0 space-y-4">
          <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-3">
            <div>
              <h2 className="text-sm font-semibold">Device groups</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Ordered containers used throughout the workspace.
              </p>
            </div>
            <span className="drift-data text-muted-foreground">
              {memberships.length} active placements
            </span>
          </div>
          {groups.length === 0 ? (
            <EmptyState
              label="No groups yet"
              detail="Create a group, then assign one or more devices."
            />
          ) : null}
          {groups.map((group, index) => {
            const members = membersOf(group.id);
            const retired = group.state !== "active";
            return (
              <section key={group.id} aria-label={group.name}>
                <Card className="rounded-none">
                  <CardHeader className="border-b border-border">
                    <CardTitle className="flex flex-wrap items-center gap-2">
                      <span className="grid size-8 place-items-center border border-border bg-muted/40">
                        <Users className="size-4" aria-hidden="true" />
                      </span>
                      <span>{group.name}</span>
                      <StatusBadge
                        label={group.state}
                        tone={retired ? "neutral" : "healthy"}
                      />
                    </CardTitle>
                    <CardDescription>
                      {members.length}{" "}
                      {members.length === 1 ? "device" : "devices"} · group
                      position {group.position}
                    </CardDescription>
                    <CardAction className="flex items-center gap-1">
                      <Button
                        size="icon-sm"
                        variant="outline"
                        aria-label={`Move ${group.name} up`}
                        disabled={index === 0 || retired}
                        onClick={() => moveGroup(index, -1)}
                      >
                        <ArrowUp className="size-3.5" />
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="outline"
                        aria-label={`Move ${group.name} down`}
                        disabled={index === groups.length - 1 || retired}
                        onClick={() => moveGroup(index, 1)}
                      >
                        <ArrowDown className="size-3.5" />
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Rename ${group.name}`}
                        disabled={retired}
                        onClick={() => openRename(group)}
                      >
                        <Pencil />
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger
                          render={
                            <Button
                              size="icon-sm"
                              variant="ghost"
                              aria-label={`Delete ${group.name}`}
                              disabled={retired}
                            />
                          }
                        >
                          <Trash2 />
                        </AlertDialogTrigger>
                        <AlertDialogContent
                          title={`Delete ${group.name}?`}
                          description={`Permanently deletes ${group.name} from the workspace and returns its devices to the unassigned list.`}
                          confirmLabel="Delete group"
                          onConfirm={() =>
                            void notifyMutation({
                              type: "deleteDeviceGroup",
                              groupId: group.id,
                              rowVersion: group.rowVersion,
                              confirmed: true,
                            })
                          }
                        />
                      </AlertDialog>
                    </CardAction>
                  </CardHeader>
                  <CardContent className="px-0">
                    {members.length === 0 ? (
                      <div className="px-4 py-7 text-center">
                        <p className="text-xs font-medium">
                          No devices in this group
                        </p>
                        <p className="mt-1 text-[11px] text-muted-foreground">
                          Assign devices to make this group operational.
                        </p>
                      </div>
                    ) : (
                      <ol className="divide-y divide-border">
                        {members.map((membership, memberIndex) => {
                          const deviceName = textForDevice(
                            snapshot.devices,
                            membership.deviceId,
                          );
                          return (
                            <li
                              key={membership.id}
                              className="flex min-w-0 items-center gap-3 px-4 py-3 text-xs"
                            >
                              <span className="grid size-7 shrink-0 place-items-center border border-border bg-muted/40 font-mono text-[10px]">
                                {membership.position}
                              </span>
                              <span className="min-w-0 flex-1 truncate font-medium">
                                {deviceName}
                              </span>
                              <div className="flex shrink-0 items-center gap-1">
                                <Button
                                  size="icon-sm"
                                  variant="ghost"
                                  aria-label={`Move ${deviceName} up`}
                                  disabled={retired || memberIndex === 0}
                                  onClick={() =>
                                    void notifyMutation({
                                      type: "moveDeviceToGroup",
                                      deviceId: membership.deviceId,
                                      groupId: group.id,
                                      position: membership.position - 1,
                                    })
                                  }
                                >
                                  <ArrowUp />
                                </Button>
                                <Button
                                  size="icon-sm"
                                  variant="ghost"
                                  aria-label={`Move ${deviceName} down`}
                                  disabled={
                                    retired ||
                                    memberIndex === members.length - 1
                                  }
                                  onClick={() =>
                                    void notifyMutation({
                                      type: "moveDeviceToGroup",
                                      deviceId: membership.deviceId,
                                      groupId: group.id,
                                      position: membership.position + 1,
                                    })
                                  }
                                >
                                  <ArrowDown />
                                </Button>
                                <Button
                                  size="icon-sm"
                                  variant="ghost"
                                  aria-label={`Remove ${deviceName} from ${group.name}`}
                                  disabled={retired}
                                  onClick={() =>
                                    void notifyMutation({
                                      type: "removeDeviceFromGroup",
                                      deviceId: membership.deviceId,
                                    })
                                  }
                                >
                                  <X />
                                </Button>
                              </div>
                            </li>
                          );
                        })}
                      </ol>
                    )}
                  </CardContent>
                  <CardFooter className="justify-end rounded-none">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={retired}
                      onClick={() => openGroupAssignment(group)}
                    >
                      <UserPlus className="size-3.5" />
                      Assign devices
                    </Button>
                  </CardFooter>
                </Card>
              </section>
            );
          })}
        </div>

        <section aria-label="Ungrouped" className="xl:sticky xl:top-4">
          <Card className="rounded-none">
            <CardHeader className="border-b border-border">
              <CardTitle className="flex items-center gap-2">
                <span>Unassigned devices</span>
                <StatusBadge
                  label={`${ungrouped.length}`}
                  tone={ungrouped.length ? "attention" : "neutral"}
                />
              </CardTitle>
              <CardDescription>
                Devices with no active placement. This is a computed view, not a
                stored group.
              </CardDescription>
            </CardHeader>
            <CardContent className="px-0">
              {ungrouped.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    label="Every device is placed"
                    detail="Newly discovered devices will appear here."
                  />
                </div>
              ) : (
                <ScrollArea className="h-72 xl:h-[26rem]">
                <ul className="divide-y divide-border">
                  {ungrouped.map((device) => (
                    <li
                      key={device.id}
                      className="flex items-center gap-3 px-4 py-3 text-xs"
                    >
                      <span
                        className="size-2 shrink-0 bg-amber-500"
                        aria-hidden="true"
                      />
                      <span className="min-w-0 flex-1 truncate font-medium">
                        {device.displayName}
                      </span>
                    </li>
                  ))}
                </ul>
                </ScrollArea>
              )}
            </CardContent>
            <CardFooter className="rounded-none">
              <Button
                className="w-full"
                size="sm"
                variant="outline"
                disabled={!ungrouped.length || !activeGroups.length}
                onClick={openUngroupedAssignment}
              >
                <UserPlus className="size-3.5" />
                Assign unassigned devices
              </Button>
            </CardFooter>
          </Card>
        </section>
      </div>

      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open);
          if (!open) {
            setGroupName("");
            setCreateError("");
          }
        }}
      >
        <DialogContent className="rounded-none">
          <form onSubmit={createGroup}>
            <DialogHeader>
              <DialogTitle>Create device group</DialogTitle>
              <DialogDescription>
                Add an ordered group to the workspace. Devices can be assigned
                after creation.
              </DialogDescription>
            </DialogHeader>
            <div className="py-5">
              <FieldLabel htmlFor="new-group-name">Group name</FieldLabel>
              <Input
                id="new-group-name"
                autoFocus
                value={groupName}
                onChange={(event) => {
                  setGroupName(event.target.value);
                  if (createError) setCreateError("");
                }}
                aria-invalid={createError ? true : undefined}
                aria-describedby={
                  createError ? "new-group-name-error" : undefined
                }
                className="mt-1 rounded-none"
              />
              {createError ? (
                <p
                  id="new-group-name-error"
                  role="alert"
                  className="mt-2 text-xs text-destructive"
                >
                  {createError}
                </p>
              ) : null}
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={!groupName.trim()}>
                <Plus />
                Create group
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={renameTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRenameTarget(null);
            setRenameError("");
          }
        }}
      >
        <DialogContent className="rounded-none">
          <form onSubmit={renameGroup}>
            <DialogHeader>
              <DialogTitle>Rename group</DialogTitle>
              <DialogDescription>
                Update the group label without changing its devices or position.
              </DialogDescription>
            </DialogHeader>
            <div className="py-5">
              <FieldLabel htmlFor="rename-group-name">Group name</FieldLabel>
              <Input
                id="rename-group-name"
                autoFocus
                value={renameName}
                onChange={(event) => {
                  setRenameName(event.target.value);
                  if (renameError) setRenameError("");
                }}
                aria-invalid={renameError ? true : undefined}
                aria-describedby={
                  renameError ? "rename-group-name-error" : undefined
                }
                className="mt-1 rounded-none"
              />
              {renameError ? (
                <p
                  id="rename-group-name-error"
                  role="alert"
                  className="mt-2 text-xs text-destructive"
                >
                  {renameError}
                </p>
              ) : null}
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setRenameTarget(null)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={
                  !renameName.trim() || renameName.trim() === renameTarget?.name
                }
              >
                Save name
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={assignment !== null}
        onOpenChange={(open) => {
          if (!open) {
            setAssignment(null);
            setAssignmentError("");
            setAssignmentQuery("");
          }
        }}
      >
        <DialogContent className="rounded-none sm:max-w-lg">
          <form onSubmit={assignSelected}>
            <DialogHeader>
              <DialogTitle>
                {assignment?.kind === "group"
                  ? `Assign devices to ${assignment.group.name}`
                  : "Assign unassigned devices"}
              </DialogTitle>
              <DialogDescription>
                Select one or more devices. They are appended to the target
                group in the order shown.
              </DialogDescription>
            </DialogHeader>
            {assignment?.kind === "ungrouped" ? (
              <div className="mt-4">
                <FieldLabel htmlFor="assignment-group">Target group</FieldLabel>
                <FormSelect
                  id="assignment-group"
                  ariaLabel="Target group"
                  value={assignmentGroupId}
                  onValueChange={setAssignmentGroupId}
                  options={activeGroups.map((group) => ({
                    value: group.id,
                    label: group.name,
                  }))}
                  className="mt-1 w-full"
                />
              </div>
            ) : null}
            <div className="relative mt-4">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input aria-label="Search devices to assign" value={assignmentQuery} onChange={(event) => setAssignmentQuery(event.target.value)} placeholder="Search name, IP, serial, or status" className="pl-8" />
            </div>
            <label className="group/field-label mt-3 flex cursor-pointer items-center gap-3 border border-border px-3 py-3 text-xs font-medium">
              <Checkbox checked={allVisibleSelected} indeterminate={someVisibleSelected} disabled={visibleCandidateIds.length === 0} onCheckedChange={(checked) => toggleAllVisible(checked === true)} />
              Select all visible devices
              <span className="ml-auto text-[10px] text-muted-foreground">{selectedVisibleCount}/{visibleCandidateIds.length}</span>
            </label>
            <fieldset className="my-3 border border-border">
              <legend className="sr-only">Devices to assign</legend>
              <ScrollArea className="h-72">
              {assignmentCandidates.length === 0 ? (
                <p className="p-4 text-xs text-muted-foreground">
                  No devices are available for this assignment.
                </p>
              ) : (
                assignmentCandidates.map((device) => (
                  <label
                    key={device.id}
                    className="group/field-label flex cursor-pointer items-center gap-3 border-b border-border px-3 py-3 last:border-b-0 hover:bg-muted/40"
                  >
                    <Checkbox
                      checked={selectedDeviceIds.includes(device.id)}
                      onCheckedChange={(checked) =>
                        toggleSelectedDevice(device.id, checked === true)
                      }
                    />
                    <span className="min-w-0 flex-1 truncate text-xs font-medium">
                      {device.displayName}
                    </span>
                    <span className="text-[10px] uppercase tracking-[.08em] text-muted-foreground">
                      {device.status}
                    </span>
                  </label>
                ))
              )}
              </ScrollArea>
            </fieldset>
            {assignmentError ? (
              <p role="alert" className="mb-4 text-xs text-destructive">
                {assignmentError}
              </p>
            ) : null}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setAssignment(null)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!assignmentGroupId || selectedDeviceIds.length === 0}
              >
                Assign {selectedDeviceIds.length || ""}{" "}
                {selectedDeviceIds.length === 1 ? "device" : "devices"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
