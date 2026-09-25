/**
 * The pointer this console draws over a mirrored device's screen.
 *
 * The big frame's pointer over a device's picture is the operator's own pointer
 * ON the device, and it was drawn as a crosshair: a glyph this console invented,
 * which reads as "pick a coordinate" rather than as "this is your pointer on the
 * screen". This console already names exactly this idea with one glyph - the
 * sidebar's Control entry is lucide's `mouse-pointer-2` - so the frame draws THAT
 * icon, and the section and the pointer over a device are one thing drawn one way.
 *
 * It is the browser's own cursor rather than an element following the pointer: an
 * element has to be moved on every mouse event and trails the real pointer, and
 * it would be drawn INTO the picture an operator is reading.
 *
 * The path below is lucide's `mouse-pointer-2` path verbatim (lucide-react
 * v1.45.0, ISC), written out because a CSS cursor is a URL and a React icon is
 * not one. The glyph is filled black and ringed in white: a solid black arrow
 * disappears on a black screen, and a white stroke drawn under the fill keeps
 * the same tip readable on black and on white without a shadow.
 */
export const controlPointerPath = "M4.037 4.688a.495.495 0 0 1 .651-.651l16 6.5a.5.5 0 0 1-.063.947l-6.124 1.58a2 2 0 0 0-1.438 1.435l-1.579 6.126a.5.5 0 0 1-.947.063z"

/** The cursor image's own markup, at the icon's own 24×24 size. */
export const controlPointerSvg = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path d="${controlPointerPath}" fill="none" stroke="#ffffff" stroke-width="3" stroke-linejoin="round"/><path d="${controlPointerPath}" fill="#000000" stroke="#000000" stroke-linejoin="round"/></svg>`

/**
 * The complete `cursor` value: the icon, its hotspot, and a platform fallback.
 *
 * The hotspot is the arrow's own tip (4, 4) in the icon's 24×24 box, so the point
 * the operator aims with is the point that reaches the device. `pointer` follows
 * as the fallback for a platform that refuses a cursor image, which keeps the
 * frame's pointer a pointer rather than a text caret.
 */
export const controlPointerCursor = `url("data:image/svg+xml,${encodeURIComponent(controlPointerSvg)}") 4 4, pointer`
