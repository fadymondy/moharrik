"use client"

/*
One preset, as a card (MH-527) — the gallery's unit, on a brand page or in a
search result shelf. Colour comes from the brand's own hex (BrandIcon), never
guessed. Clicking the card opens the detail sheet; a preset already added
opens straight to its workflow instead.
*/

import Link from "next/link"
import { CheckIcon, ExternalLinkIcon, ShieldCheckIcon } from "lucide-react"

import { BrandIcon } from "@/components/automation/brand-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import type { Brand } from "@/lib/brands"
import type { AutomationPreset } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkspace } from "@/components/workspace/workspace-context"

/** A preset's plain words in the page's language (MH-518), English when missing. */
export function plainOf(p: AutomationPreset, locale: string) {
  return p.plain?.[locale] ?? p.plain?.en ?? { name: p.name, summary: "" }
}

export function PresetCard({
  preset: p,
  brand,
  twin,
  onOpen,
}: {
  preset: AutomationPreset
  brand: Brand
  twin?: AutomationPreset
  onOpen: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const plain = plainOf(p, locale)
  const tint = brand.icon ? `#${brand.icon.hex}` : undefined

  return (
    <li
      className="relative flex flex-col gap-2 overflow-hidden rounded-lg border border-line p-4"
      style={{ borderInlineStartWidth: 3, borderInlineStartColor: tint ?? "var(--grid-muted)" }}
      id={`preset-${p.slug}`}
      data-preset={p.slug}
      data-writes={p.writes ? "true" : undefined}
    >
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 flex-col gap-2 text-start"
        data-preset-open={p.slug}
      >
        <div className="flex items-start gap-2.5">
          <BrandIcon brand={brand} size={32} />
          <div className="min-w-0 flex-1">
            <p className="flex flex-wrap items-center gap-1.5 text-sm font-medium">
              <span data-preset-name>{plain.name}</span>
            </p>
            {plain.summary ? (
              <p className="line-clamp-2 text-xs text-grid-muted" data-preset-summary>
                {plain.summary}
              </p>
            ) : null}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {p.writes ? (
            <Badge variant="secondary" data-preset-publishes>
              {t("presets.publishes")}
            </Badge>
          ) : null}
          {p.dry_run_of ? <Badge variant="outline">{t("presets.rehearsal")}</Badge> : null}
          {p.workflow_id ? (
            <Badge variant="outline" data-preset-added>
              <CheckIcon />
              {t("presets.added")}
            </Badge>
          ) : null}
        </div>
        <p
          className="flex items-start gap-1 text-xs text-grid-muted"
          data-preset-hosts={p.hosts.join(",")}
        >
          <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
          <span className="min-w-0 break-words">
            {p.hosts.length
              ? t("presets.goesTo", { hosts: p.hosts.join(", ") })
              : t("presets.anySite")}
          </span>
        </p>
      </button>
      {twin && p.dry_run ? (
        <p className="text-xs text-grid-muted">
          {t("presets.rehearseFirst")}{" "}
          <a className="underline" href={`#preset-${twin.slug}`}>
            {plainOf(twin, locale).name}
          </a>
        </p>
      ) : null}
      {p.workflow_id ? (
        <Button
          variant="ghost"
          size="sm"
          className="self-start"
          nativeButton={false}
          render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows/${p.workflow_id}`} />}
          data-open-added
        >
          <ExternalLinkIcon />
          {t("presets.open")}
        </Button>
      ) : null}
    </li>
  )
}
