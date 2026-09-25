import type { useTranslations } from "@/lib/i18n"

/**
 * An MCP scope's description in the page's language (web/lang "oauth.scope.<scope>"), or the
 * API's English one when there is no translation. Shared by the OAuth consent page and the
 * Create token dialog, so both say the same thing in Arabic (MH-485).
 */
export function scopeText(
  t: ReturnType<typeof useTranslations>["t"],
  scope: string,
  fallback: string,
) {
  const key = `oauth.scope.${scope}`
  const v = t(key)
  return v === key ? fallback : v
}
