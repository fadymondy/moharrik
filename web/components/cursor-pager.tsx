"use client"

import { useCallback, useEffect, useState } from "react"
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Pagination, PaginationContent, PaginationItem } from "@/components/ui/pagination"
import { useTranslations } from "@/lib/i18n"

/*
Keyset paging for the workspace lists (MH-467), the shape MH-408 gave the
contacts list: the API answers a page, its `total` and a `next_cursor`, and
"previous" is the stack of cursors already visited rather than an offset — a
row added while somebody pages does not shift every page by one.
*/

/** The cursor stack. `reset` whenever the filter changes: a cursor belongs to one filter. */
export function useCursorStack(resetKey: string) {
  // cursors[i] opens page i+1; cursors[0] is the first page.
  const [cursors, setCursors] = useState<string[]>([""])
  useEffect(() => setCursors([""]), [resetKey])
  const cursor = cursors[cursors.length - 1]
  const next = useCallback((c: string) => setCursors((s) => (s[s.length - 1] === c ? s : [...s, c])), [])
  const prev = useCallback(() => setCursors((s) => (s.length > 1 ? s.slice(0, -1) : s)), [])
  return { cursor, index: cursors.length - 1, next, prev }
}

/** Where a page sits in the list: first row, last row, and the page count. */
export function pageRange(index: number, pageSize: number, shown: number, total: number) {
  return {
    from: shown ? index * pageSize + 1 : 0,
    to: index * pageSize + shown,
    pages: Math.max(1, Math.ceil(total / pageSize)),
  }
}

/** "Showing X–Y of N", above the rows. */
export function PageSummary({ index, pageSize, shown, total, name }: { index: number; pageSize: number; shown: number; total: number; name: string }) {
  const { t, formatNumber } = useTranslations()
  const { from, to } = pageRange(index, pageSize, shown, total)
  return (
    <p className="px-6 pb-3 text-sm text-grid-muted" aria-live="polite" data-pager-summary={name} data-range={`${from}-${to}/${total}`}>
      {t("common.showing", { from: formatNumber(from), to: formatNumber(to), total: formatNumber(total) })}
    </p>
  )
}

/** Previous / next under the rows, when there is more than one page. */
export function PageNav({
  index,
  pageSize,
  total,
  nextCursor,
  busy,
  onNext,
  onPrev,
  name,
}: {
  index: number
  pageSize: number
  total: number
  nextCursor: string | undefined
  /** The page on screen is not the one asked for yet: do not act on it. */
  busy?: boolean
  onNext: (cursor: string) => void
  onPrev: () => void
  name: string
}) {
  const { t, formatNumber } = useTranslations()
  const { pages } = pageRange(index, pageSize, 0, total)
  if (pages <= 1 && index === 0) return null
  return (
    <Pagination className="px-6 pt-4" data-pager={name}>
      <PaginationContent className="gap-2">
        <PaginationItem>
          <Button variant="ghost" disabled={busy || index === 0} onClick={onPrev} data-page="prev">
            <ChevronLeftIcon data-icon="inline-start" className="rtl:rotate-180" />
            {t("common.prevPage")}
          </Button>
        </PaginationItem>
        <PaginationItem>
          <span className="px-2 text-sm text-grid-muted tabular-nums" data-page-of={`${index + 1}/${pages}`}>
            {t("common.pageOf", { page: formatNumber(index + 1), pages: formatNumber(pages) })}
          </span>
        </PaginationItem>
        <PaginationItem>
          <Button variant="ghost" disabled={busy || !nextCursor} onClick={() => nextCursor && onNext(nextCursor)} data-page="next">
            {t("common.nextPage")}
            <ChevronRightIcon data-icon="inline-end" className="rtl:rotate-180" />
          </Button>
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}
