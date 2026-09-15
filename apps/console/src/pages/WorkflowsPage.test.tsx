// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { WorkflowsPage } from "./WorkflowsPage"

describe("WorkflowsPage", () => {
  it("reviews and confirms skill publish on an existing version", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)
    const view = () => (
      <WorkflowsPage snapshot={client.getSnapshot()} dispatch={dispatch} view="skills" onViewChange={() => undefined} />
    )
    const { rerender } = render(view())

    expect(screen.getByRole("heading", { name: "Workflows" })).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Skills" })).toHaveAttribute("aria-selected", "true")

    const reviewButtons = screen.getAllByRole("button", { name: "Review Skill" })
    expect(reviewButtons[0]).toBeDisabled()
    expect(reviewButtons[1]).toBeDisabled()
    await user.click(reviewButtons[2]!)
    rerender(view())
    expect(screen.getByText(/Skill version reviewed/i)).toBeInTheDocument()
    expect(client.getSnapshot().skills.find((skill) => skill.id === "skill-draft")?.trust).toBe("reviewed")

    await user.click(screen.getAllByRole("button", { name: "Publish Skill" })[1]!)
    await user.click(within(document.body).getByRole("button", { name: "Confirm Publish" }))
    rerender(view())
    expect(screen.getByText(/Skill version published/i)).toBeInTheDocument()
    expect(client.getSnapshot().skills.find((skill) => skill.id === "skill-review")?.trust).toBe("approved")
  })
})
