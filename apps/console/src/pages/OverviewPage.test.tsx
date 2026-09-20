// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneSnapshot, DispatchIntent, EventView } from "@/lib/domain/control-plane"
import { OverviewPage } from "./OverviewPage"

const dispatch: DispatchIntent = async (intent) => ({ ok: true, kind: intent.type, message: "ok" })

function renderOverview(changes: Partial<ControlPlaneSnapshot> = {}) {
  const snapshot = { ...new MockControlPlaneClient().getSnapshot(), ...changes }
  return render(<OverviewPage snapshot={snapshot} dispatch={dispatch} />)
}

function event(id: string, name: string, occurredAt: string, extra: Partial<EventView> = {}): EventView {
  return { id, kind: "audit", name, occurredAt, actor: "operator", resourceType: "device", resourceId: "device-1", correlationId: id, payloadSummary: `${name} detail`, ...extra }
}

describe("OverviewPage", () => {
  it("keeps metrics, activity, and observed readiness visible without device inspection surfaces", () => {
    renderOverview()
    expect(screen.getByLabelText("Fleet metrics")).toBeInTheDocument()
    expect(screen.getByText("Recent activity")).toBeInTheDocument()
    expect(screen.getByText("Runtime readiness")).toBeInTheDocument()
    expect(screen.getByText("Control-plane connection")).toBeInTheDocument()
    expect(screen.getByText("Device adapter")).toBeInTheDocument()
    expect(screen.getByText("Command spool")).toBeInTheDocument()
    expect(screen.getByText("Emergency stop")).toBeInTheDocument()
    expect(screen.queryByText("Device Fleet")).not.toBeInTheDocument()
    expect(screen.queryByText("Selected Device")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /show|hide/i })).not.toBeInTheDocument()
  })

  it("filters routine noise, sorts actionable entries newest-first, and links supported resources", () => {
    renderOverview({ events: [
      event("old", "device.departed", "2026-09-20T01:00:00Z"),
      event("noise", "observation.sampled", "2026-09-21T03:00:00Z"),
      event("new", "scan.failed", "2026-09-21T02:00:00Z", { resourceType: "scan", failureClass: "timeout" }),
    ] })
    const links = screen.getAllByRole("link").filter((link) => ["scan.failed", "device.departed"].includes(link.textContent ?? ""))
    expect(links.map((link) => link.textContent)).toEqual(["scan.failed", "device.departed"])
    expect(links[0]).toHaveAttribute("href", "#network-profiles/scans")
    expect(screen.queryByText("observation.sampled")).not.toBeInTheDocument()
  })

  it("states the activity empty condition and exposes degraded runtime facts", () => {
    const base = new MockControlPlaneClient().getSnapshot()
    renderOverview({
      events: [],
      runtimeConnection: { ...base.runtimeConnection, state: "disconnected", disconnectedReason: "Service unavailable" },
      labAdapter: { ...base.labAdapter, readiness: "blocked", indeterminate: true, failureClass: "permission_denied" },
      spoolHealth: { ...base.spoolHealth, pending: 4, blocked: 2, exhausted: false },
      halt: { ...base.halt, state: "emergency_stop", reason: "Operator stop" },
    })
    expect(screen.getByText("No actionable activity")).toBeInTheDocument()
    expect(screen.getByText("Service unavailable")).toBeInTheDocument()
    expect(screen.getByText("Indeterminate")).toBeInTheDocument()
    expect(screen.getByText("2 blocked")).toBeInTheDocument()
    expect(screen.getByText("Active")).toBeInTheDocument()
  })
})
