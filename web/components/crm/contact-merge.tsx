"use client"

import { useMemo, useState } from "react"
import { CombineIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { BdiBlock } from "@/components/bidi"
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useDebounced } from "@/components/crm/contact-picker"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, contactIdentity, type ContactDetail } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContactsPage } from "@/lib/queries"

type Option = { value: string; label: string }

const label = (c: { name: string; email: string }) => (c.name ? `${c.name} · ${c.email}` : c.email)

/**
 * "Merge another contact into this one" (MH-647): pick a duplicate by search,
 * confirm which one survives, then merge. The action stays out of the way
 * (a ghost button, same row as delete) — this is a rare, destructive-adjacent
 * cleanup step, not something to put in front of the everyday edit form.
 */
export function MergeContactAction({
  contact,
  onMerged,
}: {
  contact: ContactDetail
  onMerged: (survivorId: string) => void
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)} data-merge-contact-open>
        <CombineIcon />
        {t("contacts.merge")}
      </Button>
      <MergeContactDialog
        contact={contact}
        open={open}
        onOpenChange={setOpen}
        onMerged={onMerged}
      />
    </>
  )
}

function MergeContactDialog({
  contact,
  open,
  onOpenChange,
  onMerged,
}: {
  contact: ContactDetail
  open: boolean
  onOpenChange: (open: boolean) => void
  onMerged: (survivorId: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [query, setQuery] = useState("")
  const q = useDebounced(query)
  const res = useContactsPage(ws.id, { q, limit: 20 }, open)
  const [chosen, setChosen] = useState<Option | null>(null)
  const [source, setSource] = useState<ContactDetail | null>(null)
  const [loadingSource, setLoadingSource] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  // The contact being merged into can't also be the one merged in.
  const items = useMemo<Option[]>(
    () =>
      (res.data?.contacts ?? [])
        .filter((c) => c.id !== contact.id)
        .map((c) => ({ value: c.id, label: label(c) })),
    [res.data, contact.id],
  )

  function reset() {
    setQuery("")
    setChosen(null)
    setSource(null)
    setConfirming(false)
  }

  async function pick(o: Option | null) {
    setChosen(o)
    if (!o) {
      setSource(null)
      return
    }
    setLoadingSource(true)
    try {
      const picked = await api<{ contact: ContactDetail }>(
        `/api/w/contacts/${encodeURIComponent(o.value)}`,
        { workspaceId: ws.id },
      )
      setSource(picked.contact)
      setConfirming(true)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoadingSource(false)
    }
  }

  async function confirmMerge() {
    if (!source) return
    setBusy(true)
    try {
      const merged = await api<{ contact: ContactDetail }>(
        `/api/w/contacts/${encodeURIComponent(contact.id)}/merge`,
        { method: "POST", json: { source_id: source.id }, workspaceId: ws.id },
      )
      toast.success(t("contacts.merged"))
      onOpenChange(false)
      reset()
      onMerged(merged.contact.id)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next)
        if (!next) reset()
      }}
    >
      <DialogContent data-merge-contact-dialog>
        {!confirming ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("contacts.mergeTitle")}</DialogTitle>
              <DialogDescription>{t("contacts.mergePickHint")}</DialogDescription>
            </DialogHeader>
            <Combobox<Option>
              items={items}
              value={chosen}
              filter={null}
              itemToStringLabel={(o) => o.label}
              isItemEqualToValue={(a, b) => a.value === b.value}
              onValueChange={(o) => void pick(o)}
              onInputValueChange={(v, details) => {
                if (details.reason === "input-change" || details.reason === "input-clear")
                  setQuery(v)
              }}
            >
              <ComboboxInput
                autoFocus
                className="w-full"
                placeholder={t("contacts.pickPlaceholder")}
                disabled={loadingSource}
              />
              <ComboboxContent>
                <ComboboxEmpty>
                  {res.isLoading ? t("common.working") : t("contacts.pickNone")}
                </ComboboxEmpty>
                <ComboboxList>
                  {(o: Option) => (
                    <ComboboxItem key={o.value} value={o}>
                      <BdiBlock className="truncate">{o.label}</BdiBlock>
                    </ComboboxItem>
                  )}
                </ComboboxList>
              </ComboboxContent>
            </Combobox>
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t("common.cancel")}
              </Button>
            </DialogFooter>
          </>
        ) : source ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("contacts.mergeConfirmTitle")}</DialogTitle>
              <DialogDescription>{t("contacts.mergeConfirmBody")}</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 py-2 sm:grid-cols-2" data-merge-contact-preview>
              <div className="border border-nq-line bg-card p-3" data-merge-survivor>
                <p className="text-xs text-muted-foreground">{t("contacts.mergeSurvivor")}</p>
                <p className="font-medium">
                  <BdiBlock>{contact.name || contactIdentity(contact)}</BdiBlock>
                </p>
                <p className="text-sm text-muted-foreground">
                  <BdiBlock>{contactIdentity(contact)}</BdiBlock>
                </p>
              </div>
              <div className="border border-nq-line bg-card p-3" data-merge-source>
                <p className="text-xs text-muted-foreground">{t("contacts.mergeSource")}</p>
                <p className="font-medium">
                  <BdiBlock>{source.name || contactIdentity(source)}</BdiBlock>
                </p>
                <p className="text-sm text-muted-foreground">
                  <BdiBlock>{contactIdentity(source)}</BdiBlock>
                </p>
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setConfirming(false)} disabled={busy}>
                {t("common.back")}
              </Button>
              <Button variant="destructive" onClick={() => void confirmMerge()} disabled={busy}>
                {busy ? t("common.working") : t("contacts.mergeConfirm")}
              </Button>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
