import { useEffect, useState } from "react"

/**
 * prefersReducedMotion reads the operator's own motion preference once, and
 * follows it if it changes while the console is open.
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches
}

export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(prefersReducedMotion)
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return
    const query = window.matchMedia("(prefers-reduced-motion: reduce)")
    const listener = (event: MediaQueryListEvent) => setReduced(event.matches)
    query.addEventListener?.("change", listener)
    return () => query.removeEventListener?.("change", listener)
  }, [])
  return reduced
}
