// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { SidebarProvider } from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { navigation, navigationGroups } from "@/lib/navigation"
import { AppSidebar } from "./app-sidebar"

function renderSidebar(onSectionChange = vi.fn()) {
  render(
    <TooltipProvider>
      <SidebarProvider>
        <AppSidebar activeSection="Devices" onSectionChange={onSectionChange} />
      </SidebarProvider>
    </TooltipProvider>,
  )
  return onSectionChange
}

describe("AppSidebar", () => {
  it("renders grouped links for pages without duplicating page tabs", () => {
    renderSidebar()

    for (const group of navigationGroups) expect(screen.getByText(group)).toBeInTheDocument()
    for (const item of navigation) {
      const link = screen.getByRole("link", { name: item.section })
      expect(link).toHaveAttribute("href", `#${item.hash}/${item.views[0].id}`)
    }

    expect(screen.getAllByRole("link")).toHaveLength(navigation.length)
    expect(screen.queryByText("Needs Attention")).not.toBeInTheDocument()
    expect(screen.queryByText("Discovery Scans")).not.toBeInTheDocument()
    expect(screen.queryByText("Rack A")).not.toBeInTheDocument()
  })

  it("marks only the current page and navigates to its default view", async () => {
    const onSectionChange = renderSidebar()

    expect(screen.getByRole("link", { name: "Devices" })).toHaveAttribute("aria-current", "page")
    expect(screen.getByRole("link", { name: "Overview" })).not.toHaveAttribute("aria-current")

    await userEvent.click(screen.getByRole("link", { name: "Policies" }))
    expect(onSectionChange).toHaveBeenCalledWith("Policies", "active")
  })

  it("opens the operator menu and routes only to implemented destinations", async () => {
    const user = userEvent.setup()
    const onSectionChange = renderSidebar()

    await user.click(screen.getByRole("button", { name: /Drift Operator/ }))
    await user.click(await screen.findByRole("menuitem", { name: "Operator Settings" }))

    expect(onSectionChange).toHaveBeenCalledWith("Settings", "workspace")
    expect(screen.queryByText("Billing")).not.toBeInTheDocument()
    expect(screen.queryByText("Log out")).not.toBeInTheDocument()
  })
})
