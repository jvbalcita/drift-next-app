// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { GroupsPage } from "./GroupsPage"

function harness() {
  const client = new MockControlPlaneClient()
  const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)
  const view = () => <GroupsPage snapshot={client.getSnapshot()} dispatch={dispatch} />
  return { client, view }
}

describe("GroupsPage", () => {
  it("renders one tabless surface instead of the Groups, Membership and Ordering tabs", () => {
    const { view } = harness()
    render(view())

    expect(screen.queryByRole("tablist")).not.toBeInTheDocument()
    expect(screen.queryByRole("tab")).not.toBeInTheDocument()
    expect(screen.getByRole("heading", { name: "Groups and Membership" })).toBeInTheDocument()
    expect(screen.getByRole("region", { name: "Rack A" })).toBeInTheDocument()
    expect(screen.getByRole("region", { name: "Ungrouped" })).toBeInTheDocument()
  })

  it("shows each group's devices in order with their positions", () => {
    const { view } = harness()
    render(view())

    const rackA = within(screen.getByRole("region", { name: "Rack A" }))
    const rows = rackA.getAllByRole("listitem")
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent("Atlas 04")
    expect(rows[0]).toHaveTextContent("1")
    expect(rows[1]).toHaveTextContent("Atlas 07")
    expect(rows[1]).toHaveTextContent("2")
  })

  it("creates a named group through the control plane", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    await user.type(screen.getByLabelText("Group Name"), "Rack D")
    await user.click(screen.getByRole("button", { name: "Create Group" }))
    rerender(view())

    expect(screen.getByText(/Device group created/i)).toBeInTheDocument()
    expect(screen.getByRole("region", { name: "Rack D" })).toBeInTheDocument()
    expect(client.getSnapshot().groups.some((group) => group.name === "Rack D")).toBe(true)
  })

  it("assigns an ungrouped device to a group", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    // Orion 03 has no active membership, so it is visible in the computed
    // Ungrouped region without any persisted Ungrouped authority.
    const ungrouped = within(screen.getByRole("region", { name: "Ungrouped" }))
    expect(ungrouped.getByText("Orion 03")).toBeInTheDocument()

    await user.selectOptions(ungrouped.getByLabelText("Group for Orion 03"), "group-rack-c")
    await user.click(ungrouped.getByRole("button", { name: "Add Orion 03 to group" }))
    rerender(view())

    const active = client.getSnapshot().memberships.find((membership) => membership.deviceId === "orion-03" && membership.state === "active")
    expect(active?.groupId).toBe("group-rack-c")
    expect(within(screen.getByRole("region", { name: "Rack C" })).getByText("Orion 03")).toBeInTheDocument()
    expect(client.getSnapshot().groups.some((group) => group.id === "ungrouped")).toBe(false)
  })

  it("assigns a device into a group in place", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackC = within(screen.getByRole("region", { name: "Rack C" }))
    await user.selectOptions(rackC.getByLabelText("Device to assign to Rack C"), "orion-03")
    await user.click(rackC.getByRole("button", { name: "Assign to Rack C" }))
    rerender(view())

    const active = client.getSnapshot().memberships.find((membership) => membership.deviceId === "orion-03" && membership.state === "active")
    expect(active?.groupId).toBe("group-rack-c")
  })

  it("reorders devices within a group", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackA = within(screen.getByRole("region", { name: "Rack A" }))
    await user.click(rackA.getByRole("button", { name: "Move Atlas 07 up" }))
    rerender(view())

    const positions = client
      .getSnapshot()
      .memberships.filter((membership) => membership.groupId === "group-rack-a" && membership.state === "active")
      .sort((left, right) => left.position - right.position)
    expect(positions.map((membership) => membership.deviceId)).toEqual(["atlas-07", "atlas-04"])
  })

  it("removes a device from a group so it returns to the computed Ungrouped view", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackA = within(screen.getByRole("region", { name: "Rack A" }))
    await user.click(rackA.getByRole("button", { name: "Remove Atlas 04 from Rack A" }))
    rerender(view())

    const stillActive = client.getSnapshot().memberships.some((membership) => membership.deviceId === "atlas-04" && membership.state === "active")
    expect(stillActive).toBe(false)
    expect(within(screen.getByRole("region", { name: "Ungrouped" })).getByText("Atlas 04")).toBeInTheDocument()
  })

  it("renames a group from the group section", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackB = within(screen.getByRole("region", { name: "Rack B" }))
    await user.clear(rackB.getByLabelText("New name for Rack B"))
    await user.type(rackB.getByLabelText("New name for Rack B"), "Rack E")
    await user.click(rackB.getByRole("button", { name: "Rename Rack B" }))
    rerender(view())

    expect(client.getSnapshot().groups.find((group) => group.id === "group-rack-b")?.name).toBe("Rack E")
    expect(screen.getByRole("region", { name: "Rack E" })).toBeInTheDocument()
  })

  it("deletes a group from the group section", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackC = within(screen.getByRole("region", { name: "Rack C" }))
    await user.click(rackC.getByRole("button", { name: "Delete Rack C" }))
    await user.click(within(screen.getByRole("region", { name: "Rack C" })).getByRole("button", { name: "Confirm delete Rack C" }))
    rerender(view())

    expect(client.getSnapshot().groups.find((group) => group.id === "group-rack-c")?.state).toBe("retired")
  })

  it("reorders the groups themselves", async () => {
    const user = userEvent.setup()
    const { client, view } = harness()
    const { rerender } = render(view())

    const rackC = within(screen.getByRole("region", { name: "Rack C" }))
    await user.click(rackC.getByRole("button", { name: "Move Rack C up" }))
    rerender(view())

    const ordered = [...client.getSnapshot().groups]
      .filter((group) => group.state === "active")
      .sort((left, right) => left.position - right.position)
    expect(ordered.map((group) => group.name)).toEqual(["Rack A", "Rack C", "Rack B"])
  })

  it("keeps feedback scoped to the action that produced it", async () => {
    const user = userEvent.setup()
    const { view } = harness()
    render(view())

    const createFeedback = document.querySelector('[data-feedback-scope="create"]')
    expect(createFeedback).not.toBeNull()

    const rackC = within(screen.getByRole("region", { name: "Rack C" }))
    await user.selectOptions(rackC.getByLabelText("Device to assign to Rack C"), "orion-03")
    await user.click(rackC.getByRole("button", { name: "Assign to Rack C" }))

    expect(document.querySelector('[data-feedback-scope="group:group-rack-c"]')).toHaveTextContent(/Orion 03/i)
    expect(createFeedback).toHaveTextContent("")
    expect(createFeedback).not.toHaveTextContent(/Orion 03/i)
  })
})
