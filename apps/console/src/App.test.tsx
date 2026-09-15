// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import App from "./App"

describe("Drift command center", () => {
  beforeEach(() => {
    window.location.hash = "#overview/fleet"
  })

  it("renders the fleet overview and selected device surfaces", () => {
    render(<App />)

    expect(screen.getByRole("heading", { name: /fleet overview/i })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: /device fleet/i })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: /selected device/i })).toBeInTheDocument()
    expect(screen.getByText("Connected")).toBeInTheDocument()
    expect(screen.getByLabelText("Rows per page")).toBeInTheDocument()
    expect(screen.getByText(/Showing 1–6 of 6 results/i)).toBeInTheDocument()
  })

  it("keeps navigation breadcrumbs in the shell header only", () => {
    render(<App />)

    expect(screen.getAllByText("Workspace", { exact: true })).toHaveLength(2)
    expect(screen.getByRole("banner").querySelector('[data-slot="breadcrumb"]')).toBeInTheDocument()
  })

  it("updates the inspector and fleet filter without enabling device actions", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /Nova 02/ }))
    expect(screen.getByText(/Android 13/)).toBeInTheDocument()
    expect(screen.getAllByText("Reconnecting to agent")).toHaveLength(2)
    expect(screen.getByRole("button", { name: /Run workflow/ })).toBeDisabled()

    await user.type(screen.getByLabelText(/search devices/i), "Orion")
    expect(screen.queryByRole("button", { name: /Atlas 04/ })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Orion 01/ })).toBeInTheDocument()
  })

  it("provides an accessible collapsible navigation shell", async () => {
    const user = userEvent.setup()
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
    expect(screen.getByRole("navigation", { name: /primary navigation/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Overview/ })).toBeInTheDocument()

    await user.click(trigger!)

    expect(sidebar).toHaveAttribute("data-state", "collapsed")
    expect(screen.getByRole("button", { name: /Overview/ })).toBeInTheDocument()
  })

  it("uses the Sidebar menu badge structure for counted navigation", () => {
    render(<App />)

    const devicesButton = screen.getByRole("button", { name: /Devices/ })
    const devicesBadge = document.querySelector<HTMLElement>(
      '[data-sidebar="menu-badge"]',
    )

    expect(devicesButton).not.toContainElement(devicesBadge)
    expect(devicesButton.parentElement).toContainElement(devicesBadge)
    expect(devicesBadge).toHaveTextContent("6")
    expect(devicesBadge).toHaveClass("right-7")
  })

  it("routes block navigation selections through the app shell", async () => {
    const user = userEvent.setup()
    render(<App />)

    const devicesButton = screen.getByRole("button", { name: /^Devices/ })
    await user.click(devicesButton)

    expect(devicesButton).toHaveAttribute("aria-current", "page")
    expect(screen.getByText("Devices", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("keeps device tabs and pagination synchronized with the hash route", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Devices/ }))
    expect(window.location.hash).toBe("#devices/all")
    await user.click(screen.getByRole("tab", { name: "Online" }))
    expect(window.location.hash).toBe("#devices/online")
    expect(screen.getByText("Online", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "All" }))
    expect(screen.getByText("Showing 1–5 of 6 results")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Next page" }))
    expect(screen.getByText("Showing 6–6 of 6 results")).toBeInTheDocument()
  })

  it("selects non-device workspace views from a deep link", async () => {
    window.location.hash = "#groups/ordering"
    const { unmount } = render(<App />)

    expect(await screen.findByRole("tab", { name: "Ordering" })).toHaveAttribute("data-active")
    expect(screen.getByText("Ordering", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
    unmount()

    window.location.hash = "#network-profiles/candidates"
    render(<App />)
    expect(await screen.findByRole("tab", { name: "Pending Candidates" })).toHaveAttribute("data-active")
    expect(screen.getByText("Pending Candidates", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("selects the Settings history view from its hash route", async () => {
    window.location.hash = "#settings/history"
    render(<App />)

    expect(await screen.findByRole("tab", { name: "History" })).toHaveAttribute("data-active")
    expect(screen.getByText("History", { selector: '[data-slot="breadcrumb-page"]' })).toBeInTheDocument()
  })

  it("restores every routed sibling workspace view from its hash", async () => {
    const routes = [
      ["#accounts/run-history", "Run History"],
      ["#network-profiles/endpoints", "Registered Endpoints"],
      ["#network-profiles/provisioning", "Device Provisioning"],
      ["#groups/membership", "Membership"],
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
    const user = userEvent.setup()
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
    const user = userEvent.setup()
    window.location.hash = "#network-profiles/profiles"
    render(<App />)

    await user.click(await screen.findByRole("button", { name: "New Profile" }))
    await user.click(screen.getByRole("button", { name: "Save profile" }))

    const alert = screen.getByRole("alert")
    expect(alert).toHaveTextContent("Correct the highlighted fields")
    await waitFor(() => expect(alert).toHaveFocus())
    expect(screen.getByLabelText("Profile name")).toHaveAttribute("aria-invalid", "true")
  })

  it("exposes Control as a compact-frame mock-only destination", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
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

  it("opens a source and selects followers by clicking compact frames", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: /Atlas 07/i }))

    const startButton = screen.getByRole("button", { name: /Start preview/i })
    expect(startButton).toBeEnabled()

    await user.click(startButton)

    expect(screen.getByText(/no device command was sent/i)).toBeInTheDocument()
  })

  it("opens the workspace sheet and exposes OTG octet inputs", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(await screen.findByRole("button", { name: /Open workspace settings/i }))
    expect(screen.getByRole("slider", { name: /Floating frame size/i })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: /OTG setup/i }))

    expect(screen.getByRole("textbox", { name: /IP range start octet 1/i })).toBeInTheDocument()
    expect(screen.getByRole("textbox", { name: /IP range end octet 4/i })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /^Activate$/i })).toBeEnabled()
    await user.click(screen.getByRole("button", { name: /Unpin workspace settings/i }))
    expect(screen.getByRole("button", { name: /Open workspace settings/i })).toBeInTheDocument()
    expect(screen.queryByRole("slider", { name: /Floating frame size/i })).not.toBeInTheDocument()
  })

  it("keeps console preferences separate from the workspace and supports pinning the open device", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    const consoleSettings = screen.getAllByRole("button", { name: /^Settings$/ }).find(
      (button) => button.getAttribute("aria-haspopup") === "dialog",
    )
    expect(consoleSettings).toBeDefined()
    await user.click(consoleSettings!)

    expect(screen.getByRole("dialog")).toHaveAccessibleName(/console settings/i)
    expect(screen.getByRole("slider", { name: /Devices gap/i })).toBeInTheDocument()
    expect(screen.getByRole("switch", { name: /Control small screen/i })).not.toBeChecked()
    expect(screen.getByRole("button", { name: /^Connection$/i })).toHaveTextContent("WebRTC")

    await user.click(screen.getByRole("tab", { name: /Device presentation/i }))
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
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    const strip = screen.getByRole("region", { name: /Device Adapter Status/i })

    expect(within(strip).getAllByText("Unavailable").length).toBeGreaterThan(0)
    expect(within(strip).getAllByText("Connected").length).toBeGreaterThan(0)
    expect(within(strip).getByText("Spool Clear")).toBeInTheDocument()
    expect(within(strip).getByText("No target confirmed")).toBeInTheDocument()
    expect(within(strip).getByRole("button", { name: /Confirm Target/i })).toBeDisabled()
    expect(within(strip).getByRole("button", { name: /Device Provisioning/i })).toBeEnabled()
    expect(within(strip).getByRole("button", { name: /Runtime And Spool/i })).toBeEnabled()
    expect(within(strip).queryByRole("button", { name: /Capture Observation/i })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Atlas 04/i })).toBeInTheDocument()

    await user.click(within(strip).getByRole("button", { name: /Discover Devices/i }))

    expect(within(strip).getByText("Blocked")).toBeInTheDocument()
    expect(within(strip).getByRole("button", { name: /Confirm Target/i })).toBeEnabled()
    expect(screen.getByText(/2 serials listed/i)).toBeInTheDocument()
  })

  it("keeps Lab Provisioning empty until evidence exists and disables Registration without Approval", async () => {
    const user = userEvent.setup()
    window.location.hash = "#network-profiles/provisioning"
    render(<App />)

    expect(await screen.findByRole("tab", { name: "Device Provisioning" })).toHaveAttribute("data-active")
    expect(screen.getByText("No Device Provisioning Evidence")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Verify Provisioning/i })).toBeDisabled()
    expect(screen.getByRole("button", { name: /Approve Provisioning/i })).toBeDisabled()
    expect(screen.getByRole("button", { name: /Register Device/i })).toBeDisabled()
    expect(screen.getByLabelText(/Device provisioning stages/i)).toHaveTextContent("Discovery")
    expect(screen.getByLabelText(/Device provisioning stages/i)).toHaveTextContent("Approval")
    expect(screen.getByLabelText(/Device provisioning stages/i)).toHaveTextContent("Provisioning")
    expect(screen.getByLabelText(/Device provisioning stages/i)).toHaveTextContent("Registration")
    expect(screen.getByLabelText(/Device provisioning stages/i)).toHaveTextContent("Awaiting Approval")

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(screen.getByRole("button", { name: /Discover Devices/i }))
    await user.click(screen.getByRole("button", { name: /Confirm Target/i }))
    const dialog = screen.getByRole("dialog")
    await user.selectOptions(within(dialog).getByLabelText("Serial"), "MOCKSERIAL0001")
    await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")
    await user.type(within(dialog).getByLabelText("Confirmation Text"), "MOCKSERIAL0001")
    await user.type(within(dialog).getByLabelText("Reason"), "Vertical slice bring-up")
    await user.click(within(dialog).getByRole("button", { name: /^Confirm Target$/i }))

    await user.click(screen.getByRole("button", { name: /Device Provisioning/i }))
    const sheet = screen.getByRole("dialog")
    await user.click(within(sheet).getByRole("button", { name: /Verify Provisioning/i }))
    expect(screen.getByText(/Provisioning verified/i)).toBeInTheDocument()

    await user.click(within(sheet).getByRole("button", { name: /Approve Provisioning/i }))
    await user.click(screen.getByRole("button", { name: /Grant Approval/i }))
    expect(screen.getByText(/Approval recorded/i)).toBeInTheDocument()

    await user.click(within(sheet).getByRole("button", { name: /Register Device/i }))
    await user.click(screen.getByRole("button", { name: /Confirm Registration/i }))
    expect(screen.getAllByText(/not a real device registration/i).length).toBeGreaterThan(0)
  })

  it("requires confirmation before spool replay after a mock disconnect", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
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

  it("rejects an incomplete lab target confirmation and reports the errors", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(screen.getByRole("button", { name: /Discover Devices/i }))
    await user.click(screen.getByRole("button", { name: /Confirm Target/i }))

    const dialog = screen.getByRole("dialog")
    await user.click(within(dialog).getByRole("button", { name: /^Confirm Target$/i }))

    expect(within(dialog).getByRole("alert")).toHaveTextContent(/Confirmation was not recorded/i)
    expect(within(dialog).getByLabelText("Serial")).toHaveAttribute("aria-invalid", "true")
    expect(within(dialog).getByLabelText("Reason")).toHaveAttribute("aria-invalid", "true")

    await user.selectOptions(within(dialog).getByLabelText("Serial"), "MOCKSERIAL0001")
    await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")
    await user.type(within(dialog).getByLabelText("Confirmation Text"), "MOCKSERIAL0002")
    await user.type(within(dialog).getByLabelText("Reason"), "Vertical slice bring-up")
    await user.click(within(dialog).getByRole("button", { name: /^Confirm Target$/i }))

    expect(within(dialog).getByRole("alert")).toHaveTextContent(/must match the selected serial exactly/i)
    expect(screen.queryByLabelText(/observation frame/i)).not.toBeInTheDocument()
  })

  it("shows the sanitized lab preview only after a confirmed target is observed", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(screen.getByRole("button", { name: /Discover Devices/i }))
    await user.click(screen.getByRole("button", { name: /Confirm Target/i }))

    const dialog = screen.getByRole("dialog")
    await user.selectOptions(within(dialog).getByLabelText("Serial"), "MOCKSERIAL0001")
    await user.type(within(dialog).getByLabelText("Display Name"), "Lab bench")
    await user.type(within(dialog).getByLabelText("Confirmation Text"), "MOCKSERIAL0001")
    await user.type(within(dialog).getByLabelText("Reason"), "Vertical slice bring-up")
    await user.click(within(dialog).getByRole("button", { name: /^Confirm Target$/i }))

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    const strip = screen.getByRole("region", { name: /Device Adapter Status/i })
    expect(within(strip).getByText("Ready")).toBeInTheDocument()
    expect(screen.queryByLabelText(/observation frame/i)).not.toBeInTheDocument()

    await user.click(within(strip).getByRole("button", { name: /Capture Observation/i }))

    const frame = screen.getByLabelText(/Lab bench observation frame/i)
    expect(within(frame).getByAltText(/Sanitized screenshot preview for Lab bench/i)).toBeInTheDocument()
    expect(within(frame).getByText("Read Only")).toBeInTheDocument()
    expect(within(frame).getByText(/sha256:mock-/)).toBeInTheDocument()

    await user.click(within(strip).getByRole("button", { name: /Clear Target/i }))
    expect(screen.queryByLabelText(/observation frame/i)).not.toBeInTheDocument()
  })

  it("keeps the lab adapter section separate from registered devices", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Devices/ }))
    expect(await screen.findByText("Device Adapter Status")).toBeInTheDocument()
    expect(screen.getByText(/Discovered serials are never registered as devices/i)).toBeInTheDocument()
    expect(screen.getByText("0 listed, 0 registered")).toBeInTheDocument()
    expect(screen.getByText("No target confirmed")).toBeInTheDocument()
    expect(screen.queryByText(/MOCKSERIAL/)).not.toBeInTheDocument()
    expect(screen.getByRole("heading", { name: /Device Registry/i })).toBeInTheDocument()
  })

  it("lists lab adapter events through the existing event filters", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Events/ }))
    expect(await screen.findByText("Adapter Readiness")).toBeInTheDocument()
    expect(screen.getByText("Read-Only Reattach")).toBeInTheDocument()

    await user.type(screen.getByLabelText(/Device or Resource/i), "lab_adapter")

    expect(screen.getByText("Adapter Readiness")).toBeInTheDocument()
    expect(screen.queryByText("lease.renewed")).not.toBeInTheDocument()
    expect(screen.getByText("2 Results")).toBeInTheDocument()
  })

  it("loads the typed browser-only destinations through the shell", async () => {
    const user = userEvent.setup()
    render(<App />)

    const destinations = [
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

    for (const [label, heading] of destinations) {
      await user.click(screen.getByRole("button", { name: new RegExp(`^${label}`) }))
      expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument()
    }
  })

  it("uses the sidebar-07 inset header composition", () => {
    render(<App />)

    const header = screen.getByRole("banner")

    expect(header).toHaveClass("shrink-0", "transition-[width,height]")
    expect(header).toHaveClass("flex", "h-16", "shrink-0")
    expect(header).not.toHaveClass("sticky")
    expect(header.querySelector('[data-slot="sidebar-trigger"]')?.parentElement).toHaveClass("px-4")
    expect(header).toHaveClass("group-has-data-[collapsible=icon]/sidebar-wrapper:h-12")
    expect(header.querySelector('[data-slot="breadcrumb"]')).toBeInTheDocument()
    const separator = header.querySelector('[data-orientation="vertical"]')
    expect(separator).toBeInTheDocument()
    expect(separator).toHaveClass("h-4", "shrink-0", "self-center")
    expect(screen.getByRole("button", { name: /Notifications/ })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Settings/ })).toBeInTheDocument()
    expect(header).not.toHaveTextContent("Local session")
  })

  it("uses the sidebar-07 block shell composition", () => {
    render(<App />)

    const workspaceLabel = document.querySelector('[data-slot="sidebar-group-label"]')
    const footerMenuButton = document.querySelector('[data-slot="sidebar-footer"] [data-sidebar="menu-button"]')

    expect(screen.getByRole("button", { name: /DRIFT.*Local Control Plane/i })).toBeInTheDocument()
    expect(footerMenuButton).toHaveAttribute("data-size", "lg")
    expect(footerMenuButton).toHaveClass("h-12", "text-sm")
    expect(workspaceLabel).toHaveTextContent("Workspace")
    expect(screen.getByText("Racks")).toBeInTheDocument()
    expect(screen.getByText("Drift Operator")).toBeInTheDocument()
    expect(screen.queryByText("Transport health")).not.toBeInTheDocument()
  })

  it("applies the Swiss editorial visual system instead of the old glow treatment", () => {
    render(<App />)

    const shell = document.querySelector('[data-visual-style="swiss-editorial"]')
    expect(shell).toBeInTheDocument()
    expect(document.querySelector(".drift-editorial-grid")).toBeInTheDocument()
    expect(screen.getByText("OPERATIONS / FLEET CONTROL")).toBeInTheDocument()
    expect(screen.getByText("Updated just now")).toHaveClass("font-mono")
    expect(document.querySelector('[class*="shadow-[0_0_"]')).not.toBeInTheDocument()
    expect(document.querySelector('[class*="shadow-"]')).not.toBeInTheDocument()
    expect(document.querySelector('[class*="backdrop-blur"]')).not.toBeInTheDocument()
  })

  it("matches the sidebar-07 profile menu dimensions and typography", async () => {
    const user = userEvent.setup()
    render(<App />)

    const footer = document.querySelector<HTMLElement>('[data-slot="sidebar-footer"]')
    expect(footer).toBeInTheDocument()

    const profileButton = within(footer!).getByRole("button", { name: /Drift Operator/ })
    expect(profileButton).toHaveAttribute("data-size", "lg")
    expect(profileButton).toHaveClass("h-12", "text-sm")
    expect(profileButton.querySelector('[data-slot="avatar"]')).toHaveClass(
      "h-8",
      "w-8",
      "rounded-lg",
    )
    expect(profileButton.querySelector(".font-semibold")).toHaveTextContent("Drift Operator")

    await user.click(profileButton)

    const profileMenu = await screen.findByRole("menu")
    expect(profileMenu).toHaveClass("w-(--anchor-width)", "min-w-56", "rounded-lg")
    expect(profileMenu.querySelector('[data-slot="avatar"]')).toHaveClass(
      "h-8",
      "w-8",
      "rounded-lg",
    )
    expect(profileMenu.querySelector(".font-semibold")).toHaveTextContent("Drift Operator")
  })

  it("uses an off-canvas navigation sheet on narrow viewports", async () => {
    const user = userEvent.setup()
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
      expect(within(dialog).getByText("Workspace")).toBeInTheDocument()
      expect(
        document.querySelector('[class*="backdrop-blur"]'),
      ).not.toBeInTheDocument()

      await user.click(within(dialog).getByRole("button", { name: /close/i }))
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        value: previousWidth,
      })
    }
  })
})
