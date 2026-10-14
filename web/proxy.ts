import { NextResponse, type NextRequest } from "next/server"

import {
  DEFAULT_LOCALE,
  LOCALE_COOKIE,
  isLocale,
  localeFromAcceptLanguage,
  type Locale,
} from "@/lib/i18n-locale"

// Set by the togo auth plugin on login/register (HttpOnly).
const SESSION_COOKIE = "togo_session"

// Locale-stripped prefixes that need a session. The API is the security boundary; this file only
// decides where to SEND someone. A missing cookie goes straight to login; a present-but-invalid
// cookie is caught client-side when /api/auth/me answers 401.
const PROTECTED_PREFIXES = ["/workspaces", "/w", "/account"]

function isProtected(rest: string): boolean {
  return PROTECTED_PREFIXES.some((p) => rest === p || rest.startsWith(p + "/"))
}

function splitLocale(pathname: string): { locale: Locale | null; rest: string } {
  const [, maybe, ...tail] = pathname.split("/")
  if (isLocale(maybe)) return { locale: maybe, rest: "/" + tail.join("/") }
  return { locale: null, rest: pathname }
}

function shouldSkip(pathname: string): boolean {
  return (
    pathname.startsWith("/api") ||
    pathname.startsWith("/_next") ||
    pathname.startsWith("/graphql") ||
    // Sign in with CircleXO: rewritten to the API (next.config), never a localized page.
    pathname.startsWith("/auth/circlexo/") ||
    pathname === "/sso/circlexo" ||
    // The feedback routes (app/feedback): the report, the widget's look and the reporter token.
    // /feedback/config was once redirected to /{locale}/feedback/config and 404'd (MH-464).
    pathname === "/feedback" ||
    pathname.startsWith("/feedback/") ||
    /\.[a-zA-Z0-9]+$/.test(pathname)
  )
}

function preferredLocale(request: NextRequest): Locale {
  const cookie = request.cookies.get(LOCALE_COOKIE)?.value
  if (isLocale(cookie)) return cookie
  return localeFromAcceptLanguage(request.headers.get("accept-language")) ?? DEFAULT_LOCALE
}

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl
  if (shouldSkip(pathname)) return NextResponse.next()

  const { locale, rest } = splitLocale(pathname)
  const signedIn = request.cookies.has(SESSION_COOKIE)

  // No locale in the URL: remembered language, then Accept-Language, then English.
  if (!locale) {
    const url = request.nextUrl.clone()
    const target = preferredLocale(request)
    url.pathname =
      pathname === "/" ? `/${target}${signedIn ? "" : "/login"}` : `/${target}${pathname}`
    return NextResponse.redirect(url)
  }

  const restPath = rest === "" ? "/" : rest.replace(/\/$/, "") || "/"

  // Signed in, /{locale} itself opens the last workspace's dashboard (MH-1108).
  if (restPath === "/" && !signedIn) {
    const url = request.nextUrl.clone()
    url.pathname = `/${locale}/login`
    return NextResponse.redirect(url)
  }

  if (isProtected(restPath) && !signedIn) {
    const url = request.nextUrl.clone()
    url.pathname = `/${locale}/login`
    url.search = ""
    url.searchParams.set("next", pathname + search)
    return NextResponse.redirect(url)
  }

  // The root layout cannot read route params, so it reads the locale from here.
  const headers = new Headers(request.headers)
  headers.set("x-locale", locale)
  const response = NextResponse.next({ request: { headers } })
  if (request.cookies.get(LOCALE_COOKIE)?.value !== locale) {
    response.cookies.set(LOCALE_COOKIE, locale, { path: "/", maxAge: 31536000, sameSite: "lax" })
  }
  return response
}

export const config = {
  matcher: "/:path*",
}
