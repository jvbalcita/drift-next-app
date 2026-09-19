// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent, ControlPlaneSnapshot, DispatchIntent, MutationResult } from "@/lib/domain/control-plane"
import { NetworkProfilesPage } from "./NetworkProfilesPage"

const genericFailureMessage = "The request could not be completed."

const noopDispatch: DispatchIntent = async (intent) => ({ ok: true, kind: intent.type, message: "" })

function renderNetworkProfilesPage(control: { failIntent?: ControlPlaneIntent["type"]; message?: string; view?: string } = {}) {
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
    <NetworkProfilesPage snapshot={client.getSnapshot()} dispatch={dispatch} view={control.view ?? "profiles"} onViewChange={() => undefined} />
  )
  const rendered = render(view())
  return { client, control, intents, refreshView: () => rendered.rerender(view()), ...rendered }
}

/** Selects a catalog profile in the page selector and runs one scan against it. */
async function runScanForProfile(user: ReturnType<typeof userEvent.setup>, profileId: string) {
  await user.click(screen.getByLabelText("Discovery profile"))
  await user.click(await screen.findByRole("option", { name: profileId === "profile-lab-b" ? "Lab B review" : "Lab A staging (default)" }))
  await user.click(screen.getAllByRole("button", { name: "Configure Scan" })[0])
  const dialog = screen.getByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: "Start scan" }))
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

describe("NetworkProfilesPage discovery profile selection", () => {
  it("starts the selector on the default profile rather than the first catalog row", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const seeded = client.getSnapshot()
    const [first, second] = seeded.networkProfiles
    const reordered: ControlPlaneSnapshot = {
      ...seeded,
      networkProfiles: [{ ...first, isDefault: false }, { ...second, isDefault: true }],
    }
    render(<NetworkProfilesPage snapshot={reordered} dispatch={noopDispatch} view="scans" onViewChange={() => undefined} />)

    expect(screen.getByLabelText("Discovery profile")).toHaveTextContent("Lab B review")
    await user.click(screen.getAllByRole("button", { name: "Configure Scan" })[0])
    expect(within(screen.getByRole("dialog")).getByText("Lab B review")).toBeInTheDocument()
  })

  it("scans a non-newest profile the operator selects, without editing it first", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ view: "scans" })

    await user.click(screen.getByLabelText("Discovery profile"))
    await user.click(await screen.findByRole("option", { name: "Lab B review" }))
    await user.click(screen.getAllByRole("button", { name: "Configure Scan" })[0])
    const dialog = screen.getByRole("dialog")

    expect(within(dialog).getByText("Lab B review")).toBeInTheDocument()
    await user.click(within(dialog).getByRole("button", { name: "Start scan" }))
    expect(page.intents).toEqual([{ type: "startScan", profileId: "profile-lab-b" }])
  })

  it("reconciles the selection when the catalog grows without a remount", () => {
    const client = new MockControlPlaneClient()
    const dispatch: DispatchIntent = async (intent) => await client.dispatch(intent)
    const empty: ControlPlaneSnapshot = { ...client.getSnapshot(), networkProfiles: [] }
    const { rerender } = render(<NetworkProfilesPage snapshot={empty} dispatch={dispatch} view="scans" onViewChange={() => undefined} />)
    for (const button of screen.getAllByRole("button", { name: "Configure Scan" })) {
      expect(button).toBeDisabled()
    }

    // A profile now exists while the page stays mounted. The same instance must
    // be able to scan it: the selection is part of the catalog, not of mount.
    rerender(<NetworkProfilesPage snapshot={client.getSnapshot()} dispatch={dispatch} view="scans" onViewChange={() => undefined} />)
    expect(screen.getByLabelText("Discovery profile")).toHaveTextContent("Lab A staging")
    for (const button of screen.getAllByRole("button", { name: "Configure Scan" })) {
      expect(button).toBeEnabled()
    }
  })

  it("falls back to a surviving profile when the selected profile is deleted", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage()

    await user.click(screen.getByLabelText("Discovery profile"))
    await user.click(await screen.findByRole("option", { name: "Lab B review" }))
    await user.click(screen.getAllByRole("button", { name: "Delete Profile" })[1])
    await user.click(within(document.body).getByRole("button", { name: "Confirm Delete" }))
    page.refreshView()

    expect(screen.getByLabelText("Discovery profile")).toHaveTextContent("Lab A staging")
    for (const button of screen.getAllByRole("button", { name: "Configure Scan" })) {
      expect(button).toBeEnabled()
    }
  })
})

describe("NetworkProfilesPage observed scan devices", () => {
  it("renders the devices a completed scan observed with their link state and identity", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ view: "scans" })

    await runScanForProfile(user, "profile-lab-a")
    // A successful mutation re-reads the projection, as the console does.
    page.refreshView()

    const observed = screen.getByRole("table", { name: "Devices observed by this scan" })
    expect(within(observed).getByText("192.0.2.10:5555")).toBeInTheDocument()
    expect(within(observed).getByText("MOCK-DEVICE-101")).toBeInTheDocument()
    expect(within(observed).getByText("Online")).toBeInTheDocument()
    expect(within(observed).getByText("Offline")).toBeInTheDocument()
    expect(within(observed).getByText("Unauthorized")).toBeInTheDocument()
    expect(within(observed).getByText("Atlas 04")).toBeInTheDocument()
    expect(within(observed).getAllByText("New device")).toHaveLength(1)
    expect(screen.getByText(/3 devices observed: 1 online, 1 offline, 1 unauthorized/)).toBeInTheDocument()
    expect(screen.getByText(/not authorized for debugging/i)).toBeInTheDocument()
  })

  it("explains an empty scan result in operator terms", async () => {
    const user = userEvent.setup()
    const page = renderNetworkProfilesPage({ view: "scans" })

    // The mock models a profile whose range answers with nothing, so the
    // operator-facing empty state stays exercised.
    await runScanForProfile(user, "profile-lab-b")
    page.refreshView()

    expect(screen.getByText("No devices observed by this scan")).toBeInTheDocument()
    expect(screen.getByText(/No ADB endpoints responded. Confirm the profile range, Wi-Fi LAN, and client isolation settings./)).toBeInTheDocument()
  })

  it("keeps one scan surface and the note that deleting a profile keeps its runs", () => {
    renderNetworkProfilesPage({ view: "scans" })

    expect(screen.queryByRole("tab", { name: "History" })).not.toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "Discovery Scans" })).toHaveAttribute("data-active")
    expect(screen.getAllByRole("table", { name: "Discovery scan history" })).toHaveLength(1)
    expect(screen.getByText(/Deleting a profile clears the profile reference without removing the run/)).toBeInTheDocument()
  })
})

describe("NetworkProfilesPage registered endpoints", () => {
  it("lists a device that moved once, at the endpoint it answers on now, and does not count the record it left", () => {
    renderNetworkProfilesPage({ view: "endpoints" })

    const board = screen.getByRole("table", { name: "Registered network endpoints" })
    // The mock seeds atlas-04 with two endpoint records: the transport it was
    // observed at first and the one it answers on now. The board reads the
    // device's CURRENT endpoint, so the device is drawn once - and the record it
    // left, which is that device's own history, is not listed here.
    expect(within(board).getAllByText("Atlas 04")).toHaveLength(1)
    expect(within(board).getByText("endpoint-atlas-04-current · MOCK-DEVICE-101")).toBeInTheDocument()
    expect(within(board).queryByText("endpoint-atlas-04-superseded · MOCK-DEVICE-101")).not.toBeInTheDocument()
    // And it is not counted: the board's own total is one current endpoint per
    // device, not one row per endpoint record.
    expect(screen.getByText(/Showing 1–6 of 6 results/)).toBeInTheDocument()
  })

  it("says why an empty endpoint list is empty", () => {
    const client = new MockControlPlaneClient()
    const empty: ControlPlaneSnapshot = { ...client.getSnapshot(), endpoints: [] }
    render(<NetworkProfilesPage snapshot={empty} dispatch={noopDispatch} view="endpoints" onViewChange={() => undefined} />)

    expect(screen.getByText("No Registered Endpoints")).toBeInTheDocument()
    expect(screen.getByText(/Run a scan against a saved profile/i)).toBeInTheDocument()
  })
})
