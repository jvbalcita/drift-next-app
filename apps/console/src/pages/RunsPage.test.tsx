// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { RunsPage } from "./RunsPage"

describe("RunsPage", () => {
  it("starts a published workflow only for confirmed explicit devices", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)
    const view = () => (
      <RunsPage snapshot={client.getSnapshot()} dispatch={dispatch} view="active" onViewChange={() => undefined} />
    )
    const { rerender } = render(view())

    expect(screen.getByRole("heading", { name: "Runs and Targets" })).toBeInTheDocument()
    expect(screen.getByLabelText("Published Workflow")).toHaveDisplayValue(/Content validation/)
    expect(screen.getByRole("checkbox", { name: "Atlas 04" })).toBeChecked()

    await user.click(screen.getByRole("checkbox", { name: "Nova 02" }))
    await user.click(screen.getByRole("button", { name: "Start Run" }))
    await user.click(within(document.body).getByRole("button", { name: "Confirm Start Run" }))
    rerender(view())

    expect(screen.getByText(/Workflow run requested for the selected devices/i)).toBeInTheDocument()
    const run = client.getSnapshot().runs.find((candidate) => candidate.state === "requested")
    expect(run?.workflowName).toBe("Content validation")
    expect(client.getSnapshot().runTargets.filter((target) => target.runId === run?.id).map((target) => target.deviceId)).toEqual(["atlas-04", "nova-02"])
  })
})
