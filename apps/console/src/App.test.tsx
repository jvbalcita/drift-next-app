// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import App from "./App"

describe("Drift command center", () => {
  it("renders the fleet overview and selected device surfaces", () => {
    render(<App />)

    expect(screen.getByRole("heading", { name: /fleet overview/i })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: /device fleet/i })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: /selected device/i })).toBeInTheDocument()
    expect(screen.getByText("Demo mode · actions disabled")).toBeInTheDocument()
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

  it("exposes Control as a separate mock-only destination", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))

    expect(screen.getByRole("heading", { name: /Mirror control/i })).toBeInTheDocument()
    expect(screen.getByText("Preview only")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Start mirror preview/i })).toBeDisabled()
    expect(screen.queryByRole("checkbox", { name: /Follower device Atlas 04/i })).not.toBeInTheDocument()
  })

  it("supports labelled all-eligible follower selection without sending a command", async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole("button", { name: /^Control/ }))
    await user.click(screen.getByRole("button", { name: /Select all eligible/i }))

    const startButton = screen.getByRole("button", { name: /Start mirror preview/i })
    expect(startButton).toBeEnabled()

    await user.click(startButton)

    expect(screen.getByText(/no device command was sent/i)).toBeInTheDocument()
  })

  it("loads the typed browser-only destinations through the shell", async () => {
    const user = userEvent.setup()
    render(<App />)

    const destinations = [
      ["Devices", /Device registry/],
      ["Network Profiles", /Network Profiles/],
      ["Groups", /Groups and membership/],
      ["Agents", /Agent profiles/],
      ["Runs", /Runs and targets/],
      ["Events", /Events and audit/],
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

    expect(screen.getByRole("button", { name: /DRIFT.*Demo control plane/i })).toBeInTheDocument()
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
