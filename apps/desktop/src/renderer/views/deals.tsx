// The pipeline as a board: a column per stage. Drag a deal to another column, or pick the stage
// from the card's menu, to move it (PATCH /api/w/deals/{id} stage_id).
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { money, type Deal } from "@moharrik/client"

import { useNav, useSession } from "../app"
import { api } from "../bridge"
import { usePrefs } from "../prefs"
import { Empty, ErrorView, Header, Loading, toast } from "../ui"

export function DealsScreen() {
  const { t, locale } = usePrefs()
  const { ws, session } = useSession()
  const { go } = useNav()
  const qc = useQueryClient()
  const stages = useQuery({ queryKey: ["stages", ws], queryFn: () => api.stages() })
  const all = stages.data?.stages ?? []
  const columns = useQueries({ queries: all.map((s) => ({ queryKey: ["deals", ws, s.id], queryFn: () => api.deals(s.id) })) })
  const [over, setOver] = useState("")

  const move = useMutation({
    mutationFn: ({ deal, to }: { deal: Deal; to: string }) => api.updateDeal(deal.id, { stage_id: to }),
    onSuccess: (_r, { to }) => {
      void qc.invalidateQueries({ queryKey: ["deals", ws] })
      void qc.invalidateQueries({ queryKey: ["stages", ws] })
      toast(t("deals.moved", { stage: all.find((s) => s.id === to)?.name ?? "" }))
    },
    onError: () => toast(t("common.networkError")),
  })
  const moveTo = (deal: Deal, to: string) => to && to !== deal.stage_id && move.mutate({ deal, to })

  return (
    <>
      <Header title={t("deals.title")} sub={session.workspace?.name} />
      {stages.isLoading ? (
        <Loading />
      ) : stages.isError ? (
        <ErrorView onRetry={() => void stages.refetch()} />
      ) : all.length === 0 ? (
        <Empty title={t("deals.noStages")} />
      ) : (
        <div className="board">
          {all.map((s, i) => {
            const page = columns[i]?.data
            const deals = (page?.deals ?? []).filter((d) => d.stage_id === s.id)
            return (
              <section
                key={s.id}
                className={`col${over === s.id ? " drop" : ""}`}
                onDragOver={(e) => {
                  e.preventDefault()
                  setOver(s.id)
                }}
                onDragLeave={() => setOver((v) => (v === s.id ? "" : v))}
                onDrop={(e) => {
                  setOver("")
                  const id = e.dataTransfer.getData("text/deal")
                  const deal = columns.flatMap((c) => c.data?.deals ?? []).find((d) => d.id === id)
                  if (deal) moveTo(deal, s.id)
                }}
              >
                <header>
                  <span className="b ellipsis">{s.name}</span>
                  <span className="small muted">
                    {t("deals.count", { n: s.deal_count })}
                    {s.value_minor ? ` · ${money(locale, s.value_minor, page?.currency)}` : ""}
                  </span>
                </header>
                <div className="cards">
                  {columns[i]?.isLoading ? <Loading /> : null}
                  {deals.map((d) => (
                    <article
                      key={d.id}
                      className="deal"
                      draggable
                      data-deal={d.id}
                      onDragStart={(e) => {
                        e.dataTransfer.setData("text/deal", d.id)
                        e.dataTransfer.effectAllowed = "move"
                      }}
                    >
                      <span className="m">{d.title}</span>
                      <span className="small muted ellipsis">
                        {d.contact?.id ? (
                          <a href="#" onClick={(e) => (e.preventDefault(), go({ screen: "contact", id: d.contact.id }))} style={{ color: "inherit" }}>
                            {d.contact.name}
                          </a>
                        ) : null}
                        {d.contact?.name && d.company?.name ? " · " : ""}
                        {d.company?.name ?? (d.contact?.name ? "" : "—")}
                      </span>
                      <span className="b action">{money(locale, d.value_minor, d.currency || page?.currency)}</span>
                      <select aria-label={t("deals.move")} value={d.stage_id} onChange={(e) => moveTo(d, e.target.value)}>
                        {all.map((x) => (
                          <option key={x.id} value={x.id}>
                            {x.id === d.stage_id ? x.name : `${t("deals.move")}: ${x.name}`}
                          </option>
                        ))}
                      </select>
                    </article>
                  ))}
                  {!columns[i]?.isLoading && deals.length === 0 ? <div className="small muted" style={{ padding: 8, textAlign: "center" }}>{t("deals.empty")}</div> : null}
                </div>
              </section>
            )
          })}
        </div>
      )}
    </>
  )
}
