import { useQuery } from "@tanstack/react-query"
import { Globe, MessageSquare } from "lucide-react"

import { ago, channelName, money, stageName } from "@moharrik/client"

import { useNav, useSession } from "../app"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, ErrorView, Header, Loading, Section, Tag } from "../ui"
import { BackButton } from "./contact"

export function CompanyScreen({ id }: { id: string }) {
  const { t, locale } = usePrefs()
  const { ws } = useSession()
  const { go } = useNav()
  const page = useQuery({ queryKey: ["company", id, ws], queryFn: () => api.company(id) })
  const x = page.data?.company
  return (
    <>
      <Header title={x?.name ?? ""} sub={t("contacts.companies")} start={<BackButton />} />
      {page.isLoading ? (
        <Loading />
      ) : page.isError || !x ? (
        <ErrorView onRetry={() => void page.refetch()} />
      ) : (
        <div className="scroll" style={{ paddingBottom: 28 }}>
          <div className="profile">
            <Avatar name={x.name} size={76} />
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <h2>{x.name}</h2>
              <div className="tags">
                {[x.industry, x.country, x.size].filter(Boolean).map((v) => (
                  <Tag key={v} text={v} />
                ))}
              </div>
              {x.domain ? (
                <button className="line action" style={{ border: 0, background: "none", padding: 0 }} onClick={() => void invoke("app:openExternal", `https://${x.domain}`)}>
                  <Globe size={15} />
                  <span dir="ltr">{x.domain}</span>
                </button>
              ) : null}
            </div>
          </div>
          <div className="cols">
            <div>
              {(page.data?.contacts ?? []).length ? (
                <Section title={t("contacts.contacts")}>
                  {(page.data?.contacts ?? []).map((p) => (
                    <button key={p.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "contact", id: p.id })}>
                      <Avatar name={p.name || p.email} size={32} />
                      <span className="body">
                        <span className="ellipsis">{p.name || p.email}</span>
                        {p.title ? <span className="small muted">{p.title}</span> : null}
                      </span>
                    </button>
                  ))}
                </Section>
              ) : null}
            </div>
            <div>
              {(page.data?.deals ?? []).length ? (
                <Section title={t("deals.title")}>
                  {(page.data?.deals ?? []).map((d) => (
                    <div key={d.id} className="row static" style={{ alignItems: "center" }}>
                      <span className="body">
                        <span className="ellipsis">{d.title}</span>
                        <span className="small muted">{stageName(t, d.stage_name)}</span>
                      </span>
                      <span className="b action">{money(locale, d.value_minor, d.currency)}</span>
                    </div>
                  ))}
                </Section>
              ) : null}
              {(page.data?.conversations ?? []).length ? (
                <Section title={t("contacts.conversations")}>
                  {(page.data?.conversations ?? []).map((cv) => (
                    <button key={cv.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "conversation", id: cv.id })}>
                      <MessageSquare size={18} className="muted" />
                      <span className="body">
                        <span className="ellipsis">{cv.customer_name || cv.subject || cv.preview}</span>
                        <span className="small muted">
                          {channelName(t, cv.channel)} · {ago(t, locale, cv.last_message_at)}
                        </span>
                      </span>
                    </button>
                  ))}
                </Section>
              ) : null}
            </div>
          </div>
        </div>
      )}
    </>
  )
}
