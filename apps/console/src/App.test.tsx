// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
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
})
