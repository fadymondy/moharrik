"use client"

/*
The preset gallery (MH-478, redone for MH-527 as a macOS-Shortcuts-Gallery-
style store): a shelf of brand tiles, each with its own logo and hex tint
(components/automation/brand-icon.tsx, built on the simple-icons package).
Typing in the search box swaps the shelves for a flat, brand-grouped list of
matching presets; clearing it goes back to the shelves. A brand tile opens
its own page (./[brand]) with that brand's presets as cards; a card opens
the detail sheet with Add / Add & run.

The data model, the plain en/ar names (MH-518) and the Add flow (including
MH-517's auto-allow) are exactly what they were — this redoes the look, not
the behaviour. Its own route, so the workflows list can still link
"New workflow → from a preset" straight here.
*/

import { useMemo, useState } from "react"
import { SearchIcon } from "lucide-react"

import { BrandIcon } from "@/components/automation/brand-icon"
import { BrandTile } from "@/components/automation/brand-tile"
import { PresetCard, plainOf } from "@/components/automation/preset-card"
import { PresetDetailSheet } from "@/components/automation/preset-detail-sheet"
import { PresetProfilePanel } from "@/components/automation/preset-profile-panel"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Input } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { AutomationPreset } from "@/lib/api"
import { BRANDS, brandForHosts } from "@/lib/brands"
import { SHELVES } from "@/lib/brand-shelves"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import { useAutomationProfile, useBrowsers, usePresets } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function PresetsGalleryPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("presets.title")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")
  const canManage = ws.can("automation:manage")
  const presets = usePresets(ws.id)
  const browsers = useBrowsers(ws.id)
  const profile = useAutomationProfile(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") void browsers.mutate()
  })

  const [q, setQ] = useState("")
  const [open, setOpen] = useState<AutomationPreset | null>(null)
  const list = presets.data?.presets ?? []
  const bySlug = useMemo(() => new Map(list.map((p) => [p.slug, p])), [list])

  // Every brand this deployment's presets actually use, with a real logo when
  // simple-icons has one — counted so an empty shelf never shows.
  const countByBrand = useMemo(() => {
    const m = new Map<string, number>()
    for (const p of list) {
      const b = brandForHosts(p.hosts).key
      m.set(b, (m.get(b) ?? 0) + 1)
    }
    return m
  }, [list])

  const needle = q.trim().toLowerCase()
  const searchResults = useMemo(() => {
    if (!needle) return null
    const hit = (p: AutomationPreset) => {
      const plain = plainOf(p, locale)
      const brand = brandForHosts(p.hosts)
      return `${plain.name} ${plain.summary} ${p.name} ${p.slug} ${p.description} ${p.hosts.join(" ")} ${brand.name}`
        .toLowerCase()
        .includes(needle)
    }
    const byBrand = new Map<string, AutomationPreset[]>()
    for (const p of list)
      if (hit(p))
        byBrand.set(brandForHosts(p.hosts).key, [
          ...(byBrand.get(brandForHosts(p.hosts).key) ?? []),
          p,
        ])
    return [...byBrand.entries()].sort(
      (a, b) => (countByBrand.get(b[0]) ?? 0) - (countByBrand.get(a[0]) ?? 0),
    )
  }, [needle, list, locale, countByBrand])
  const searchCount = searchResults?.reduce((n, [, ps]) => n + ps.length, 0) ?? 0

  return (
    <>
      <SectionHeader
        title={t("presets.title")}
        description={t("presets.hint")}
      />

      <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-6 py-4">
        <div className="relative min-w-0 flex-1 sm:max-w-sm">
          <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("presets.search")}
            aria-label={t("presets.search")}
            className="ps-8"
            data-preset-search
          />
        </div>
        {presets.data && needle ? (
          <p className="text-sm text-muted-foreground" data-preset-count={searchCount}>
            {t("presets.count", { n: formatNumber(searchCount), total: formatNumber(list.length) })}
          </p>
        ) : null}
      </div>

      {presets.error ? (
        <ErrorState error={presets.error} />
      ) : presets.isLoading ? (
        <LoadingRows rows={6} layout="cards" />
      ) : needle ? (
        searchCount === 0 ? (
          <EmptyState title={t("presets.noMatch", { q })} />
        ) : (
          searchResults!.map(([brandKey, ps]) => {
            const brand = BRANDS[brandKey]
            return (
              <section key={brandKey} className="mt-8" data-preset-group={brandKey}>
                <h2 className="flex items-center gap-2 px-6 pb-2 text-base font-medium">
                  <BrandIcon brand={brand} size={24} />
                  {brand.name}
                  <span className="text-sm font-normal text-muted-foreground tabular-nums">
                    {formatNumber(ps.length)}
                  </span>
                </h2>
                <ul className="grid gap-3 px-6 sm:grid-cols-2 lg:grid-cols-3">
                  {ps.map((p) => (
                    <PresetCard
                      key={p.slug}
                      preset={p}
                      brand={brand}
                      twin={bySlug.get(p.dry_run ?? p.dry_run_of ?? "")}
                      onOpen={() => setOpen(p)}
                    />
                  ))}
                </ul>
              </section>
            )
          })
        )
      ) : (
        SHELVES.map((shelf) => {
          const brands = shelf.brands.filter((b) => (countByBrand.get(b) ?? 0) > 0)
          if (brands.length === 0) return null
          return (
            <section key={shelf.labelKey} className="mt-8" data-shelf={shelf.labelKey}>
              <h2 className="px-6 pb-2 text-base font-medium">{t(shelf.labelKey)}</h2>
              <div className="grid grid-cols-3 gap-2 px-4 sm:grid-cols-5 md:grid-cols-6 lg:grid-cols-8">
                {brands.map((b) => (
                  <BrandTile
                    key={b}
                    brand={BRANDS[b]}
                    count={countByBrand.get(b) ?? 0}
                    href={`/${locale}/w/${ws.slug}/automation/presets/${b}`}
                  />
                ))}
              </div>
            </section>
          )
        })
      )}

      <PresetProfilePanel
        canManage={canManage}
        data={profile.data}
        onSaved={() => profile.mutate()}
      />
      <div className="pb-8" />

      {open ? (
        <PresetDetailSheet
          preset={open}
          brand={brandForHosts(open.hosts)}
          browsers={browsers.data?.browsers ?? []}
          canRun={canRun}
          canManage={canManage}
          profile={profile.data?.profile ?? {}}
          onClose={() => setOpen(null)}
          onChanged={() => {
            void presets.mutate()
            void browsers.mutate()
          }}
        />
      ) : null}
    </>
  )
}
