import { useInfiniteQuery } from "@tanstack/react-query"
import { useMemo, useState } from "react"

import { useNav, useSession } from "../app"
import { api } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, Empty, ErrorView, Header, Loading, Segmented, Tag, useDebounced } from "../ui"

type Tab = "contacts" | "companies"

export function ContactsScreen({ tab }: { tab: Tab }) {
  const { t } = usePrefs()
  const { go } = useNav()
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim(), 300)
  return (
    <>
      <Header
        title={t(tab === "contacts" ? "contacts.contacts" : "contacts.companies")}
        end={
          <Segmented<Tab>
            value={tab}
            onChange={(v) => go({ screen: "contacts", tab: v })}
            options={[
              { value: "contacts", label: t("contacts.contacts") },
              { value: "companies", label: t("contacts.companies") },
            ]}
          />
        }
      />
      <div className="toolbar">
        <input className="search" type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t(tab === "contacts" ? "contacts.searchContacts" : "contacts.searchCompanies")} />
      </div>
      {tab === "contacts" ? <ContactList q={q} /> : <CompanyList q={q} />}
    </>
  )
}

function useMore(list: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => unknown }) {
  return (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    if (el.scrollTop + el.clientHeight > el.scrollHeight - 200 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage()
  }
}

function ContactList({ q }: { q: string }) {
  const { t } = usePrefs()
  const { ws } = useSession()
  const { go } = useNav()
  const list = useInfiniteQuery({
    queryKey: ["contacts", ws, q],
    queryFn: ({ pageParam }) => api.contacts(q || undefined, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.contacts ?? []) ?? [], [list.data])
  const more = useMore(list)
  if (list.isLoading) return <Loading />
  if (list.isError) return <ErrorView onRetry={() => void list.refetch()} />
  if (!rows.length) return <Empty title={t("contacts.empty")} />
  return (
    <div className="scroll" onScroll={more}>
      {rows.map((x) => (
        <button key={x.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "contact", id: x.id })} data-contact-row={x.id}>
          <Avatar name={x.name || x.email} url={x.avatar_url} />
          <span className="body">
            <span className="m ellipsis">{x.name || x.email || x.phone}</span>
            <span className="small muted ellipsis">{[x.title, x.company_name].filter(Boolean).join(" · ") || x.email || x.phone}</span>
          </span>
          <span className="small muted ellipsis" dir="ltr" style={{ width: 220 }}>
            {x.email || x.phone}
          </span>
          {x.lifecycle ? <Tag text={t(`lifecycle.${x.lifecycle}` as never)} /> : null}
        </button>
      ))}
    </div>
  )
}

function CompanyList({ q }: { q: string }) {
  const { t } = usePrefs()
  const { ws } = useSession()
  const { go } = useNav()
  const list = useInfiniteQuery({
    queryKey: ["companies", ws, q],
    queryFn: ({ pageParam }) => api.companies(q || undefined, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.companies ?? []) ?? [], [list.data])
  const more = useMore(list)
  if (list.isLoading) return <Loading />
  if (list.isError) return <ErrorView onRetry={() => void list.refetch()} />
  if (!rows.length) return <Empty title={t("contacts.empty")} />
  return (
    <div className="scroll" onScroll={more}>
      {rows.map((x) => (
        <button key={x.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "company", id: x.id })} data-company-row={x.id}>
          <Avatar name={x.name} />
          <span className="body">
            <span className="m ellipsis">{x.name}</span>
            <span className="small muted ellipsis">{[x.domain, x.industry, x.country].filter(Boolean).join(" · ")}</span>
          </span>
          <Tag text={`${x.contact_count} · ${t("contacts.contacts")}`} />
        </button>
      ))}
    </div>
  )
}
