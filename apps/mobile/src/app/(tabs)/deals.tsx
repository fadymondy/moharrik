import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Check } from "lucide-react-native"
import { useEffect, useMemo, useState } from "react"
import { FlatList, RefreshControl, ScrollView, View } from "react-native"

import { money, type Deal } from "@moharrik/client"

import { useBanner } from "@/components/banner"
import { Sheet } from "@/components/sheet"
import { Chip, Empty, ErrorView, Header, Loading, Row, Screen, Text, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function DealsScreen() {
  const { t, locale } = usePrefs()
  const c = useColors()
  const qc = useQueryClient()
  const banner = useBanner()
  const { api, workspace } = useSession()
  const ws = workspace?.id ?? ""
  const stages = useQuery({ queryKey: ["stages", ws], queryFn: () => api.stages(), enabled: !!workspace })
  const all = stages.data?.stages ?? []
  const [stage, setStage] = useState("")
  useEffect(() => {
    if (!stage && all[0]) setStage(all[0].id)
  }, [all, stage])

  const list = useInfiniteQuery({
    queryKey: qk.deals(ws, stage),
    queryFn: ({ pageParam }) => api.deals(stage, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace && !!stage,
  })
  const currency = list.data?.pages[0]?.currency
  const deals = useMemo(() => (list.data?.pages.flatMap((p) => p.deals ?? []) ?? []).filter((d) => d.stage_id === stage), [list.data, stage])
  const [moving, setMoving] = useState<Deal | null>(null)
  const move = useMutation({
    mutationFn: ({ deal, to }: { deal: Deal; to: string }) => api.updateDeal(deal.id, { stage_id: to }),
    onSuccess: (_r, { to }) => {
      void qc.invalidateQueries({ queryKey: ["deals", ws] })
      void qc.invalidateQueries({ queryKey: ["stages", ws] })
      banner({ title: t("deals.moved", { stage: all.find((s) => s.id === to)?.name ?? "" }) })
    },
  })

  if (stages.isLoading)
    return (
      <Screen>
        <Header title={t("deals.title")} />
        <Loading />
      </Screen>
    )
  if (stages.isError)
    return (
      <Screen>
        <Header title={t("deals.title")} />
        <ErrorView onRetry={() => void stages.refetch()} />
      </Screen>
    )

  return (
    <Screen>
      <Header title={t("deals.title")} sub={workspace?.name} />
      {all.length === 0 ? (
        <Empty title={t("deals.noStages")} />
      ) : (
        <>
          <View>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8, paddingHorizontal: 16, paddingVertical: 10 }}>
              {all.map((s) => (
                <Chip key={s.id} label={`${s.name} · ${s.deal_count}`} active={s.id === stage} onPress={() => setStage(s.id)} />
              ))}
            </ScrollView>
          </View>
          {(() => {
            const s = all.find((x) => x.id === stage)
            return s ? (
              <Text size="sm" tone="muted" style={{ paddingHorizontal: 16, paddingBottom: 6 }}>
                {t("deals.count", { n: s.deal_count })}
                {s.value_minor ? ` · ${money(locale, s.value_minor, currency)}` : ""}
              </Text>
            ) : null
          })()}
          {list.isLoading ? (
            <Loading />
          ) : list.isError ? (
            <ErrorView onRetry={() => void list.refetch()} />
          ) : (
            <FlatList
              data={deals}
              keyExtractor={(d) => d.id}
              renderItem={({ item: d }) => (
                <Row onPress={() => setMoving(d)}>
                  <View style={{ flex: 1, gap: 2 }}>
                    <Text weight="medium" numberOfLines={1}>
                      {d.title}
                    </Text>
                    <Text size="sm" tone="muted" numberOfLines={1}>
                      {[d.contact?.name, d.company?.name].filter(Boolean).join(" · ") || "—"}
                    </Text>
                  </View>
                  <Text weight="bold" tone="action">
                    {money(locale, d.value_minor, d.currency || currency)}
                  </Text>
                </Row>
              )}
              onEndReached={() => list.hasNextPage && !list.isFetchingNextPage && void list.fetchNextPage()}
              refreshControl={<RefreshControl refreshing={list.isRefetching && !list.isFetchingNextPage} onRefresh={() => void list.refetch()} tintColor={c.action} colors={[c.action]} />}
              ListEmptyComponent={<Empty title={t("deals.empty")} />}
              contentContainerStyle={deals.length ? undefined : { flexGrow: 1 }}
            />
          )}
        </>
      )}
      <MoveSheet
        deal={moving}
        stages={all}
        onClose={() => setMoving(null)}
        onPick={(to) => {
          if (moving && to !== moving.stage_id) move.mutate({ deal: moving, to })
          setMoving(null)
        }}
      />
    </Screen>
  )
}

function MoveSheet({ deal, stages, onClose, onPick }: { deal: Deal | null; stages: { id: string; name: string }[]; onClose: () => void; onPick: (id: string) => void }) {
  const { t } = usePrefs()
  const c = useColors()
  return (
    <Sheet open={!!deal} onClose={onClose} title={t("deals.move")} sub={deal?.title}>
      {stages.map((s) => (
        <Row key={s.id} onPress={() => onPick(s.id)}>
          <Text style={{ flex: 1 }}>{s.name}</Text>
          {s.id === deal?.stage_id ? <Check size={18} color={c.action} /> : null}
        </Row>
      ))}
    </Sheet>
  )
}
