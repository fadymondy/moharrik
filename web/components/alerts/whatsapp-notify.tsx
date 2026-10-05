"use client"

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, MessageCircleIcon, SendIcon, XCircleIcon } from "lucide-react"
import {
  Badge,
  Button,
  Checkbox,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  toast,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError, FormHint } from "@/components/form-layout"

import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NotifyKind, type WhatsAppNotify } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWhatsAppNotify } from "@/lib/queries"

/*
WhatsApp notifications for the signed-in member (MH-411), beside web push.

Their own number, verified with a code the workspace's number sends; then what
reaches them, quiet hours, a daily cap and how long events are gathered into
one message. Nothing here is anyone else's: the API only ever reads and writes
the caller's own row.
*/

const toClock = (m: number) =>
  `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`
const fromClock = (s: string) => {
  const [h, m] = s.split(":").map((x) => Number(x))
  return Number.isFinite(h) && Number.isFinite(m) ? Math.min(1439, h * 60 + m) : 0
}

export function WhatsAppNotifySection() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const q = useWhatsAppNotify(ws.id)
  const d = q.data

  return (
    <section aria-labelledby="whatsapp-notify" className="pb-10" data-section="whatsapp-notify">
      <div className="px-6 pb-3">
        <h2 id="whatsapp-notify" className="flex items-center gap-2 text-base font-medium">
          <MessageCircleIcon className="size-4 text-muted-foreground" />
          {t("waNotify.title")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("waNotify.hint")}</p>
      </div>
      <div className="border-y border-nq-line px-6 py-5">
        {q.error ? (
          <p className="text-sm text-nq-danger-text">
            {q.error instanceof ApiError ? q.error.message : t("common.networkError")}
          </p>
        ) : !d ? (
          <p className="text-sm text-muted-foreground">{t("common.working")}</p>
        ) : !d.available ? (
          <p className="text-sm text-muted-foreground" data-wa-notify="unavailable">
            {t("waNotify.unavailable")}
          </p>
        ) : !d.verified ? (
          d.workspace_linked ? (
            <Verify data={d} onChange={() => q.mutate()} />
          ) : (
            <p className="text-sm text-muted-foreground" data-wa-notify="not-linked">
              {t("waNotify.notLinked")}{" "}
              {ws.can("inbox:manage") ? (
                <Link
                  className="underline underline-offset-4"
                  href={`/${locale}/w/${ws.slug}/settings/integrations?type=whatsapp`}
                >
                  {t("contactWa.linkNumber")}
                </Link>
              ) : null}
            </p>
          )
        ) : (
          <Settings
            key={`${d.phone}:${d.verified_at}`}
            data={d}
            onChange={(v) => q.mutate(v, { revalidate: false })}
            refresh={() => q.mutate()}
          />
        )}
      </div>
    </section>
  )
}

function Verify({ data, onChange }: { data: WhatsAppNotify; onChange: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [phone, setPhone] = useState(data.phone ? `+${data.phone}` : "")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function sendCode(e?: FormEvent) {
    e?.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/notify/whatsapp/verify", {
        json: { phone: phone.trim(), locale },
        workspaceId: ws.id,
      })
      toast.success(t("waNotify.codeSent"))
      onChange()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function confirm(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/notify/whatsapp/confirm", {
        json: { code: code.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("waNotify.verified"))
      setCode("")
      onChange()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="flex max-w-xl flex-col gap-5"
      data-wa-notify={data.code_pending ? "code-pending" : "unverified"}
    >
      <form onSubmit={sendCode} noValidate>
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="wa-notify-phone">{t("waNotify.phone")}</FieldLabel>
            <Input
              id="wa-notify-phone"
              dir="ltr"
              inputMode="tel"
              autoComplete="tel"
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
              placeholder="+20 100 000 0000"
            />
          </Field>
          <Button
            type="submit"
            variant={data.code_pending ? "secondary" : "primary"}
            disabled={busy || phone.replace(/\D/g, "").length < 8}
          >
            <SendIcon />
            {data.code_pending ? t("waNotify.resend") : t("waNotify.sendCode")}
          </Button>
        </FieldGroup>
        <FormHint className="mt-2">{t("waNotify.phoneHint")}</FormHint>
      </form>
      {data.code_pending ? (
        <form onSubmit={confirm} noValidate>
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:w-48">
              <FieldLabel htmlFor="wa-notify-code">{t("waNotify.code")}</FieldLabel>
              <Input
                id="wa-notify-code"
                dir="ltr"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
            </Field>
            <Button type="submit" disabled={busy || code.length !== 6} data-wa-notify-confirm>
              {t("waNotify.confirm")}
            </Button>
          </FieldGroup>
          <FormHint className="mt-2">
            {t("waNotify.codeHint", { phone: `+${data.phone}` })}
          </FormHint>
        </form>
      ) : null}
      {err ? <FieldError>{err}</FieldError> : null}
    </div>
  )
}

function Settings({
  data,
  onChange,
  refresh,
}: {
  data: WhatsAppNotify
  onChange: (v: WhatsAppNotify) => void
  refresh: () => void
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [enabled, setEnabled] = useState(data.enabled)
  const [kinds, setKinds] = useState<NotifyKind[]>(data.kinds)
  const [quiet, setQuiet] = useState(data.quiet_start !== data.quiet_end)
  const [qs, setQs] = useState(toClock(data.quiet_start))
  const [qe, setQe] = useState(toClock(data.quiet_end === data.quiet_start ? 420 : data.quiet_end))
  const [tz, setTz] = useState(data.timezone)
  const [cap, setCap] = useState(String(data.daily_cap))
  const [batch, setBatch] = useState(String(data.batch_minutes))
  const [lang, setLang] = useState(data.locale)
  const [busy, setBusy] = useState<"" | "save" | "test">("")
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => setErr(null), [data])

  const batchItems = ["0", "1", "5", "15", "30", "60"].map((v) => ({
    value: v,
    label: t(`waNotify.batch.${v}`),
  }))
  const langItems = [
    { value: "en", label: t("lang.en") },
    { value: "ar", label: t("lang.ar") },
  ]

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy("save")
    setErr(null)
    try {
      const start = quiet ? fromClock(qs) : 0
      const end = quiet ? fromClock(qe) : 0
      const res = await api<WhatsAppNotify>("/api/w/notify/whatsapp", {
        method: "PUT",
        json: {
          enabled,
          kinds,
          quiet_start: start,
          quiet_end: end,
          timezone: tz.trim(),
          daily_cap: Math.max(1, Math.min(200, Number(cap) || 1)),
          batch_minutes: Number(batch),
          locale: lang,
        },
        workspaceId: ws.id,
      })
      toast.success(t("waNotify.saved"))
      onChange(res)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  async function test() {
    setBusy("test")
    try {
      await api("/api/w/notify/whatsapp/test", { method: "POST", workspaceId: ws.id })
      toast.success(t("waNotify.testSent"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
      refresh()
    }
  }

  async function remove() {
    await api("/api/w/notify/whatsapp", { method: "DELETE", workspaceId: ws.id })
    toast.success(t("waNotify.removed"))
    refresh()
  }

  return (
    <div className="flex flex-col gap-6" data-wa-notify="verified">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant="neutral" data-wa-notify-phone={data.phone}>
          <CheckCircle2Icon />
          <Ltr>+{data.phone}</Ltr>
        </Badge>
        {data.verified_at ? (
          <span className="text-muted-foreground">
            {t("waNotify.verifiedOn", { date: formatDate(data.verified_at) })}
          </span>
        ) : null}
        <span className="text-muted-foreground" data-wa-notify-pending={data.pending}>
          ·{" "}
          {t("waNotify.status", {
            pending: data.pending,
            sent: data.sent_today,
            cap: data.daily_cap,
          })}
        </span>
        {!data.workspace_linked ? (
          <span className="text-nq-warning">· {t("waNotify.notLinkedShort")}</span>
        ) : null}
      </div>

      <form onSubmit={save} noValidate className="max-w-3xl" data-form="wa-notify">
        <FieldGroup>
          <Field className="flex-row items-center gap-3">
            <Switch
              id="wa-notify-enabled"
              checked={enabled}
              onCheckedChange={(v) => setEnabled(!!v)}
            />
            <FieldLabel htmlFor="wa-notify-enabled">{t("waNotify.enabled")}</FieldLabel>
          </Field>

          <fieldset className="flex flex-col gap-2">
            <legend className="mb-2 text-sm font-medium">{t("waNotify.kinds")}</legend>
            {data.all_kinds.map((k) => (
              <Field
                key={k.kind}
                data-disabled={!k.allowed || undefined}
                className="flex-row items-center gap-3"
              >
                <Checkbox
                  id={`wa-kind-${k.kind}`}
                  checked={kinds.includes(k.kind)}
                  disabled={!k.allowed}
                  onCheckedChange={(v) =>
                    setKinds((cur) =>
                      v
                        ? [...cur.filter((x) => x !== k.kind), k.kind]
                        : cur.filter((x) => x !== k.kind),
                    )
                  }
                />
                <FieldLabel htmlFor={`wa-kind-${k.kind}`} className="font-normal">
                  <span>
                    {t(`waNotify.kind.${k.kind}`)}
                    {!k.allowed ? (
                      <span className="text-muted-foreground">
                        {" "}
                        · {t("waNotify.kindNotAllowed")}
                      </span>
                    ) : null}
                  </span>
                </FieldLabel>
              </Field>
            ))}
          </fieldset>

          <div className="flex flex-col gap-3">
            <Field className="flex-row items-center gap-3">
              <Switch id="wa-notify-quiet" checked={quiet} onCheckedChange={(v) => setQuiet(!!v)} />
              <FieldLabel htmlFor="wa-notify-quiet">{t("waNotify.quiet")}</FieldLabel>
            </Field>
            {quiet ? (
              <div className="flex flex-col gap-4 sm:flex-row">
                <Field className="sm:w-36">
                  <FieldLabel htmlFor="wa-quiet-start">{t("waNotify.quietFrom")}</FieldLabel>
                  <Input
                    id="wa-quiet-start"
                    type="time"
                    dir="ltr"
                    value={qs}
                    onChange={(e) => setQs(e.target.value)}
                  />
                </Field>
                <Field className="sm:w-36">
                  <FieldLabel htmlFor="wa-quiet-end">{t("waNotify.quietTo")}</FieldLabel>
                  <Input
                    id="wa-quiet-end"
                    type="time"
                    dir="ltr"
                    value={qe}
                    onChange={(e) => setQe(e.target.value)}
                  />
                </Field>
                <Field className="sm:flex-1">
                  <FieldLabel htmlFor="wa-tz">{t("waNotify.timezone")}</FieldLabel>
                  <Input
                    id="wa-tz"
                    dir="ltr"
                    value={tz}
                    onChange={(e) => setTz(e.target.value)}
                    placeholder={data.effective_timezone}
                  />
                </Field>
              </div>
            ) : null}
            <FormHint>{t("waNotify.quietHint", { zone: data.effective_timezone })}</FormHint>
          </div>

          <div className="flex flex-col gap-4 sm:flex-row">
            <Field className="sm:w-40">
              <FieldLabel htmlFor="wa-cap">{t("waNotify.cap")}</FieldLabel>
              <Input
                id="wa-cap"
                dir="ltr"
                inputMode="numeric"
                value={cap}
                onChange={(e) => setCap(e.target.value.replace(/\D/g, ""))}
              />
            </Field>
            <Field className="sm:w-56">
              <FieldLabel htmlFor="wa-batch">{t("waNotify.batch")}</FieldLabel>
              <Select
                items={batchItems}
                value={batch}
                onValueChange={(v) => setBatch(String(v ?? "5"))}
              >
                <SelectTrigger id="wa-batch" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {batchItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field className="sm:w-40">
              <FieldLabel htmlFor="wa-lang">{t("waNotify.language")}</FieldLabel>
              <Select
                items={langItems}
                value={lang}
                onValueChange={(v) => setLang(v === "ar" ? "ar" : "en")}
              >
                <SelectTrigger id="wa-lang" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {langItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <FormHint>{t("waNotify.batchHint")}</FormHint>

          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={!!busy}>
              {busy === "save" ? t("common.working") : t("common.save")}
            </Button>
            <Button
              type="button"
              variant="secondary"
              disabled={!!busy || !data.workspace_linked}
              onClick={() => void test()}
              data-wa-notify-test
            >
              <SendIcon />
              {busy === "test" ? t("common.working") : t("waNotify.test")}
            </Button>
            <ConfirmButton
              label={t("waNotify.remove")}
              title={t("waNotify.removeTitle")}
              description={t("waNotify.removeBody")}
              confirmLabel={t("waNotify.remove")}
              onConfirm={remove}
            />
          </div>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>

      {data.recent.length > 0 ? (
        <div data-wa-notify-log>
          <h3 className="mb-2 text-sm font-medium">{t("waNotify.log")}</h3>
          <ul className="flex flex-col gap-1 text-xs text-muted-foreground">
            {data.recent.map((r, i) => (
              <li key={i} className="flex flex-wrap items-center gap-2">
                {r.ok ? (
                  <CheckCircle2Icon className="size-3.5 text-nq-brand" />
                ) : (
                  <XCircleIcon className="size-3.5 text-nq-danger-text" />
                )}
                <Ltr>{formatDate(r.at)}</Ltr>
                <span>
                  ·{" "}
                  {r.kind === "test"
                    ? t("waNotify.logTest")
                    : t("waNotify.logBatch", { n: r.events })}
                </span>
                {!r.ok && r.error ? <span className="text-nq-danger-text">· {r.error}</span> : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}
