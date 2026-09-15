// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { AgentsPage } from "./AgentsPage"

describe("AgentsPage", () => {
  it("creates an automation agent and assigns an explicit device", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)
    const view = (tab: string) => (
      <AgentsPage snapshot={client.getSnapshot()} dispatch={dispatch} view={tab} onViewChange={() => undefined} />
    )
    const { rerender } = render(view("profiles"))

    expect(screen.getByRole("heading", { name: "Agent Profiles" })).toBeInTheDocument()
    await user.type(screen.getByLabelText("Agent Name"), "Night steward")
    await user.click(screen.getByRole("button", { name: "Create Agent" }))
    rerender(view("assignments"))

    expect(client.getSnapshot().automationAgents.some((agent) => agent.name === "Night steward")).toBe(true)
    await user.selectOptions(screen.getByLabelText("Automation Agent"), "Night steward")
    await user.selectOptions(screen.getByLabelText("Device"), "Atlas 04")
    await user.click(screen.getByRole("button", { name: "Assign Device" }))
    rerender(view("assignments"))

    expect(screen.getByText(/Automation agent assigned to the selected device/i)).toBeInTheDocument()
    const created = client.getSnapshot().automationAgents.find((agent) => agent.name === "Night steward")
    expect(client.getSnapshot().automationAgentProfiles.find((profile) => profile.automationAgentId === created?.id)?.assignmentSummary).toBe("Atlas 04")
  })
})
