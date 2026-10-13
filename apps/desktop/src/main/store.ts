// What the app remembers between launches. The device token lives only in `secret`, encrypted by
// the OS (safeStorage: Keychain on macOS, DPAPI on Windows). Ported from Mahaam's desktop store.
import { app, safeStorage } from "electron"
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { dirname, join } from "node:path"

import type { Locale } from "@moharrik/client"

import type { CurrentWorkspace, Prefs } from "../shared/types"

export interface Stored {
  server: string
  email: string
  workspace: CurrentWorkspace | null
  /** safeStorage-encrypted token, base64. */
  secret?: string
}

const systemLocale = (): Locale => (app.getPreferredSystemLanguages()[0] ?? app.getLocale()).toLowerCase().startsWith("en") ? "en" : "ar"

export const defaultPrefs = (): Prefs => ({ theme: "system", locale: systemLocale(), notify: true, sound: true, badge: true })

const file = (name: string) => join(app.getPath("userData"), name)

/** Linux without a keyring falls back to a fixed key ("basic_text"), which is not a keychain. */
export function canEncrypt(): boolean {
  if (!safeStorage.isEncryptionAvailable()) return false
  if (process.platform === "linux" && safeStorage.getSelectedStorageBackend?.() === "basic_text") return false
  return true
}

function readJson<T>(name: string): T | null {
  try {
    return existsSync(file(name)) ? (JSON.parse(readFileSync(file(name), "utf8")) as T) : null
  } catch {
    return null
  }
}

function writeJson(name: string, value: unknown): void {
  mkdirSync(dirname(file(name)), { recursive: true })
  writeFileSync(file(name), JSON.stringify(value, null, 2), { mode: 0o600 })
}

export function loadSession(): Stored | null {
  return readJson<Stored>("session.json")
}

export function readToken(s: Stored | null): string | null {
  if (!s?.secret || !canEncrypt()) return null
  try {
    return safeStorage.decryptString(Buffer.from(s.secret, "base64"))
  } catch {
    return null
  }
}

/** Without a keychain the token is not written at all: the person signs in each launch. */
export function saveSession(s: Omit<Stored, "secret">, token: string | null): void {
  const out: Stored = { ...s }
  if (token && canEncrypt()) out.secret = safeStorage.encryptString(token).toString("base64")
  writeJson("session.json", out)
}

export function loadPrefs(): Prefs {
  return { ...defaultPrefs(), ...(readJson<Partial<Prefs>>("prefs.json") ?? {}) }
}

export function savePrefs(p: Prefs): void {
  writeJson("prefs.json", p)
}

export function forget(): void {
  rmSync(file("session.json"), { force: true })
}
