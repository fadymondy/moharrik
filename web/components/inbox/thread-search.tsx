"use client"

import { useEffect, useRef, useState } from "react"
import { ChevronDownIcon, ChevronUpIcon, XIcon } from "lucide-react"

import { Button, Input } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { ApiError, type InboxMessage } from "@/lib/api"
import { searchConversation } from "@/lib/queries"
import { useTranslations } from "@/lib/i18n"

/*
Find-in-chat (MH-662): a bar under the thread header, not a dialog — WhatsApp
never blocks the thread while searching. Matches come back oldest-first per
page (the same "load older with a cursor" shape as the thread itself), so
"next" walks toward the newest match already loaded and "previous" walks
toward the oldest, fetching another page from the cursor when the reader runs
off either end.
*/
export function ThreadSearch({
  conversationId,
  onClose,
  onJump,
}: {
  conversationId: string
  onClose: () => void
  onJump: (messageId: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [q, setQ] = useState("")
  const [matches, setMatches] = useState<InboxMessage[]>([])
  const [cursor, setCursor] = useState("")
  const [index, setIndex] = useState(-1)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const reqId = useRef(0)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    const query = q.trim()
    if (!query) {
      setMatches([])
      setCursor("")
      setIndex(-1)
      setErr(null)
      return
    }
    const id = ++reqId.current
    setBusy(true)
    const handle = window.setTimeout(async () => {
      try {
        const res = await searchConversation(ws.id, conversationId, query)
        if (reqId.current !== id) return
        setMatches(res.messages)
        setCursor(res.next_cursor)
        setIndex(res.messages.length - 1)
        setErr(null)
        if (res.messages.length > 0) onJump(res.messages[res.messages.length - 1]!.id)
      } catch (e) {
        if (reqId.current !== id) return
        setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      } finally {
        if (reqId.current === id) setBusy(false)
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, 300)
    return () => window.clearTimeout(handle)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, conversationId, ws.id])

  async function loadOlder(): Promise<InboxMessage[]> {
    if (!cursor) return []
    setBusy(true)
    try {
      const res = await searchConversation(ws.id, conversationId, q.trim(), cursor)
      setMatches((prev) => [...res.messages, ...prev])
      setCursor(res.next_cursor)
      return res.messages
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      return []
    } finally {
      setBusy(false)
    }
  }

  async function goPrev() {
    if (index > 0) {
      const next = index - 1
      setIndex(next)
      onJump(matches[next]!.id)
      return
    }
    const older = await loadOlder()
    if (older.length > 0) {
      // The new page was prepended; the previous match is now at the end of it.
      setIndex(older.length - 1)
      onJump(older[older.length - 1]!.id)
    }
  }

  function goNext() {
    if (index < matches.length - 1) {
      const next = index + 1
      setIndex(next)
      onJump(matches[next]!.id)
    }
  }

  const hasMatches = matches.length > 0
  const canGoOlder = index === 0 && !!cursor

  return (
    <div
      className="flex items-center gap-2 border-b border-nq-line bg-nq-surface-overlay px-6 py-2"
      data-thread-search
    >
      <Input
        ref={inputRef}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={t("inbox.search.placeholder")}
        aria-label={t("inbox.search.placeholder")}
        className="h-8 max-w-xs"
        data-search-input
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose()
          if (e.key === "Enter") {
            e.preventDefault()
            if (e.shiftKey) void goPrev()
            else goNext()
          }
        }}
      />
      <span className="min-w-0 truncate text-xs text-muted-foreground" data-search-count>
        {err
          ? err
          : q.trim() === ""
            ? ""
            : busy && matches.length === 0
              ? t("common.working")
              : hasMatches
                ? t("inbox.search.matchCount", {
                    n: index + 1,
                    total: cursor ? `${matches.length}+` : matches.length,
                  })
                : t("inbox.search.noMatches")}
      </span>
      <div className="ms-auto flex items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("inbox.search.prev")}
          disabled={!hasMatches || (index === 0 && !canGoOlder)}
          onClick={() => void goPrev()}
          data-search-prev
        >
          <ChevronUpIcon />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("inbox.search.next")}
          disabled={!hasMatches || index >= matches.length - 1}
          onClick={goNext}
          data-search-next
        >
          <ChevronDownIcon />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("inbox.search.close")}
          onClick={onClose}
          data-search-close
        >
          <XIcon />
        </Button>
      </div>
    </div>
  )
}
