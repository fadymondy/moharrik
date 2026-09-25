"use client"

import { Suspense, useEffect, useState, type ReactNode } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import {
  ChartLineIcon,
  CheckCircle2Icon,
  RefreshCwIcon,
  SearchIcon,
  TriangleAlertIcon,
  XIcon,
  YoutubeIcon,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { Ga4Conversions } from "@/components/google/ga4-conversions"
import { ResourcePicker } from "@/components/google/resource-picker"
import { Ltr } from "@/components/copy-field"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
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
  api,
  ApiError,
  GOOGLE_PRODUCTS,
  type GoogleConnection,
  type GoogleDisconnected,
  type GoogleProduct,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useGoogleConnections } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

// What the callback can answer (internal/google, Result*). Anything else reads as "failed".
const RESULTS = [
  "connected",
  "cancelled",
  "access_denied",
  "state",
  "forbidden",
  "failed",
  "no_refresh",
  "scope",
  "no_identity",
  "not_configured",
]

// Partial: "contacts" has no row here (its own panel lives on the vCard
// import page instead, see GoogleContactsPanel), so it needs no icon.
const PRODUCT_ICON: Partial<Record<GoogleProduct, ReactNode>> = {
  analytics: <ChartLineIcon />,
  search_console: <SearchIcon />,
  youtube: <YoutubeIcon />,
}

export default function IntegrationsPage() {
  return (
    <Suspense>
      <Integrations />
    </Suspense>
  )
}

function Integrations() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.integrations")} · ${ws.summary.name}`)
  const canManage = ws.can("integrations:manage")
  const google = useGoogleConnections(ws.id, canManage)
  const outcome = useCallbackOutcome()

  const list = google.data?.connections ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.integrations")}
        description={t("integrations.hint")}
      />
      {outcome.result ? (
        <OutcomeBanner result={outcome.result} product={outcome.product} onClose={outcome.clear} />
      ) : null}

      {!ws.current ? (
        <LoadingRows rows={2} />
      ) : !canManage ? (
        <p className="px-6 py-6 text-sm text-grid-muted" data-integrations="admin-only">
          {t("integrations.adminOnly")}
        </p>
      ) : google.error ? (
        <ErrorState error={google.error} />
      ) : !google.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <section className="px-6 pb-2">
            <h2 className="flex items-center gap-2 text-base font-medium">
              <GoogleMark />
              {t("integrations.google")}
            </h2>
            <p className="mt-1 max-w-3xl text-sm text-grid-muted">{t("integrations.googleHint")}</p>
          </section>

          {!google.data.configured ? (
            <NotConfigured redirectURL={google.data.redirect_url} />
          ) : (
            <ConnectProducts />
          )}

          {list.length > 0 ? (
            <ResourcePicker emails={Object.fromEntries(list.map((c) => [c.id, c.email]))} />
          ) : null}

          <div className="grid-separator-contained hatch-band mt-6" aria-hidden />
          <section className="pt-6 pb-10">
            <h3 className="px-6 pb-3 text-sm font-medium">{t("integrations.connected")}</h3>
            {list.length === 0 ? (
              <EmptyState
                title={t("integrations.none")}
                body={google.data.configured ? t("integrations.noneBody") : undefined}
              />
            ) : (
              <div className="border-y border-line">
                <Table data-google-connections={list.length}>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="ps-6">{t("integrations.account")}</TableHead>
                      <TableHead>{t("integrations.products")}</TableHead>
                      <TableHead className="hidden sm:table-cell">
                        {t("integrations.status")}
                      </TableHead>
                      <TableHead className="hidden md:table-cell">
                        {t("integrations.connectedBy")}
                      </TableHead>
                      <TableHead className="pe-6 text-end">
                        <span className="sr-only">{t("common.actions")}</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {list.map((c) => (
                      <ConnectionRow
                        key={c.id}
                        connection={c}
                        configured={google.data!.configured}
                        onChange={() => google.mutate()}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </section>

          <div className="grid-separator-contained hatch-band" aria-hidden />
          <Ga4Conversions
            googleConnected={list.some((c) => (c.products ?? []).includes("analytics"))}
          />
        </>
      )}
    </>
  )
}

/** Reads ?google=…&product=… once, then clears them from the address bar so a reload does not repeat it. */
function useCallbackOutcome() {
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const [state, setState] = useState<{ result: string; product: string } | null>(null)

  useEffect(() => {
    const raw = params.get("google")
    if (!raw) return
    setState({
      result: RESULTS.includes(raw) ? raw : "failed",
      product: params.get("product") ?? "",
    })
    router.replace(pathname, { scroll: false })
  }, [params, router, pathname])

  return { result: state?.result, product: state?.product ?? "", clear: () => setState(null) }
}

function OutcomeBanner({
  result,
  product,
  onClose,
}: {
  result: string
  product: string
  onClose: () => void
}) {
  const { t } = useTranslations()
  const ok = result === "connected"
  const productName = GOOGLE_PRODUCTS.includes(product as GoogleProduct)
    ? t(`integrations.product.${product}`)
    : t("integrations.google")
  return (
    <div
      role={ok ? "status" : "alert"}
      data-google-result={result}
      className="mx-6 mb-4 flex items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm"
    >
      {ok ? (
        <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-grid-brand" />
      ) : (
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger" />
      )}
      <p className={ok ? "text-grid-fg" : "text-grid-danger-text"}>
        {t(`integrations.result.${result}`, { product: productName })}
      </p>
      <Button
        variant="ghost"
        size="icon-sm"
        className="ms-auto -my-1"
        onClick={onClose}
        aria-label={t("integrations.dismiss")}
      >
        <XIcon />
      </Button>
    </div>
  )
}

function NotConfigured({ redirectURL }: { redirectURL: string }) {
  const { t } = useTranslations()
  return (
    <div
      role="note"
      data-google-configured="false"
      className="mx-6 mt-4 flex max-w-3xl items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm"
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
      <div className="min-w-0 space-y-1 [overflow-wrap:anywhere]">
        <p className="font-medium text-grid-fg">{t("integrations.notConfigured")}</p>
        <p className="text-grid-muted">{t("integrations.notConfiguredBody")}</p>
        {redirectURL ? (
          <p className="text-grid-muted">
            {t("integrations.redirectURL")} <Ltr className="break-all">{redirectURL}</Ltr>
          </p>
        ) : null}
      </div>
    </div>
  )
}

async function startConnect(
  workspaceId: string,
  product: GoogleProduct,
  locale: string,
  opts: { monetary?: boolean; connection?: string } = {},
) {
  const q = new URLSearchParams({ product, locale })
  if (opts.monetary) q.set("monetary", "true")
  if (opts.connection) q.set("connection", opts.connection)
  const res = await api<{ url: string }>(`/api/w/google/connect?${q}`, { workspaceId })
  // A full navigation: Google's consent screen, then back to this page.
  window.location.assign(res.url)
}

function ConnectProducts() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState<GoogleProduct | null>(null)
  const [revenue, setRevenue] = useState(false)

  async function connect(product: GoogleProduct) {
    setBusy(product)
    try {
      await startConnect(ws.id, product, locale, { monetary: product === "youtube" && revenue })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(null)
    }
  }

  return (
    <ul className="mt-4 divide-y divide-line border-y border-line" data-google-configured="true">
      {GOOGLE_PRODUCTS.map((p) => (
        <li
          key={p}
          className="flex flex-col gap-3 px-6 py-4 sm:flex-row sm:items-center"
          data-google-product={p}
        >
          <span className="flex size-9 shrink-0 items-center justify-center border border-line text-grid-muted [&_svg]:size-4">
            {PRODUCT_ICON[p]}
          </span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">{t(`integrations.product.${p}`)}</p>
            <p className="text-sm text-grid-muted">{t(`integrations.product.${p}.hint`)}</p>
            {p === "youtube" ? (
              <Label className="mt-2 flex w-fit items-center gap-2 text-sm font-normal text-grid-muted">
                <Checkbox
                  checked={revenue}
                  onCheckedChange={(v) => setRevenue(v === true)}
                  data-revenue
                />
                {t("integrations.revenue")}
              </Label>
            ) : null}
          </div>
          <Button
            variant="outline"
            onClick={() => connect(p)}
            disabled={busy !== null}
            data-connect={p}
          >
            {busy === p ? t("integrations.redirecting") : t("integrations.connect")}
          </Button>
        </li>
      ))}
    </ul>
  )
}

function ConnectionRow({
  connection: c,
  configured,
  onChange,
}: {
  connection: GoogleConnection
  configured: boolean
  onChange: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const products = c.products ?? []
  const reauth = c.status === "reauth"

  async function reconnect() {
    setBusy(true)
    try {
      await startConnect(ws.id, products[0] ?? "analytics", locale, { connection: c.id })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  async function disconnect() {
    try {
      const res = await api<GoogleDisconnected>(
        `/api/w/google/connections/${encodeURIComponent(c.id)}`,
        { method: "DELETE", workspaceId: ws.id },
      )
      if (res.revoked) toast.success(t("integrations.disconnected"))
      else if (res.shared) toast.success(t("integrations.disconnectedShared"))
      else toast.warning(t("integrations.disconnectedNoRevoke"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const status = reauth ? (
    <div className="space-y-1 whitespace-normal">
      <Badge variant="destructive">{t("integrations.statusReauth")}</Badge>
      <p className="max-w-56 text-xs text-grid-muted">{t("integrations.statusReauthHint")}</p>
    </div>
  ) : (
    <Badge variant="outline">{t("integrations.statusOk")}</Badge>
  )

  return (
    <TableRow data-google-connection={c.email} data-status={c.status || "ok"}>
      <TableCell className="ps-6 font-medium">
        <Ltr>{c.email || "—"}</Ltr>
        {/* Narrow screens: the status column is hidden, so it sits under the account. */}
        <div className="mt-1 font-normal sm:hidden">{status}</div>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap gap-1">
          {products.length === 0 ? (
            <span className="text-grid-muted">{t("integrations.noProducts")}</span>
          ) : null}
          {products.map((p) => (
            <Badge key={p} variant="secondary">
              {t(`integrations.product.${p}`)}
            </Badge>
          ))}
          {c.revenue ? <Badge variant="outline">{t("integrations.revenueBadge")}</Badge> : null}
        </div>
      </TableCell>
      <TableCell className="hidden sm:table-cell">{status}</TableCell>
      <TableCell className="hidden text-grid-muted md:table-cell">
        <Ltr>{c.connected_by_email || "—"}</Ltr>
      </TableCell>
      <TableCell className="pe-6 text-end">
        <div className="flex flex-wrap items-center justify-end gap-1">
          {reauth && configured ? (
            <Button variant="outline" size="sm" onClick={reconnect} disabled={busy}>
              <RefreshCwIcon />
              {busy ? t("integrations.redirecting") : t("integrations.reconnect")}
            </Button>
          ) : null}
          <ConfirmButton
            label={t("integrations.disconnect")}
            title={t("integrations.disconnectTitle", { email: isolate(c.email) })}
            description={t("integrations.disconnectBody")}
            confirmLabel={t("integrations.disconnect")}
            onConfirm={disconnect}
          />
        </div>
      </TableCell>
    </TableRow>
  )
}

/** Google's "G", drawn in the current text colour: a brand mark, not a Moharrik colour. */
function GoogleMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden className="size-4 fill-current text-grid-muted">
      <path d="M21.35 11.1H12v2.98h5.35c-.23 1.5-1.66 4.4-5.35 4.4-3.22 0-5.85-2.67-5.85-5.96S8.78 6.56 12 6.56c1.83 0 3.06.78 3.76 1.45l2.57-2.47C16.68 4 14.55 3 12 3 7.03 3 3 7.03 3 12s4.03 9 9 9c5.2 0 8.64-3.65 8.64-8.8 0-.59-.06-1.04-.14-1.5z" />
    </svg>
  )
}
