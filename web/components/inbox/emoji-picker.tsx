"use client"

import { useMemo, useState } from "react"
import { SmilePlusIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { useTranslations } from "@/lib/i18n"

/*
MH-659's emoji picker. The shadcn MCP registry search (chanhdai.com/shadcnspace/
reactbits/animate-ui/lucide-animated/shadcncraft/shadcnblocks/benday, per
web/components.json) is unreachable in this environment (CONNECT_TIMEOUT) and
`npx shadcn@latest search` has nowhere to install TO even if it answered:
web/node_modules is a junction into moharrik-integrate's physical node_modules
(shared by every worktree), so adding a new npm dependency here would write
into a tree this task is explicitly forbidden from touching. A well-maintained
picker library was the card's fallback ("if none fits, use a well-maintained
lightweight picker") — same problem, it still needs `npm install`.

So: a small hand-rolled picker, built only from primitives already vendored
here (Popover, Input, Button — no new dependency). It is curated rather than
exhaustive (the full Unicode emoji set is thousands of glyphs and would need a
data file this repo doesn't have), grouped the way most pickers group, with a
substring search over each emoji's name. RTL/dark follow automatically: it is
plain text glyphs in the existing Popover, which already mirrors and themes.
*/

type Entry = { char: string; name: string }

const CATEGORIES: { key: string; label: string; items: Entry[] }[] = [
  {
    key: "smileys",
    label: "Smileys",
    items: [
      { char: "😀", name: "grinning" },
      { char: "😁", name: "beaming" },
      { char: "😂", name: "joy tears" },
      { char: "🤣", name: "rofl" },
      { char: "😊", name: "smiling blush" },
      { char: "😉", name: "wink" },
      { char: "😍", name: "heart eyes love" },
      { char: "🥰", name: "smiling hearts love" },
      { char: "😘", name: "kiss" },
      { char: "😎", name: "cool sunglasses" },
      { char: "🤔", name: "thinking" },
      { char: "😐", name: "neutral" },
      { char: "😴", name: "sleeping" },
      { char: "😢", name: "crying sad" },
      { char: "😭", name: "sobbing sad" },
      { char: "😡", name: "angry mad" },
      { char: "😱", name: "screaming shocked" },
      { char: "🥳", name: "party celebrate" },
      { char: "😇", name: "angel innocent" },
      { char: "🙃", name: "upside down" },
    ],
  },
  {
    key: "gestures",
    label: "Gestures",
    items: [
      { char: "👍", name: "thumbs up like" },
      { char: "👎", name: "thumbs down" },
      { char: "👏", name: "clap applause" },
      { char: "🙏", name: "pray thanks please" },
      { char: "🤝", name: "handshake deal" },
      { char: "👋", name: "wave hello bye" },
      { char: "✌️", name: "peace victory" },
      { char: "🤞", name: "fingers crossed" },
      { char: "💪", name: "muscle strong" },
      { char: "👌", name: "ok" },
      { char: "🙌", name: "raised hands celebrate" },
      { char: "☝️", name: "point up" },
    ],
  },
  {
    key: "hearts",
    label: "Hearts",
    items: [
      { char: "❤️", name: "red heart love" },
      { char: "🧡", name: "orange heart" },
      { char: "💛", name: "yellow heart" },
      { char: "💚", name: "green heart" },
      { char: "💙", name: "blue heart" },
      { char: "💜", name: "purple heart" },
      { char: "🖤", name: "black heart" },
      { char: "💔", name: "broken heart" },
      { char: "💯", name: "hundred perfect" },
      { char: "✨", name: "sparkles" },
      { char: "🔥", name: "fire lit" },
      { char: "⭐", name: "star" },
    ],
  },
  {
    key: "objects",
    label: "Objects",
    items: [
      { char: "✅", name: "check done" },
      { char: "❌", name: "cross no" },
      { char: "❓", name: "question" },
      { char: "❗", name: "exclamation important" },
      { char: "📎", name: "paperclip attachment" },
      { char: "📷", name: "camera photo" },
      { char: "🎉", name: "party popper celebrate" },
      { char: "⏰", name: "alarm clock time" },
      { char: "📅", name: "calendar date" },
      { char: "💰", name: "money" },
      { char: "📦", name: "package box" },
      { char: "🚀", name: "rocket launch" },
    ],
  },
]

const ALL: Entry[] = CATEGORIES.flatMap((c) => c.items)

/** The composer's emoji button (MH-659). Inserts at the caret, does not send. */
export function EmojiPicker({
  onInsert,
  disabled,
}: {
  onInsert: (emoji: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState("")

  const results = useMemo(() => {
    const query = q.trim().toLowerCase()
    if (!query) return null
    return ALL.filter((e) => e.name.includes(query))
  }, [q])

  return (
    <Popover
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (!v) setQ("")
      }}
    >
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            disabled={disabled}
            data-action="emoji"
            aria-label={t("inbox.composer.emoji")}
          />
        }
      >
        <SmilePlusIcon />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-2" data-emoji-picker>
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("inbox.composer.emojiSearch")}
          dir="auto"
          className="mb-2"
          aria-label={t("inbox.composer.emojiSearch")}
          data-emoji-search
        />
        <div className="max-h-56 overflow-y-auto">
          {results ? (
            results.length === 0 ? (
              <p className="px-1 py-4 text-center text-xs text-grid-muted">
                {t("inbox.composer.emojiNone")}
              </p>
            ) : (
              <div className="grid grid-cols-8 gap-0.5" data-emoji-results>
                {results.map((e) => (
                  <EmojiButton key={e.char} entry={e} onPick={onInsert} setOpen={setOpen} />
                ))}
              </div>
            )
          ) : (
            CATEGORIES.map((cat) => (
              <div key={cat.key} className="mb-2" data-emoji-category={cat.key}>
                <p className="mb-1 px-1 text-xs font-medium text-grid-muted">{cat.label}</p>
                <div className="grid grid-cols-8 gap-0.5">
                  {cat.items.map((e) => (
                    <EmojiButton key={e.char} entry={e} onPick={onInsert} setOpen={setOpen} />
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function EmojiButton({
  entry,
  onPick,
  setOpen,
}: {
  entry: Entry
  onPick: (emoji: string) => void
  setOpen: (open: boolean) => void
}) {
  return (
    <button
      type="button"
      className="flex size-8 items-center justify-center rounded text-lg hover:bg-grid-elevated"
      title={entry.name}
      data-emoji={entry.char}
      onClick={() => {
        onPick(entry.char)
        setOpen(false)
      }}
    >
      {entry.char}
    </button>
  )
}
