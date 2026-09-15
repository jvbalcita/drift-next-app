// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { GroupsPage } from "./GroupsPage"

describe("GroupsPage", () => {
  it("creates a named group through the control plane", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)
    const view = () => (
      <GroupsPage snapshot={client.getSnapshot()} dispatch={dispatch} view="groups" onViewChange={() => undefined} />
    )
    const { rerender } = render(view())

    expect(screen.getByRole("heading", { name: "Groups and Membership" })).toBeInTheDocument()
    await user.type(screen.getByLabelText("Group Name"), "Rack D")
    await user.click(screen.getByRole("button", { name: "Create Group" }))
    rerender(view())

    expect(screen.getByText(/Device group created/i)).toBeInTheDocument()
    expect(screen.getByText("Rack D")).toBeInTheDocument()
    expect(client.getSnapshot().groups.some((group) => group.name === "Rack D")).toBe(true)
  })
})
