"use client"

/*
The public "how to delete your account" page, for the App Store and Google Play listings (Play's
account-deletion URL; Apple 5.1.1(v)). Public: no sign-in needed to read it. The deletion itself
is self-service in the app (Settings → Account → Delete account) and on the web (/account).
*/

import Link from "next/link"

import { Button } from "@fadymondy/nasaq/web"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function DeleteAccountInfoPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("deleteInfo.title"))
  const sections: [string, string][] = [
    ["deleteInfo.inApp", "deleteInfo.inAppSteps"],
    ["deleteInfo.onWeb", "deleteInfo.onWebSteps"],
    ["deleteInfo.whatTitle", "deleteInfo.what"],
    ["deleteInfo.keptTitle", "deleteInfo.kept"],
    ["deleteInfo.helpTitle", "deleteInfo.help"],
  ]
  return (
    <PublicFrame
      eyebrow={t("deleteInfo.micro")}
      title={t("deleteInfo.title")}
      description={t("deleteInfo.hint")}
    >
      <PublicPanel className="space-y-6 py-6" data-delete-info>
        {sections.map(([title, body]) => (
          <section key={title} className="space-y-1 px-4">
            <h2 className="text-base font-medium">{t(title)}</h2>
            <p className="text-sm text-pretty text-muted-foreground">{t(body)}</p>
          </section>
        ))}
        <div className="px-4">
          <Button nativeButton={false} render={<Link href={`/${locale}/account`} />}>
            {t("deleteInfo.open")}
          </Button>
        </div>
      </PublicPanel>
    </PublicFrame>
  )
}
