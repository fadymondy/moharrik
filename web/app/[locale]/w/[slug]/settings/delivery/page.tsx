"use client"

import { useEffect, useState, type ReactNode } from "react"
import { BellRingIcon, MonitorSmartphoneIcon, SendIcon, Volume2Icon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  currentSubscription,
  pushSupported,
  subscribePush,
  useChimeMuted,
  useNotificationPermission,
} from "@/lib/alerts"
import { api, ApiError, type PushDevice, type PushResult } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { usePushStatus } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"
import { WhatsAppNotifySection } from "@/components/alerts/whatsapp-notify"
import { TrendsDigestSection } from "@/components/alerts/trends-digest"

/*
Delivery (MH-378): how this workspace reaches the signed-in member.

  - this device: web push on or off, desktop alerts allowed or not, the chime
  - the member's own devices where push is on, with what last happened on each
  - a test push to all of them

Everything here is the member's own. Nobody sees or removes another member's
devices, the owner included (internal/alerts/routes.go).
*/

function uaLabel(ua: string, t: (k: string, v?: Record<string, string | number>) => string) {
  if (!ua) return t("delivery.unknownDevice")
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : ""
  const os = /Windows/.test(ua)
    ? "Windows"
    : /Android/.test(ua)
      ? "Android"
      : /iPhone|iPad|iOS/.test(ua)
        ? "iOS"
        : /Mac OS X|Macintosh/.test(ua)
          ? "macOS"
          : /Linux/.test(ua)
            ? "Linux"
            : ""
  if (!browser && !os) return t("delivery.unknownDevice")
  if (!os) return browser
  if (!browser) return os
  return t("delivery.uaOn", { browser, os })
}

function Row({
  icon,
  title,
  hint,
  children,
}: {
  icon: ReactNode
  title: string
  hint: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-3 px-6 py-4 [&>svg]:size-5 [&>svg]:shrink-0 [&>svg]:text-muted-foreground">
      {icon}
      <div className="min-w-0 flex-1 basis-60">
        <p className="text-sm font-medium">{title}</p>
        <div className="text-sm text-muted-foreground">{hint}</div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">{children}</div>
    </div>
  )
}

export default function DeliveryPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.delivery")} · ${ws.summary.name}`)
  const allowed = ws.can("push:manage")
  const status = usePushStatus(ws.id, allowed)
  const [perm, requestPerm] = useNotificationPermission()
  const [muted, setMuted] = useChimeMuted()
  const [supported, setSupported] = useState<boolean | null>(null)
  const [endpoint, setEndpoint] = useState<string | null>(null)
  const [busy, setBusy] = useState<"" | "enable" | "disable" | "test" | string>("")

  // Which of the listed devices is this browser: its own subscription's endpoint.
  useEffect(() => {
    setSupported(pushSupported())
    void currentSubscription().then((s) => setEndpoint(s?.endpoint ?? null))
  }, [])

  const devices = status.data?.devices ?? []
  const mine = endpoint ? devices.find((d) => d.endpoint === endpoint) : undefined
  const on = !!mine

  async function enable() {
    if (!status.data?.public_key) return
    setBusy("enable")
    try {
      // The permission prompt comes from here, inside the click.
      const sub = await subscribePush(
        status.data.public_key,
        devices.map((d) => d.endpoint),
      )
      await api("/api/w/push/subscriptions", {
        json: { endpoint: sub.endpoint, keys: sub.keys, locale },
        workspaceId: ws.id,
      })
      setEndpoint(sub.endpoint ?? null)
      toast.success(t("delivery.enabled"))
      await status.mutate()
    } catch (e) {
      toast.error(
        e instanceof ApiError
          ? e.message
          : e instanceof Error && e.name === "NotAllowedError"
            ? t("delivery.blocked")
            : t("common.networkError"),
      )
    } finally {
      setBusy("")
    }
  }

  async function remove(d: PushDevice, self: boolean) {
    setBusy(self ? "disable" : d.id)
    try {
      // The browser's own subscription is left alone: another workspace on
      // this browser may be using it. This workspace just stops sending to it.
      await api(`/api/w/push/subscriptions/${encodeURIComponent(d.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(self ? t("delivery.disabled") : t("delivery.removed"))
      await status.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  async function test() {
    setBusy("test")
    try {
      const r = await api<PushResult>("/api/w/push/test", { method: "POST", workspaceId: ws.id })
      if (r.removed || r.failed)
        toast.warning(
          t("delivery.testPartial", { sent: r.sent, removed: r.removed, failed: r.failed }),
        )
      else toast.success(t("delivery.testSent", { n: r.sent }))
      await status.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  let pushHint: ReactNode = t("delivery.pushHint")
  let pushControl: ReactNode = null
  if (supported === false) pushHint = t("delivery.unsupported")
  else if (status.data && !status.data.configured) pushHint = t("delivery.notConfigured")
  else if (perm === "denied" && !on) pushHint = t("delivery.blocked")
  else if (status.data && supported) {
    pushControl = on ? (
      <>
        <Badge variant="secondary" data-push-state="on">
          {t("delivery.pushOn")}
        </Badge>
        <Button
          variant="outline"
          size="sm"
          disabled={!!busy}
          onClick={() => mine && void remove(mine, true)}
          data-push-disable
        >
          {busy === "disable" ? t("common.working") : t("delivery.disable")}
        </Button>
      </>
    ) : (
      <>
        <Badge variant="outline" data-push-state="off">
          {t("delivery.pushOff")}
        </Badge>
        <Button size="sm" disabled={!!busy} onClick={() => void enable()} data-push-enable>
          <BellRingIcon />
          {busy === "enable" ? t("common.working") : t("delivery.enable")}
        </Button>
      </>
    )
  }

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.delivery")}
        description={t("delivery.hint")}
      />
      {!ws.current ? (
        <LoadingRows rows={3} />
      ) : !allowed ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("alerts.nothingToRead")}</p>
      ) : status.error ? (
        <ErrorState error={status.error} />
      ) : !status.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <section aria-labelledby="this-device" data-section="this-device">
            <h2 id="this-device" className="eyebrow px-6 pb-2">
              {t("delivery.thisDevice")}
            </h2>
            <div className="divide-y divide-nq-line border-y border-nq-line">
              <Row icon={<MonitorSmartphoneIcon />} title={t("delivery.push")} hint={pushHint}>
                {pushControl}
              </Row>
              <Row
                icon={<BellRingIcon />}
                title={t("delivery.desktop")}
                hint={
                  perm === "denied"
                    ? t("delivery.blocked")
                    : perm === "unsupported"
                      ? t("delivery.unsupported")
                      : t("delivery.desktopHint")
                }
              >
                {perm === "granted" ? (
                  <Badge variant="secondary" data-desktop-state="granted">
                    {t("delivery.desktopAllowed")}
                  </Badge>
                ) : perm === "default" ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void requestPerm()}
                    data-allow-desktop
                  >
                    {t("alerts.enableDesktop")}
                  </Button>
                ) : null}
              </Row>
              <Row
                icon={<Volume2Icon />}
                title={t("delivery.chime")}
                hint={t("delivery.chimeHint")}
              >
                <Switch
                  checked={!muted}
                  onCheckedChange={(v) => setMuted(!v)}
                  aria-label={t("delivery.chime")}
                  data-chime-toggle
                />
              </Row>
            </div>
          </section>

          <section aria-labelledby="devices" className="pt-8 pb-8" data-section="devices">
            <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-3">
              <div>
                <h2 id="devices" className="text-base font-medium">
                  {t("delivery.devices")}
                </h2>
                <p className="text-sm text-muted-foreground">{t("delivery.devicesHint")}</p>
              </div>
              {status.data.configured && devices.length ? (
                <div className="flex items-center gap-2">
                  <span className="hidden text-xs text-muted-foreground sm:inline">
                    {t("delivery.testHint")}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy}
                    onClick={() => void test()}
                    data-push-test
                  >
                    <SendIcon />
                    {busy === "test" ? t("common.working") : t("delivery.test")}
                  </Button>
                </div>
              ) : null}
            </div>
            {devices.length === 0 ? (
              <EmptyState title={t("delivery.noDevices")} />
            ) : (
              <div className="border-y border-nq-line">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="ps-6">{t("delivery.device")}</TableHead>
                      <TableHead className="hidden sm:table-cell">{t("delivery.added")}</TableHead>
                      <TableHead>{t("delivery.lastDelivered")}</TableHead>
                      <TableHead className="hidden md:table-cell">
                        {t("delivery.lastError")}
                      </TableHead>
                      <TableHead className="pe-6 text-end">
                        <span className="sr-only">{t("common.actions")}</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {devices.map((d) => {
                      const self = d.endpoint === endpoint
                      return (
                        <TableRow
                          key={d.id}
                          data-device={d.id}
                          data-device-self={self ? "true" : "false"}
                        >
                          <TableCell className="ps-6">
                            <span className="flex items-center gap-2">
                              <span className="font-medium" title={d.user_agent}>
                                {uaLabel(d.user_agent, t)}
                              </span>
                              {self ? (
                                <Badge variant="outline">{t("delivery.thisDeviceBadge")}</Badge>
                              ) : null}
                            </span>
                            {/* Below md the column is hidden; the problem still shows. */}
                            {d.last_error ? (
                              <span className="mt-0.5 block text-xs text-nq-danger-text md:hidden">
                                {d.last_error}
                              </span>
                            ) : null}
                          </TableCell>
                          <TableCell className="hidden text-muted-foreground sm:table-cell">
                            <Ltr>{formatDate(d.created_at)}</Ltr>
                          </TableCell>
                          <TableCell className="text-muted-foreground">
                            {d.last_ok_at ? (
                              <Ltr>{formatDate(d.last_ok_at)}</Ltr>
                            ) : (
                              t("delivery.never")
                            )}
                          </TableCell>
                          <TableCell
                            className="hidden max-w-64 truncate text-muted-foreground md:table-cell"
                            title={d.last_error}
                          >
                            {d.last_error || "—"}
                          </TableCell>
                          <TableCell className="pe-6 text-end">
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-nq-danger-text"
                              disabled={!!busy}
                              onClick={() => void remove(d, self)}
                            >
                              {t("delivery.remove")}
                            </Button>
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </div>
            )}
          </section>
        </>
      )}
      {/* MH-411: a second channel for the same events, the member's own number. */}
      {ws.current ? <WhatsAppNotifySection /> : null}
      {/* The trends digest (MH-389): for anyone who reads the trends center. */}
      {ws.current && ws.can("trends:read") ? <TrendsDigestSection /> : null}
    </>
  )
}
