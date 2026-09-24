"use client"

/*
A run's own lines, shown before its steps (MH-517): what was done before the
first step, e.g. the sites it added to its browser. The server writes them in
English, for the run record and MCP; the one it knows how to write is read
back here and said in the page's language.
*/

import { GlobeIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { useTranslations } from "@/lib/i18n"

const SITES_ADDED = /^Added (.+) to (.+)'s allowed sites$/

export function RunNotes({ notes, className }: { notes?: string[]; className?: string }) {
  const { t } = useTranslations()
  if (!notes?.length) return null
  return (
    <ul className={className} data-run-notes>
      {notes.map((n, i) => {
        const m = SITES_ADDED.exec(n)
        return (
          <li key={i} className="flex items-start gap-1.5 text-xs text-grid-muted" data-run-note={m ? "sites-added" : "text"}>
            <GlobeIcon className="mt-0.5 size-3 shrink-0" aria-hidden />
            <span className="min-w-0 break-words">
              {m ? t("automation.note.sitesAdded", { hosts: m[1], browser: m[2] }) : <Bdi>{n}</Bdi>}
            </span>
          </li>
        )
      })}
    </ul>
  )
}
