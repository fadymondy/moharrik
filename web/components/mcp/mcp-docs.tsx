"use client"

import Link from "next/link"
import { useEffect, useMemo, useState, type ReactNode } from "react"
import useSWR from "swr"
import {
  ChevronDownIcon,
  EyeIcon,
  FileClockIcon,
  GlobeLockIcon,
  KeyRoundIcon,
  LockKeyholeIcon,
  PenLineIcon,
  SearchIcon,
  ShieldCheckIcon,
  TimerIcon,
  UserCheckIcon,
  MessageCircleWarningIcon,
  MailCheckIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { CopyField, Ltr } from "@/components/copy-field"
import { ConnectClients } from "@/components/mcp/connect-clients"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { api } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { type McpDocArg, type McpDocsCatalog, type McpDocTool } from "@/lib/mcp-docs"
import { useDocumentTitle } from "@/lib/title"

/*
  The public MCP reference (MH-428) at /{locale}/docs/mcp.

  Everything about the tools — which exist, what each does, its scope, the lowest role that may
  hold that scope, its arguments, an example call and its result — comes from GET /api/mcp/docs,
  which the API builds from its own tool registry. The page adds only the prose around it:
  connecting each client, how scopes shrink with a member's role, and the rules agents are held to.
*/
export function McpDocs({ initial }: { initial: McpDocsCatalog | null }) {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("docs.mcp.metaTitle"))

  // Rendered on the server when the API answered there; fetched here otherwise.
  const fetched = useSWR<McpDocsCatalog>(initial?.lang === locale ? null : `/api/mcp/docs?lang=${locale}`, (url: string) => api<McpDocsCatalog>(url))
  const cat = initial?.lang === locale ? initial : fetched.data

  return (
    <PublicFrame eyebrow={t("docs.mcp.eyebrow")} title={t("docs.mcp.title")} description={t("docs.mcp.description", { count: cat?.tool_count ?? "—" })}>
      <PublicPanel className="p-0">
        <nav aria-label={t("docs.mcp.onThisPage")} className="flex flex-wrap items-center gap-x-5 gap-y-2 px-4 py-3 text-sm">
          <span className="grid-micro">{t("docs.mcp.onThisPage")}</span>
          <TocLink href="#connect">{t("docs.mcp.connect.title")}</TocLink>
          <TocLink href="#scopes">{t("docs.mcp.scopes.title")}</TocLink>
          <TocLink href="#safety">{t("docs.mcp.safety.title")}</TocLink>
          <TocLink href="#reference">{t("docs.mcp.reference.title")}</TocLink>
        </nav>
      </PublicPanel>

      {fetched.error && !cat ? (
        <PublicPanel>
          <ErrorState error={fetched.error} />
        </PublicPanel>
      ) : !cat ? (
        <PublicPanel>
          <LoadingRows />
        </PublicPanel>
      ) : (
        <>
          <ConnectSection cat={cat} locale={locale} />
          <ScopesSection cat={cat} />
          <SafetySection />
          <Reference cat={cat} />
        </>
      )}
    </PublicFrame>
  )
}

function TocLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} className="text-grid-body underline-offset-4 hover:text-grid-fg hover:underline">
      {children}
    </a>
  )
}

function SectionTitle({ id, children, hint }: { id: string; children: ReactNode; hint?: ReactNode }) {
  return (
    <div className="mb-5 max-w-2xl space-y-1.5">
      <h2 id={id} className="grid-title scroll-mt-20 text-xl">
        {children}
      </h2>
      {hint ? <p className="grid-body text-sm text-pretty">{hint}</p> : null}
    </div>
  )
}

function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex gap-2.5 text-sm text-grid-body">
      <span className="grid size-5 shrink-0 place-items-center border border-line font-mono text-[11px] text-grid-muted">{n}</span>
      <span className="min-w-0">{children}</span>
    </li>
  )
}

// ---- connect ---------------------------------------------------------------

function ConnectSection({ cat, locale }: { cat: McpDocsCatalog; locale: string }) {
  const { t } = useTranslations()
  // The API's own MCPServerURL() (internal/mcp/oauth.go, MH-516): the dedicated
  // mcp.<domain> host once configured, else this app's /api/mcp. No client-side
  // guessing any more — the catalog is the single source, same as Settings.
  const url = cat.server_url
  const link = "text-grid-action underline-offset-4 hover:underline"

  return (
    <PublicPanel className="py-8">
      <SectionTitle id="connect" hint={t("docs.mcp.connect.intro")}>
        {t("docs.mcp.connect.title")}
      </SectionTitle>

      <div className="max-w-3xl space-y-6">
        <div className="space-y-1.5">
          <p className="grid-micro">{t("mcp.client.serverUrl")}</p>
          <CopyField value={url} label={t("mcp.client.serverUrl")} nowrap />
          {cat.legacy_url ? (
            <p className="text-xs text-grid-muted" data-docs-legacy-url>
              {t("docs.mcp.connect.legacy")} <Ltr mono>{cat.legacy_url}</Ltr>
            </p>
          ) : null}
        </div>

        <div className="space-y-3" data-docs-token-steps>
          <h3 className="text-base font-medium">{t("docs.mcp.connect.tokenTitle")}</h3>
          <p className="text-sm text-grid-body">{t("docs.mcp.connect.tokenIntro")}</p>
          <ol className="space-y-2.5">
            <Step n={1}>
              {t("docs.mcp.connect.token1")}{" "}
              <Link className={link} href={`/${locale}/login`}>
                {t("docs.mcp.connect.signIn")}
              </Link>
              {" · "}
              <Link className={link} href={`/${locale}/register`}>
                {t("docs.mcp.connect.register")}
              </Link>
            </Step>
            <Step n={2}>{t("docs.mcp.connect.token2")}</Step>
            <Step n={3}>{t("docs.mcp.connect.token3")}</Step>
            <Step n={4}>{t("docs.mcp.connect.token4")}</Step>
          </ol>
        </div>

        <div className="space-y-3">
          <h3 className="text-base font-medium">{t("docs.mcp.connect.clientsTitle")}</h3>
          <ConnectClients url={url} oauthUrl={url} where="docs" />
        </div>

        <div className="space-y-2 border border-line bg-grid-card p-4">
          <h3 className="text-sm font-medium">{t("docs.mcp.connect.checkTitle")}</h3>
          <p className="text-sm text-grid-body">{t("docs.mcp.connect.check")}</p>
          <CopyField value={t("docs.mcp.connect.checkPrompt")} label={t("docs.mcp.connect.checkTitle")} />
          <p className="text-sm text-grid-muted">{t("docs.mcp.connect.checkAfter")}</p>
        </div>

        <div className="space-y-2">
          <h3 className="text-base font-medium">{t("docs.mcp.connect.otherTitle")}</h3>
          <p className="text-sm text-grid-body">
            {t("docs.mcp.connect.other", { versions: cat.protocol_versions.join(", ") })}
          </p>
        </div>
      </div>
    </PublicPanel>
  )
}

// ---- scopes and roles ---------------------------------------------------

function ScopesSection({ cat }: { cat: McpDocsCatalog }) {
  const { t } = useTranslations()
  return (
    <PublicPanel className="py-8">
      <SectionTitle id="scopes" hint={t("docs.mcp.scopes.intro")}>
        {t("docs.mcp.scopes.title")}
      </SectionTitle>

      <div className="mb-6 max-w-2xl space-y-3 text-sm text-grid-body">
        <p>{t("docs.mcp.scopes.roles")}</p>
        <ol className="flex flex-wrap items-center gap-1.5" aria-label={t("docs.mcp.scopes.ladder")}>
          {cat.roles.map((r, i) => (
            <li key={r} className="flex items-center gap-1.5">
              <Badge variant={i === 0 ? "default" : "outline"}>{t(`role.${r}`)}</Badge>
              {i < cat.roles.length - 1 ? <span className="text-grid-muted" aria-hidden>›</span> : null}
            </li>
          ))}
        </ol>
        <p>{t("docs.mcp.scopes.shrink")}</p>
        <p>{t("docs.mcp.scopes.client")}</p>
      </div>

      <ul className="divide-y divide-line border-y border-line" data-docs-scopes>
        {cat.scopes.map((s) => (
          <li key={s.scope} className="grid gap-2 py-3 sm:grid-cols-[13rem_1fr] sm:gap-4">
            <div className="flex flex-wrap items-center gap-2 sm:flex-col sm:items-start">
              <Ltr mono className="text-sm text-grid-fg">
                {s.scope}
              </Ltr>
              <span className="text-xs text-grid-muted">{t("docs.mcp.scopes.minRole", { role: t(`role.${s.min_role}`) })}</span>
            </div>
            <div className="min-w-0 space-y-1.5">
              <p className="text-sm text-grid-body">{s.description}</p>
              <p className="flex flex-wrap gap-1">
                {s.tools.map((name) => (
                  <a key={name} href={`#tool-${name}`} dir="ltr" className="border border-line px-1.5 py-0.5 font-mono text-[11px] text-grid-muted hover:border-grid-action hover:text-grid-fg">
                    {name}
                  </a>
                ))}
              </p>
            </div>
          </li>
        ))}
      </ul>
    </PublicPanel>
  )
}

// ---- safety ----------------------------------------------------------------

const SAFETY: { key: string; icon: ReactNode }[] = [
  { key: "workspace", icon: <GlobeLockIcon /> },
  { key: "deny", icon: <LockKeyholeIcon /> },
  { key: "role", icon: <UserCheckIcon /> },
  { key: "consent", icon: <ShieldCheckIcon /> },
  { key: "audit", icon: <FileClockIcon /> },
  { key: "whatsapp", icon: <MessageCircleWarningIcon /> },
  { key: "campaigns", icon: <MailCheckIcon /> },
  { key: "browser", icon: <EyeIcon /> },
  { key: "data", icon: <PenLineIcon /> },
  { key: "secrets", icon: <KeyRoundIcon /> },
  { key: "limits", icon: <TimerIcon /> },
]

function SafetySection() {
  const { t } = useTranslations()
  return (
    <PublicPanel className="py-8">
      <SectionTitle id="safety" hint={t("docs.mcp.safety.intro")}>
        {t("docs.mcp.safety.title")}
      </SectionTitle>
      <ul className="grid gap-px border border-line bg-line sm:grid-cols-2" data-docs-safety>
        {SAFETY.map((r) => (
          <li key={r.key} className="flex gap-3 bg-background p-4 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-grid-muted">
            <span className="mt-0.5">{r.icon}</span>
            <span className="min-w-0 space-y-1">
              <span className="block text-sm font-medium">{t(`docs.mcp.safety.${r.key}.title`)}</span>
              <span className="block text-sm text-grid-body">{t(`docs.mcp.safety.${r.key}.body`)}</span>
            </span>
          </li>
        ))}
      </ul>
    </PublicPanel>
  )
}

// ---- the tool reference ---------------------------------------------------

function Reference({ cat }: { cat: McpDocsCatalog }) {
  const { t, formatNumber } = useTranslations()
  const [q, setQ] = useState("")
  const [hash, setHash] = useState("")

  // A link to #tool-name opens that tool (from the scope list above, or from outside).
  useEffect(() => {
    const read = () => setHash(decodeURIComponent(window.location.hash.replace(/^#/, "")))
    read()
    window.addEventListener("hashchange", read)
    return () => window.removeEventListener("hashchange", read)
  }, [])

  const needle = q.trim().toLowerCase()
  const groups = useMemo(
    () =>
      cat.groups
        .map((g) => ({
          ...g,
          tools: needle
            ? g.tools.filter((tool) => [tool.name, tool.scope, tool.description].some((v) => v.toLowerCase().includes(needle)))
            : g.tools,
        }))
        .filter((g) => g.tools.length > 0),
    [cat.groups, needle],
  )
  const shown = groups.reduce((n, g) => n + g.tools.length, 0)

  return (
    <PublicPanel className="py-8">
      <SectionTitle id="reference" hint={t("docs.mcp.reference.intro", { count: formatNumber(cat.tool_count) })}>
        {t("docs.mcp.reference.title")}
      </SectionTitle>

      <div className="mb-4 flex flex-wrap gap-x-4 gap-y-1.5 text-sm">
        {cat.groups.map((g) => (
          <a key={g.id} href={`#tools-${g.id}`} className="text-grid-body underline-offset-4 hover:text-grid-fg hover:underline">
            {g.title} <span className="text-grid-muted">({formatNumber(g.tools.length)})</span>
          </a>
        ))}
      </div>

      <div className="sticky top-14 z-10 -mx-4 mb-4 border-y border-line bg-background px-4 py-2.5">
        <InputGroup className="max-w-md">
          <InputGroupAddon>
            <SearchIcon />
          </InputGroupAddon>
          <InputGroupInput
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("docs.mcp.reference.filter")}
            aria-label={t("docs.mcp.reference.filter")}
          />
        </InputGroup>
        <p className="mt-1.5 text-xs text-grid-muted" aria-live="polite">
          {t("docs.mcp.reference.showing", { n: formatNumber(shown), total: formatNumber(cat.tool_count) })}
        </p>
      </div>

      {shown === 0 ? <p className="py-6 text-sm text-grid-muted">{t("docs.mcp.reference.none", { q })}</p> : null}

      <div className="space-y-10">
        {groups.map((g) => (
          <section key={g.id} aria-labelledby={`tools-${g.id}`} data-docs-group={g.id}>
            <h3 id={`tools-${g.id}`} className="scroll-mt-40 text-lg font-medium">
              {g.title}
            </h3>
            <p className="mt-1 mb-3 max-w-2xl text-sm text-grid-body">{g.intro}</p>
            <ul className="divide-y divide-line border-y border-line">
              {g.tools.map((tool) => (
                <ToolEntry key={tool.name} tool={tool} open={hash === `tool-${tool.name}`} />
              ))}
            </ul>
          </section>
        ))}
      </div>
    </PublicPanel>
  )
}

function ToolEntry({ tool, open: linked }: { tool: McpDocTool; open: boolean }) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (linked) {
      setOpen(true)
      document.getElementById(`tool-${tool.name}`)?.scrollIntoView({ block: "start" })
    }
  }, [linked, tool.name])

  const call = JSON.stringify(tool.example_call, null, 2)
  const result = prettyResult(tool.example_result)

  return (
    <li id={`tool-${tool.name}`} className="scroll-mt-40" data-docs-tool={tool.name}>
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger className="group flex w-full items-start gap-3 py-3 text-start outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
          <ChevronDownIcon className="mt-0.5 size-4 shrink-0 text-grid-muted transition-transform group-aria-expanded:rotate-180" />
          <span className="min-w-0 flex-1 space-y-1">
            <span className="flex flex-wrap items-center gap-1.5">
              <Ltr mono className="text-sm font-medium text-grid-fg">
                {tool.name}
              </Ltr>
              <Badge variant="outline" className="font-mono" dir="ltr">
                {tool.scope}
              </Badge>
              <Badge variant="secondary">{tool.writes ? t("docs.mcp.reference.writes") : t("docs.mcp.reference.reads")}</Badge>
            </span>
            <span className="block text-sm text-grid-body">{tool.description}</span>
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-4 pb-5 ps-7">
          <p className="text-xs text-grid-muted">
            {t("docs.mcp.reference.needs", { scope: tool.scope, role: t(`role.${tool.min_role}`) })}
          </p>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("docs.mcp.reference.arguments")}</p>
            {tool.arguments.length === 0 ? (
              <p className="text-sm text-grid-muted">{t("docs.mcp.reference.noArguments")}</p>
            ) : (
              <ul className="divide-y divide-line border border-line">
                {tool.arguments.map((a) => (
                  <ArgRow key={a.name} arg={a} />
                ))}
              </ul>
            )}
          </div>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("docs.mcp.reference.exampleCall")}</p>
            <CopyField value={call} label={t("docs.mcp.reference.exampleCall")} pre />
          </div>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("docs.mcp.reference.exampleResult")}</p>
            <CopyField value={result} label={t("docs.mcp.reference.exampleResult")} pre />
          </div>
        </CollapsibleContent>
      </Collapsible>
    </li>
  )
}

function ArgRow({ arg }: { arg: McpDocArg }) {
  const { t } = useTranslations()
  return (
    <li className="space-y-1 px-3 py-2">
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Ltr mono className="text-sm text-grid-fg">
          {arg.name}
        </Ltr>
        <Ltr mono className="text-xs text-grid-muted">
          {arg.type}
        </Ltr>
        {arg.required ? <Badge variant="outline">{t("docs.mcp.reference.required")}</Badge> : null}
      </p>
      {arg.description ? <p className="text-sm text-grid-body">{arg.description}</p> : null}
      {arg.enum?.length ? (
        <p className="flex flex-wrap items-center gap-1 text-xs text-grid-muted">
          <span>{t("docs.mcp.reference.oneOf")}</span>
          {arg.enum.map((v) => (
            <code key={String(v)} dir="ltr" className="border border-line px-1 font-mono text-[11px]">
              {v === "" ? '""' : String(v)}
            </code>
          ))}
        </p>
      ) : null}
    </li>
  )
}

/** A JSON answer indented for reading; a text answer as it is. */
function prettyResult(s: string): string {
  const v = s.trim()
  if (v.startsWith("{") || v.startsWith("[")) {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return s
    }
  }
  return s
}
