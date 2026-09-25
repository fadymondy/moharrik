"use client"

import { useEffect, useRef, useState, type ComponentProps } from "react"

import { cn } from "@/lib/utils"

/**
 * A horizontal scroller that SHOWS it scrolls (MH-463). Wide tables and the deal board scroll
 * inside their own box so the page never grows wider than the viewport, but a bare
 * `overflow-x-auto` gives no hint on a Mac (overlay scrollbars) or a phone: the last columns
 * just looked cut off. Scrollbars are hidden app-wide (MH-601), so the hint is a fade on
 * whichever edge still has content past it (tracked on scroll and resize, and correct in RTL,
 * where scrollLeft runs negative).
 * When it does overflow the box becomes focusable, so the keyboard can scroll it too.
 */
export function ScrollX({ className, innerClassName, children, ...props }: ComponentProps<"div"> & { innerClassName?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [edge, setEdge] = useState({ start: false, end: false })

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => {
      const max = el.scrollWidth - el.clientWidth
      const pos = Math.abs(el.scrollLeft)
      const next = { start: max > 1 && pos > 1, end: max > 1 && max - pos > 1 }
      setEdge((e) => (e.start === next.start && e.end === next.end ? e : next))
    }
    update()
    el.addEventListener("scroll", update, { passive: true })
    const ro = new ResizeObserver(update)
    ro.observe(el)
    for (const child of el.children) ro.observe(child)
    return () => {
      el.removeEventListener("scroll", update)
      ro.disconnect()
    }
  }, [])

  const overflowing = edge.start || edge.end
  return (
    <div
      data-scroll-x
      data-overflow-start={edge.start || undefined}
      data-overflow-end={edge.end || undefined}
      className={cn("relative min-w-0", className)}
      {...props}
    >
      <div ref={ref} tabIndex={overflowing ? 0 : undefined} className={cn("scroll-x relative overflow-x-auto outline-none focus-visible:ring-2 focus-visible:ring-ring/50", innerClassName)}>
        {children}
      </div>
      <div
        aria-hidden
        className={cn(
          "pointer-events-none absolute top-0 bottom-2 start-0 w-10 bg-gradient-to-r from-background to-transparent transition-opacity rtl:bg-gradient-to-l",
          edge.start ? "opacity-100" : "opacity-0",
        )}
      />
      <div
        aria-hidden
        className={cn(
          "pointer-events-none absolute top-0 bottom-2 end-0 w-10 bg-gradient-to-l from-background to-transparent transition-opacity rtl:bg-gradient-to-r",
          edge.end ? "opacity-100" : "opacity-0",
        )}
      />
    </div>
  )
}
