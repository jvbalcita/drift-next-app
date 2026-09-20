// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import App from "./App"
import { liveMirrorCopy } from "@/lib/live-mirror"

// userEvent's default delay waits on a real timer between the events of every interaction. On a
// loaded runner that wait, not the render, is what pushes a heavy console test past its 5s
// timeout: measured 2026-09-18, a walk over ten destinations spent ~9s of an ~11s test waiting
// between events, and the same walk was ~2s without it. These tests still dispatch every event;
// they just do not idle between them. See AGENTS.md, "Testing and verification".
const setupUser = () => userEvent.setup({ delay: null })

describe("Drift command center", () => {
  beforeEach(() => {
    window.location.hash = "#overview/fleet"
  })

  it("renders the fleet overview with permanently visible activity and readiness", () => {
    render(<App />)

    expect(screen.getByRole("heading", { name: /fleet overview/i })).toBeInTheDocument()
    expect(screen.getByText("Recent activity")).toBeInTheDocument()
    expect(screen.getByText("Runtime readiness")).toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: /device fleet/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: /selected device/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /show|hide/i })).not.toBeInTheDocument()
  })

  it("keeps navigation breadcrumbs in the shell header only", () => {
    render(<App />)

    expect(screen.getAllByText("Workspace", { exact: true })).toHaveLength(1)
    expect(screen.getByRole("banner").querySelector('[data-slot="breadcrumb"]')).toBeInTheDocument()
  })

  it("places the only sidebar toggle and breadcrumbs in the window toolbar", () => {
    render(<App />)

    const titlebar = document.querySelector('[data-slot="app-titlebar"]')

    expect(titlebar).toHaveAttribute("data-tauri-drag-region")
    expect(titlebar?.querySelector('[data-slot="sidebar-trigger"]')).toBeInTheDocument()
    expect(titlebar?.querySelector('[data-slot="breadcrumb"]')).toBeInTheDocument()
    expect(document.querySelectorAll('[data-slot="sidebar-trigger"]')).toHaveLength(1)
    expect(screen.getAllByRole("banner")).toHaveLength(1)
  })

  it("opens device details from a registry row without enabling device actions", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Devices" }))
    await user.click(await screen.findByRole("row", { name: /Open details for Nova 02/ }))
    expect(screen.getByText(/Android 13/)).toBeInTheDocument()
    expect(screen.getByText("Reconnecting to agent")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Run workflow/ })).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Close" }))
    await user.type(screen.getByLabelText("Search Device Registry"), "Orion")
    expect(screen.queryByRole("row", { name: /Open details for Atlas 04/ })).not.toBeInTheDocument()
    expect(screen.getByRole("row", { name: /Open details for Orion 01/ })).toBeInTheDocument()
  })

  it("provides an accessible collapsible navigation shell", async () => {
    const user = setupUser()
    render(<App />)

    const sidebar = document.querySelector<HTMLDivElement>(
      '[data-slot="sidebar"][data-state]',
    )
    const trigger = document.querySelector<HTMLButtonElement>(
      '[data-slot="sidebar-trigger"]',
    )

    expect(sidebar).toBeInTheDocument()
    expect(trigger).toBeInTheDocument()
    expect(trigger).toHaveAccessibleName(/toggle sidebar/i)
    expect(sidebar).toHaveAttribute("data-state", "expanded")
    const primaryNavigation = screen.getByRole("navigation", { name: /primary navigation/i })
    expect(primaryNavigation).toBeInTheDocument()
    expect(within(primaryNavigation).getByRole("link", { name: "Overview" })).toBeInTheDocument()

    await user.click(trigger!)

    expect(sidebar).toHaveAttribute("data-state", "collapsed")
    expect(within(primaryNavigation).getByRole("link", { name: "Overview" })).toBeInTheDocument()
  })

  it("does not present stale counts or submenu affordances on page links", () => {
    render(<App />)

    expect(screen.getByRole("link", { name: "Devices" })).toHaveAttribute("href", "#devices/all")
    expect(document.querySelector('[data-sidebar="menu-badge"]')).not.toBeInTheDocument()
    expect(document.querySelector('[data-sidebar="menu-sub"]')).not.toBeInTheDocument()
  })

  it("routes block navigation selections through the app shell", async () => {
    const user = setupUser()
    render(<App />)

    const devicesLink = screen.getByRole("link", { name: "Devices" })
    await user.click(devicesLink)

    expect(devicesLink).toHaveAttribute("aria-current", "page")
    expect(screen.getByText("Devices", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("keeps device tabs and pagination synchronized with the hash route", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Devices" }))
    expect(window.location.hash).toBe("#devices/all")
    // The destination's module loads on demand and the shell shows its loading state until it
    // resolves, so await the tab instead of querying it synchronously.
    await user.click(await screen.findByRole("tab", { name: "Online" }))
    expect(window.location.hash).toBe("#devices/online")
    expect(screen.getByText("Online", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "All" }))
    expect(screen.getByText("Showing 1–6 of 6 results")).toBeInTheDocument()
    expect(screen.getByLabelText("Rows per page")).toHaveTextContent("10")
  })

  it("selects non-device workspace views from a deep link", async () => {
    window.location.hash = "#groups/membership"
    const { unmount } = render(<App />)

    // Groups is a single tabless surface now: a deep link to a retired sibling
    // view resolves to it and there is no tab control left to select.
    expect(await screen.findByRole("heading", { name: "Groups and Membership" })).toBeInTheDocument()
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument()
    expect(screen.getAllByText("Groups", { selector: '[data-slot="breadcrumb-page"]' }).length).toBeGreaterThan(0)
    unmount()

    window.location.hash = "#network-profiles/scans"
    render(<App />)
    expect(await screen.findByRole("tab", { name: "Discovery Scans" })).toHaveAttribute("data-active")
    expect(screen.getByText("Discovery Scans", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("selects the Settings history view from its hash route", async () => {
    window.location.hash = "#settings/history"
    render(<App />)

    expect(await screen.findByRole("tab", { name: "History" })).toHaveAttribute("data-active")
    expect(screen.getByText("History", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("lands on the first Network Profiles view when its removed History view is deep-linked", async () => {
    window.location.hash = "#network-profiles/history"
    render(<App />)

    // The removed view falls back instead of rendering an empty surface.
    expect(await screen.findByRole("tab", { name: "Profiles" })).toHaveAttribute("data-active")
    expect(screen.getByText("Profiles", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("restores every routed sibling workspace view from its hash", async () => {
    const routes = [
      ["#accounts/run-history", "Run History"],
      ["#network-profiles/endpoints", "Registered Endpoints"],
      ["#workflows/skills", "Skills"],
      ["#agents/capabilities", "Capabilities"],
      ["#runs/failed", "Failed / Indeterminate"],
      ["#artifacts/storage", "Storage"],
      ["#policies/decisions", "Decision Log"],
      ["#settings/automation-agent", "Automation Agent"],
    ] as const

    for (const [hash, tabName] of routes) {
      window.location.hash = hash
      const { unmount } = render(<App />)
      expect(await screen.findByRole("tab", { name: tabName })).toHaveAttribute("data-active")
      unmount()
    }
  })

  it("keeps invalid setting JSON inline and focuses its error summary", async () => {
    const user = setupUser()
    window.location.hash = "#settings/workspace"
    render(<App />)

    await user.click(await screen.findByRole("button", { name: "Edit" }))
    const value = screen.getByLabelText("Value JSON")
    await user.clear(value)
    await user.type(value, "not json")
    await user.click(screen.getByRole("button", { name: "Save Setting" }))

    expect(screen.getByRole("alert")).toHaveTextContent("Enter valid JSON")
    await waitFor(() => expect(screen.getByRole("alert")).toHaveFocus())
    expect(value).toHaveAttribute("aria-invalid", "true")
  })

  it("keeps invalid network profile fields inline and focuses their summary", async () => {
    const user = setupUser()
    window.location.hash = "#network-profiles/profiles"
    render(<App />)

    await user.click(await screen.findByRole("button", { name: "New Profile" }))
    await user.click(screen.getByRole("button", { name: "Save Profile" }))

    const alert = screen.getByRole("alert")
    expect(alert).toHaveTextContent("Correct the highlighted fields")
    await waitFor(() => expect(alert).toHaveFocus())
    expect(screen.getByLabelText("Profile Name")).toHaveAttribute("aria-invalid", "true")
  })

  it("exposes Control as a compact-frame mock-only destination", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    await user.click(await screen.findByRole("button", { name: /Open workspace settings/i }))

    expect(screen.getByText("Control / Device Workspace")).toBeInTheDocument()
    expect(screen.getByText("Control Center", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
    expect(screen.getByRole("region", { name: /Device Adapter Status/i })).toBeInTheDocument()
    expect(screen.getByRole("slider", { name: /Floating frame size/i })).toHaveAttribute("min", "480")
    expect(screen.getByRole("slider", { name: /Small screen/i })).toHaveAttribute("max", "840")
    expect(screen.getByRole("slider", { name: /Floating frame size/i })).toHaveAttribute("step", "40")
    expect(screen.getByRole("slider", { name: /Small screen/i })).toHaveAttribute("step", "24")
    expect(screen.queryByRole("dialog", { name: /workspace settings/i })).not.toBeInTheDocument()
    expect(document.querySelector('[data-slot="scroll-area"]')).toBeInTheDocument()
    await user.keyboard("{Escape}")
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    expect(screen.getByLabelText(/Atlas 04 floating phone frame/i)).toHaveStyle({ width: "270px", height: "480px" })
    expect(screen.getByRole("button", { name: /Atlas 07/i })).toHaveStyle({ width: "108px", height: "192px" })
  })

  it("opens a source and selects followers by clicking compact frames, with no preview control to press", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: /Atlas 07/i }))

    // Selecting a frame IS what makes it a follower: the panel carries no
    // preview control at all now, and the selection told the plane itself.
    expect(screen.queryByRole("button", { name: /Start preview/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/Preview mirrors the selected followers/i)).not.toBeInTheDocument()
    expect(screen.getByText(/1 follower selected/i)).toBeInTheDocument()
  })

  it("opens the workspace sheet and exposes OTG octet inputs", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    await user.click(await screen.findByRole("button", { name: /Open workspace settings/i }))
    expect(screen.getByRole("slider", { name: /Floating frame size/i })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: /OTG setup/i }))

    expect(screen.getByRole("textbox", { name: /IP range start octet 1/i })).toBeInTheDocument()
    expect(screen.getByRole("textbox", { name: /IP range end octet 4/i })).toBeInTheDocument()
    expect(screen.getByRole("textbox", { name: /Set Port/i })).toHaveValue("5555")
    expect(screen.getByRole("button", { name: /^Activate$/i })).toBeEnabled()
    // The per-device target picker and the second transport-mode control are
    // gone: Activate is the fleet operation, and it needs no target. Connect is
    // gone with them — the scan after Activate is what sees a moved device on
    // the port, so the panel opens no transport by hand.
    expect(screen.queryByRole("button", { name: /Target Device/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /^Change$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /Unpin workspace settings/i }))
    expect(screen.getByRole("button", { name: /Open workspace settings/i })).toBeInTheDocument()
    expect(screen.queryByRole("slider", { name: /Floating frame size/i })).not.toBeInTheDocument()
  })

  it("keeps console preferences separate from the workspace and supports pinning the open device", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    const consoleSettings = screen.getAllByRole("button", { name: /^Settings$/ }).find(
      (button) => button.getAttribute("aria-haspopup") === "dialog",
    )
    expect(consoleSettings).toBeDefined()
    await user.click(consoleSettings!)

    expect(screen.getByRole("dialog")).toHaveAccessibleName(/console settings/i)
    expect(screen.getByRole("slider", { name: /Devices gap/i })).toBeInTheDocument()
    expect(screen.getByRole("switch", { name: /Control small screen/i })).not.toBeChecked()
    // Both transports work, so the transport is CHOSEN here rather than stated:
    // the setting an operator reads is the one this console opens its streams
    // over, and the notice says what each one is. The default is the measured
    // one, so the control reads the transport this fleet was faster over.
    expect(screen.queryByRole("button", { name: /^Connection$/i })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Live Mirror Transport/i })).toHaveTextContent(liveMirrorCopy.settings.choice.tcp)
    expect(screen.getByText(liveMirrorCopy.settings.notice)).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "Presentation" }))
    expect(screen.getByRole("group", { name: /Workspace Position/i })).toBeInTheDocument()
    expect(screen.queryByText("Connected mock devices available in this browser workspace.")).not.toBeInTheDocument()

    await user.keyboard("{Escape}")
    const deviceListTrigger = screen.getAllByRole("button", { name: /^Devices$/ }).find(
      (button) => button.getAttribute("aria-haspopup") === "dialog",
    )
    expect(deviceListTrigger).toBeDefined()
    await user.click(deviceListTrigger!)
    expect(screen.getByRole("dialog")).toHaveAccessibleName(/device list/i)
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: /Pin floating device beside frames/i }))

    expect(screen.getByRole("button", { name: /Unpin floating device/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Reset position/i })).toBeDisabled()
  })

  it("surfaces lab adapter status beside the mock workspace without replacing it", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    const strip = screen.getByRole("region", { name: /Device Adapter Status/i })

    expect(within(strip).getAllByText("Unavailable").length).toBeGreaterThan(0)
    expect(within(strip).getAllByText("Connected").length).toBeGreaterThan(0)
    expect(within(strip).getByText("Spool Clear")).toBeInTheDocument()
    expect(within(strip).getByText("Nothing observed yet")).toBeInTheDocument()
    // The duplicate discovery and confirmation lifecycle is gone: a capture names
    // its own device, so there is nothing to discover or confirm first.
    expect(within(strip).queryByRole("button", { name: /Discover Devices/i })).not.toBeInTheDocument()
    expect(within(strip).queryByRole("button", { name: /Confirm Target/i })).not.toBeInTheDocument()
    expect(within(strip).queryByRole("button", { name: /Clear Target/i })).not.toBeInTheDocument()
    expect(within(strip).getByRole("button", { name: /Capture Observation/i })).toBeEnabled()
    expect(within(strip).getByRole("button", { name: /Runtime And Spool/i })).toBeEnabled()
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()
  })

  it("requires confirmation before spool replay after a mock disconnect", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    await user.click(screen.getByRole("button", { name: /Runtime And Spool/i }))
    const sheet = screen.getByRole("dialog")
    expect(within(sheet).getByText("No Indeterminate Actions")).toBeInTheDocument()
    expect(within(sheet).getByRole("button", { name: /Confirm Spool Replay/i })).toBeDisabled()

    await user.click(within(sheet).getByRole("button", { name: /Enqueue Spool Item/i }))
    await user.click(within(sheet).getByRole("button", { name: /Disconnect Runtime/i }))
    expect(within(sheet).getAllByText(/Disconnected/i).length).toBeGreaterThan(0)
    expect(within(sheet).getByText(/Confirmation Required/i)).toBeInTheDocument()
    expect(within(sheet).getByRole("button", { name: /Confirm Spool Replay/i })).toBeDisabled()

    await user.click(within(sheet).getByRole("button", { name: /Begin Reconnect/i }))
    await user.click(within(sheet).getByRole("button", { name: /Complete Reconnect/i }))
    expect(within(sheet).getByRole("button", { name: /Confirm Spool Replay/i })).toBeEnabled()

    await user.click(within(sheet).getByRole("button", { name: /Confirm Spool Replay/i }))
    await user.click(screen.getByRole("button", { name: /Confirm Replay/i }))
    expect(screen.getByText(/not automatic replay/i)).toBeInTheDocument()
  })

  it("refuses a capture that names no device and reports the error", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    const strip = screen.getByRole("region", { name: /Device Adapter Status/i })
    await user.click(within(strip).getByRole("button", { name: /^Capture Observation$/i }))

    const dialog = screen.getByRole("dialog")
    await user.click(within(dialog).getByRole("button", { name: /^Capture Observation$/i }))

    expect(within(dialog).getByRole("alert")).toHaveTextContent(/The observation was not captured/i)
    expect(within(dialog).getByLabelText("Target Serial")).toHaveAttribute("aria-invalid", "true")

    // A serial that is not attached is refused rather than inferred.
    await user.type(within(dialog).getByLabelText("Target Serial"), "MOCKSERIAL9999")
    await user.click(within(dialog).getByRole("button", { name: /^Capture Observation$/i }))

    expect(within(dialog).getByRole("alert")).toHaveTextContent(/not among the attached/i)
    expect(screen.queryByLabelText(/observation frame/i)).not.toBeInTheDocument()
  })

  it("shows the sanitized lab preview only for the serial that was captured", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Control" }))
    const strip = screen.getByRole("region", { name: /Device Adapter Status/i })
    await user.click(within(strip).getByRole("button", { name: /^Capture Observation$/i }))

    const dialog = screen.getByRole("dialog")
    await user.type(within(dialog).getByLabelText("Target Serial"), "MOCKSERIAL0001")
    await user.click(within(dialog).getByRole("button", { name: /^Capture Observation$/i }))

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(within(strip).getByText("Ready")).toBeInTheDocument()

    const frame = screen.getByLabelText(/MOCKSERIAL0001 observation frame/i)
    expect(within(frame).getByAltText(/Sanitized screenshot preview for MOCKSERIAL0001/i)).toBeInTheDocument()
    expect(within(frame).getByText("Read Only")).toBeInTheDocument()
    expect(within(frame).getByText(/sha256:mock-/)).toBeInTheDocument()
  })

  it("keeps the lab adapter boundary separate from registered devices", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Devices" }))
    expect(await screen.findByRole("heading", { name: /Device Registry/i })).toBeInTheDocument()
    // the adapter is a compact on-demand signal, not a permanent header panel
    expect(screen.queryByRole("heading", { name: "Device Adapter Status" })).not.toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Device Adapter" })).getByText(/Adapter (ready|unavailable)/i)).toBeInTheDocument()
    expect(screen.queryByText(/observed · 0 registered/i)).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /Adapter details/ }))
    const sheet = await screen.findByRole("dialog")
    expect(within(sheet).getByRole("heading", { name: "Device Adapter Diagnostics" })).toBeInTheDocument()
    expect(within(sheet).getByText(/Observation is not registration/i)).toBeInTheDocument()
    expect(within(sheet).getByText("Registry Changes")).toBeInTheDocument()
    expect(within(sheet).getByText("None — diagnostics do not register devices")).toBeInTheDocument()
    expect(screen.queryByText(/MOCKSERIAL/)).not.toBeInTheDocument()
  })

  it("lists lab adapter events through the existing event filters", async () => {
    const user = setupUser()
    render(<App />)

    await user.click(screen.getByRole("link", { name: "Events" }))
    expect(await screen.findByText("Adapter Readiness")).toBeInTheDocument()
    expect(screen.getByText("Read-Only Reattach")).toBeInTheDocument()

    await user.type(screen.getByLabelText(/Device or Resource/i), "lab_adapter")

    expect(screen.getByText("Adapter Readiness")).toBeInTheDocument()
    expect(screen.queryByText("lease.renewed")).not.toBeInTheDocument()
    expect(screen.getByText("2 Results")).toBeInTheDocument()
  })

  // The ten destinations are ten independent routing assertions. Asserting all ten in one
  // test made that test's cost the sum of ten load-sensitive navigations (ten page mounts and
  // ten accessible-name tree walks) under a single 5s budget, which is what timed out on a
  // loaded runner. One destination per test keeps each assertion at one navigation's cost and
  // asserts exactly what the walk did: clicking the destination's entry in the shell shows it.
  const destinationRoutes = [
    ["Devices", /Device Registry/],
    ["Accounts", /^Accounts$/],
    ["Network Profiles", /Network Profiles/],
    ["Groups", /Groups and Membership/],
    ["Agents", /Agent Profiles/],
    ["Runs", /Runs and Targets/],
    ["Artifacts", /^Artifacts$/],
    ["Events", /Events and Audit/],
    ["Policies", /^Policies$/],
    ["Settings", /^Settings$/],
  ] as const

  for (const [label, heading] of destinationRoutes) {
    it(`loads the typed browser-only destination ${label} through the shell`, async () => {
      const user = setupUser()
      render(<App />)

      await user.click(screen.getByRole("link", { name: label }))
      expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument()
    })
  }

  it("uses one compact application shell for window and page controls", () => {
    render(<App />)

    const header = screen.getByRole("banner")

    expect(header).toHaveClass("fixed", "h-10", "bg-background")
    expect(header).not.toHaveClass("bg-[#1f1f1f]")
    expect(header.querySelector('[data-slot="sidebar-trigger"]')).toBeInTheDocument()
    expect(header.querySelector('[data-slot="breadcrumb"]')).toBeInTheDocument()
    expect(header.querySelector('[data-slot="titlebar-sidebar-surface"]')).toHaveClass("bg-sidebar")
    expect(screen.getByRole("button", { name: /Notifications/ })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Settings/ })).toBeInTheDocument()
    expect(header).not.toHaveTextContent("Local session")
  })

  it("uses a grouped page-level sidebar without false affordances", () => {
    render(<App />)

    expect(screen.getByText("DRIFT")).toBeInTheDocument()
    expect(screen.getByText("Local Control Plane")).toBeInTheDocument()
    for (const label of ["Fleet", "Automation", "Records", "System"]) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    expect(screen.queryByText("Racks")).not.toBeInTheDocument()
    expect(screen.queryByText("Upgrade to Pro")).not.toBeInTheDocument()
    expect(screen.getByText("Drift Operator")).toBeInTheDocument()
    expect(screen.queryByText("Transport health")).not.toBeInTheDocument()
  })

  it("applies the Swiss editorial visual system instead of the old glow treatment", () => {
    render(<App />)

    const shell = document.querySelector('[data-visual-style="swiss-editorial"]')
    expect(shell).toBeInTheDocument()
    expect(document.querySelector(".drift-editorial-grid")).toBeInTheDocument()
    expect(screen.getByText("OPERATIONS / FLEET CONTROL")).toBeInTheDocument()
    expect(screen.getByText("connected")).toHaveClass("font-mono")
    expect(document.querySelector('[class*="shadow-[0_0_"]')).not.toBeInTheDocument()
    expect(document.querySelector('[class*="shadow-"]')).not.toBeInTheDocument()
    expect(document.querySelector('[class*="backdrop-blur"]')).not.toBeInTheDocument()
  })

  it("shows an operator menu trigger without false account affordances", () => {
    render(<App />)

    const footer = document.querySelector<HTMLElement>('[data-slot="sidebar-footer"]')
    expect(footer).toBeInTheDocument()
    expect(within(footer!).getByText("Drift Operator")).toBeInTheDocument()
    expect(within(footer!).getByText("Operator Session")).toBeInTheDocument()
    const trigger = within(footer!).getByRole("button", { name: /Drift Operator/ })
    expect(trigger).toHaveAttribute("aria-haspopup", "menu")

    expect(screen.queryByText("Upgrade to Pro")).not.toBeInTheDocument()
  })

  it("uses an off-canvas navigation sheet on narrow viewports", async () => {
    const user = setupUser()
    const previousWidth = window.innerWidth
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 390,
    })

    try {
      render(<App />)

      const trigger = document.querySelector<HTMLButtonElement>(
        '[data-slot="sidebar-trigger"]',
      )
      expect(trigger).toBeInTheDocument()
      expect(
        document.querySelector('[data-slot="sidebar"][data-mobile="true"]'),
      ).not.toBeInTheDocument()

      await user.click(trigger!)

      const dialog = screen.getByRole("dialog")
      expect(dialog).toBeInTheDocument()
      expect(
        document.querySelector('[data-slot="sidebar"][data-mobile="true"]'),
      ).toBeInTheDocument()
      expect(within(dialog).getByText("Fleet")).toBeInTheDocument()
      expect(
        document.querySelector('[class*="backdrop-blur"]'),
      ).not.toBeInTheDocument()

      await user.click(within(dialog).getByRole("link", { name: "Devices" }))
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        value: previousWidth,
      })
    }
  })
})
