"use client"

import { useState, type ComponentType } from "react"
import {
  ArchiveIcon,
  ArchiveRestoreIcon,
  BanIcon,
  BellIcon,
  BellOffIcon,
  BookmarkIcon,
  BriefcaseIcon,
  Building2Icon,
  CheckIcon,
  CircleAlertIcon,
  CircleOffIcon,
  CoffeeIcon,
  EraserIcon,
  FlagIcon,
  GiftIcon,
  HeartIcon,
  MailIcon,
  MailOpenIcon,
  PaletteIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  ShapesIcon,
  ShoppingBagIcon,
  SmileIcon,
  StarIcon,
  StarOffIcon,
  TagIcon,
  Trash2Icon,
  TruckIcon,
  UserPlusIcon,
  UsersIcon,
  ZapIcon,
  type LucideProps,
} from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
} from "@/components/ui/context-menu"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, TAG_COLORS, type Conversation, type TagColor } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useConversationLabels } from "@/lib/queries"

/*
The inbox row's menu (MH-657), opened by right-click, a touch long-press, the
keyboard Menu key, or the chevron that shows on hover: WhatsApp's chat menu.
Pin, archive, mute and favourite are the member's own view; colour, icon and
labels are the team's; block, clear and delete need inbox:manage and ask first.
*/

/** The icons a chat can carry, by the name the API stores. */
export const CHAT_ICONS: Record<string, ComponentType<LucideProps>> = {
  star: StarIcon,
  heart: HeartIcon,
  flag: FlagIcon,
  bookmark: BookmarkIcon,
  briefcase: BriefcaseIcon,
  building: Building2Icon,
  users: UsersIcon,
  "shopping-bag": ShoppingBagIcon,
  truck: TruckIcon,
  gift: GiftIcon,
  coffee: CoffeeIcon,
  zap: ZapIcon,
  bell: BellIcon,
  alert: CircleAlertIcon,
  smile: SmileIcon,
}

/** A chat's icon, or nothing for "" or a name this build does not know. */
export function ChatIcon({ name, className }: { name: string; className?: string }) {
  const Icon = CHAT_ICONS[name]
  return Icon ? <Icon className={className} aria-hidden /> : null
}

export function isMuted(c: Pick<Conversation, "muted_until">): boolean {
  return !!c.muted_until && new Date(c.muted_until).getTime() > Date.now()
}

type Confirm = "block" | "clear" | "delete" | null

export function ChatRowMenu({
  c,
  onChanged,
  onContactEdit,
  onDeleted,
  onUnread,
  onStatus,
}: {
  c: Conversation
  onChanged?: () => void
  onContactEdit: () => void
  onDeleted?: () => void
  onUnread: () => void
  onStatus: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("inbox:write")
  const canManage = ws.can("inbox:manage")
  const [labelsOpen, setLabelsOpen] = useState(false)
  const [confirm, setConfirm] = useState<Confirm>(null)
  const muted = isMuted(c)
  const path = `/api/w/conversations/${encodeURIComponent(c.id)}`

  async function organise(patch: Record<string, unknown>, message: string) {
    try {
      await api(`${path}/organise`, { method: "PATCH", json: patch, workspaceId: ws.id })
      toast.success(message)
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function runConfirm() {
    const what = confirm
    setConfirm(null)
    try {
      if (what === "block") {
        await api(`${path}/organise`, { method: "PATCH", json: { blocked: !c.blocked }, workspaceId: ws.id })
        toast.success(c.blocked ? t("inbox.org.unblocked") : t("inbox.org.blocked"))
        onChanged?.()
      } else if (what === "clear") {
        await api(`${path}/clear`, { method: "POST", workspaceId: ws.id })
        toast.success(t("inbox.org.cleared"))
        onChanged?.()
      } else if (what === "delete") {
        await api(path, { method: "DELETE", workspaceId: ws.id })
        toast.success(t("inbox.org.deleted"))
        onDeleted?.()
        onChanged?.()
      }
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <>
      <ContextMenuContent className="min-w-52" data-chat-row-menu>
        <ContextMenuItem
          data-action="pin"
          onClick={() =>
            organise({ pinned: !c.pinned }, c.pinned ? t("inbox.org.unpinned") : t("inbox.org.pinned"))
          }
        >
          {c.pinned ? <PinOffIcon /> : <PinIcon />}
          {c.pinned ? t("inbox.org.unpin") : t("inbox.org.pin")}
        </ContextMenuItem>
        <ContextMenuItem
          data-action="archive"
          onClick={() =>
            organise(
              { archived: !c.archived },
              c.archived ? t("inbox.org.unarchived") : t("inbox.org.archived"),
            )
          }
        >
          {c.archived ? <ArchiveRestoreIcon /> : <ArchiveIcon />}
          {c.archived ? t("inbox.org.unarchive") : t("inbox.org.archive")}
        </ContextMenuItem>
        {muted ? (
          <ContextMenuItem data-action="unmute" onClick={() => organise({ mute: "" }, t("inbox.org.unmuted"))}>
            <BellIcon />
            {t("inbox.org.unmute")}
          </ContextMenuItem>
        ) : (
          <ContextMenuSub>
            <ContextMenuSubTrigger data-action="mute">
              <BellOffIcon />
              {t("inbox.org.mute")}
            </ContextMenuSubTrigger>
            <ContextMenuSubContent data-mute-menu>
              {(["8h", "1w", "always"] as const).map((d) => (
                <ContextMenuItem
                  key={d}
                  data-mute={d}
                  onClick={() => organise({ mute: d }, t("inbox.org.muted"))}
                >
                  {t(`inbox.org.mute.${d}`)}
                </ContextMenuItem>
              ))}
            </ContextMenuSubContent>
          </ContextMenuSub>
        )}
        <ContextMenuItem data-action="toggle-unread" onClick={onUnread}>
          {c.unread ? <MailOpenIcon /> : <MailIcon />}
          {c.unread ? t("inbox.markRead") : t("inbox.markUnread")}
        </ContextMenuItem>
        <ContextMenuItem
          data-action="favourite"
          onClick={() =>
            organise(
              { favourite: !c.favourite },
              c.favourite ? t("inbox.org.unfavourited") : t("inbox.org.favourited"),
            )
          }
        >
          {c.favourite ? <StarOffIcon /> : <StarIcon />}
          {c.favourite ? t("inbox.org.unfavourite") : t("inbox.org.favourite")}
        </ContextMenuItem>
        <ContextMenuItem data-action="toggle-status" onClick={onStatus}>
          <CheckIcon />
          {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
        </ContextMenuItem>

        {canWrite ? (
          <>
            <ContextMenuSeparator />
            <ContextMenuSub>
              <ContextMenuSubTrigger data-action="color">
                <PaletteIcon />
                {t("inbox.org.color")}
              </ContextMenuSubTrigger>
              <ContextMenuSubContent data-color-menu>
                <ContextMenuItem data-color="" onClick={() => organise({ color: "" }, t("inbox.org.saved"))}>
                  <CircleOffIcon />
                  {t("inbox.org.none")}
                </ContextMenuItem>
                {TAG_COLORS.map((col) => (
                  <ContextMenuItem
                    key={col}
                    data-color={col}
                    onClick={() => organise({ color: col }, t("inbox.org.saved"))}
                  >
                    <span data-tag={col} className="size-3 rounded-full bg-(--tag)" aria-hidden />
                    {t(`inbox.org.colors.${col}`)}
                    {c.color === col ? <CheckIcon className="ms-auto" /> : null}
                  </ContextMenuItem>
                ))}
              </ContextMenuSubContent>
            </ContextMenuSub>
            <ContextMenuSub>
              <ContextMenuSubTrigger data-action="icon">
                <ShapesIcon />
                {t("inbox.org.icon")}
              </ContextMenuSubTrigger>
              <ContextMenuSubContent data-icon-menu className="min-w-0">
                <div className="grid grid-cols-4 gap-1">
                  <ContextMenuItem
                    data-icon=""
                    aria-label={t("inbox.org.none")}
                    className="justify-center"
                    onClick={() => organise({ icon: "" }, t("inbox.org.saved"))}
                  >
                    <CircleOffIcon />
                  </ContextMenuItem>
                  {Object.entries(CHAT_ICONS).map(([name, Icon]) => (
                    <ContextMenuItem
                      key={name}
                      data-icon={name}
                      aria-label={name}
                      aria-current={c.icon === name ? "true" : undefined}
                      className="justify-center aria-[current=true]:bg-accent"
                      onClick={() => organise({ icon: name }, t("inbox.org.saved"))}
                    >
                      <Icon />
                    </ContextMenuItem>
                  ))}
                </div>
              </ContextMenuSubContent>
            </ContextMenuSub>
            <ContextMenuItem data-action="labels" onClick={() => setLabelsOpen(true)}>
              <TagIcon />
              {t("inbox.org.labels")}
            </ContextMenuItem>
          </>
        ) : null}

        <ContextMenuSeparator />
        <ContextMenuItem onClick={onContactEdit} data-contact-edit>
          {c.contact_id ? <PencilIcon /> : <UserPlusIcon />}
          {c.contact_id ? t("inbox.crm.editContact") : t("inbox.crm.addContact")}
        </ContextMenuItem>

        {canManage ? (
          <>
            <ContextMenuSeparator />
            <ContextMenuItem data-action="block" variant="destructive" onClick={() => setConfirm("block")}>
              <BanIcon />
              {c.blocked ? t("inbox.org.unblock") : t("inbox.org.block")}
            </ContextMenuItem>
            <ContextMenuItem data-action="clear" variant="destructive" onClick={() => setConfirm("clear")}>
              <EraserIcon />
              {t("inbox.org.clear")}
            </ContextMenuItem>
            <ContextMenuItem data-action="delete" variant="destructive" onClick={() => setConfirm("delete")}>
              <Trash2Icon />
              {t("inbox.org.delete")}
            </ContextMenuItem>
          </>
        ) : null}
      </ContextMenuContent>

      <LabelsDialog c={c} open={labelsOpen} onOpenChange={setLabelsOpen} onDone={() => onChanged?.()} />

      <AlertDialog open={confirm != null} onOpenChange={(open) => !open && setConfirm(null)}>
        <AlertDialogContent data-confirm-dialog={confirm ?? undefined}>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblockTitle")
                  : t("inbox.org.blockTitle")
                : confirm === "clear"
                  ? t("inbox.org.clearTitle")
                  : t("inbox.org.deleteTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblockBody")
                  : c.channel === "whatsapp"
                    ? t("inbox.org.blockBodyWhatsApp")
                    : t("inbox.org.blockBody")
                : confirm === "clear"
                  ? t("inbox.org.clearBody")
                  : t("inbox.org.deleteBody")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              data-confirm
              variant={confirm === "block" && c.blocked ? "default" : "destructive"}
              onClick={runConfirm}
            >
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblock")
                  : t("inbox.org.block")
                : confirm === "clear"
                  ? t("inbox.org.clear")
                  : t("inbox.org.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/** A label as a chip: its colour's soft fill and text. */
export function LabelChip({ name, color }: { name: string; color: TagColor }) {
  return (
    <span
      data-tag={color}
      data-label-chip
      className="inline-flex max-w-32 items-center gap-1 truncate rounded-full bg-(--tag-soft) px-1.5 py-px text-[0.7rem] leading-4 font-medium text-(--tag-text)"
    >
      <bdi className="truncate">{name}</bdi>
    </span>
  )
}

/*
Labels… — tick the workspace's labels this chat carries, or make a new one
(a name and a colour) and tick it in the same step.
*/
function LabelsDialog({
  c,
  open,
  onOpenChange,
  onDone,
}: {
  c: Conversation
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const { data, mutate } = useConversationLabels(open ? ws.id : null)
  const [picked, setPicked] = useState<Set<string> | null>(null)
  const [name, setName] = useState("")
  const [color, setColor] = useState<TagColor>("gray")
  const [busy, setBusy] = useState(false)
  const current = picked ?? new Set(c.labels.map((l) => l.id))
  const canDelete = ws.can("inbox:manage")

  function close(o: boolean) {
    if (!o) {
      setPicked(null)
      setName("")
      setColor("gray")
    }
    onOpenChange(o)
  }

  function toggle(id: string) {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setPicked(next)
  }

  async function create() {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    try {
      const res = await api<{ id: string }>("/api/w/conversation_labels", {
        method: "POST",
        json: { name: n, color },
        workspaceId: ws.id,
      })
      // Ticked before the list comes back, so a quick Save can't miss it.
      setPicked(new Set([...current, res.id]))
      await mutate()
      setName("")
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove(id: string) {
    try {
      await api(`/api/w/conversation_labels/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
      const next = new Set(current)
      next.delete(id)
      setPicked(next)
      await mutate()
      onDone()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function save() {
    setBusy(true)
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}/organise`, {
        method: "PATCH",
        json: { label_ids: [...current] },
        workspaceId: ws.id,
      })
      toast.success(t("inbox.org.saved"))
      onDone()
      close(false)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const labels = data?.labels ?? []
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md" data-labels-dialog>
        <DialogHeader>
          <DialogTitle>{t("inbox.org.labelsTitle")}</DialogTitle>
          <DialogDescription>{t("inbox.org.labelsHint")}</DialogDescription>
        </DialogHeader>
        <ul className="flex max-h-64 flex-col gap-1 overflow-y-auto">
          {labels.length === 0 ? (
            <li className="py-2 text-sm text-grid-muted">{t("inbox.org.noLabels")}</li>
          ) : (
            labels.map((l) => (
              <li key={l.id} className="flex items-center gap-2">
                <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 hover:bg-grid-soft">
                  <input
                    type="checkbox"
                    className="size-4 accent-(--primary)"
                    checked={current.has(l.id)}
                    onChange={() => toggle(l.id)}
                    data-label-option={l.name}
                  />
                  <span data-tag={l.color} className="size-2.5 shrink-0 rounded-full bg-(--tag)" aria-hidden />
                  <bdi className="truncate text-sm">{l.name}</bdi>
                  <span className="ms-auto text-xs text-grid-muted">{l.count}</span>
                </label>
                {canDelete ? (
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={t("inbox.org.deleteLabel", { name: l.name })}
                    onClick={() => remove(l.id)}
                  >
                    <Trash2Icon />
                  </Button>
                ) : null}
              </li>
            ))
          )}
        </ul>
        <form
          className="flex flex-col gap-2 border-t border-grid-line pt-3"
          onSubmit={(e) => {
            e.preventDefault()
            void create()
          }}
        >
          <div className="flex gap-2">
            <Input
              value={name}
              maxLength={40}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("inbox.org.newLabel")}
              aria-label={t("inbox.org.newLabel")}
              data-new-label
            />
            <Button type="submit" variant="outline" disabled={busy || !name.trim()}>
              {t("inbox.org.addLabel")}
            </Button>
          </div>
          <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t("inbox.org.color")}>
            {TAG_COLORS.map((col) => (
              <button
                key={col}
                type="button"
                role="radio"
                aria-checked={color === col}
                aria-label={t(`inbox.org.colors.${col}`)}
                data-tag={col}
                onClick={() => setColor(col)}
                className="size-6 rounded-full bg-(--tag) ring-offset-2 ring-offset-(--grid-elevated) aria-checked:ring-2 aria-checked:ring-(--grid-fg)"
              />
            ))}
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" onClick={() => close(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} disabled={busy} data-labels-save>
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
