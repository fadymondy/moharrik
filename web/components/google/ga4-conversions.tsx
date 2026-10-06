"use client"

/*
  GA4 conversions (MH-449): the workspace's own GA4 web stream receives its funnel moments
  — a lead created, qualified, converted; a deal won — through the Measurement Protocol, and
  the embed snippets can carry the GA4 tag under Consent Mode v2. No Google sign-in is needed
  for any of it: the measurement id and an API secret from GA4's Admin are enough. When a
  Google account IS connected, its GA4 property's web streams are offered, so the id is picked
  rather than copied.

  The secret is write-only here: the API never returns it, and the field only says whether one
  is stored. Every switch is refused by the server until it can work, and the page says why.
*/

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { ChartLineIcon, CheckCircle2Icon, SendIcon, TriangleAlertIcon } from "lucide-react"
import {
  Badge,
  Button,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Switch,
  toast,
} from "@fadymondy/nasaq/web"
import { FieldGroup } from "@/components/form-layout"

import { Ltr } from "@/components/copy-field"
import { PickSelect } from "@/components/crm/shared"
import { useFormat } from "@/components/overview/overview-cards"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type Ga4ConversionEvent,
  type Ga4ConversionsPage,
  type Ga4Streams,
  type Ga4TestResult,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useGa4Conversions } from "@/lib/queries"

export function Ga4Conversions({ googleConnected }: { googleConnected: boolean }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const page = useGa4Conversions(ws.id)
  const s = page.data?.settings

  const [mid, setMid] = useState("")
  const [secret, setSecret] = useState("")
  const [events, setEvents] = useState(false)
  const [tag, setTag] = useState(false)
  const [debug, setDebug] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<Ga4TestResult | null>(null)
  const [streams, setStreams] = useState<Ga4Streams | null>(null)

  // The form starts from what is stored, and again after every save.
  useEffect(() => {
    if (!s) return
    setMid(s.measurement_id)
    setEvents(s.events_enabled)
    setTag(s.tag_in_embeds)
    setDebug(s.debug_mode)
  }, [s])

  // The connected account's GA4 web streams, so the id is picked rather than copied.
  useEffect(() => {
    if (!googleConnected) return
    let live = true
    api<Ga4Streams>("/api/w/ga4-conversions/streams", { workspaceId: ws.id })
      .then((r) => live && setStreams(r))
      .catch(() => live && setStreams(null))
    return () => {
      live = false
    }
  }, [googleConnected, ws.id])

  async function save(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError("")
    try {
      const json: Record<string, unknown> = {
        measurement_id: mid.trim(),
        events_enabled: events,
        tag_in_embeds: tag,
        debug_mode: debug,
      }
      if (secret.trim()) json.api_secret = secret.trim()
      const out = await api<Ga4ConversionsPage>("/api/w/ga4-conversions", {
        method: "PATCH",
        json,
        workspaceId: ws.id,
      })
      await page.mutate(out, { revalidate: false })
      setSecret("")
      toast.success(t("ga4conv.saved"))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  async function removeSecret() {
    setError("")
    try {
      const out = await api<Ga4ConversionsPage>("/api/w/ga4-conversions", {
        method: "PATCH",
        json: { api_secret: "", events_enabled: false },
        workspaceId: ws.id,
      })
      await page.mutate(out, { revalidate: false })
      toast.success(t("ga4conv.secretRemoved"))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  async function sendTest() {
    setTesting(true)
    setTest(null)
    try {
      setTest(
        await api<Ga4TestResult>("/api/w/ga4-conversions/test", {
          method: "POST",
          workspaceId: ws.id,
        }),
      )
    } catch (err) {
      setTest({
        valid: false,
        sent: false,
        messages: [],
        error: err instanceof ApiError ? err.message : t("common.networkError"),
      })
    } finally {
      setTesting(false)
    }
  }

  if (!page.data) return null
  const list = streams?.streams ?? []
  const ready = !!s?.measurement_id && !!s?.api_secret_set
  const dirty =
    !!s &&
    (mid.trim().toUpperCase() !== s.measurement_id ||
      !!secret.trim() ||
      events !== s.events_enabled ||
      tag !== s.tag_in_embeds ||
      debug !== s.debug_mode)

  return (
    <section className="pt-6" data-ga4-conversions data-ga4-ready={ready}>
      <div className="px-6 pb-3">
        <h3 className="flex items-center gap-2 text-sm font-medium [&_svg]:size-4">
          <ChartLineIcon />
          {t("ga4conv.title")}
        </h3>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{t("ga4conv.hint")}</p>
      </div>

      <form onSubmit={save} className="border-y border-nq-line px-6 py-5">
        <FieldGroup className="max-w-2xl">
          {list.length > 0 ? (
            <Field>
              <FieldLabel htmlFor="ga4-stream">{t("ga4conv.pickStream")}</FieldLabel>
              <PickSelect
                id="ga4-stream"
                value={
                  list.some((x) => x.measurement_id === mid.trim().toUpperCase())
                    ? mid.trim().toUpperCase()
                    : ""
                }
                onChange={(v) => v && setMid(v)}
                noneLabel={t("ga4conv.pickStreamNone")}
                options={list.map((x) => ({
                  value: x.measurement_id,
                  label: `${x.property_name} · ${x.name} · ${x.measurement_id}`,
                }))}
              />
              <FieldDescription>{t("ga4conv.pickStreamHint")}</FieldDescription>
            </Field>
          ) : null}
          {(streams?.errors ?? []).map((e) => (
            <p key={e} className="text-xs text-muted-foreground">
              <Ltr>{e}</Ltr>
            </p>
          ))}

          <Field>
            <FieldLabel htmlFor="ga4-mid">{t("ga4conv.measurementId")}</FieldLabel>
            <Input
              id="ga4-mid"
              dir="ltr"
              className="font-mono"
              value={mid}
              onChange={(e) => setMid(e.target.value)}
              placeholder="G-XXXXXXXXXX"
              autoComplete="off"
              data-ga4-mid
            />
            <FieldDescription>{t("ga4conv.measurementIdHint")}</FieldDescription>
          </Field>

          <Field>
            <FieldLabel htmlFor="ga4-secret">{t("ga4conv.apiSecret")}</FieldLabel>
            <div className="flex items-center gap-2">
              <Input
                id="ga4-secret"
                type="password"
                // LTR and monospace only for what is typed: the Arabic placeholder reads as Arabic.
                dir={secret ? "ltr" : undefined}
                className={secret ? "font-mono" : undefined}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder={s?.api_secret_set ? t("ga4conv.secretStored") : ""}
                autoComplete="new-password"
                data-ga4-secret
              />
              {s?.api_secret_set ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="shrink-0 text-nq-danger-text"
                  onClick={() => void removeSecret()}
                  data-ga4-remove-secret
                >
                  {t("ga4conv.removeSecret")}
                </Button>
              ) : null}
            </div>
            <FieldDescription>{t("ga4conv.apiSecretHint")}</FieldDescription>
          </Field>

          <ToggleRow
            id="ga4-events"
            checked={events}
            onChange={setEvents}
            title={t("ga4conv.sendEvents")}
            hint={t("ga4conv.sendEventsHint")}
          />
          <ToggleRow
            id="ga4-tag"
            checked={tag}
            onChange={setTag}
            title={t("ga4conv.tag")}
            hint={t("ga4conv.tagHint")}
          />
          <ToggleRow
            id="ga4-debug"
            checked={debug}
            onChange={setDebug}
            title={t("ga4conv.debug")}
            hint={t("ga4conv.debugHint")}
          />

          {error ? (
            <p
              role="alert"
              className="flex items-start gap-2 text-sm text-nq-danger-text"
              data-ga4-error
            >
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
              {error}
            </p>
          ) : null}

          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={saving || !dirty} data-ga4-save>
              {saving ? t("common.working") : t("common.save")}
            </Button>
            {/* Offered only once a test can work: an id and a secret are stored. */}
            {ready ? (
              <Button
                type="button"
                variant="secondary"
                onClick={() => void sendTest()}
                disabled={testing}
                data-ga4-test
              >
                <SendIcon />
                {testing ? t("common.working") : t("ga4conv.test")}
              </Button>
            ) : (
              <span className="text-xs text-muted-foreground">{t("ga4conv.testNeeds")}</span>
            )}
          </div>
          {test ? <TestOutcome r={test} /> : null}
        </FieldGroup>
      </form>

      <div className="px-6 pt-5 pb-2">
        <h4 className="text-sm font-medium">{t("ga4conv.whatIsSent")}</h4>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
          {t("ga4conv.whatIsSentHint")}
        </p>
      </div>
      <ul className="divide-y divide-nq-line border-y border-nq-line" data-ga4-event-map>
        {page.data.events.map((e) => (
          <li
            key={e.name}
            className="flex flex-wrap items-center gap-x-4 gap-y-1 px-6 py-2.5 text-sm"
          >
            <span className="min-w-44">
              <Ltr mono className="text-xs text-foreground">
                {e.name}
              </Ltr>
            </span>
            <span className="text-nq-fg-body">{t(`ga4conv.when.${e.when}`)}</span>
          </li>
        ))}
      </ul>

      <Recent list={page.data.recent ?? []} />
    </section>
  )
}

function ToggleRow({
  id,
  checked,
  onChange,
  title,
  hint,
}: {
  id: string
  checked: boolean
  onChange: (v: boolean) => void
  title: string
  hint: string
}) {
  return (
    <Field className="flex-row items-center gap-3 items-start">
      <Switch id={id} checked={checked} onCheckedChange={onChange} data-toggle={id} />
      <div className="min-w-0 space-y-0.5">
        <FieldLabel htmlFor={id}>{title}</FieldLabel>
        <FieldDescription>{hint}</FieldDescription>
      </div>
    </Field>
  )
}

function TestOutcome({ r }: { r: Ga4TestResult }) {
  const { t } = useTranslations()
  if (r.sent) {
    return (
      <p
        role="status"
        className="flex items-start gap-2 text-sm text-foreground"
        data-ga4-test-result="sent"
      >
        <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-nq-action" />
        {t("ga4conv.testSent")}
      </p>
    )
  }
  return (
    <div role="alert" className="space-y-1 text-sm" data-ga4-test-result="failed">
      <p className="flex items-start gap-2 text-nq-danger-text">
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
        {r.valid ? t("ga4conv.testNotSent") : t("ga4conv.testInvalid")}
      </p>
      {r.error ? (
        <p className="text-muted-foreground">
          <Ltr>{r.error}</Ltr>
        </p>
      ) : null}
      {(r.messages ?? []).map((m, i) => (
        <p key={i} className="text-muted-foreground">
          <Ltr>
            {m.fieldPath ? `${m.fieldPath}: ` : ""}
            {m.description}
          </Ltr>
        </p>
      ))}
    </div>
  )
}

const STATUS_VARIANT: Record<
  Ga4ConversionEvent["status"],
  "brand" | "neutral" | "outline" | "danger"
> = {
  sent: "neutral",
  pending: "outline",
  skipped: "outline",
  failed: "danger",
}

function Recent({ list }: { list: Ga4ConversionEvent[] }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  return (
    <div className="pt-5 pb-10">
      <h4 className="px-6 pb-2 text-sm font-medium">{t("ga4conv.recent")}</h4>
      {list.length === 0 ? (
        <p className="px-6 text-sm text-muted-foreground" data-ga4-recent="empty">
          {t("ga4conv.recentEmpty")}
        </p>
      ) : (
        <ul
          className="divide-y divide-nq-line border-y border-nq-line"
          data-ga4-recent={list.length}
        >
          {list.map((e) => (
            <li
              key={e.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-2.5 text-sm"
              data-ga4-event={e.name}
              data-status={e.status}
            >
              <Ltr mono className="text-xs text-foreground">
                {e.name}
              </Ltr>
              <Badge variant={STATUS_VARIANT[e.status]}>{t(`ga4conv.status.${e.status}`)}</Badge>
              {e.attributed === false ? (
                <Badge variant="outline">{t("ga4conv.unattributed")}</Badge>
              ) : null}
              {e.debug ? <Badge variant="outline">DebugView</Badge> : null}
              <Link
                href={
                  e.subject_kind === "lead"
                    ? `/${locale}/w/${ws.slug}/leads/${e.subject_id}`
                    : `/${locale}/w/${ws.slug}/deals/${e.subject_id}`
                }
                className="text-xs text-nq-action underline-offset-4 hover:underline"
              >
                {t(`ga4conv.subject.${e.subject_kind}`)}
              </Link>
              <span
                className="ms-auto text-xs text-muted-foreground"
                title={f.exact(e.occurred_at)}
              >
                {f.ago(e.sent_at ?? e.occurred_at)}
              </span>
              {e.error ? (
                <p className="w-full text-xs text-muted-foreground">
                  <Ltr>{e.error}</Ltr>
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
