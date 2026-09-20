// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { AppTitlebar } from "@/components/app-titlebar"
import { SidebarProvider } from "@/components/ui/sidebar"

function renderTitlebar(platform: "macos" | "windows") {
  render(
    <SidebarProvider>
      <AppTitlebar platform={platform} />
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
})
