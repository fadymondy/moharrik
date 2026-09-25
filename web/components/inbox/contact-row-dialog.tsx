"use client"

import { ContactEditForm } from "@/components/crm/contact-edit-form"
import { CreateContactForm } from "@/components/inbox/thread-crm"
import { LoadingRows } from "@/components/states"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { Conversation } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContact } from "@/lib/queries"

/**
 * MH-595: editing a conversation's contact straight from its row in the list,
 * without opening the thread. A linked conversation edits the SAME contact
 * fields through the SAME `PATCH /api/w/contacts/{id}` ThreadCRM's own
 * "edit contact" page uses (via the shared `ContactEditForm`); an unlinked one
 * reuses ThreadCRM's `CreateContactForm` and the `POST
 * /api/w/conversations/{id}/contact` create-or-link call it already makes —
 * seeded from the row's own customer_name/email/phone/locale since a list row
 * never loaded the fuller VisitorGuess a thread does.
 */
export function ConversationContactDialog({
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
  const contactId = c.contact_id
  const detail = useContact(ws.id, open && contactId ? contactId : undefined)

  function done() {
    onDone()
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg" data-contact-dialog>
        <DialogHeader>
          <DialogTitle>{contactId ? t("inbox.crm.editContactTitle") : t("inbox.crm.createTitle")}</DialogTitle>
          <DialogDescription>{contactId ? t("inbox.crm.editContactHint") : t("inbox.crm.createHint")}</DialogDescription>
        </DialogHeader>
        {contactId ? (
          detail.data ? (
            <ContactEditForm contact={detail.data.contact} onSaved={done} idPrefix="row-contact" />
          ) : (
            <LoadingRows rows={3} />
          )
        ) : (
          <CreateContactForm
            c={c}
            visitor={{ name: c.customer_name, email: c.customer_email, phone: c.customer_phone, locale: c.locale === "ar" ? "ar" : "en" }}
            onDone={done}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
