// One conversation: the thread, the reply box and the web toolbar's actions (close/reopen, snooze,
// assign, labels, mark unread).
//
// The reply box sends a REAL message to the customer on the conversation's channel (WhatsApp,
// email, chat…). Test only against a test conversation or a local stack.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Check, Clock, MailOpen, Paperclip, RotateCcw, SendHorizontal, Tag as TagIcon, UserRound } from "lucide-react"
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"

import { ago, channelName, type ConversationPatch, type Message } from "@moharrik/client"

import { useNav, useSession } from "../app"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { customerName, ErrorView, Header, IconButton, labelColor, Loading, Popover, Tag, toast } from "../ui"

type Menu = null | "snooze" | "assign" | "labels"

export function ConversationPane({ id }: { id: string }) {
  const { t, locale } = usePrefs()
  const { ws } = useSession()
  const { go, back } = useNav()
  const qc = useQueryClient()
  const [menu, setMenu] = useState<Menu>(null)
  const [draft, setDraft] = useState("")
  const end = useRef<HTMLDivElement>(null)

  // Its own messages on screen don't pop a notification (live.ts showing()).
  useEffect(() => {
    void invoke("app:screen", id)
    return () => void invoke("app:screen", "")
  }, [id])

  const detail = useQuery({ queryKey: ["conversation", id, ws], queryFn: () => api.conversation(id) })
  const conv = detail.data?.conversation
  const older = useInfiniteQuery({
    queryKey: ["olderMessages", id, detail.data?.messages_next_cursor ?? ""],
    queryFn: ({ pageParam }) => api.messages(id, pageParam),
    initialPageParam: detail.data?.messages_next_cursor ?? "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: false,
  })
  const members = useQuery({ queryKey: ["members", ws], queryFn: () => api.members(), staleTime: 5 * 60_000 })
  const labels = useQuery({ queryKey: ["labels", ws], queryFn: () => api.labels(), enabled: menu === "labels" })
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.me(), staleTime: 10 * 60_000 })

  // Oldest first: each page is in order, and each older page goes before the one fetched before it.
  const messages = useMemo(
    () => [...[...(older.data?.pages ?? [])].reverse().flatMap((p) => p.messages ?? []), ...(detail.data?.messages ?? [])],
    [detail.data, older.data],
  )
  const newest = messages[messages.length - 1]?.id
  // Braces: scrollIntoView returns a Promise in current Chromium, and an effect may only return a cleanup.
  useLayoutEffect(() => {
    void end.current?.scrollIntoView({ block: "end" })
  }, [newest])

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["conversation", id] })
    void qc.invalidateQueries({ queryKey: ["conversations", ws] })
  }
  const patch = useMutation({ mutationFn: (p: ConversationPatch) => api.updateConversation(id, p), onSuccess: invalidate, onError: () => toast(t("common.networkError")) })
  const organise = useMutation({ mutationFn: (ids: string[]) => api.organise(id, { label_ids: ids }), onSuccess: invalidate })
  const reply = useMutation({
    mutationFn: (body: string) => api.reply(id, body),
    onSuccess: () => {
      setDraft("")
      invalidate()
    },
    onError: () => toast(t("common.networkError")),
  })

  const memberName = (uid: string | null | undefined) => {
    if (!uid) return ""
    if (me.data && String(me.data.id) === uid) return t("inbox.you")
    const m = members.data?.members?.find((x) => x.user_id === uid)
    return m ? (m.email.split("@")[0] ?? m.email) : ""
  }

  if (detail.isLoading) return <Loading />
  if (detail.isError || !conv) return <ErrorView onRetry={() => void detail.refetch()} />

  const name = customerName(conv)
  const closed = conv.status === "closed"
  const selected = new Set((conv.labels ?? []).map((l) => l.id))
  const snoozeAt = (h: number | "tomorrow" | "week") => {
    const d = new Date()
    if (h === "tomorrow") {
      d.setDate(d.getDate() + 1)
      d.setHours(9, 0, 0, 0)
    } else if (h === "week") {
      d.setDate(d.getDate() + ((8 - d.getDay()) % 7 || 7))
      d.setHours(9, 0, 0, 0)
    } else d.setTime(d.getTime() + h * 3600_000)
    patch.mutate({ status: "snoozed", snoozed_until: d.toISOString() })
    setMenu(null)
  }
  const assign = (uid: string) => {
    patch.mutate({ assignee_user_id: uid })
    setMenu(null)
  }
  const send = () => draft.trim() && !reply.isPending && reply.mutate(draft.trim())
  const toggle = (m: Menu) => (open: boolean) => setMenu(open ? m : null)

  return (
    <>
      <Header
        title={
          conv.contact?.id ? (
            <button style={{ border: 0, background: "none", padding: 0, fontWeight: 700 }} onClick={() => go({ screen: "contact", id: conv.contact.id })}>
              {name}
            </button>
          ) : (
            name
          )
        }
        sub={[channelName(t, conv.channel), conv.subject, conv.assignee_user_id ? t("inbox.assignedTo", { name: memberName(conv.assignee_user_id) }) : t("inbox.unassigned")].filter(Boolean).join(" · ")}
        end={
          <>
            <Popover
              open={menu === "snooze"}
              onOpenChange={toggle("snooze")}
              button={
                <IconButton label={t("inbox.snooze")} onClick={() => setMenu(menu === "snooze" ? null : "snooze")}>
                  <Clock size={19} />
                </IconButton>
              }
            >
              <div className="label">{t("inbox.snooze")}</div>
              <button onClick={() => snoozeAt(1)}>{t("inbox.snooze1h")}</button>
              <button onClick={() => snoozeAt("tomorrow")}>{t("inbox.snoozeTomorrow")}</button>
              <button onClick={() => snoozeAt("week")}>{t("inbox.snoozeWeek")}</button>
            </Popover>
            <Popover
              open={menu === "assign"}
              onOpenChange={toggle("assign")}
              button={
                <IconButton label={t("inbox.assign")} onClick={() => setMenu(menu === "assign" ? null : "assign")}>
                  <UserRound size={19} />
                </IconButton>
              }
            >
              <div className="label">{t("inbox.assign")}</div>
              {me.data ? <button onClick={() => assign(String(me.data.id))}>{t("inbox.assignMe")}</button> : null}
              <button onClick={() => assign("")}>
                <span className="flex1">{t("inbox.unassigned")}</span>
                {!conv.assignee_user_id ? <Check size={16} className="action" /> : null}
              </button>
              <hr />
              {(members.data?.members ?? [])
                .filter((m) => m.status !== "removed")
                .map((m) => (
                  <button key={m.user_id} onClick={() => assign(m.user_id)}>
                    <span className="flex1 ellipsis">{m.email}</span>
                    {conv.assignee_user_id === m.user_id ? <Check size={16} className="action" /> : null}
                  </button>
                ))}
            </Popover>
            <Popover
              open={menu === "labels"}
              onOpenChange={toggle("labels")}
              button={
                <IconButton label={t("inbox.labels")} onClick={() => setMenu(menu === "labels" ? null : "labels")}>
                  <TagIcon size={19} />
                </IconButton>
              }
            >
              <div className="label">{t("inbox.labels")}</div>
              {labels.isLoading ? <div className="label">{t("common.loading")}</div> : null}
              {labels.data && !(labels.data.labels ?? []).length ? <div className="label">{t("inbox.noLabels")}</div> : null}
              {(labels.data?.labels ?? []).map((l) => (
                <button
                  key={l.id}
                  onClick={() => {
                    const next = new Set(selected)
                    if (next.has(l.id)) next.delete(l.id)
                    else next.add(l.id)
                    organise.mutate([...next])
                  }}
                >
                  <span className="dot" style={{ background: labelColor(l.color) }} />
                  <span className="flex1">{l.name}</span>
                  {selected.has(l.id) ? <Check size={16} className="action" /> : null}
                </button>
              ))}
            </Popover>
            <IconButton
              label={t("inbox.markUnread")}
              onClick={() => {
                patch.mutate({ unread: true })
                back()
              }}
            >
              <MailOpen size={19} />
            </IconButton>
            <button className={`btn ${closed ? "secondary" : ""}`} onClick={() => patch.mutate({ status: closed ? "open" : "closed" })} disabled={patch.isPending}>
              {closed ? <RotateCcw size={16} /> : <Check size={16} />}
              {closed ? t("inbox.reopen") : t("inbox.close")}
            </button>
          </>
        }
      />
      {conv.status === "snoozed" && conv.snoozed_until ? (
        <div className="banner">{t("inbox.snoozedUntil", { at: new Date(conv.snoozed_until).toLocaleString(locale === "ar" ? "ar-EG" : "en-GB") })}</div>
      ) : null}
      {(conv.labels ?? []).length ? (
        <div className="tags" style={{ padding: "8px 20px", borderBottom: "1px solid var(--line)" }}>
          {(conv.labels ?? []).map((l) => (
            <Tag key={l.id} text={l.name} color={labelColor(l.color)} />
          ))}
        </div>
      ) : null}
      <div className="thread">
        {detail.data?.messages_next_cursor && (!older.data || older.hasNextPage) ? (
          <button className="chip" style={{ alignSelf: "center" }} disabled={older.isFetching} onClick={() => void (older.data ? older.fetchNextPage() : older.refetch())}>
            {older.isFetching ? t("common.loading") : t("common.more")}
          </button>
        ) : null}
        {messages.length === 0 ? <div className="empty">{t("inbox.noMessages")}</div> : null}
        {messages.map((m) => (
          <Bubble key={m.id} m={m} author={memberName(m.author_user_id)} />
        ))}
        <div ref={end} />
      </div>
      <div className="composer">
        <div className="line">
          <textarea
            value={draft}
            rows={2}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={t("inbox.replyPlaceholder")}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault()
                send()
              }
            }}
          />
          <button className="btn" onClick={send} disabled={!draft.trim() || reply.isPending} aria-label={t("inbox.send")}>
            {reply.isPending ? <span className="spinner" /> : <SendHorizontal size={17} className="mirror" />}
            {reply.isPending ? t("inbox.sending") : t("inbox.send")}
          </button>
        </div>
        <span className="xs muted">{t("inbox.sendHint", { channel: channelName(t, conv.channel) })} · Ctrl+Enter</span>
      </div>
    </>
  )
}

function Bubble({ m, author }: { m: Message; author: string }) {
  const { t, locale } = usePrefs()
  const out = m.direction === "out"
  const who = out ? author || m.author_email : m.sender_name
  return (
    <div className={`msg ${out ? "out" : "in"}`} data-message={m.id}>
      <div className="bubble">
        {who ? <span className="xs m muted">{who}</span> : null}
        {m.subject ? <span className="small b">{m.subject}</span> : null}
        {m.body ? <span>{m.body.trim()}</span> : null}
        {(m.attachments ?? []).map((a) => (
          <a key={a.id} href={a.url || undefined} target="_blank" rel="noreferrer" className="small line" style={{ color: "inherit" }}>
            <Paperclip size={14} />
            <span className="ellipsis">{a.filename || t("inbox.attachment")}</span>
          </a>
        ))}
        <span className="meta muted">
          {ago(t, locale, m.created_at)}
          {out && m.status ? ` · ${m.status}` : ""}
        </span>
      </div>
    </div>
  )
}
