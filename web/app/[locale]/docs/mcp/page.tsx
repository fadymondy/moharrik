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
    // The API already marks this response no-store (internal/mcp/docs.go): the
    // catalog is rebuilt from the in-memory tool registry on every call, so
    // there is no real cost to always refetching it — and Next's own fetch
    // cache would otherwise serve a stale tool_count for up to 5 minutes after
    // a deploy adds a tool, on top of the browser-side staleness that
    // Cache-Control: no-store alone fixes. Seen live in testing (MH-428): this
    // page kept SSR-ing the pre-rebase tool count long after the API restarted
    // with 5 new tools, purely because of `next: { revalidate: 300 }` here.
    const res = await fetch(`${origin}/api/mcp/docs?lang=${locale}`, { cache: "no-store" })
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
