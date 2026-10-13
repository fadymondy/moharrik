// A contact card: who they are, their conversations and deals, and the timeline.
import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { ArrowLeft, Building2, Mail, MessageSquare, Phone } from "lucide-react"
import { useMemo } from "react"

import { ago, channelName, eventText, money, type ContactEvent } from "@moharrik/client"

import { useNav, useSession } from "../app"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, ErrorView, Header, IconButton, Loading, Section, Tag } from "../ui"

export function BackButton() {
  const { t } = usePrefs()
  const { back } = useNav()
  return (
    <IconButton label={t("common.back")} onClick={back}>
      <ArrowLeft size={20} className="mirror" />
    </IconButton>
  )
}

export function ContactScreen({ id }: { id: string }) {
  const { t, locale } = usePrefs()
  const { ws } = useSession()
  const { go } = useNav()
  const page = useQuery({ queryKey: ["contact", id, ws], queryFn: () => api.contact(id) })
  const timeline = useInfiniteQuery({
    queryKey: ["timeline", id, ws],
    queryFn: ({ pageParam }) => api.timeline(id, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const events = useMemo(() => timeline.data?.pages.flatMap((p) => p.events ?? []) ?? [], [timeline.data])

  if (page.isLoading)
    return (
      <>
        <Header title="" start={<BackButton />} />
        <Loading />
      </>
    )
  const x = page.data?.contact
  if (page.isError || !x)
    return (
      <>
        <Header title="" start={<BackButton />} />
        <ErrorView onRetry={() => void page.refetch()} />
      </>
    )
  const convs = page.data?.conversations ?? []
  const deals = page.data?.deals ?? []

  return (
    <>
      <Header title={x.name || x.email} sub={t("contacts.contacts")} start={<BackButton />} />
      <div className="scroll" data-contact-card={x.id}>
        <div className="profile">
          <Avatar name={x.name || x.email} url={x.avatar_url || x.photo_url} size={76} />
          <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
            <h2 className="selectable">{x.name || x.email || x.phone}</h2>
            {x.title || x.company?.name ? <div className="muted">{[x.title, x.company?.name].filter(Boolean).join(" · ")}</div> : null}
            <div className="tags">
              {x.lifecycle ? <Tag text={t(`lifecycle.${x.lifecycle}` as never)} /> : null}
              {x.score != null ? <Tag text={`${t("contacts.score")} ${x.score}`} /> : null}
            </div>
          </div>
        </div>
        <div className="cols">
          <div>
            <Section title={t("contacts.contacts")} list={false}>
              <div className="fields">
                {x.email ? (
                  <>
                    <Mail size={16} className="action" />
                    <span className="small muted">{t("contacts.email")}</span>
                    <button dir="ltr" className="ellipsis selectable" onClick={() => void invoke("app:openExternal", `mailto:${x.email}`)}>
                      {x.email}
                    </button>
                  </>
                ) : null}
                {x.phone ? (
                  <>
                    <Phone size={16} className="action" />
                    <span className="small muted">{t("contacts.phone")}</span>
                    <button dir="ltr" className="selectable" onClick={() => void invoke("app:openExternal", `tel:${x.phone}`)}>
                      {x.phone}
                    </button>
                  </>
                ) : null}
                {x.company?.id ? (
                  <>
                    <Building2 size={16} className="action" />
                    <span className="small muted">{t("contacts.company")}</span>
                    <button onClick={() => go({ screen: "company", id: x.company.id })}>{x.company.name}</button>
                  </>
                ) : null}
              </div>
            </Section>
            {convs.length ? (
              <Section title={t("contacts.conversations")}>
                {convs.map((cv) => (
                  <button key={cv.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "conversation", id: cv.id })}>
                    <MessageSquare size={18} className="muted" />
                    <span className="body">
                      <span className="ellipsis">{cv.subject || cv.preview || channelName(t, cv.channel)}</span>
                      <span className="small muted">
                        {channelName(t, cv.channel)} · {ago(t, locale, cv.last_message_at)}
                      </span>
                    </span>
                  </button>
                ))}
              </Section>
            ) : null}
            {deals.length ? (
              <Section title={t("deals.title")}>
                {deals.map((d) => (
                  <button key={d.id} className="row" style={{ alignItems: "center" }} onClick={() => go({ screen: "deals" })}>
                    <span className="body">
                      <span className="ellipsis">{d.title}</span>
                      <span className="small muted">{d.stage_name}</span>
                    </span>
                    <span className="b action">{money(locale, d.value_minor, d.currency)}</span>
                  </button>
                ))}
              </Section>
            ) : null}
          </div>
          <Section title={t("contacts.timeline")} list={false}>
            <div style={{ margin: "-6px -16px" }}>
              {events.map((e) => (
                <EventRow key={e.id} e={e} />
              ))}
              {timeline.isFetching ? <Loading /> : null}
              {!timeline.isLoading && !events.length ? <div className="pad muted">{t("contacts.timelineEmpty")}</div> : null}
              {timeline.hasNextPage && !timeline.isFetching ? (
                <div className="pad">
                  <button className="chip" onClick={() => void timeline.fetchNextPage()}>
                    {t("common.more")}
                  </button>
                </div>
              ) : null}
            </div>
          </Section>
        </div>
      </div>
    </>
  )
}

function EventRow({ e }: { e: ContactEvent }) {
  const { t, locale } = usePrefs()
  const { title, body } = eventText(t, e)
  return (
    <div className="event">
      <div className="rail">
        <i className={e.kind.startsWith("deal.won") ? "won" : ""} />
        <span />
      </div>
      <div className="flex1" style={{ display: "flex", flexDirection: "column", gap: 2, paddingBottom: 6, minWidth: 0 }}>
        <div className="line">
          <span className="m flex1">{title}</span>
          <span className="xs muted">{ago(t, locale, e.at)}</span>
        </div>
        {body ? <span className="small bodytone selectable">{body}</span> : null}
      </div>
    </div>
  )
}
