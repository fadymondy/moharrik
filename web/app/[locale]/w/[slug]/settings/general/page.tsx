"use client"

import { useState } from "react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type DefaultCountryOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCurrentWorkspace } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

/*
Settings → General (MH-646): today just the workspace's default_country,
used to resolve a phone number that carries no country code of its own on
vCard import and Google Contacts sync (internal/vcard's ToE164). Its own page
rather than folded into an existing one because nothing here belonged to
email, delivery or any other existing settings screen.

DEFAULT_COUNTRIES mirrors internal/vcard/phone.go's dialCodes map exactly —
the backend rejects anything outside that set with a 422, so offering only
those here means the picker can never submit a code the server refuses.
*/
const DEFAULT_COUNTRIES = [
  "EG",
  "SA",
  "AE",
  "KW",
  "QA",
  "BH",
  "OM",
  "JO",
  "LB",
  "SY",
  "IQ",
  "YE",
  "PS",
  "LY",
  "TN",
  "DZ",
  "MA",
  "SD",
  "US",
  "CA",
  "GB",
  "IE",
  "FR",
  "DE",
  "IT",
  "ES",
  "PT",
  "NL",
  "BE",
  "CH",
  "AT",
  "SE",
  "NO",
  "DK",
  "FI",
  "PL",
  "GR",
  "TR",
  "RU",
  "UA",
  "IN",
  "PK",
  "BD",
  "CN",
  "JP",
  "KR",
  "SG",
  "MY",
  "ID",
  "PH",
  "VN",
  "TH",
  "AU",
  "NZ",
  "ZA",
  "NG",
  "KE",
  "GH",
  "ET",
  "BR",
  "MX",
  "AR",
  "CO",
  "CL",
] as const

export default function GeneralSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.general")} · ${ws.summary.name}`)
  const canWrite = ws.can("workspace:update")
  const current = useCurrentWorkspace(ws.id)
  const [pending, setPending] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const stored = current.data?.workspace.default_country ?? ""
  const value = pending ?? stored

  async function save(next: string) {
    setBusy(true)
    try {
      const res = await api<DefaultCountryOut>("/api/w/settings/default-country", {
        method: "PUT",
        json: { country: next },
        workspaceId: ws.id,
      })
      setPending(null)
      await current.mutate()
      toast.success(
        res.default_country
          ? t("generalSettings.saved", { country: res.default_country })
          : t("generalSettings.cleared"),
      )
    } catch (e) {
      setPending(null)
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const items = [
    { value: "", label: t("generalSettings.defaultCountryNone") },
    ...DEFAULT_COUNTRIES.map((c) => ({ value: c, label: t(`country.${c}`) })),
  ]

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.general")}
        description={t("generalSettings.hint")}
      />
      <div className="px-6 pb-10">
        {!current.data ? (
          <LoadingRows rows={2} />
        ) : !canWrite ? (
          <p className="py-6 text-sm text-grid-muted">{t("generalSettings.adminOnly")}</p>
        ) : (
          <FieldGroup className="max-w-md pt-2">
            <Field>
              <FieldLabel htmlFor="default-country">
                {t("generalSettings.defaultCountry")}
              </FieldLabel>
              <Select
                items={items}
                value={value}
                onValueChange={(v) => {
                  const next = String(v ?? "")
                  setPending(next)
                  void save(next)
                }}
              >
                <SelectTrigger
                  id="default-country"
                  className="w-full"
                  disabled={busy}
                  data-testid="default-country-trigger"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {items.map((i) => (
                    <SelectItem
                      key={i.value || "none"}
                      value={i.value}
                      data-country={i.value || "none"}
                    >
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t("generalSettings.defaultCountryHint")}</FieldDescription>
            </Field>
          </FieldGroup>
        )}
      </div>
    </>
  )
}
