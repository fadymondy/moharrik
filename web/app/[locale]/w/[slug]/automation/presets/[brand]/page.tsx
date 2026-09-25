"use client"

/*
A brand's page (MH-527): every preset that visits this brand's sites, as a
shelf of cards. Reached by tapping a tile on the gallery home, or a deep
link — an unknown or now-empty brand key sends the reader back to the
gallery rather than showing a blank shelf.
*/

import Link from "next/link"
import { useParams } from "next/navigation"
import { useMemo, useState } from "react"
import { ArrowLeftIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { BrandIcon } from "@/components/automation/brand-icon"
import { PresetCard } from "@/components/automation/preset-card"
import { PresetDetailSheet } from "@/components/automation/preset-detail-sheet"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { BRANDS, brandForHosts } from "@/lib/brands"
import { useTranslations } from "@/lib/i18n"
import { useAutomationProfile, useBrowsers, usePresets } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import type { AutomationPreset } from "@/lib/api"

export default function PresetBrandPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const { brand: brandKey } = useParams<{ brand: string }>()
  const brand = BRANDS[brandKey]
  useDocumentTitle(
    `${brand?.name ?? t("presets.title")} · ${t("presets.title")} · ${ws.summary.name}`,
  )
  const canRun = ws.can("automation:run")
  const canManage = ws.can("automation:manage")
  const presets = usePresets(ws.id)
  const browsers = useBrowsers(ws.id)
  const profile = useAutomationProfile(ws.id)
  const [open, setOpen] = useState<AutomationPreset | null>(null)

  const list = useMemo(
    () => (presets.data?.presets ?? []).filter((p) => brandForHosts(p.hosts).key === brandKey),
    [presets.data, brandKey],
  )
  const bySlug = useMemo(
    () => new Map((presets.data?.presets ?? []).map((p) => [p.slug, p])),
    [presets.data],
  )
  const galleryHref = `/${locale}/w/${ws.slug}/automation/presets`

  if (!brand) {
    return (
      <>
        <SectionHeader
          micro={<Bdi>{ws.summary.name}</Bdi>}
          title={t("presets.title")}
          action={
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={galleryHref} />}
            >
              <ArrowLeftIcon className="rtl:rotate-180" />
              {t("presets.backToGallery")}
            </Button>
          }
        />
        <EmptyState title={t("presets.unknownBrand")} />
      </>
    )
  }

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={
          <span className="flex items-center gap-3">
            <BrandIcon brand={brand} size={36} />
            {brand.name}
          </span>
        }
        description={
          presets.data ? t("presets.brandCount", { n: formatNumber(list.length) }) : undefined
        }
        action={
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href={galleryHref} />}
            data-back-to-gallery
          >
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("presets.backToGallery")}
          </Button>
        }
      />
      {presets.error ? (
        <ErrorState error={presets.error} />
      ) : presets.isLoading ? (
        <LoadingRows rows={4} />
      ) : list.length === 0 ? (
        <EmptyState title={t("presets.noMatch", { q: brand.name })} />
      ) : (
        <ul
          className="grid gap-3 px-6 pb-8 sm:grid-cols-2 lg:grid-cols-3"
          data-preset-shelf={brandKey}
        >
          {list.map((p) => (
            <PresetCard
              key={p.slug}
              preset={p}
              brand={brand}
              twin={bySlug.get(p.dry_run ?? p.dry_run_of ?? "")}
              onOpen={() => setOpen(p)}
            />
          ))}
        </ul>
      )}
      {open ? (
        <PresetDetailSheet
          preset={open}
          brand={brand}
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
