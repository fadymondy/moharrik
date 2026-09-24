"use client"

/*
The node picker (MH-480): n8n's "What happens next?" — every step the browser
can take, grouped and searchable by name, description or command. Arrow keys
move through the matches and Enter adds the highlighted one, so a workflow can
be built without leaving the keyboard.
*/

import { useEffect, useMemo, useRef, useState } from "react"
import { SearchIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

import { CATEGORIES, SPECS, type NodeKind } from "./model"

type Kind = Exclude<NodeKind, "unknown">

export function NodePicker({
  position,
  onPick,
  onClose,
}: {
  position: number
  onPick: (kind: Kind) => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  const [q, setQ] = useState("")
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => input.current?.focus(), [])

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return CATEGORIES.map((c) => ({
      id: c.id,
      kinds: c.kinds.filter((k) => {
        if (!needle) return true
        const hay = [
          t(`canvas.kind.${k}`),
          t(`canvas.desc.${k}`),
          SPECS[k].command,
          k.replace("_", " "),
        ]
          .join(" ")
          .toLowerCase()
        return hay.includes(needle)
      }),
    })).filter((g) => g.kinds.length > 0)
  }, [q, t])
  const flat = groups.flatMap((g) => g.kinds)
  const current = flat[Math.min(active, flat.length - 1)]

  return (
    <div
      className="flex h-full flex-col"
      data-node-picker
      onKeyDown={(e) => e.key === "Escape" && onClose()}
    >
      <div className="flex items-start justify-between gap-2 border-b border-line px-4 py-3">
        <div>
          <h2 className="text-base font-medium">{t("canvas.picker.title")}</h2>
          <p className="text-xs text-grid-muted">{t("canvas.picker.hint", { n: position + 1 })}</p>
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label={t("canvas.close")}>
          <XIcon />
        </Button>
      </div>
      <div className="relative border-b border-line px-4 py-3">
        <SearchIcon className="pointer-events-none absolute start-6 top-1/2 size-4 -translate-y-1/2 text-grid-muted" />
        <Input
          ref={input}
          value={q}
          onChange={(e) => {
            setQ(e.target.value)
            setActive(0)
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault()
              setActive((a) => Math.min(a + 1, flat.length - 1))
            } else if (e.key === "ArrowUp") {
              e.preventDefault()
              setActive((a) => Math.max(a - 1, 0))
            } else if (e.key === "Enter" && current) {
              e.preventDefault()
              onPick(current)
            }
          }}
          className="ps-8"
          placeholder={t("canvas.picker.search")}
          aria-label={t("canvas.picker.search")}
          data-picker-search
        />
      </div>
      <div
        className="min-h-0 flex-1 overflow-y-auto py-2"
        role="listbox"
        aria-label={t("canvas.picker.title")}
      >
        {groups.length === 0 ? (
          <p className="px-4 py-6 text-center text-sm text-grid-muted">
            {t("canvas.picker.none", { q })}
          </p>
        ) : null}
        {groups.map((g) => (
          <div key={g.id} className="pb-2">
            <p className="grid-micro px-4 pt-2 pb-1">{t(`canvas.cat.${g.id}`)}</p>
            <ul>
              {g.kinds.map((k) => {
                const Icon = SPECS[k].icon
                const on = k === current
                return (
                  <li key={k}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={on}
                      onMouseEnter={() => setActive(flat.indexOf(k))}
                      onClick={() => onPick(k)}
                      className={cn(
                        "flex w-full items-start gap-3 px-4 py-2.5 text-start transition-colors",
                        on ? "bg-grid-soft" : "hover:bg-grid-soft",
                      )}
                      data-pick-kind={k}
                    >
                      <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line bg-grid-card">
                        <Icon className="size-4.5 text-grid-fg" />
                      </span>
                      <span className="min-w-0">
                        <span className="block text-sm font-medium">{t(`canvas.kind.${k}`)}</span>
                        <span className="block text-xs text-grid-muted">
                          {t(`canvas.desc.${k}`)}
                        </span>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          </div>
        ))}
      </div>
    </div>
  )
}
