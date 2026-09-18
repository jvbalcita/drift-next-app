// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs"

describe("Tabs", () => {
  it("provides one responsive treatment and preserves tab behavior", async () => {
    const user = userEvent.setup()
    render(
      <Tabs defaultValue="appearance">
        <TabsList aria-label="Console sections">
          <TabsTrigger value="appearance">Appearance</TabsTrigger>
          <TabsTrigger value="presentation">Device Presentation</TabsTrigger>
          <TabsTrigger value="fleet">Fleet Defaults</TabsTrigger>
        </TabsList>
        <TabsContent value="appearance">Appearance content</TabsContent>
        <TabsContent value="presentation">Presentation content</TabsContent>
        <TabsContent value="fleet">Fleet content</TabsContent>
      </Tabs>,
    )

    const tablist = screen.getByRole("tablist", { name: "Console sections" })
    expect(tablist).toHaveClass("max-w-full", "overflow-x-auto", "border-b")

    const presentation = screen.getByRole("tab", { name: "Device Presentation" })
    expect(presentation).toHaveClass("shrink-0", "whitespace-nowrap")
    await user.click(presentation)
    expect(presentation).toHaveAttribute("aria-selected", "true")
    expect(screen.getByText("Presentation content")).toBeVisible()
  })
})
