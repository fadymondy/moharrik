"use client"

import { useEffect, useMemo, useState } from "react"

import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@fadymondy/nasaq/web"
import { useDebounced } from "@/components/crm/contact-picker"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useCompaniesPage, useCompany } from "@/lib/queries"

type Option = { value: string; label: string }

const PICK_LIMIT = 20

/**
 * Pick one company by searching for it (MH-467).
 *
 * The deal and contact forms used to fill a select from GET /api/w/companies, which stopped at
 * 2,000: company 2,001 could not be put on a deal. Now it asks the server, and says so when more
 * match than it shows — the same shape as ContactPicker (MH-408).
 */
export function CompanyPicker({
  id,
  value,
  selected,
  onChange,
}: {
  id: string
  value: string
  /** The company already chosen, so its name shows before any search runs. */
  selected?: { id: string; name: string } | null
  onChange: (id: string) => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [query, setQuery] = useState("")
  const q = useDebounced(query)
  const res = useCompaniesPage(ws.id, { q, limit: PICK_LIMIT })
  const [chosen, setChosen] = useState<Option | null>(
    selected ? { value: selected.id, label: selected.name } : null,
  )
  // A preset id with no name (a deal started from a company's page): look it up once.
  const preset = useCompany(ws.id, value && !chosen ? value : undefined)
  useEffect(() => {
    if (value && !chosen && preset.data) setChosen({ value, label: preset.data.company.name })
  }, [value, chosen, preset.data])
  const current = value && chosen?.value === value ? chosen : null

  const items = useMemo<Option[]>(() => {
    const found = (res.data?.companies ?? []).map((c) => ({ value: c.id, label: c.name }))
    if (current && !found.some((o) => o.value === current.value)) found.unshift(current)
    return found
  }, [res.data, current])

  const total = res.data?.total ?? 0
  const shown = res.data?.companies?.length ?? 0

  return (
    <Combobox<Option>
      items={items}
      value={current}
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
      <ComboboxInput
        id={id}
        className="w-full"
        placeholder={t("companies.pickPlaceholder")}
        clearable={!!current}
      />
      <ComboboxContent>
        <ComboboxEmpty>
          {res.isLoading ? t("common.working") : t("companies.pickNone")}
        </ComboboxEmpty>
        <ComboboxList>
          {(o: Option) => (
            <ComboboxItem key={o.value} value={o}>
              <span className="truncate">{o.label}</span>
            </ComboboxItem>
          )}
        </ComboboxList>
        {total > shown ? (
          <p className="border-t border-nq-line px-2.5 py-1.5 text-xs text-muted-foreground" data-pick-more>
            {t("companies.pickMore", { shown: formatNumber(shown), total: formatNumber(total) })}
          </p>
        ) : null}
      </ComboboxContent>
    </Combobox>
  )
}
