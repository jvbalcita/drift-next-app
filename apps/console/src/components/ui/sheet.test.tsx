// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest"
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { Button } from "./button"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "./sheet"

describe("SheetContent", () => {
  it("keeps header and footer fixed while placing the body in one shadcn ScrollArea viewport", () => {
    render(
      <Sheet open>
        <SheetContent>
          <SheetHeader><SheetTitle>Details</SheetTitle><SheetDescription>Inspectable facts.</SheetDescription></SheetHeader>
          <div>Scrollable facts</div>
          <SheetFooter><Button>Done</Button></SheetFooter>
        </SheetContent>
      </Sheet>,
    )
    const dialog = screen.getByRole("dialog", { name: "Details" })
    expect(dialog.querySelectorAll('[data-slot="sheet-body"]')).toHaveLength(1)
    expect(dialog.querySelectorAll('[data-slot="scroll-area-viewport"]')).toHaveLength(1)
    expect(dialog.querySelector('[data-slot="sheet-header"]')?.parentElement).toBe(dialog)
    expect(dialog.querySelector('[data-slot="sheet-footer"]')?.parentElement).toBe(dialog)
    expect(dialog.querySelector('[data-slot="sheet-body"]')).toHaveTextContent("Scrollable facts")
  })
})
