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
  const rendered = render(view())
  return { client, control, intents, refreshView: () => rendered.rerender(view()), ...rendered }
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
    const page = renderNetworkProfilesPage()
    // The profile is already gone from the control plane (deleted in another
    // session) while this catalog still lists it.
    await page.client.dispatch({ type: "deleteNetworkProfile", profileId: "profile-lab-a", confirmed: true })
    const dialog = await openEditDialog(user)

    await user.click(within(dialog).getByRole("button", { name: "Save Profile" }))

    const failure = within(screen.getByRole("dialog")).getByRole("alert")
    expect(failure).toHaveTextContent("Network Profile was not found.")
    expect(pageBanner()).not.toHaveTextContent("was not found")
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
    expect(pageBanner()).toHaveTextContent(/Network Profile saved; no scan was started/i)
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

})

describe("NetworkProfilesPage saved profile catalog", () => {
  it("enables Configure Scan for any selected profile instead of gating on a lifecycle state", async () => {
    const user = userEvent.setup()
    renderNetworkProfilesPage()
    // A saved profile has no lifecycle: it is either saved or deleted. Selecting
    // the second catalog row must be enough to start configuring a scan.
    await user.click(screen.getAllByRole("button", { name: "Edit Profile" })[1])
    await user.keyboard("{Escape}")

    const scanButtons = screen.getAllByRole("button", { name: "Configure Scan" })
    expect(scanButtons.length).toBeGreaterThan(0)
    for (const button of scanButtons) {
      expect(button).toBeEnabled()
    }
  })

  it("deletes a confirmed profile and drops it from the catalog", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage()
    expect(screen.getByText("Lab A staging")).toBeInTheDocument()

    await user.click(screen.getAllByRole("button", { name: "Delete Profile" })[0])
    await user.click(within(document.body).getByRole("button", { name: "Confirm Delete" }))

    expect(page.intents).toEqual([{ type: "deleteNetworkProfile", profileId: "profile-lab-a", confirmed: true }])
    page.refreshView()
    expect(screen.queryByText("Lab A staging")).not.toBeInTheDocument()
  })

  it("surfaces a failed delete in the page banner and keeps the profile listed", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ failIntent: "deleteNetworkProfile", message: "Network Profile was not found." })

    await user.click(screen.getAllByRole("button", { name: "Delete Profile" })[0])
    await user.click(within(document.body).getByRole("button", { name: "Confirm Delete" }))

    expect(pageBanner()).toHaveTextContent("Network Profile was not found.")
    page.refreshView()
    expect(screen.getByText("Lab A staging")).toBeInTheDocument()
  })

  it("renders no lifecycle state column and no row version for a saved profile", () => {
    renderNetworkProfilesPage()

    const catalog = screen.getByRole("table", { name: "Network profile catalog" })
    expect(within(catalog).queryByRole("columnheader", { name: "State" })).not.toBeInTheDocument()
    expect(within(catalog).queryByText(/row v/)).not.toBeInTheDocument()
  })

  it("describes scan history without claiming a scan runs against an active profile", () => {
    renderNetworkProfilesPage()

    expect(screen.queryByText(/active profile/i)).not.toBeInTheDocument()
  })
})
