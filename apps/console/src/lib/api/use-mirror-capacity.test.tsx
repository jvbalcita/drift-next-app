import "@testing-library/jest-dom/vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import type { LiveMirrorClient } from "@/lib/api/control-plane-clients"
import { useMirrorCapacity } from "@/lib/api/use-mirror-capacity"
import type { MirrorCapacityView } from "@/lib/live-mirror"
import { planeCapacity } from "@/test/mirror-fixtures"

/**
 * The console reads how much room the control plane has, and these cases are about
 * the reading: it is the plane's own number, a reading that did not arrive is not a
 * plane with no room in it, and a console with no control plane at all says which
 * fact it is missing.
 */

/** capacityClient answers the plane's own bound, or a failure, and counts the reads. */
function capacityClient(answer: MirrorCapacityView | Error): { client: LiveMirrorClient; reads: string[] } {
  const reads: string[] = []
  const client: LiveMirrorClient = {
    async startStream() { throw new Error("no stream is opened by a capacity read") },
    async getCapacity(workspaceId) {
      reads.push(workspaceId)
      if (answer instanceof Error) throw answer
      return answer
    },
    async negotiate() { throw new Error("no stream is negotiated by a capacity read") },
    async stopStream() { throw new Error("no stream is stopped by a capacity read") },
    async getStream() { throw new Error("no stream is read by a capacity read") },
    streamEndpoint(path) { return { url: path, headers: {} } },
  }
  return { client, reads }
}

describe("the control plane's live-stream capacity, as this console reads it", () => {
  it("reads the plane's own bound for the workspace it is reading for, once", async () => {
    const plane: MirrorCapacityView = planeCapacity(6, 2)
    const { client, reads } = capacityClient(plane)
    const { result } = renderHook(() => useMirrorCapacity(client, "workspace-lab-local"))

    await waitFor(() => expect(result.current.capacity).toEqual(plane))
    expect(result.current.failure).toBe("")
    // Once, and not on a poll: the bound belongs to the deployment, so it cannot change
    // while this console is open.
    expect(reads).toEqual(["workspace-lab-local"])
  })

  it("reports a reading it did not get as a reading it did not get, never as a plane with no room", async () => {
    const { client } = capacityClient(new Error("the control plane is not answering"))
    const { result } = renderHook(() => useMirrorCapacity(client, "workspace-lab-local"))

    await waitFor(() => expect(result.current.failure).toContain("not answering"))
    // The capacity stays UNREAD: it is not zero. The two lead to the same place -
    // the grid carries no tiles - and only one of them is true, and a console that
    // published a bound here would be inventing the number this whole reading
    // replaced.
    expect(result.current.capacity).toBeNull()
  })

  it("says which fact it is missing when this console has no control plane behind it", async () => {
    const { result } = renderHook(() => useMirrorCapacity(undefined, "workspace-lab-local"))
    await waitFor(() => expect(result.current.failure).toContain("no control plane"))
    expect(result.current.capacity).toBeNull()
  })
})
