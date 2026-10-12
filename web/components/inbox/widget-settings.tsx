"use client"

import { useState, type FormEvent } from "react"
import { MessageCircleIcon, PlusIcon, Trash2Icon } from "lucide-react"
import {
  Button,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  Switch,
  Textarea,
  toast,
  Toggle,
  ToggleGroup,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldLegend, FieldSet, FieldError, FormHint } from "@/components/form-layout"

import { Bdi } from "@/components/bidi"
import { CopyField } from "@/components/copy-field"
import { PickSelect } from "@/components/crm/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type ChatQuickAction,
  type ChatWidget,
  type ChatWidgetSettings,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/*
Everything a chat widget does on the customer's page (MH-382): greeting, look,
the invitation, page-dependent quick actions and the lead event. Ported from
fadymondy.com's chat widget, where these are one site's constants; here each
widget has its own. The widget reads them from its config, so saving here
changes every page that already embeds the key.
*/

const HEX = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i
// What the SDK paints with when no accent is set (moharrik-chat's DEFAULT_ACCENT).
const SDK_DEFAULT_ACCENT = "#00A0A8"

/*
The ink the widget itself puts on the accent: dark or light, whichever reads
(the SDK's onAccent, WCAG relative luminance). Duplicated so the preview shows
the embedded widget exactly — these two are the widget's colours, not the
app's theme.
*/
function inkOn(hex: string): string {
  let h = hex.slice(1)
  if (h.length === 3) h = h.replace(/./g, (c) => c + c)
  const lin = (i: number) => {
    const c = parseInt(h.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  const L = 0.2126 * lin(0) + 0.7152 * lin(2) + 0.0722 * lin(4)
  return (L + 0.05) / 0.0575 >= 1.05 / (L + 0.05) ? "#0B1429" : "#ffffff"
}

/**
 * The allowed origins as typed (comma separated) → the list the API stores,
 * or null when an entry is not a bare origin. The widget matches the browser's
 * Origin header exactly, so "example.com" or "https://example.com/contact"
 * would never match and silently block every visitor (MH-687).
 */
export function parseOrigins(raw: string): string[] | null {
  const out: string[] = []
  for (const part of raw.split(",")) {
    const v = part.trim().replace(/\/+$/, "")
    if (!v) continue
    // A scheme and host; a leading "*." covers every subdomain (not the apex).
    if (!/^https?:\/\/(\*\.)?[^/\s?#*@]+$/i.test(v)) return null
    out.push(v)
  }
  return out
}

function settingsOf(w: ChatWidget): ChatWidgetSettings {
  return {
    greeting: w.greeting ?? "",
    greeting_ar: w.greeting_ar ?? "",
    title: w.title ?? "",
    title_ar: w.title_ar ?? "",
    accent: w.accent ?? "",
    theme: w.theme ?? "auto",
    position: w.position ?? "end",
    invite: !!w.invite,
    invite_after_seconds: w.invite_after_seconds || 25,
    quick_actions: (w.quick_actions ?? []).map((q) => ({
      ...q,
      path: q.path ?? "",
      locale: q.locale ?? "",
    })),
    lead_event: w.lead_event !== false,
    require_contact: w.require_contact !== false,
  }
}

export function WidgetSettingsSheet({
  widget,
  onOpenChange,
  onSaved,
}: {
  widget: ChatWidget | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t, isRtl } = useTranslations()
  return (
    <Sheet open={!!widget} onOpenChange={onOpenChange}>
      <SheetContent
        side="end"
        className="w-full overflow-y-auto sm:w-[min(36rem,100vw)]"
        data-widget-settings
      >
        <SheetHeader className="pe-12">
          <SheetTitle>{widget ? <Bdi>{widget.name}</Bdi> : null}</SheetTitle>
          <SheetDescription>{t("inbox.widgetSettingsHint")}</SheetDescription>
        </SheetHeader>
        {widget ? (
          <SettingsForm
            key={widget.id}
            widget={widget}
            onSaved={onSaved}
            onCancel={() => onOpenChange(false)}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

function SettingsForm({
  widget,
  onSaved,
  onCancel,
}: {
  widget: ChatWidget
  onSaved: () => void
  onCancel: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [s, setS] = useState<ChatWidgetSettings>(() => settingsOf(widget))
  // Who the widget is and where it may run live beside the settings: the
  // same PATCH takes them, and they were only settable at creation (MH-687).
  const [name, setName] = useState(widget.name)
  const [origins, setOrigins] = useState((widget.allowed_origins ?? []).join(", "))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const set = <K extends keyof ChatWidgetSettings>(k: K, v: ChatWidgetSettings[K]) =>
    setS((cur) => ({ ...cur, [k]: v }))

  const accentOk = s.accent === "" || HEX.test(s.accent)
  const previewAccent = accentOk && s.accent ? s.accent : SDK_DEFAULT_ACCENT
  // The preview speaks the language this page is in, as the widget would.
  const ar = locale === "ar"
  const previewTitle = (ar && s.title_ar) || s.title || t("inbox.widgetDefaultTitle")
  const previewGreeting = (ar && s.greeting_ar) || s.greeting || t("inbox.widgetDefaultGreeting")
  const previewActions = s.quick_actions.filter(
    (q) => q.label.trim() && (!q.locale || q.locale === (ar ? "ar" : "en")),
  )

  function setAction(i: number, patch: Partial<ChatQuickAction>) {
    set(
      "quick_actions",
      s.quick_actions.map((q, j) => (j === i ? { ...q, ...patch } : q)),
    )
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) {
      setErr(t("inbox.widgetNameRequired"))
      return
    }
    const allowed = parseOrigins(origins)
    if (!allowed) {
      setErr(t("inbox.originsInvalid"))
      return
    }
    if (!accentOk) {
      setErr(t("inbox.accentInvalid"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/chat_widgets/${encodeURIComponent(widget.id)}`, {
        method: "PATCH",
        workspaceId: ws.id,
        json: {
          ...s,
          name: name.trim(),
          allowed_origins: allowed,
          quick_actions: s.quick_actions.map((q) => ({
            label: q.label.trim(),
            message: q.message.trim(),
            ...(q.path?.trim() ? { path: q.path.trim() } : {}),
            ...(q.locale ? { locale: q.locale } : {}),
          })),
        },
      })
      toast.success(t("inbox.widgetSettingsSaved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-6 px-4 pb-6" data-widget-settings-form>
      {/* What the visitor sees first, in the colours chosen below. Inline
          styles only for the accent: it is the workspace's own brand colour,
          not a theme colour, and the widget itself paints with exactly it. */}
      <div className="border border-nq-line" aria-hidden data-widget-preview>
        <div
          className="flex items-center gap-2 px-3 py-2.5 text-sm font-medium"
          style={{ background: previewAccent, color: inkOn(previewAccent) }}
        >
          <MessageCircleIcon className="size-4" />
          <span className="truncate" dir="auto">
            {previewTitle}
          </span>
        </div>
        <div className="flex flex-col gap-2 p-3">
          <p
            className="max-w-[85%] self-start bg-card px-3 py-2 text-sm whitespace-pre-wrap"
            dir="auto"
          >
            {previewGreeting}
          </p>
          {previewActions.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {previewActions.map((q, i) => (
                <span key={i} className="border border-nq-line px-2.5 py-1 text-xs" dir="auto">
                  {q.label}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </div>

      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="ws-name">{t("inbox.widgetName")}</FieldLabel>
          <Input
            id="ws-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={80}
            dir="auto"
            required
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="ws-origins">{t("inbox.allowedOrigins")}</FieldLabel>
          <Input
            id="ws-origins"
            dir="ltr"
            value={origins}
            onChange={(e) => setOrigins(e.target.value)}
            placeholder="https://example.com"
            data-widget-origins
          />
          <FieldDescription>{t("inbox.allowedOriginsHint")}</FieldDescription>
        </Field>
      </FieldGroup>

      <Field>
        <FieldLabel>{t("inbox.embed")}</FieldLabel>
        <CopyField value={widget.embed} label={t("inbox.embed")} pre />
        <FieldDescription>{t("inbox.embedHint")}</FieldDescription>
      </Field>

      <FieldSet>
        <FieldLegend>{t("inbox.widgetText")}</FieldLegend>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="ws-greeting">{t("inbox.greeting")}</FieldLabel>
            <Textarea
              id="ws-greeting"
              rows={2}
              maxLength={500}
              value={s.greeting}
              onChange={(e) => set("greeting", e.target.value)}
              dir="auto"
            />
            <FieldDescription>{t("inbox.greetingHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="ws-greeting-ar">{t("inbox.greetingAr")}</FieldLabel>
            <Textarea
              id="ws-greeting-ar"
              rows={2}
              maxLength={500}
              value={s.greeting_ar}
              onChange={(e) => set("greeting_ar", e.target.value)}
              dir="rtl"
              lang="ar"
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="ws-title">{t("inbox.widgetTitle")}</FieldLabel>
              <Input
                id="ws-title"
                maxLength={60}
                value={s.title}
                onChange={(e) => set("title", e.target.value)}
                placeholder={t("inbox.widgetDefaultTitle")}
                dir="auto"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="ws-title-ar">{t("inbox.widgetTitleAr")}</FieldLabel>
              <Input
                id="ws-title-ar"
                maxLength={60}
                value={s.title_ar}
                onChange={(e) => set("title_ar", e.target.value)}
                dir="rtl"
                lang="ar"
              />
            </Field>
          </div>
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>{t("inbox.widgetLook")}</FieldLegend>
        <FieldGroup>
          <Field invalid={!!!accentOk}>
            <FieldLabel htmlFor="ws-accent">{t("inbox.accent")}</FieldLabel>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label={t("inbox.accent")}
                className="size-9 shrink-0 cursor-pointer border border-nq-line bg-transparent p-0.5"
                value={
                  accentOk && s.accent.length === 7
                    ? s.accent
                    : previewAccent.length === 7
                      ? previewAccent
                      : SDK_DEFAULT_ACCENT
                }
                onChange={(e) => set("accent", e.target.value.toUpperCase())}
                data-accent-picker
              />
              <Input
                id="ws-accent"
                dir="ltr"
                className="font-mono"
                maxLength={7}
                value={s.accent}
                onChange={(e) => set("accent", e.target.value.trim())}
                placeholder={SDK_DEFAULT_ACCENT}
                aria-invalid={!accentOk || undefined}
              />
              {s.accent ? (
                <Button type="button" variant="ghost" size="sm" onClick={() => set("accent", "")}>
                  {t("inbox.accentReset")}
                </Button>
              ) : null}
            </div>
            <FieldDescription>{t("inbox.accentHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel>{t("inbox.widgetTheme")}</FieldLabel>
            <ToggleGroup
              value={[s.theme]}
              onValueChange={(v) => v[0] && set("theme", v[0] as ChatWidgetSettings["theme"])}
              aria-label={t("inbox.widgetTheme")}
            >
              {(["auto", "light", "dark"] as const).map((v) => (
                <Toggle key={v} value={v} data-theme-option={v}>
                  {t(`inbox.theme.${v}`)}
                </Toggle>
              ))}
            </ToggleGroup>
          </Field>
          <Field>
            <FieldLabel>{t("inbox.widgetPosition")}</FieldLabel>
            <ToggleGroup
              value={[s.position]}
              onValueChange={(v) => v[0] && set("position", v[0] as ChatWidgetSettings["position"])}
              aria-label={t("inbox.widgetPosition")}
            >
              {(["end", "start"] as const).map((v) => (
                <Toggle key={v} value={v} data-position-option={v}>
                  {t(`inbox.position.${v}`)}
                </Toggle>
              ))}
            </ToggleGroup>
            <FieldDescription>{t("inbox.positionHint")}</FieldDescription>
          </Field>
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>{t("inbox.widgetBehaviour")}</FieldLegend>
        <FieldGroup>
          <Field className="flex-row items-center gap-3">
            <Switch id="ws-invite" checked={s.invite} onCheckedChange={(v) => set("invite", v)} />
            <div className="flex flex-col gap-1">
              <FieldLabel htmlFor="ws-invite">{t("inbox.invite")}</FieldLabel>
              <FieldDescription>{t("inbox.inviteHint")}</FieldDescription>
            </div>
          </Field>
          {s.invite ? (
            <Field className="max-w-48">
              <FieldLabel htmlFor="ws-invite-after">{t("inbox.inviteAfter")}</FieldLabel>
              <Input
                id="ws-invite-after"
                type="number"
                min={5}
                max={600}
                value={s.invite_after_seconds}
                onChange={(e) => set("invite_after_seconds", Number(e.target.value) || 0)}
              />
            </Field>
          ) : null}
          <Field className="flex-row items-center gap-3">
            <Switch
              id="ws-contact"
              checked={s.require_contact}
              onCheckedChange={(v) => set("require_contact", v)}
            />
            <div className="flex flex-col gap-1">
              <FieldLabel htmlFor="ws-contact">{t("inbox.requireContact")}</FieldLabel>
              <FieldDescription>{t("inbox.requireContactHint")}</FieldDescription>
            </div>
          </Field>
          <Field className="flex-row items-center gap-3">
            <Switch
              id="ws-lead"
              checked={s.lead_event}
              onCheckedChange={(v) => set("lead_event", v)}
            />
            <div className="flex flex-col gap-1">
              <FieldLabel htmlFor="ws-lead">{t("inbox.leadEvent")}</FieldLabel>
              <FieldDescription>{t("inbox.leadEventHint")}</FieldDescription>
            </div>
          </Field>
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>{t("inbox.quickActions")}</FieldLegend>
        <FormHint>{t("inbox.quickActionsHint")}</FormHint>
        <div className="flex flex-col gap-3">
          {s.quick_actions.map((q, i) => (
            <div key={i} className="grid gap-2 border border-nq-line p-3" data-quick-action={i}>
              <div className="flex items-center gap-2">
                <Input
                  aria-label={t("inbox.qaLabel")}
                  placeholder={t("inbox.qaLabel")}
                  maxLength={40}
                  value={q.label}
                  onChange={(e) => setAction(i, { label: e.target.value })}
                  dir="auto"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("inbox.qaRemove")}
                  onClick={() =>
                    set(
                      "quick_actions",
                      s.quick_actions.filter((_, j) => j !== i),
                    )
                  }
                >
                  <Trash2Icon />
                </Button>
              </div>
              <Textarea
                aria-label={t("inbox.qaMessage")}
                placeholder={t("inbox.qaMessage")}
                rows={2}
                maxLength={500}
                value={q.message}
                onChange={(e) => setAction(i, { message: e.target.value })}
                dir="auto"
              />
              <div className="grid gap-2 sm:grid-cols-2">
                <Input
                  aria-label={t("inbox.qaPath")}
                  placeholder={t("inbox.qaPathPlaceholder")}
                  dir="ltr"
                  maxLength={200}
                  value={q.path ?? ""}
                  onChange={(e) => setAction(i, { path: e.target.value })}
                />
                <PickSelect
                  id={`qa-locale-${i}`}
                  value={q.locale ?? ""}
                  onChange={(v) => setAction(i, { locale: v as ChatQuickAction["locale"] })}
                  noneLabel={t("inbox.qaAnyLanguage")}
                  options={[
                    { value: "en", label: t("inbox.qaEnglish") },
                    { value: "ar", label: t("inbox.qaArabic") },
                  ]}
                />
              </div>
            </div>
          ))}
          {s.quick_actions.length < 8 ? (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="self-start"
              onClick={() =>
                set("quick_actions", [
                  ...s.quick_actions,
                  { label: "", message: "", path: "", locale: "" },
                ])
              }
            >
              <PlusIcon />
              {t("inbox.qaAdd")}
            </Button>
          ) : null}
        </div>
      </FieldSet>

      {err ? <FieldError>{err}</FieldError> : null}
      <SheetFooter className="flex-row justify-end gap-2 p-0">
        <Button type="button" variant="secondary" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
      </SheetFooter>
    </form>
  )
}
