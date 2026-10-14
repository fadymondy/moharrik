import { createHash, randomBytes } from "node:crypto"
import { describe, expect, it } from "vitest"

import { ApiError, base64url, challengeOf, errorCode, finishSignIn, MoharrikClient, parseSignInOptions, readCallback, startSignIn, type PkceCrypto } from "../index"

const nodeCrypto: PkceCrypto = {
  randomBytes: (n) => new Uint8Array(randomBytes(n)),
  sha256: async (d) => new Uint8Array(createHash("sha256").update(d).digest()),
}

describe("app sign-in", () => {
  it("base64url matches Node's", () => {
    for (let n = 0; n < 40; n++) {
      const b = randomBytes(n)
      expect(base64url(new Uint8Array(b))).toBe(b.toString("base64url"))
    }
  })

  it("computes the RFC 7636 appendix B challenge", async () => {
    expect(await challengeOf("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", nodeCrypto)).toBe(
      "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
    )
  })

  it("opens the web page with what the API validates", async () => {
    const p = await startSignIn({
      serverUrl: "https://app.moharrik.com/",
      redirectUri: "moharrik://auth/callback",
      device: "Pixel 9",
      locale: "ar",
      crypto: nodeCrypto,
    })
    const u = new URL(p.url)
    expect(u.origin + u.pathname).toBe("https://app.moharrik.com/ar/app-signin")
    expect(u.searchParams.get("redirect_uri")).toBe("moharrik://auth/callback")
    expect(u.searchParams.get("code_challenge_method")).toBe("S256")
    expect(u.searchParams.get("code_challenge")).toBe(await challengeOf(p.verifier, nodeCrypto))
    expect(u.searchParams.get("device")).toBe("Pixel 9")
    // The API's limits (internal/appsignin): verifier 43–128, challenge ≥ 43, state ≥ 16.
    expect(p.verifier.length).toBeGreaterThanOrEqual(43)
    expect(u.searchParams.get("code_challenge")!.length).toBeGreaterThanOrEqual(43)
    expect(p.state.length).toBeGreaterThanOrEqual(16)
  })

  it("refuses a callback with another state", () => {
    expect(readCallback("moharrik://auth/callback?code=mas_1&state=abc", { state: "abc" })).toBe("mas_1")
    expect(() => readCallback("moharrik://auth/callback?code=mas_1&state=evil", { state: "abc" })).toThrow(/state/)
    expect(() => readCallback("http://127.0.0.1:5/callback?state=abc", { state: "abc" })).toThrow()
  })

  it("trades the code and stores the token", async () => {
    let sent: unknown
    let value: string | null = null
    const c = new MoharrikClient({
      baseUrl: "http://x",
      store: {
        get: async () => value,
        set: async (v) => {
          value = v
        },
      },
      fetch: (async (_u: string, init: RequestInit) => {
        sent = JSON.parse(String(init.body))
        return new Response(JSON.stringify({ token: "togo_pat_new", email: "a@b.c" }), { status: 200 })
      }) as unknown as typeof fetch,
    })
    const r = await finishSignIn(c, { verifier: "v".repeat(43), redirectUri: "moharrik://auth/callback" }, "mas_1")
    expect(r.email).toBe("a@b.c")
    expect(value).toBe("togo_pat_new")
    expect(sent).toEqual({ code: "mas_1", code_verifier: "v".repeat(43), redirect_uri: "moharrik://auth/callback" })
  })
})

describe("social sign-in", () => {
  it("reads the server's sign-in options, ignoring what it does not know", () => {
    expect(
      parseSignInOptions({
        methods: [
          { name: "apple", type: "oauth", url: "/api/auth/oauth/apple" },
          { name: "apple", type: "native", url: "/api/auth/native/apple" },
          { name: "google", type: "oauth", url: "/api/auth/oauth/google" },
          { name: "github", type: "oauth" },
          "junk",
        ],
      }),
    ).toEqual({ appleWeb: true, appleNative: true, googleWeb: true })
    expect(parseSignInOptions({ methods: [] })).toEqual({ appleWeb: false, appleNative: false, googleWeb: false })
    expect(parseSignInOptions(null)).toEqual({ appleWeb: false, appleNative: false, googleWeb: false })
  })

  it("asks the sign-in page to go straight to a provider", async () => {
    const crypto = {
      randomBytes: (n: number) => new Uint8Array(n).fill(7),
      sha256: async (d: Uint8Array) => new Uint8Array(createHash("sha256").update(d).digest()),
    }
    const p = await startSignIn({ serverUrl: "https://app.moharrik.com/", redirectUri: "moharrik://auth/callback", device: "Pixel", locale: "ar", crypto, provider: "google" })
    const u = new URL(p.url)
    expect(u.pathname).toBe("/ar/app-signin")
    expect(u.searchParams.get("provider")).toBe("google")
    const plain = await startSignIn({ serverUrl: "https://app.moharrik.com", redirectUri: "moharrik://auth/callback", device: "Pixel", locale: "en", crypto })
    expect(new URL(plain.url).searchParams.has("provider")).toBe(false)
  })

  it("reads a refusal code from an API error", () => {
    expect(errorCode(new ApiError(409, "conflict", { error: "x", code: "account_conflict" }))).toBe("account_conflict")
    expect(errorCode(new ApiError(500, "boom"))).toBeUndefined()
    expect(errorCode(new Error("x"))).toBeUndefined()
  })
})
