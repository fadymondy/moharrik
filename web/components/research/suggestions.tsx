"use client"

import { useState, type FormEvent } from "react"
import { CheckIcon, SparklesIcon, XIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { EvidenceItem, SignalBadge } from "@/components/research/evidence"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type CompanySuggestion } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompanySuggestions } from "@/lib/queries"

/**
 * Companies research found (MH-388), on the companies page. Nothing here is in
 * the CRM until a person accepts it; accepting adds it (or links the one that
 * is already there) with its evidence as a timeline note.
 */
const SHOWN = 3

export function SuggestionsPanel({ onAccepted }: { onAccepted: () => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canRead = ws.can("research:read")
  const list = useCompanySuggestions(ws.id, "suggested", canRead)
  const [all, setAll] = useState(false)
  const loaded = list.data?.suggestions ?? []
  // The strongest few first: the CRM table below is what this page is for.
  const items = all ? loaded : loaded.slice(0, SHOWN)
  const total = list.data?.total ?? 0
  if (!canRead || !list.data || total === 0) return null

  return (
    <section id="suggestions" className="scroll-mt-4 pb-6" data-suggestions={total}>
      <h2 className="flex items-center gap-2 px-6 text-base font-medium">
        <SparklesIcon className="size-4 text-nq-brand" aria-hidden />
        {t("research.suggestions")}
        <span className="text-sm font-normal text-muted-foreground tabular-nums">
          {formatNumber(total)}
        </span>
      </h2>
      <p className="px-6 pt-1 pb-3 text-sm text-muted-foreground">{t("research.suggestionsHint")}</p>
      <ul className="divide-y divide-nq-line border-y border-nq-line">
        {items.map((s) => (
          <SuggestionRow
            key={s.id}
            s={s}
            onDone={() => {
              void list.mutate()
              onAccepted()
            }}
          />
        ))}
      </ul>
      {total > items.length ? (
        <div className="flex flex-wrap items-center gap-3 px-6 pt-3">
          {!all && loaded.length > items.length ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setAll(true)}
              data-action="all-suggestions"
            >
              {t("research.suggestionsAll", { n: formatNumber(loaded.length) })}
            </Button>
          ) : null}
          {total > loaded.length ? (
            <p className="text-xs text-muted-foreground">
              {t("research.suggestionMore", { n: formatNumber(total - loaded.length) })}
            </p>
          ) : null}
        </div>
      ) : null}
    </section>
  )
}

function SuggestionRow({ s, onDone }: { s: CompanySuggestion; onDone: () => void }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const top = s.evidence[0]

  async function dismiss() {
    setBusy(true)
    try {
      await api(`/api/w/company-suggestions/${encodeURIComponent(s.id)}/dismiss`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("research.dismissed"))
      onDone()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="px-6 py-4" data-suggestion={s.name}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <Bdi>{s.name}</Bdi>
            {s.domain ? (
              <span className="text-xs font-normal text-muted-foreground" dir="ltr">
                {s.domain}
              </span>
            ) : null}
            {s.signal_types.map((sig) => (
              <SignalBadge key={sig} signal={sig} />
            ))}
          </p>
          <p className="text-xs text-muted-foreground">
            {t("research.suggestionEvidence", { n: formatNumber(s.evidence_count) })}
          </p>
        </div>
        {canWrite ? (
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              onClick={() => setOpen(true)}
              disabled={busy}
              data-action="accept-suggestion"
            >
              <CheckIcon />
              {t("research.accept")}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={dismiss}
              disabled={busy}
              data-action="dismiss-suggestion"
            >
              <XIcon />
              {t("research.dismiss")}
            </Button>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t("research.noCompanyWrite")}</p>
        )}
      </div>
      {top ? (
        <ul className="-mx-6 mt-2">
          <EvidenceItem e={top} showRun />
        </ul>
      ) : null}
      {open ? (
        <AcceptDialog
          s={s}
          onClose={() => setOpen(false)}
          onAccepted={(id, created) => {
            setOpen(false)
            toast.success(
              created
                ? t("research.accepted", { name: isolate(s.name) })
                : t("research.linkedExisting", { name: isolate(s.name) }),
              {
                action: {
                  label: t("research.openCompany"),
                  onClick: () =>
                    window.location.assign(
                      `/${locale}/w/${ws.slug}/companies/${encodeURIComponent(id)}`,
                    ),
                },
              },
            )
            onDone()
          }}
        />
      ) : null}
    </li>
  )
}

function AcceptDialog({
  s,
  onClose,
  onAccepted,
}: {
  s: CompanySuggestion
  onClose: () => void
  onAccepted: (companyId: string, created: boolean) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(s.name)
  const [domain, setDomain] = useState(s.domain)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ company_id: string; company_created: boolean }>(
        `/api/w/company-suggestions/${encodeURIComponent(s.id)}/accept`,
        {
          json: { name: name.trim(), domain: domain.trim() },
          workspaceId: ws.id,
        },
      )
      onAccepted(res.company_id, res.company_created)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent data-dialog="accept-suggestion">
        <DialogHeader>
          <DialogTitle>{t("research.acceptTitle", { name: isolate(s.name) })}</DialogTitle>
          <DialogDescription>{t("research.acceptBody")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="acc-name">{t("companies.name")}</FieldLabel>
              <Input
                id="acc-name"
                dir="auto"
                maxLength={120}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="acc-domain">
                {t("companies.domain")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="acc-domain"
                dir="ltr"
                value={domain}
                placeholder="company.com"
                onChange={(e) => setDomain(e.target.value)}
              />
            </Field>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
          <DialogFooter className="mt-6">
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim()} data-action="confirm-accept">
              <CheckIcon />
              {busy ? t("common.working") : t("research.accept")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
