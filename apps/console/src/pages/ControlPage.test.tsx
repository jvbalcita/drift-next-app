// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import type { ControlPlaneIntent } from "@/lib/domain/control-plane"
import { ControlPage } from "./ControlPage"

describe("ControlPage screenshot", () => {
  it("does not authorize capture when the confirmed transport is unbound", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    render(<ControlPage snapshot={client.getSnapshot()} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: "Screenshot" }))

    await waitFor(() => {
      expect(screen.getByText(/Confirm the connected transport for this device/i)).toBeInTheDocument()
    })
    expect(intents.some((intent) => intent.type === "submitDeviceAction" && intent.kind === "capture")).toBe(false)
    expect(intents.some((intent) => intent.type === "captureLabObservation")).toBe(false)
  })

  it("authorizes capture once the lab adapter reports a confirmed serial", async () => {
    const user = userEvent.setup()
    const client = new MockControlPlaneClient()
    const intents: ControlPlaneIntent[] = []
    const dispatch = async (intent: ControlPlaneIntent) => {
      intents.push(intent)
      return client.dispatch(intent)
    }
    const snapshot = client.getSnapshot()
    snapshot.labAdapter = { ...snapshot.labAdapter, confirmedSerial: "SERIAL-A", confirmedDisplayName: "Atlas 04" }
    render(<ControlPage snapshot={snapshot} dispatch={dispatch} />)

    await user.click(screen.getByRole("button", { name: /Atlas 04/i }))
    await user.click(screen.getByRole("button", { name: "Screenshot" }))

    await waitFor(() => {
      expect(intents.some((intent) => intent.type === "captureLabObservation" && intent.serial === "SERIAL-A")).toBe(true)
    })
    const captureIndex = intents.findIndex((intent) => intent.type === "submitDeviceAction" && intent.kind === "capture")
    const observeIndex = intents.findIndex((intent) => intent.type === "captureLabObservation")
    expect(captureIndex).toBeGreaterThanOrEqual(0)
    expect(observeIndex).toBeGreaterThan(captureIndex)
  })
})
