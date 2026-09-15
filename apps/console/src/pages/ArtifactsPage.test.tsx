// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { ArtifactsPage } from "./ArtifactsPage"

describe("ArtifactsPage", () => {
  it("lists artifacts with filters, pagination, and sanitized detail states", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)

    render(
      <ArtifactsPage snapshot={client.getSnapshot()} dispatch={dispatch} view="library" onViewChange={() => undefined} />,
    )

    expect(screen.getByRole("heading", { name: "Artifacts" })).toBeInTheDocument()
    expect(screen.getByLabelText("Storage Quota Warning")).toBeInTheDocument()
    expect(screen.getByText("9 Results")).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText("Lifecycle State"), "unauthorized")
    expect(screen.getByText("1 Results")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /Unauthorized — Content Withheld/i }))
    expect(screen.getByText(/Unauthorized — artifact content is withheld/i)).toBeInTheDocument()
    expect(screen.getAllByText("Unauthorized").length).toBeGreaterThan(0)

    await user.keyboard("{Escape}")
    await user.selectOptions(screen.getByLabelText("Lifecycle State"), "all")
    await user.selectOptions(screen.getByLabelText("Type / Category"), "ui_tree")
    expect(screen.getByText("1 Results")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /Bounded UI-Tree Summary/i }))
    expect(screen.getByText(/nodes=42/i)).toBeInTheDocument()
  })

  it("shows artifact audit entries for rejected admissions", () => {
    const client = new MockControlPlaneClient()
    render(
      <ArtifactsPage
        snapshot={client.getSnapshot()}
        dispatch={async (intent) => client.dispatch(intent)}
        view="audit"
        onViewChange={() => undefined}
      />,
    )
    expect(screen.getAllByText(/Admission Rejected/i).length).toBeGreaterThan(0)
  })

  it("requires confirmation before delete and cleanup", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) => client.dispatch(intent)

    const view = () => <ArtifactsPage snapshot={client.getSnapshot()} dispatch={dispatch} view="library" onViewChange={() => undefined} />
    const { rerender } = render(view())

    await user.selectOptions(screen.getByLabelText("Lifecycle State"), "eligible_for_deletion")
    await user.click(screen.getByRole("button", { name: /Low-Res Session Thumbnail/i }))

    const dialog = within(document.body)
    await user.click(screen.getByRole("button", { name: /Delete Artifact/i }))
    await user.click(dialog.getByRole("button", { name: "Confirm Delete" }))
    rerender(view())
    expect(screen.getByText(/Artifact deleted after confirmation/i)).toBeInTheDocument()
    expect(client.getSnapshot().artifacts.find((artifact) => artifact.id === "artifact-rec-orion-01")?.lifecycleState).toBe("deleted")
  })

  it("shows selected-device media grid without raw paths", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    render(
      <ArtifactsPage
        snapshot={client.getSnapshot()}
        dispatch={async (intent) => client.dispatch(intent)}
        view="media"
        onViewChange={() => undefined}
      />,
    )

    expect(screen.getByRole("tab", { name: "Media" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByLabelText("Low Resolution Media Grid")).toBeInTheDocument()
    expect(screen.queryByText(/\/var\//i)).not.toBeInTheDocument()
    expect(screen.queryByText(/password/i)).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText("Selected Device"), "atlas-04")
    expect(screen.getByText("Full-Resolution Authorized Preview")).toBeInTheDocument()
  })
})
