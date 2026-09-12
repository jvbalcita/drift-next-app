// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
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
})
