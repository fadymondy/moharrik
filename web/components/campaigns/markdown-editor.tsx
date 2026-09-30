"use client"

import { useRef, type KeyboardEvent } from "react"
import { BoldIcon, Heading2Icon, ItalicIcon, LinkIcon, ListIcon } from "lucide-react"

import { Button, ButtonGroup, Textarea } from "@fadymondy/nasaq/web"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/*
The campaign body editor (MH-407). Composed from registry pieces — shadcn's
ButtonGroup + Button, laid out like @shadcnspace/button-group-10's editor
toolbar — over the plain Textarea, because no registry item edits MARKDOWN:
the rich-text ones drive contentEditable, and a campaign body is stored and
sent as markdown (internal/marketing/mail.go markdownToHTML).

The toolbar offers exactly the subset that renderer supports — a heading,
bold, italic, a link and a list — so nothing it inserts arrives in an inbox as
literal punctuation.
*/

type Action = "heading" | "bold" | "italic" | "link" | "list"

/** Applies one toolbar action to the selection; returns the new text and selection. */
function apply(
  action: Action,
  text: string,
  start: number,
  end: number,
): { text: string; start: number; end: number } {
  const sel = text.slice(start, end)
  const wrap = (open: string, close: string, placeholder: string) => {
    const inner = sel || placeholder
    return {
      text: text.slice(0, start) + open + inner + close + text.slice(end),
      start: start + open.length,
      end: start + open.length + inner.length,
    }
  }
  // Prefix every line the selection touches.
  const prefixLines = (prefix: string) => {
    const lineStart = text.lastIndexOf("\n", start - 1) + 1
    const block = text.slice(lineStart, end)
    const next = block
      .split("\n")
      .map((l) => (l.startsWith(prefix) ? l : prefix + l.replace(/^#{1,3} |^[-*] /, "")))
      .join("\n")
    return {
      text: text.slice(0, lineStart) + next + text.slice(end),
      start: lineStart,
      end: lineStart + next.length,
    }
  }
  switch (action) {
    case "bold":
      return wrap("**", "**", "bold")
    case "italic":
      return wrap("*", "*", "italic")
    case "link": {
      const label = sel || "link"
      const url = "https://"
      const out = text.slice(0, start) + `[${label}](${url})` + text.slice(end)
      // Select the URL: it is the part that has to be typed next.
      const urlStart = start + label.length + 3
      return { text: out, start: urlStart, end: urlStart + url.length }
    }
    case "heading":
      return prefixLines("## ")
    case "list":
      return prefixLines("- ")
  }
}

export function MarkdownEditor({
  id,
  value,
  onValueChange,
  dir,
  rows = 12,
  toolbar = true,
  className,
}: {
  id?: string
  value: string
  onValueChange: (v: string) => void
  dir?: "ltr" | "rtl"
  rows?: number
  /** Off for plain-text bodies (WhatsApp), where markdown would arrive as punctuation. */
  toolbar?: boolean
  className?: string
}) {
  const { t } = useTranslations()
  const ref = useRef<HTMLTextAreaElement>(null)

  function run(action: Action) {
    const el = ref.current
    if (!el) return
    const next = apply(
      action,
      value,
      el.selectionStart ?? value.length,
      el.selectionEnd ?? value.length,
    )
    onValueChange(next.text)
    requestAnimationFrame(() => {
      el.focus()
      el.setSelectionRange(next.start, next.end)
    })
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (!toolbar || !(e.ctrlKey || e.metaKey)) return
    const key = e.key.toLowerCase()
    const action: Action | null =
      key === "b" ? "bold" : key === "i" ? "italic" : key === "k" ? "link" : null
    if (action) {
      e.preventDefault()
      run(action)
    }
  }

  const tools: { action: Action; label: string; Icon: typeof BoldIcon }[] = [
    { action: "heading", label: t("editor.heading"), Icon: Heading2Icon },
    { action: "bold", label: t("editor.bold"), Icon: BoldIcon },
    { action: "italic", label: t("editor.italic"), Icon: ItalicIcon },
    { action: "link", label: t("editor.link"), Icon: LinkIcon },
    { action: "list", label: t("editor.list"), Icon: ListIcon },
  ]

  return (
    <div
      className={cn("flex flex-col border border-nq-line bg-card", className)}
      data-markdown-editor
    >
      {toolbar ? (
        <div className="flex items-center gap-2 border-b border-nq-line px-2 py-1.5">
          <ButtonGroup aria-label={t("editor.toolbar")}>
            {tools.map(({ action, label, Icon }) => (
              <Button
                key={action}
                type="button"
                variant="secondary"
                size="icon-sm"
                aria-label={label}
                title={label}
                data-md-action={action}
                // Keep the textarea's selection: a click would otherwise blur it first.
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => run(action)}
              >
                <Icon />
              </Button>
            ))}
          </ButtonGroup>
          <span className="ms-auto hidden text-xs text-muted-foreground sm:inline">
            {t("editor.markdown")}
          </span>
        </div>
      ) : null}
      <Textarea
        ref={ref}
        id={id}
        rows={rows}
        value={value}
        dir={dir}
        onChange={(e) => onValueChange(e.target.value)}
        onKeyDown={onKeyDown}
        className="min-h-48 resize-y rounded-none border-0 bg-transparent font-mono text-sm shadow-none focus-visible:ring-0 dark:bg-transparent"
      />
    </div>
  )
}
