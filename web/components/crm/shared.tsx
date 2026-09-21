"use client"

import { CalendarIcon, CheckSquareIcon, PhoneIcon, StickyNoteIcon } from "lucide-react"
import { useMemo } from "react"

import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { COMPANY_SIZES, type ActivityKind, type StageKind } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag, isLocale } from "@/lib/i18n-locale"
import { useMembers } from "@/lib/queries"

// ── money ────────────────────────────────────────────────────────────────
// Deals store minor units; how many depends on the currency (JPY 0, KWD 3).

export function currencyDigits(currency: string): number {
  try {
    return new Intl.NumberFormat("en", { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

export function formatMoney(minor: number, currency: string, locale: string): string {
  const digits = currencyDigits(currency)
  const major = minor / 10 ** digits
  try {
    return new Intl.NumberFormat(intlTag(isLocale(locale) ? locale : "en"), {
      style: "currency",
      currency,
      maximumFractionDigits: digits,
    }).format(major)
  } catch {
    return `${major} ${currency}`
  }
}

/** "1,250.5" → minor units, or null when it is not a non-negative number. */
export function toMinor(major: string, currency: string): number | null {
  const s = major.replace(/[,\s]/g, "")
  if (s === "") return 0
  const n = Number(s)
  if (!Number.isFinite(n) || n < 0) return null
  return Math.round(n * 10 ** currencyDigits(currency))
}

export function toMajor(minor: number, currency: string): string {
  const digits = currencyDigits(currency)
  return (minor / 10 ** digits).toFixed(digits).replace(/\.0+$/, "")
}

// ── pickers ──────────────────────────────────────────────────────────────

const NONE = "__none"

/** A Select over ids with an optional "none" row (Base UI Select has no empty value). */
export function PickSelect({
  id,
  value,
  onChange,
  options,
  noneLabel,
  disabled,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
  noneLabel?: string
  disabled?: boolean
}) {
  const items = noneLabel ? [{ value: NONE, label: noneLabel }, ...options] : options
  return (
    <Select
      items={items}
      value={value || (noneLabel ? NONE : (options[0]?.value ?? ""))}
      onValueChange={(v) => onChange(v == null || v === NONE ? "" : String(v))}
      disabled={disabled}
    >
      <SelectTrigger id={id} className="w-full min-w-0">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((i) => (
          <SelectItem key={i.value} value={i.value}>
            {i.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/** user_id → email for owners and authors (members:read is a member's right, like contacts:read). */
export function useMemberNames(workspaceId: string) {
  const members = useMembers(workspaceId)
  return useMemo(() => {
    const list = (members.data?.members ?? []).filter((m) => m.status === "active")
    const byId = new Map(list.map((m) => [m.user_id, m.email]))
    return {
      options: list.map((m) => ({ value: m.user_id, label: m.email })),
      name: (id: string | null | undefined) => (id ? (byId.get(id) ?? "—") : "—"),
    }
  }, [members.data])
}

// ── badges ───────────────────────────────────────────────────────────────

export function ActivityIcon({ kind }: { kind: ActivityKind }) {
  switch (kind) {
    case "call":
      return <PhoneIcon />
    case "meeting":
      return <CalendarIcon />
    case "task":
      return <CheckSquareIcon />
    default:
      return <StickyNoteIcon />
  }
}

export const stageTone: Record<StageKind, string> = {
  open: "bg-grid-brand",
  won: "bg-grid-ok",
  lost: "bg-grid-danger",
}

// ── forms ────────────────────────────────────────────────────────────────

/** Name, domain, industry, size — the fields common to adding and editing a company. */
export function CompanyEditFields({
  name,
  setName,
  domain,
  setDomain,
  industry,
  setIndustry,
  size,
  setSize,
  idPrefix = "company",
}: {
  name: string
  setName: (v: string) => void
  domain: string
  setDomain: (v: string) => void
  industry: string
  setIndustry: (v: string) => void
  size: string
  setSize: (v: string) => void
  idPrefix?: string
}) {
  const { t } = useTranslations()
  const sizeOpts = COMPANY_SIZES.map((s) => ({ value: s, label: s }))
  const id = (f: string) => `${idPrefix}-${f}`
  return (
    <FieldGroup className="grid gap-4 sm:grid-cols-2">
      <Field>
        <FieldLabel htmlFor={id("name")}>{t("companies.name")}</FieldLabel>
        <Input id={id("name")} value={name} onChange={(e) => setName(e.target.value)} maxLength={200} />
      </Field>
      <Field>
        <FieldLabel htmlFor={id("domain")}>{t("companies.domain")}</FieldLabel>
        <Input id={id("domain")} dir="ltr" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="company.com" />
      </Field>
      <Field>
        <FieldLabel htmlFor={id("industry")}>{t("companies.industry")}</FieldLabel>
        <Input id={id("industry")} value={industry} onChange={(e) => setIndustry(e.target.value)} maxLength={120} />
      </Field>
      <Field>
        <FieldLabel htmlFor={id("size")}>{t("companies.size")}</FieldLabel>
        <PickSelect id={id("size")} value={size} onChange={setSize} options={sizeOpts} noneLabel={t("companies.sizeUnknown")} />
      </Field>
    </FieldGroup>
  )
}
