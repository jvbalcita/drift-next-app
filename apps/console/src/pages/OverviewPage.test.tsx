// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent, ControlPlaneSnapshot, MutationResult } from "@/lib/domain/control-plane"
import { OverviewPage } from "./OverviewPage"

function renderOverview(snapshot?: ControlPlaneSnapshot) {
  const client = new MockControlPlaneClient()
  const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => await client.dispatch(intent)
  return render(<OverviewPage snapshot={snapshot ?? client.getSnapshot()} dispatch={dispatch} />)
}

describe("OverviewPage", () => {
  it("derives median latency from observed device projections", () => {
    renderOverview()

    const metric = screen.getByRole("heading", { name: "Median latency" }).closest("article")
    expect(metric).not.toBeNull()
    expect(within(metric as HTMLElement).getByText("51 ms")).toBeInTheDocument()
    expect(within(metric as HTMLElement).getByText("5 measured devices")).toBeInTheDocument()
  })

  it("does not offer a control that has no dispatch path", () => {
    renderOverview()

    expect(screen.queryByRole("button", { name: "Run workflow" })).not.toBeInTheDocument()
  })

  it("states when no device latency has been measured", () => {
    const client = new MockControlPlaneClient()
    const snapshot = { ...client.getSnapshot(), devices: client.getSnapshot().devices.map((device) => ({ ...device, latencyMs: 0 })) }
    renderOverview(snapshot)

    const metric = screen.getByRole("heading", { name: "Median latency" }).closest("article")
    expect(within(metric as HTMLElement).getByText("Not measured")).toBeInTheDocument()
    expect(within(metric as HTMLElement).getByText("No device observations yet")).toBeInTheDocument()
  })
})
