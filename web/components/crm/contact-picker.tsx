"use client"

import { useEffect, useMemo, useState } from "react"

import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useContact, useContactsPage } from "@/lib/queries"

type Option = { value: string; label: string }

const PICK_LIMIT = 20

const label = (c: { name: string; email: string }) => (c.name ? `${c.name} · ${c.email}` : c.email)

/** Settles a value that changes on every keystroke into one request per pause. */
export function useDebounced<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

/**
 * Pick one contact by searching for it (MH-408).
 *
 * This used to be a select over GET /api/w/contacts — every contact in one
 * dropdown, which is also where the silent 1,000-row cap bit: contact 1,001 could
 * not be put on a deal at all. Now it asks the server, and when more match than
 * it shows it says so, instead of pretending the first 20 are all there are.
 */
export function ContactPicker({
  id,
  value,
  selected,
  onChange,
}: {
  id: string
  value: string
  /** The contact already chosen, so its label shows before any search runs. */
  selected?: { id: string; name: string; email: string } | null
  onChange: (id: string) => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [query, setQuery] = useState("")
  const q = useDebounced(query)
  const res = useContactsPage(ws.id, { q, limit: PICK_LIMIT })

  const [chosen, setChosen] = useState<Option | null>(selected ? { value: selected.id, label: label(selected) } : null)
  // A preset id with no label (a deal started from a contact's page): look it up once.
  const preset = useContact(ws.id, value && !chosen ? value : undefined)
  useEffect(() => {
    if (!value) setChosen(null)
    else if (!chosen && preset.data) setChosen({ value, label: label(preset.data.contact) })
  }, [value, chosen, preset.data])

  const items = useMemo<Option[]>(() => {
    const found = (res.data?.contacts ?? []).map((c) => ({ value: c.id, label: label(c) }))
    // Keep the current choice selectable even when it is not in this page of results.
    if (chosen && !found.some((o) => o.value === chosen.value)) found.unshift(chosen)
    return found
  }, [res.data, chosen])

  const total = res.data?.total ?? 0
  const shown = res.data?.contacts?.length ?? 0

  return (
    <Combobox<Option>
      items={items}
      value={chosen}
      filter={null}
      itemToStringLabel={(o) => o.label}
      isItemEqualToValue={(a, b) => a.value === b.value}
      onValueChange={(o) => {
        setChosen(o)
        onChange(o?.value ?? "")
      }}
      onInputValueChange={(v, details) => {
        if (details.reason === "input-change" || details.reason === "input-clear") setQuery(v)
      }}
    >
      <ComboboxInput id={id} className="w-full" placeholder={t("contacts.pickPlaceholder")} showClear={!!chosen} />
      <ComboboxContent>
        <ComboboxEmpty>{res.isLoading ? t("common.working") : t("contacts.pickNone")}</ComboboxEmpty>
        <ComboboxList>
          {(o: Option) => (
            <ComboboxItem key={o.value} value={o}>
              <span className="truncate">{o.label}</span>
            </ComboboxItem>
          )}
        </ComboboxList>
        {total > shown ? (
          <p className="border-t border-line px-2.5 py-1.5 text-xs text-grid-muted" data-pick-more>
            {t("contacts.pickMore", { shown: formatNumber(shown), total: formatNumber(total) })}
          </p>
        ) : null}
      </ComboboxContent>
    </Combobox>
  )
}
