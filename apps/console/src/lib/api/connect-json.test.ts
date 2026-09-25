import { afterEach, describe, expect, it, vi } from "vitest"
import { ConnectJsonClient, requestContext, resolvedOperatorId } from "@/lib/api/connect-json"
import { DeviceMirrorClient } from "@/lib/api/control-plane-clients"

afterEach(() => vi.restoreAllMocks())

describe("resolvedOperatorId", () => {
  it("prefers an explicit configured operator id", () => {
    expect(resolvedOperatorId({ configured: "operator-fixed", useMock: false })).toBe("operator-fixed")
  })

  it("uses the stable mock operator in mock or test mode", () => {
    expect(resolvedOperatorId({ useMock: true })).toBe("console-local-operator")
  })

  it("reuses a session-scoped operator id for the real console", () => {
    const storage = new Map<string, string>()
    const fake = {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => {
        storage.set(key, value)
      },
    }

    const first = resolvedOperatorId({ useMock: false, storage: fake })
    const second = resolvedOperatorId({ useMock: false, storage: fake })

    expect(first).toMatch(/^console-operator-/)
    expect(second).toBe(first)
  })

  it("always includes an actor id in mutation contexts", () => {
    expect(requestContext({ requestId: "request-1", actorId: "  " }).actorId).toBeTruthy()
  })

  it("propagates the mirror negotiation abort signal to the authenticated RPC fetch", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("The negotiation was cancelled", "AbortError")), { once: true })
    }))
    const controller = new AbortController()
    const client = new DeviceMirrorClient(new ConnectJsonClient("http://control-plane.test", "lab-token"))
    const request = client.negotiate("stream-1", "offer-sdp", "workspace-1", undefined, controller.signal)

    await vi.waitFor(() => expect(fetchSpy).toHaveBeenCalledOnce())
    expect(fetchSpy).toHaveBeenCalledWith(
      "http://control-plane.test/drift.v1.DeviceMirrorService/NegotiateMirrorStream",
      expect.objectContaining({ signal: controller.signal }),
    )
    controller.abort()
    await expect(request).rejects.toMatchObject({ name: "AbortError" })
  })
})
