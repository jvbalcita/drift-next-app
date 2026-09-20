// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"

import { AppTitlebar } from "@/components/app-titlebar"
import { SidebarProvider } from "@/components/ui/sidebar"
import { routeFromHash } from "@/lib/navigation"

function renderTitlebar(platform: "macos" | "windows") {
  render(
    <SidebarProvider>
      <AppTitlebar platform={platform} route={routeFromHash("#overview/fleet")} />
    </SidebarProvider>,
  )
}

describe("AppTitlebar", () => {
  it("places the sidebar toggle after the native macOS traffic lights", () => {
    renderTitlebar("macos")

    const toggle = screen.getByRole("button", { name: "Toggle sidebar" })
    expect(toggle).toHaveAttribute("data-platform", "macos")
    expect(toggle).toHaveClass("data-[platform=macos]:left-24")
    expect(screen.queryByLabelText("Window controls")).not.toBeInTheDocument()
  })

  it("places the sidebar toggle before accessible Windows controls", () => {
    renderTitlebar("windows")

    const toggle = screen.getByRole("button", { name: "Toggle sidebar" })
    expect(toggle).toHaveAttribute("data-platform", "windows")
    expect(toggle).toHaveClass("data-[platform=windows]:right-[146px]")
    expect(screen.getByRole("button", { name: "Minimize window" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Maximize window" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Close window" })).toBeInTheDocument()
  })

  it("keeps the titlebar aligned with the full sidebar and collapsed icon rail", async () => {
    const user = userEvent.setup({ delay: null })
    renderTitlebar("macos")

    const titlebar = screen.getByRole("banner")
    const sidebarSurface = document.querySelector('[data-slot="titlebar-sidebar-surface"]')
    expect(titlebar).toHaveAttribute("data-sidebar-state", "expanded")
    expect(sidebarSurface).toHaveClass("w-(--sidebar-width)")

    await user.click(screen.getByRole("button", { name: "Toggle sidebar" }))

    expect(titlebar).toHaveAttribute("data-sidebar-state", "collapsed")
    expect(sidebarSurface).toHaveClass("w-(--sidebar-width-icon)")
    expect(document.querySelector('[data-slot="titlebar-content"]')).toHaveClass("ml-32")
  })
})
