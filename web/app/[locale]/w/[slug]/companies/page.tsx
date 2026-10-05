"use client"

import { useRouter } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"
import { Building2Icon, Trash2Icon } from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
  CardMeta,
  CursorPager,
  EmptyState as NqEmptyState,
  EntityIdentity,
  EntityList,
  Field,
  FieldLabel,
  Input,
  PersonCell,
  toast,
  type DataTableColumn,
  type DataTableRowAction,
  type EntityFacet,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { useDebounced } from "@/components/crm/contact-picker"
import { pageRange, useCursorStack } from "@/components/cursor-pager"

import { useMemberNames } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { SuggestionsPanel } from "@/components/research/suggestions"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Company } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompaniesPage } from "@/lib/queries"
import { ScoreButton } from "@/components/crm/icp"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

const PAGE_SIZE = 50
const MIN_SCORES = [50, 75] as const

export default function CompaniesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.companies")} · ${ws.summary.name}`)
  const canWrite = ws.can("contacts:write")
  const [version, setVersion] = useState(0)
  const changed = () => setVersion((v) => v + 1)

  return (
    <>
      <SectionHeader
        title={t("nav.companies")}
        description={t("companies.hint")}
      />

      {/* What research found (MH-388): suggestions only, never added on their own. */}
      <SuggestionsPanel onAccepted={changed} />

      <CompaniesBrowser version={version} onChange={changed} />

      <div className="hatch h-8 border-y border-border mt-8" aria-hidden />

      {canWrite ? (
        <AddCompany onAdded={changed} />
      ) : (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("companies.readOnly")}</p>
      )}
      <div className="pb-8" />
    </>
  )
}

/*
The companies on Nasaq's EntityList in server mode (MH-1126), the look of Nasaq's
CompanyList: the server searches, filters by score, orders and pages (MH-467),
the list only shows the page it was given.
*/
function CompaniesBrowser({ version, onChange }: { version: number; onChange: () => void }) {
  const { t, locale, formatNumber, formatDate } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const members = useMemberNames(ws.id)
  const base = `/${locale}/w/${ws.slug}`
  const canWrite = ws.can("contacts:write")

  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim())
  // MH-395: best fit first, and a minimum score.
  const [sort, setSort] = useState<"" | "score">("")
  const [minScore, setMinScore] = useState(0)
  const pager = useCursorStack(`${q}|${sort}|${minScore}`)
  const companies = useCompaniesPage(ws.id, {
    q,
    sort,
    minScore,
    cursor: pager.cursor,
    limit: PAGE_SIZE,
  })
  const { mutate } = companies
  useEffect(() => {
    if (version > 0) void mutate()
  }, [version, mutate])
  const rows = companies.data?.companies ?? []
  const total = companies.data?.total ?? 0
  const { from, to, pages } = pageRange(pager.index, PAGE_SIZE, rows.length, total)

  const [deleting, setDeleting] = useState<Company | null>(null)
  const [busy, setBusy] = useState(false)

  async function remove(c: Company) {
    setBusy(true)
    try {
      await api(`/api/w/companies/${encodeURIComponent(c.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("companies.deleted"))
      setDeleting(null)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const owner = (c: Company) =>
    c.owner_user_id ? { name: members.name(c.owner_user_id) } : undefined

  // The server takes one minimum score: picking a second replaces the first.
  const facets: EntityFacet<Company>[] = [
    {
      id: "score",
      title: t("icp.score"),
      options: MIN_SCORES.map((n) => ({
        value: String(n),
        label: t("icp.atLeast", { n: formatNumber(n) }),
      })),
      getValues: () => [],
    },
  ]
  const facetValues: Record<string, string[]> = { score: minScore ? [String(minScore)] : [] }
  const onFacetValuesChange = (next: Record<string, string[]>) => {
    const values = next.score ?? []
    const picked = values.find((v) => !facetValues.score.includes(v)) ?? values[0] ?? ""
    setMinScore(Number(picked) || 0)
  }

  const columns: DataTableColumn<Company>[] = [
    {
      id: "name",
      header: t("companies.title"),
      hideable: false,
      className: "min-w-56",
      cell: (c) => (
        <span data-company-row={c.name} className="block min-w-0">
          <EntityIdentity
            name={c.name}
            avatarName={c.name}
            shape="square"
            subtitle={c.domain ? <Ltr>{c.domain}</Ltr> : undefined}
          />
        </span>
      ),
    },
    {
      id: "industry",
      header: t("companies.industry"),
      cell: (c) =>
        c.industry ? <Bdi>{c.industry}</Bdi> : <span className="text-muted-foreground">—</span>,
    },
    {
      id: "score",
      header: t("icp.score"),
      // The server orders by score; name order is the default.
      sortValue: (c) => c.score,
      cell: (c) => <ScoreButton score={c.score} reasons={c.score_reasons} who={isolate(c.name)} />,
    },
    {
      id: "contacts",
      header: t("companies.contacts"),
      align: "end",
      cell: (c) => <span className="tabular-nums">{formatNumber(c.contact_count)}</span>,
    },
    {
      id: "deals",
      header: t("companies.openDeals"),
      align: "end",
      cell: (c) => <span className="tabular-nums">{formatNumber(c.open_deals)}</span>,
    },
    {
      id: "owner",
      header: t("crm.owner"),
      cell: (c) => <PersonCell person={owner(c)} />,
    },
    {
      id: "added",
      header: t("contacts.added"),
      align: "end",
      defaultHidden: true,
      cell: (c) => (
        <span className="text-muted-foreground">
          <Ltr>{formatDate(c.created_at)}</Ltr>
        </span>
      ),
    },
  ]

  const rowActions = (c: Company): DataTableRowAction[] => [
    {
      id: "delete",
      label: t("common.delete"),
      icon: Trash2Icon,
      danger: true,
      group: "danger",
      onSelect: () => setDeleting(c),
    },
  ]

  const filtered = !!q || minScore > 0

  return (
    <section data-companies-browser className="px-4 pt-6 sm:px-6">
      <EntityList<Company>
        data={rows}
        columns={columns}
        getRowId={(c) => c.id}
        rowLabel={(c) => c.name}
        label={t("nav.companies")}
        manual
        rowCount={total}
        selectable={false}
        query={{ value: search, onChange: setSearch }}
        searchPlaceholder={t("companies.search")}
        facets={facets}
        facetValues={facetValues}
        onFacetValuesChange={onFacetValuesChange}
        sort={{
          value: sort === "score" ? { id: "score", direction: "desc" } : null,
          onChange: (s) => setSort(s ? "score" : ""),
        }}
        rowActions={canWrite ? rowActions : undefined}
        onRowClick={(c) => router.push(`${base}/companies/${c.id}`)}
        loading={!companies.data && !companies.error}
        error={
          companies.error
            ? companies.error instanceof Error
              ? companies.error.message
              : t("common.networkError")
            : undefined
        }
        onRetry={() => void mutate()}
        empty={
          <NqEmptyState
            icon={Building2Icon}
            title={filtered ? t("companies.noMatch") : t("companies.empty")}
            description={!filtered && canWrite ? t("companies.emptyBody") : undefined}
            className="border-0"
          />
        }
        renderCard={(c) => (
          <div className="flex min-w-0 flex-col gap-3" data-company-card={c.name}>
            <EntityIdentity
              className="pe-(--entity-card-controls)"
              name={c.name}
              avatarName={c.name}
              shape="square"
              size="lg"
              subtitle={c.domain ? <Ltr>{c.domain}</Ltr> : c.industry || undefined}
            />
            <div className="flex flex-col gap-1.5">
              {c.industry ? (
                <CardMeta label={t("companies.industry")}>
                  <Bdi>{c.industry}</Bdi>
                </CardMeta>
              ) : null}
              <CardMeta label={t("icp.score")}>
                <ScoreButton score={c.score} reasons={c.score_reasons} who={isolate(c.name)} />
              </CardMeta>
              <CardMeta label={t("companies.contacts")}>
                <span className="tabular-nums">{formatNumber(c.contact_count)}</span>
              </CardMeta>
              <CardMeta label={t("companies.openDeals")}>
                <span className="tabular-nums">{formatNumber(c.open_deals)}</span>
              </CardMeta>
              <CardMeta label={t("crm.owner")}>
                <PersonCell person={owner(c)} />
              </CardMeta>
            </div>
          </div>
        )}
        pagination={
          companies.data && total > 0 ? (
            <div
              className="flex flex-wrap items-center justify-between gap-3"
              data-pager-current={companies.current ? "yes" : "no"}
            >
              <p
                className="text-sm text-muted-foreground"
                aria-live="polite"
                data-pager-summary="companies"
                data-range={`${from}-${to}/${total}`}
              >
                {t("common.showing", {
                  from: formatNumber(from),
                  to: formatNumber(to),
                  total: formatNumber(total),
                })}
              </p>
              {pages > 1 || pager.index > 0 ? (
                <CursorPager
                  data-pager="companies"
                  from={0}
                  to={0}
                  summary={
                    <span data-page-of={`${pager.index + 1}/${pages}`}>
                      {t("common.pageOf", {
                        page: formatNumber(pager.index + 1),
                        pages: formatNumber(pages),
                      })}
                    </span>
                  }
                  previousLabel={t("common.prevPage")}
                  nextLabel={t("common.nextPage")}
                  hasPrevious={pager.index > 0}
                  hasNext={!!companies.data.next_cursor}
                  loading={!companies.current}
                  onPrevious={pager.prev}
                  onNext={() => {
                    const next = companies.data?.next_cursor
                    if (next && companies.current) pager.next(next)
                  }}
                />
              ) : null}
            </div>
          ) : (
            false
          )
        }
        className={companies.current ? undefined : "[&_[data-slot=table]]:opacity-60"}
      />

      <AlertDialog open={deleting !== null} onOpenChange={(v) => (v ? null : setDeleting(null))}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("companies.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("companies.deleteBody", { name: isolate(deleting?.name ?? "") })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              disabled={busy}
              onClick={() => deleting && remove(deleting)}
            >
              {busy ? t("common.working") : t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function AddCompany({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [domain, setDomain] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/companies", {
        json: { name: name.trim(), domain: domain.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("companies.created"))
      setName("")
      setDomain("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-form="add-company">
      <h2 className="text-base font-medium">{t("companies.addTitle")}</h2>
      <form onSubmit={submit} noValidate className="mt-5">
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="company-name">{t("companies.name")}</FieldLabel>
            <Input
              id="company-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={200}
            />
          </Field>
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="company-domain">
              {t("companies.domain")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="company-domain"
              dir="ltr"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              placeholder="company.com"
            />
          </Field>
          <Button type="submit" disabled={busy || !name.trim()}>
            {busy ? t("common.working") : t("companies.create")}
          </Button>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}
