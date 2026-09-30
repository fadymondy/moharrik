"use client"

import Link from "next/link"

import { BrandIcon } from "@/components/automation/brand-icon"
import type { Brand } from "@/lib/brands"
import { useTranslations } from "@/lib/i18n"

/** One brand on the gallery home (MH-527) — a macOS-Shortcuts-style app tile. */
export function BrandTile({ brand, count, href }: { brand: Brand; count: number; href: string }) {
  const { formatNumber } = useTranslations()
  return (
    <Link
      href={href}
      className="group flex flex-col items-center gap-2 rounded-lg border border-transparent p-3 text-center transition-colors hover:border-nq-line hover:bg-nq-surface-soft"
      data-brand-tile={brand.key}
    >
      <BrandIcon brand={brand} size={56} className="transition-transform group-hover:scale-105" />
      <span className="line-clamp-1 text-sm font-medium">{brand.name}</span>
      <span className="text-xs text-muted-foreground tabular-nums">{formatNumber(count)}</span>
    </Link>
  )
}
