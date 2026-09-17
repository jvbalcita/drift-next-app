// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane"
import { useControlPlane } from "@/lib/api/use-control-plane"
import type {
  ControlPlaneClient,
  ControlPlaneIntent,
  ControlPlaneSnapshot,
  DeviceView,
  MutationResult,
} from "@/lib/domain/control-plane"
import { ControlPage } from "@/pages/ControlPage"

/**
 * ArrivingControlPlane stands in for the control plane across an arrival: it
 * answers the launch projection until a device has arrived, then answers the
 * projection with that device in it - which is what the real one does once the
 * post-launch watcher has recorded an arrival in the registry. Nothing here is
 * clicked, typed, or dispatched: the console has to notice on its own.
 */
class ArrivingControlPlane implements ControlPlaneClient {
  private reads = 0

  constructor(
    private readonly launch: ControlPlaneSnapshot,
    private readonly arrived: DeviceView,
    /**
     * latencyMs is how long a read takes to answer. A control plane answers over
     * a socket rather than in the same microtask, and that difference is the whole
     * point of the loading-state test below: a read that is still in flight is
     * what an unquiet refresh would announce.
     */
    private readonly latencyMs = 5,
  ) {}

  get readCount(): number {
    return this.reads
  }

  getSnapshot(): ControlPlaneSnapshot {
    return this.projection()
  }

  async refresh(): Promise<ControlPlaneSnapshot> {
    await new Promise((resolve) => setTimeout(resolve, this.latencyMs))
    this.reads += 1
    return this.projection()
  }

  async dispatch(intent: ControlPlaneIntent): Promise<MutationResult> {
    return { ok: true, kind: intent.type, message: "No command was sent by this test." }
  }

  private projection(): ControlPlaneSnapshot {
    // The launch read is what the console already had; every read after it is the
    // projection the watcher's arrival landed in.
    if (this.reads <= 1) return this.launch
    return { ...this.launch, devices: [...this.launch.devices, this.arrived] }
  }
}

function arrivalFixture(): DeviceView {
  const launch = new MockControlPlaneClient().getSnapshot()
  const first = launch.devices[0]
  return {
    ...first,
    id: "orbiter-09",
    stableIdentity: "device-arrived",
    displayName: "Orbiter 09",
    status: "online",
    endpointId: "endpoint-orbiter-09-current",
  }
}

function launchFixture(): ControlPlaneSnapshot {
  // The console's launch projection: what the startup scan registered, with no
  // sign of the device that has not attached yet.
  return new MockControlPlaneClient().getSnapshot()
}

describe("console arrivals", () => {
  it("shows a device that arrived after launch with no operator action", async () => {
    const client = new ArrivingControlPlane(launchFixture(), arrivalFixture())
    render(<ConsoleUnderTest client={client} />)

    // The console starts from what was attached at launch.
    expect(screen.queryByRole("button", { name: /Orbiter 09/i })).not.toBeInTheDocument()

    // Nothing is clicked between here and the assertion: the console reads the
    // control plane's projection on its own schedule until the arrival is in it.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Orbiter 09/i })).toBeInTheDocument()
    })
    expect(client.readCount).toBeGreaterThan(1)
  })

  it("keeps the devices that were already there and dispatches nothing", async () => {
    const launch = launchFixture()
    const client = new ArrivingControlPlane(launch, arrivalFixture())
    const dispatched: ControlPlaneIntent[] = []
    render(<ConsoleUnderTest client={client} dispatched={dispatched} />)

    const first = launch.devices[0]
    await waitFor(() => {
      expect(screen.getByRole("button", { name: new RegExp(first.displayName, "i") })).toBeInTheDocument()
    })
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Orbiter 09/i })).toBeInTheDocument()
    })

    // No scan was started and no reload was pressed: the arrival was surfaced by
    // reading state the service already held.
    expect(dispatched).toEqual([])
  })

  it("refreshes without raising the loading state it settled out of", async () => {
    const client = new ArrivingControlPlane(launchFixture(), arrivalFixture())
    const rendered: boolean[] = []
    render(<LoadingUnderTest client={client} onLoadingChange={(loading) => rendered.push(loading)} />)

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("settled"))
    await waitFor(() => expect(client.readCount).toBeGreaterThan(2))

    // idle -> the console's first read -> idle, and every read after that is a
    // refresh the operator did not ask for, so the surface never goes back to
    // announcing itself as loading.
    const settleTransitions = rendered.filter((loading, index) => loading && rendered[index + 1] === false).length
    expect(settleTransitions).toBe(1)
    expect(rendered.at(-1)).toBe(false)
    expect(screen.getByRole("status")).toHaveTextContent("settled")
  })
})

function ConsoleUnderTest({ client, dispatched }: { client: ControlPlaneClient; dispatched?: ControlPlaneIntent[] }) {
  const { snapshot } = useControlPlane({ client, refreshIntervalMs: 20 })
  return (
    <ControlPage
      snapshot={snapshot}
      dispatch={async (intent) => {
        dispatched?.push(intent)
        return client.dispatch(intent)
      }}
    />
  )
}

function LoadingUnderTest({ client, onLoadingChange }: { client: ControlPlaneClient; onLoadingChange: (loading: boolean) => void }) {
  const { loading } = useControlPlane({ client, refreshIntervalMs: 20 })
  onLoadingChange(loading)
  return <p role="status">{loading ? "loading" : "settled"}</p>
}
