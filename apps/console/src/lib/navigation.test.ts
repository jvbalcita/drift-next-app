import { describe, expect, it } from "vitest"
import { hashForRoute, navigation, navigationGroups, routeFromHash } from "./navigation"

describe("navigation", () => {
  it("assigns every page to one sidebar group", () => {
    expect(new Set(navigation.map((item) => item.section)).size).toBe(navigation.length)
    expect(new Set(navigation.map((item) => item.group))).toEqual(new Set(navigationGroups))
  })

  it("keeps page-local tab routes addressable", () => {
    for (const item of navigation) {
      for (const view of item.views) {
        const route = routeFromHash(`#${item.hash}/${view.id}`)
        expect(route).toEqual({ section: item.section, view: view.id, recordId: undefined })
        expect(hashForRoute(route)).toBe(`#${item.hash}/${view.id}`)
      }
    }
  })
})
