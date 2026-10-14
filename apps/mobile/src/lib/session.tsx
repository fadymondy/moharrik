// The signed-in state: the API client, this install's device token, and the chosen workspace.
//
// Sign-in (docs/apps-plan.md §2) runs in the system browser through an auth session, which
// returns to moharrik://auth/callback; the code is traded for a device token (@moharrik/client
// signin.ts). A 401 on any call means the token was revoked elsewhere: the client forgets it and
// the app returns to sign-in.
import { useQueryClient } from "@tanstack/react-query"
import * as AppleAuthentication from "expo-apple-authentication"
import Constants from "expo-constants"
import * as Crypto from "expo-crypto"
import * as Device from "expo-device"
import * as WebBrowser from "expo-web-browser"
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { Platform } from "react-native"

import {
  api,
  base64url,
  finishAppleSignIn,
  finishSignIn,
  MoharrikClient,
  readCallback,
  startSignIn,
  type Api,
  type PkceCrypto,
  type SocialProvider,
  type Workspace,
} from "@moharrik/client"

import { usePrefs } from "./prefs"
import { getJSON, KEYS, setJSON, tokenStore } from "./storage"

export const REDIRECT = "moharrik://auth/callback"
const VERSION = Constants.expoConfig?.version ?? "0"

/** The configured server (app.json extra.serverUrl, or EXPO_PUBLIC_MOHARRIK_URL at build time). */
export const defaultServer = (): string => String(Constants.expoConfig?.extra?.serverUrl ?? "")

export type CurrentWorkspace = Pick<Workspace, "id" | "slug" | "name" | "role" | "avatar_url">

type Status = "loading" | "signedOut" | "signedIn"

interface Session {
  status: Status
  server: string
  client: MoharrikClient
  api: Api
  workspace: CurrentWorkspace | null
  /** Set when the server refused the stored token (shown once on the sign-in screen). */
  lost: boolean
  setServer(url: string): Promise<void>
  /** The browser flow; provider goes straight to Google (or Apple, on Android). */
  signIn(provider?: SocialProvider): Promise<"ok" | "cancelled">
  /** Native Sign in with Apple (iOS). */
  signInWithApple(): Promise<"ok" | "cancelled">
  signOut(): Promise<void>
  /** Forget this install's sign-in locally (the account was deleted on the server). */
  forget(): Promise<void>
  selectWorkspace(w: CurrentWorkspace | null): Promise<void>
}

const Ctx = createContext<Session | null>(null)

const crypto: PkceCrypto = {
  randomBytes: (n) => Crypto.getRandomBytes(n),
  sha256: async (d) => new Uint8Array(await Crypto.digest(Crypto.CryptoDigestAlgorithm.SHA256, new Uint8Array(d))),
}

function deviceName(): string {
  return Device.deviceName || [Device.manufacturer, Device.modelName].filter(Boolean).join(" ") || `Moharrik ${Platform.OS}`
}

function makeClient(server: string, onSignedOut: () => void) {
  return new MoharrikClient({
    baseUrl: server,
    store: tokenStore,
    onSignedOut,
    clientName: `mobile/${VERSION} (${Platform.OS})`,
  })
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const { locale } = usePrefs()
  const qc = useQueryClient()
  const [status, setStatus] = useState<Status>("loading")
  const [server, setServerState] = useState(defaultServer)
  const [workspace, setWorkspace] = useState<CurrentWorkspace | null>(null)
  const [lost, setLost] = useState(false)
  const onSignedOut = useRef(() => {})
  onSignedOut.current = () => {
    setLost(true)
    setStatus("signedOut")
    qc.clear()
  }
  const client = useMemo(() => makeClient(server, () => onSignedOut.current()), [server])
  client.workspaceId = workspace?.id ?? null

  useEffect(() => {
    void (async () => {
      const saved = await getJSON<string>(KEYS.server)
      const url = saved || defaultServer()
      if (url !== server) setServerState(url)
      const c = makeClient(url, () => {})
      const [token, ws] = await Promise.all([c.token(), getJSON<CurrentWorkspace>(KEYS.workspace)])
      setWorkspace(ws)
      setStatus(token ? "signedIn" : "signedOut")
    })()
    // Once, at launch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const setServer = useCallback(async (url: string) => {
    const clean = url.trim().replace(/\/+$/, "")
    const next = clean || defaultServer()
    if (next !== server) {
      // A device token belongs to the server that issued it: never send it to another one.
      await tokenStore.set(null)
      setStatus((s) => (s === "signedIn" ? "signedOut" : s))
    }
    await setJSON(KEYS.server, clean && clean !== defaultServer() ? clean : null)
    setServerState(next)
  }, [server])

  const signIn = useCallback(async (provider?: SocialProvider) => {
    const pending = await startSignIn({ serverUrl: client.baseUrl, redirectUri: REDIRECT, device: deviceName(), locale, crypto, provider })
    const res = await WebBrowser.openAuthSessionAsync(pending.url, REDIRECT, { preferEphemeralSession: false })
    if (res.type !== "success") return "cancelled"
    const code = readCallback(res.url, pending)
    await finishSignIn(client, pending, code)
    setLost(false)
    setStatus("signedIn")
    return "ok"
  }, [client, locale])

  const signInWithApple = useCallback(async () => {
    // Apple gets the sha256 of the nonce; the server gets the raw value and checks both.
    const raw = base64url(Crypto.getRandomBytes(32))
    const hashed = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, raw)
    let cred: AppleAuthentication.AppleAuthenticationCredential
    try {
      cred = await AppleAuthentication.signInAsync({
        requestedScopes: [AppleAuthentication.AppleAuthenticationScope.FULL_NAME, AppleAuthentication.AppleAuthenticationScope.EMAIL],
        nonce: hashed,
      })
    } catch (err) {
      if ((err as { code?: string }).code === "ERR_REQUEST_CANCELED") return "cancelled"
      throw err
    }
    if (!cred.identityToken || !cred.authorizationCode) throw new Error("Apple returned no token")
    await finishAppleSignIn(
      client,
      {
        identityToken: cred.identityToken,
        authorizationCode: cred.authorizationCode,
        nonce: raw,
        givenName: cred.fullName?.givenName,
        familyName: cred.fullName?.familyName,
      },
      deviceName(),
    )
    setLost(false)
    setStatus("signedIn")
    return "ok"
  }, [client])

  const forget = useCallback(async () => {
    await client.setToken(null)
    await setJSON(KEYS.workspace, null)
    await setJSON(KEYS.pushToken, null)
    setWorkspace(null)
    qc.clear()
    setStatus("signedOut")
  }, [client, qc])

  const signOut = useCallback(async () => {
    try {
      // Revokes this device's token and unregisters its push token on the server.
      await api(client).signOut()
    } catch {
      // Offline or already revoked: forget it locally all the same.
    }
    await client.setToken(null)
    await setJSON(KEYS.workspace, null)
    await setJSON(KEYS.pushToken, null)
    setWorkspace(null)
    qc.clear()
    setStatus("signedOut")
  }, [client, qc])

  const selectWorkspace = useCallback(
    async (w: CurrentWorkspace | null) => {
      await setJSON(KEYS.workspace, w)
      if (w?.id !== workspace?.id) qc.clear()
      setWorkspace(w)
    },
    [qc, workspace?.id],
  )

  const value = useMemo<Session>(
    () => ({ status, server, client, api: api(client), workspace, lost, setServer, signIn, signInWithApple, signOut, forget, selectWorkspace }),
    [status, server, client, workspace, lost, setServer, signIn, signInWithApple, signOut, forget, selectWorkspace],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useSession(): Session {
  const v = useContext(Ctx)
  if (!v) throw new Error("useSession outside SessionProvider")
  return v
}

/** The API, inside a workspace (every screen under (tabs)). */
export function useApi(): Api {
  return useSession().api
}
