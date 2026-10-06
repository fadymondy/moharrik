"use client"

// The platform audit log (MH-1184): every change an admin made, from the panel or over MCP, newest first.
// Search matches the admin, the action or the target; a row with changes expands to show them.
import { useDeferredValue, useState } from "react"
import { AdminPage, Input } from "@fadymondy/nasaq/web"

import { AuditTable } from "@/components/admin/admin-bits"
import { useAdminAudit } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function AdminAuditPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(`${t("admin.nav.audit")} · ${t("admin.title")}`)
  const [q, setQ] = useState("")
  const query = useDeferredValue(q.trim())
  const audit = useAdminAudit({ q: query })
  return (
    <AdminPage
      title={t("admin.nav.audit")}
      description={t("admin.audit.hint")}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.audit") },
      ]}
      data-admin-page="audit"
    >
      <div className="flex flex-col gap-3">
        <Input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("admin.audit.search")}
          aria-label={t("admin.audit.search")}
          className="sm:max-w-sm"
        />
        <AuditTable
          entries={audit.data ?? []}
          loading={!audit.data && !audit.error}
          error={audit.error ? t("common.networkError") : undefined}
          onRetry={() => audit.mutate()}
        />
      </div>
    </AdminPage>
  )
}
