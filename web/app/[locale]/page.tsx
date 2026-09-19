import { redirect } from "next/navigation"

// proxy.ts normally redirects /{locale} before this renders (to workspaces or login by session).
// This is the fallback; the workspaces page sends a signed-out visitor to login.
export default async function LocaleIndex({ params }: { params: Promise<{ locale: string }> }) {
  const { locale } = await params
  redirect(`/${locale}/workspaces`)
}
