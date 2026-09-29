import { headers } from "next/headers"
import type { Metadata } from "next"
import { Inter } from "next/font/google"

import "./globals.css"
import { Providers } from "@/components/providers"
import { dirForLocale, isLocale, type Locale } from "@/lib/i18n-locale"

// English UI is set in Inter: built for screens and legible at small sizes on desktop and mobile.
// next/font self-hosts it at build time, so no request reaches Google at runtime. Arabic keeps the
// grid's Lusail/Alexandria; the switch is in styles/moharrik-brand.css (English only).
const inter = Inter({ subsets: ["latin"], display: "swap", variable: "--font-inter" })

export const metadata: Metadata = {
  title: "Moharrik",
  description:
    "The AI engine behind a business: automation, inbox, campaigns, CRM and research — all over MCP.",
}

// Hosbah real-user monitoring: page loads, Web Vitals and browser errors, per route. The key is
// public by design (every page carries it) but still per-deploy config, so it comes from the env and
// the tag is simply absent where it is unset — local development and worktrees report nothing.
const HOSBAH_URL = (process.env.HOSBAH_URL ?? "https://app.hosbah.com").replace(/\/$/, "")

// The locale lives in the URL (/en/..., /ar/...). A root layout cannot read route params, so
// proxy.ts resolves it and forwards it on x-locale.
export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const raw = (await headers()).get("x-locale")
  const locale: Locale = isLocale(raw) ? raw : "en"
  const rumKey = process.env.HOSBAH_RUM_KEY?.trim()

  return (
    <html
      lang={locale}
      dir={dirForLocale(locale)}
      data-brand="moharrik"
      className={`grid-surface ${inter.variable}`}
      suppressHydrationWarning
    >
      <head>{rumKey && <script defer src={`${HOSBAH_URL}/rum.js`} data-key={rumKey} />}</head>
      <body className="min-h-dvh" suppressHydrationWarning>
        <Providers locale={locale}>{children}</Providers>
      </body>
    </html>
  )
}
