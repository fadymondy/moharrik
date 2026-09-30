"use client"

import { useState, type FormEvent } from "react"
import {
  BellIcon,
  BellOffIcon,
  Building2Icon,
  FileIcon,
  ImageIcon,
  LinkIcon,
  StarIcon,
} from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { PersonAvatar } from "@/components/avatar-with-fallback"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { isMuted } from "@/components/inbox/chat-row-menu"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Conversation } from "@/lib/api"
import { useContact, useConversationGallery, useStarredMessages } from "@/lib/queries"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

/*
The Contact info side panel (MH-662) — WhatsApp's "Contact info" screen: who
this is, the team's own notes (CRM, separate from what the contact said about
themselves), the mute toggle, and the Media/links/docs + Starred galleries.
A Sheet rather than a route: it sits over the thread and never loses the
reader's place in it. Full-screen at 390px via the data-[side] override below.
*/
export function ContactInfoPanel({
  c,
  open,
  onOpenChange,
  onChanged,
}: {
  c: Conversation
  open: boolean
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("inbox:write") && ws.can("contacts:write")
  const contactQuery = useContact(open ? ws.id : null, c.contact_id || undefined)
  const gallery = useConversationGallery(open ? ws.id : null, c.id)
  const starred = useStarredMessages(open ? ws.id : null, c.id)
  const [notes, setNotes] = useState<string | null>(null)
  const [savingNotes, setSavingNotes] = useState(false)
  const muted = isMuted(c)

  const contact = contactQuery.data?.contact
  const who = contact?.name || c.contact?.name || c.customer_name || c.customer_email || c.customer_phone || t("inbox.anonymous")
  const notesValue = notes ?? contact?.notes ?? ""

  async function saveNotes() {
    if (!contact) return
    setSavingNotes(true)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
        method: "PATCH",
        json: { notes: notesValue },
        workspaceId: ws.id,
      })
      toast.success(t("common.saved"))
      await contactQuery.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSavingNotes(false)
    }
  }

  async function toggleMute() {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}/organise`, {
        method: "PATCH",
        json: { mute: muted ? "" : "always" },
        workspaceId: ws.id,
      })
      toast.success(muted ? t("inbox.org.unmuted") : t("inbox.org.muted"))
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const counts = gallery.data?.counts

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="data-[side=right]:w-full data-[side=right]:max-w-full sm:data-[side=right]:max-w-sm"
        data-contact-info-panel
      >
        <SheetHeader>
          <SheetTitle>{t("inbox.menu.contactInfo")}</SheetTitle>
          <SheetDescription className="sr-only">{who}</SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="flex flex-col items-center gap-2 text-center">
            <PersonAvatar id={c.contact_id || c.id} name={who} photoUrl={c.avatar_url} size="lg" className="size-20" />
            <Bdi className="text-base font-medium">{who}</Bdi>
            {c.customer_phone ? (
              <p className="text-sm text-muted-foreground">
                <Ltr>{c.customer_phone}</Ltr>
              </p>
            ) : null}
            {c.customer_email ? (
              <p className="text-sm text-muted-foreground">
                <Ltr>{c.customer_email}</Ltr>
              </p>
            ) : null}
            {contact?.company ? (
              <p className="flex items-center gap-1 text-xs text-muted-foreground">
                <Building2Icon className="size-3" />
                <Bdi>{contact.company.name}</Bdi>
              </p>
            ) : null}
          </div>

          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void toggleMute()}
            data-action="toggle-mute"
          >
            {muted ? <BellIcon /> : <BellOffIcon />}
            {muted ? t("inbox.org.unmute") : t("inbox.org.mute")}
          </Button>

          {contact?.biography ? (
            <section data-panel-about>
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">{t("inbox.contactInfo.about")}</h3>
              <p className="text-sm">
                <Bdi>{contact.biography}</Bdi>
              </p>
            </section>
          ) : null}

          {contact ? (
            <section data-panel-notes>
              <h3 className="mb-1 text-xs font-medium text-muted-foreground">{t("inbox.contactInfo.notes")}</h3>
              <Textarea
                value={notesValue}
                onChange={(e) => setNotes(e.target.value)}
                onBlur={() => canWrite && notesValue !== (contact.notes ?? "") && void saveNotes()}
                placeholder={t("inbox.contactInfo.notesPlaceholder")}
                disabled={!canWrite || savingNotes}
                rows={3}
                data-notes-field
              />
            </section>
          ) : null}

          <Tabs defaultValue="media" className="min-h-0 flex-1">
            <TabsList className="grid w-full grid-cols-4" variant="line">
              <TabsTrigger value="media" data-tab="media">
                <ImageIcon /> {counts ? counts.media : "–"}
              </TabsTrigger>
              <TabsTrigger value="links" data-tab="links">
                <LinkIcon /> {counts ? counts.links : "–"}
              </TabsTrigger>
              <TabsTrigger value="docs" data-tab="docs">
                <FileIcon /> {counts ? counts.docs : "–"}
              </TabsTrigger>
              <TabsTrigger value="starred" data-tab="starred">
                <StarIcon /> {starred.data?.messages.length ?? "–"}
              </TabsTrigger>
            </TabsList>

            <TabsContent value="media">
              {!gallery.data || gallery.data.media.length === 0 ? (
                <EmptyGallery text={t("inbox.contactInfo.noMedia")} />
              ) : (
                <div className="grid grid-cols-3 gap-1.5 pt-2">
                  {gallery.data.media.map((item) => (
                    <a
                      key={item.message_id + (item.attachment?.id ?? "")}
                      href={item.attachment?.url}
                      target="_blank"
                      rel="noreferrer"
                      className="block aspect-square overflow-hidden rounded-md bg-nq-surface-overlay"
                      data-gallery-item="media"
                    >
                      {item.attachment?.kind === "image" && item.attachment.url ? (
                        // eslint-disable-next-line @next/next/no-img-element
                        <img src={item.attachment.url} alt="" className="size-full object-cover" />
                      ) : (
                        <span className="flex size-full items-center justify-center text-muted-foreground">
                          <ImageIcon className="size-6" />
                        </span>
                      )}
                    </a>
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="links">
              {!gallery.data || gallery.data.links.length === 0 ? (
                <EmptyGallery text={t("inbox.contactInfo.noLinks")} />
              ) : (
                <ul className="flex flex-col gap-1 pt-2">
                  {gallery.data.links.map((item) => (
                    <li key={item.message_id}>
                      <a
                        href={item.link}
                        target="_blank"
                        rel="noreferrer"
                        className="block truncate rounded-md px-2 py-1.5 text-sm text-primary hover:bg-nq-surface-soft"
                        data-gallery-item="link"
                      >
                        <Ltr>{item.link}</Ltr>
                      </a>
                    </li>
                  ))}
                </ul>
              )}
            </TabsContent>

            <TabsContent value="docs">
              {!gallery.data || gallery.data.docs.length === 0 ? (
                <EmptyGallery text={t("inbox.contactInfo.noDocs")} />
              ) : (
                <ul className="flex flex-col gap-1 pt-2">
                  {gallery.data.docs.map((item) => (
                    <li key={item.message_id + (item.attachment?.id ?? "")}>
                      <a
                        href={item.attachment?.url}
                        target="_blank"
                        rel="noreferrer"
                        className="flex items-center gap-2 truncate rounded-md px-2 py-1.5 text-sm hover:bg-nq-surface-soft"
                        data-gallery-item="doc"
                      >
                        <FileIcon className="size-4 shrink-0" />
                        <bdi className="truncate">{item.attachment?.filename}</bdi>
                      </a>
                    </li>
                  ))}
                </ul>
              )}
            </TabsContent>

            <TabsContent value="starred">
              {!starred.data || starred.data.messages.length === 0 ? (
                <EmptyGallery text={t("inbox.contactInfo.noStarred")} />
              ) : (
                <ul className="flex flex-col gap-2 pt-2">
                  {starred.data.messages.map((m) => (
                    <li key={m.id} className="rounded-md border border-nq-line px-2 py-1.5 text-sm" data-starred-item>
                      <p className="truncate">
                        <Bdi>{m.body}</Bdi>
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(
                          new Date(m.created_at),
                        )}
                      </p>
                    </li>
                  ))}
                </ul>
              )}
            </TabsContent>
          </Tabs>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function EmptyGallery({ text }: { text: string }) {
  return <p className="py-6 text-center text-sm text-muted-foreground">{text}</p>
}
