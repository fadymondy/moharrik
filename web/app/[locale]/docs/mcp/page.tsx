import type { Metadata } from "next"

import { McpDocs } from "@/components/mcp/mcp-docs"
import { isLocale, type Locale } from "@/lib/i18n-locale"
import type { McpDocsCatalog } from "@/lib/mcp-docs"
import en from "@/lang/en.json"
import ar from "@/lang/ar.json"

// The public MCP reference (MH-428). Rendered on the server from the API's own registry
// (GET /api/mcp/docs), so the tools are in the HTML for readers and search engines alike.

const DICT: Record<Locale, Record<string, string>> = { en, ar }

type Params = { params: Promise<{ locale: string }> }

export async function generateMetadata({ params }: Params): Promise<Metadata> {
  const { locale: raw } = await params
  const locale: Locale = isLocale(raw) ? raw : "en"
  const d = DICT[locale]
  const name = locale === "ar" ? "محرّك" : "Moharrik"
  return {
    title: `${d["docs.mcp.metaTitle"]} · ${name}`,
    description: d["docs.mcp.metaDescription"],
    alternates: { languages: { en: "/en/docs/mcp", ar: "/ar/docs/mcp" } },
  }
}

async function loadCatalog(locale: Locale): Promise<McpDocsCatalog | null> {
  const origin = process.env.API_ORIGIN ?? "http://localhost:8120"
  try {
    const res = await fetch(`${origin}/api/mcp/docs?lang=${locale}`, { next: { revalidate: 300 } })
    if (!res.ok) return null
    return (await res.json()) as McpDocsCatalog
  } catch {
    // The page fetches it in the browser instead.
    return null
  }
}

export default async function McpDocsPage({ params }: Params) {
  const { locale: raw } = await params
  const locale: Locale = isLocale(raw) ? raw : "en"
  return <McpDocs initial={await loadCatalog(locale)} />
}
