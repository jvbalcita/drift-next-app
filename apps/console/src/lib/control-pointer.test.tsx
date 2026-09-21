// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render } from "@testing-library/react"
import { MousePointer2 } from "lucide-react"
import { describe, expect, it } from "vitest"
import { controlPointerCursor, controlPointerPath, controlPointerSvg } from "./control-pointer"

describe("the big frame's pointer", () => {
  it("is the sidebar's own Control glyph, all black", () => {
    // The sidebar names this console's Control section with lucide's
    // `MousePointer2`. The cursor is drawn from a path constant because a CSS
    // cursor is a URL and a React icon is not one, so the two are compared here
    // rather than assumed to agree: a lucide upgrade that moved the glyph would
    // otherwise leave the frame's pointer drawing the old one.
    const { container } = render(<MousePointer2 />)
    const path = container.querySelector("path")
    expect(path).not.toBeNull()
    expect(controlPointerPath).toBe(path?.getAttribute("d"))
  })

  it("draws that glyph solid black, and is never the crosshair it replaced", () => {
    expect(controlPointerSvg).toContain(`fill="#000000"`)
    expect(controlPointerSvg).toContain(`stroke="#000000"`)
    expect(controlPointerSvg).toContain(controlPointerPath)
    expect(controlPointerCursor.startsWith("url(\"data:image/svg+xml,")).toBe(true)
    // The hotspot is the arrow's own tip, and the fallback is a pointer.
    expect(controlPointerCursor.endsWith('") 4 4, pointer')).toBe(true)
    expect(controlPointerCursor).not.toContain("crosshair")
  })

  it("carries the glyph through the URL encoding a cursor image needs", () => {
    // A cursor image is a data URL, so the markup is percent-encoded and the
    // glyph has to survive the round trip: decoded, it is the same SVG.
    const encoded = controlPointerCursor.slice('url("data:image/svg+xml,'.length, controlPointerCursor.indexOf('") 4 4'))
    expect(decodeURIComponent(encoded)).toBe(controlPointerSvg)
  })
})
