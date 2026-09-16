// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent, MutationResult } from "@/lib/domain/control-plane"
import { NetworkProfilesPage } from "./NetworkProfilesPage"

const genericFailureMessage = "The request could not be completed."

function renderNetworkProfilesPage(control: { failIntent?: ControlPlaneIntent["type"]; message?: string } = {}) {
  const client = new MockControlPlaneClient()
  const intents: ControlPlaneIntent[] = []
  const dispatch = async (intent: ControlPlaneIntent): Promise<MutationResult> => {
    intents.push(intent)
    if (control.failIntent === intent.type) {
      return { ok: false, kind: intent.type, message: control.message ?? genericFailureMessage }
    }
    return await client.dispatch(intent)
  }
  const view = () => (
    <NetworkProfilesPage snapshot={client.getSnapshot()} dispatch={dispatch} view="profiles" onViewChange={() => undefined} />
  )
  return { client, control, intents, ...render(view()) }
}

function pageBanner() {
  // The modal marks outside content aria-hidden, so query past the accessibility tree for the page banner.
  return screen.getByRole("status", { hidden: true })
}

async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "New Profile" }))
  return screen.getByRole("dialog")
}

async function openEditDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getAllByRole("button", { name: "Edit Profile" })[0])
  return screen.getByRole("dialog")
}

describe("NetworkProfilesPage profile save failures", () => {
  it("keeps the dialog open and renders the failure inside the dialog, not in the page banner", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ failIntent: "createNetworkProfile", message: genericFailureMessage })
    const dialog = await openCreateDialog(user)

    await user.type(within(dialog).getByLabelText("Profile Name"), "Test")
    await user.click(within(dialog).getByRole("button", { name: "Save Profile" }))

    const stillOpen = screen.getByRole("dialog")
    const failure = within(stillOpen).getByRole("alert")
    expect(failure).toHaveTextContent(genericFailureMessage)
    expect(failure).toHaveAttribute("role", "alert")
    expect(within(stillOpen).getByText(/save failed/i)).toBeInTheDocument()
    expect(page.intents).toHaveLength(1)
    expect(pageBanner()).not.toHaveTextContent(genericFailureMessage)
  })

  it("surfaces a control-plane rejection from the real client inside the dialog", async () => {
    const user = userEvent.setup()
    renderNetworkProfilesPage()
    const dialog = await openCreateDialog(user)

    await user.type(within(dialog).getByLabelText("Profile Name"), "Test")
    await user.click(within(dialog).getByLabelText("Use as Default Discovery Policy"))
    await user.click(within(dialog).getByRole("button", { name: "Save Profile" }))

    const failure = within(screen.getByRole("dialog")).getByRole("alert")
    expect(failure).toHaveTextContent("A draft Network Profile cannot be default; activate it first.")
    expect(pageBanner()).not.toHaveTextContent("cannot be default")
  })

  it("closes the dialog and clears the failure notice after a successful save", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ failIntent: "createNetworkProfile", message: genericFailureMessage })
    const dialog = await openCreateDialog(user)

    await user.type(within(dialog).getByLabelText("Profile Name"), "Test")
    await user.click(within(dialog).getByRole("button", { name: "Save Profile" }))
    expect(within(screen.getByRole("dialog")).getByRole("alert")).toBeInTheDocument()

    page.control.failIntent = undefined
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save Profile" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
    expect(pageBanner()).toHaveTextContent(/saved as draft/i)
  })

  it("keeps per-field validation errors reported and distinguishable from a save failure", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage()
    const dialog = await openCreateDialog(user)

    await user.click(within(dialog).getByRole("button", { name: "Save Profile" }))

    const fieldSummary = within(dialog).getByRole("alert")
    expect(fieldSummary).toHaveTextContent("Correct the highlighted fields before saving.")
    expect(within(dialog).getByLabelText("Profile Name")).toHaveAttribute("aria-invalid", "true")
    expect(within(dialog).getByText("Enter a profile name.", { selector: "#profile-name-error" })).toBeInTheDocument()
    expect(within(dialog).queryByText(/save failed/i)).not.toBeInTheDocument()
    expect(page.intents).toHaveLength(0)
  })

  it("still reports failures for non-modal actions in the page-level banner", async () => {
    const user = userEvent.setup()
    renderNetworkProfilesPage({ failIntent: "retireNetworkProfile", message: "Network Profile was not found." })
    const dialog = await openEditDialog(user)

    await user.click(within(dialog).getByRole("button", { name: "Retire" }))

    expect(pageBanner()).toHaveTextContent("Network Profile was not found.")
    expect(within(dialog).queryByText(/save failed/i)).not.toBeInTheDocument()
  })
})
