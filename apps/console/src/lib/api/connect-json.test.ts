import { describe, expect, it } from "vitest"
import { resolvedOperatorId } from "@/lib/api/connect-json"

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
})
