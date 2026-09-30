"use client"

/*
The trends digest for the signed-in member (MH-389): an email of the
workspace's top trend stories, daily or weekly at their hour, in their
language, through the workspace's SMTP service — and a reminder that the same
stories answer "trends" on WhatsApp from their verified number.

Everything here is the member's own; the API reads and writes only their row.
*/

import Link from "next/link"
import { useMemo, useState } from "react"
import { MailIcon, SendIcon } from "lucide-react"
import { Button, Field, FieldDescription, FieldLabel, Input, toast } from "@fadymondy/nasaq/web"
import useSWR from "swr"

import { PickSelect } from "@/components/crm/shared"
import { Bdi } from "@/components/bidi"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

type Digest = {
  frequency: "off" | "daily" | "weekly"
  hour: number
  weekday: number
  timezone: string
  locale: "en" | "ar"
  top: number
  email: string
  email_ready: boolean
  workspace_timezone: string
  next_at: string | null
  last_sent_at: string | null
  last_status: string
  last_error: string
}

export function TrendsDigestSection() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const q = useSWR<Digest>(
    ws.id ? [`/api/w/notify/digest?locale=${locale}`, ws.id] : null,
    ([p, id]: [string, string]) => api<Digest>(p, { workspaceId: id }),
  )
  return (
    <section aria-labelledby="trends-digest" className="pb-10" data-section="trends-digest">
      <div className="px-6 pb-3">
        <h2 id="trends-digest" className="flex items-center gap-2 text-base font-medium">
          <MailIcon className="size-4 text-muted-foreground" />
          {t("digest.title")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("digest.hint")}</p>
      </div>
      <div className="border-y border-nq-line px-6 py-5">
        {q.error ? (
          <p className="text-sm text-nq-danger-text">
            {q.error instanceof ApiError ? q.error.message : t("common.networkError")}
          </p>
        ) : !q.data ? (
          <p className="text-sm text-muted-foreground">{t("common.working")}</p>
        ) : (
          <DigestForm
            key={JSON.stringify([
              q.data.frequency,
              q.data.hour,
              q.data.weekday,
              q.data.timezone,
              q.data.locale,
              q.data.top,
            ])}
            data={q.data}
            onSaved={(d) => q.mutate(d, { revalidate: false })}
          />
        )}
      </div>
    </section>
  )
}

function DigestForm({ data, onSaved }: { data: Digest; onSaved: (d: Digest) => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [d, setD] = useState({
    frequency: data.frequency,
    hour: data.hour,
    weekday: data.weekday,
    timezone: data.timezone,
    locale: data.locale,
    top: data.top,
  })
  const [busy, setBusy] = useState<"" | "save" | "test">("")
  const [err, setErr] = useState("")
  const dirty =
    d.frequency !== data.frequency ||
    d.hour !== data.hour ||
    d.weekday !== data.weekday ||
    d.timezone !== data.timezone ||
    d.locale !== data.locale ||
    d.top !== data.top

  // Times are shown in the zone the digest goes out in, and say which: the
  // viewer's browser may be somewhere else.
  const zone = data.timezone || data.workspace_timezone || "UTC"
  const when = useMemo(() => {
    let f: Intl.DateTimeFormat
    try {
      f = new Intl.DateTimeFormat(intlTag(locale), {
        dateStyle: "full",
        timeStyle: "short",
        timeZone: zone,
      })
    } catch {
      f = new Intl.DateTimeFormat(intlTag(locale), {
        dateStyle: "full",
        timeStyle: "short",
        timeZone: "UTC",
      })
    }
    return { format: (d: Date) => `${f.format(d)} (${zone})` }
  }, [locale, zone])
  const hourFmt = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { hour: "numeric", minute: "2-digit" }),
    [locale],
  )
  const dayFmt = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { weekday: "long" }),
    [locale],
  )
  // 2023-01-01 was a Sunday: day i of that week is weekday i.
  const days = Array.from({ length: 7 }, (_, i) => ({
    value: String(i),
    label: dayFmt.format(new Date(2023, 0, 1 + i)),
  }))
  const hours = Array.from({ length: 24 }, (_, h) => ({
    value: String(h),
    label: hourFmt.format(new Date(2023, 0, 1, h)),
  }))

  async function save() {
    setBusy("save")
    setErr("")
    try {
      const res = await api<Digest>("/api/w/notify/digest", {
        method: "PUT",
        json: d,
        workspaceId: ws.id,
      })
      onSaved(res)
      toast.success(d.frequency === "off" ? t("digest.stopped") : t("digest.saved"))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }
  async function test() {
    setBusy("test")
    setErr("")
    try {
      const res = await api<{ status: string; topics: number; to: string }>(
        "/api/w/notify/digest/test",
        { method: "POST", workspaceId: ws.id },
      )
      if (res.status === "empty") toast.message(t("digest.testEmpty"))
      else toast.success(t("digest.testSent", { to: res.to, n: res.topics }))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  return (
    <div className="flex max-w-3xl flex-col gap-5" data-digest={data.frequency}>
      {!data.email_ready ? (
        <p
          className="rounded-md border border-nq-line bg-card px-3 py-2 text-sm text-muted-foreground"
          data-digest-no-smtp
        >
          {t("digest.noSmtp")}{" "}
          {ws.can("mail:settings") ? (
            <Link
              className="underline underline-offset-4"
              href={`/${locale}/w/${ws.slug}/settings/email`}
            >
              {t("digest.setUpSmtp")}
            </Link>
          ) : null}
        </p>
      ) : null}

      <div
        className="inline-flex w-fit rounded-lg bg-muted p-[3px]"
        role="radiogroup"
        aria-label={t("digest.frequency")}
      >
        {(["off", "daily", "weekly"] as const).map((f) => (
          <button
            key={f}
            type="button"
            role="radio"
            aria-checked={d.frequency === f}
            onClick={() => setD({ ...d, frequency: f })}
            className={cn(
              "rounded-md px-3 py-1 text-sm font-medium transition-colors",
              d.frequency === f
                ? "bg-background text-foreground shadow-sm"
                : "text-foreground/60 hover:text-foreground",
            )}
            data-frequency={f}
          >
            {t(`digest.freq.${f}`)}
          </button>
        ))}
      </div>

      {d.frequency !== "off" ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {d.frequency === "weekly" ? (
            <Field>
              <FieldLabel htmlFor="digest-day">{t("digest.day")}</FieldLabel>
              <PickSelect
                id="digest-day"
                value={String(d.weekday)}
                onChange={(v) => setD({ ...d, weekday: Number(v) })}
                options={days}
              />
            </Field>
          ) : null}
          <Field>
            <FieldLabel htmlFor="digest-hour">{t("digest.hour")}</FieldLabel>
            <PickSelect
              id="digest-hour"
              value={String(d.hour)}
              onChange={(v) => setD({ ...d, hour: Number(v) })}
              options={hours}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="digest-tz">{t("digest.timezone")}</FieldLabel>
            <Input
              id="digest-tz"
              dir="ltr"
              value={d.timezone}
              placeholder={data.workspace_timezone || "UTC"}
              onChange={(e) => setD({ ...d, timezone: e.target.value })}
            />
            <FieldDescription>
              {t("digest.timezoneHint", { tz: data.workspace_timezone || "UTC" })}
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="digest-lang">{t("digest.language")}</FieldLabel>
            <PickSelect
              id="digest-lang"
              value={d.locale}
              onChange={(v) => setD({ ...d, locale: v === "ar" ? "ar" : "en" })}
              options={[
                { value: "en", label: "English" },
                { value: "ar", label: "العربية" },
              ]}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="digest-top">{t("digest.top")}</FieldLabel>
            <PickSelect
              id="digest-top"
              value={String(d.top)}
              onChange={(v) => setD({ ...d, top: Number(v) })}
              options={[3, 5, 7, 10].map((n) => ({
                value: String(n),
                label: t("digest.stories", { n }),
              }))}
            />
          </Field>
        </div>
      ) : null}

      <div className="space-y-1 text-sm text-muted-foreground" data-digest-status>
        <p>
          {t("digest.to")} <Bdi className="font-medium text-foreground">{data.email}</Bdi>
        </p>
        {data.frequency !== "off" && data.next_at ? (
          <p data-digest-next>{t("digest.next", { when: when.format(new Date(data.next_at)) })}</p>
        ) : null}
        {data.last_status ? (
          <p
            className={data.last_status === "failed" ? "text-nq-danger-text" : undefined}
            data-digest-last={data.last_status}
          >
            {data.last_status === "sent" && data.last_sent_at
              ? t("digest.lastSent", { when: when.format(new Date(data.last_sent_at)) })
              : data.last_status === "empty"
                ? t("digest.lastEmpty")
                : t("digest.lastFailed", { why: data.last_error })}
          </p>
        ) : null}
      </div>

      {err ? (
        <p className="text-sm text-nq-danger-text" role="alert" data-digest-error>
          {err}
        </p>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => void save()} disabled={!dirty || busy !== ""} data-digest-save>
          {busy === "save" ? t("common.working") : t("common.save")}
        </Button>
        <Button
          variant="secondary"
          onClick={() => void test()}
          disabled={!data.email_ready || busy !== "" || dirty}
          title={dirty ? t("digest.saveFirst") : undefined}
          data-digest-test
        >
          <SendIcon />
          {t("digest.sendNow")}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground" data-digest-whatsapp>
        {t("digest.whatsapp")}
      </p>
    </div>
  )
}
