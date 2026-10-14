// Two stores: the device and refresh tokens in the OS keystore (Keychain / Android Keystore through
// expo-secure-store), everything else (workspace, language, appearance, server) in AsyncStorage.
import AsyncStorage from "@react-native-async-storage/async-storage"
import * as SecureStore from "expo-secure-store"
import { Platform } from "react-native"

import type { TokenStore } from "@moharrik/client"

function secureStore(key: string): TokenStore {
  return Platform.OS === "web"
    ? {
        // Web is a development preview only; it has no keystore.
        get: async () => globalThis.sessionStorage?.getItem(key) ?? null,
        set: async (v) => (v ? globalThis.sessionStorage?.setItem(key, v) : globalThis.sessionStorage?.removeItem(key)),
      }
    : {
        get: () => SecureStore.getItemAsync(key),
        set: async (v) => {
          if (v) await SecureStore.setItemAsync(key, v, { keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK })
          else await SecureStore.deleteItemAsync(key)
        },
      }
}

/** The device token (short-lived) and the refresh token that renews it. */
export const tokenStore = secureStore("moharrik.token")
export const refreshStore = secureStore("moharrik.refresh")

export const KEYS = {
  workspace: "moharrik.workspace",
  server: "moharrik.server",
  locale: "moharrik.locale",
  theme: "moharrik.theme",
  pushToken: "moharrik.push.token",
  pushDevice: "moharrik.push.device",
} as const

export async function getJSON<T>(key: string): Promise<T | null> {
  try {
    const v = await AsyncStorage.getItem(key)
    return v ? (JSON.parse(v) as T) : null
  } catch {
    return null
  }
}

export const setJSON = (key: string, v: unknown) =>
  v === null || v === undefined ? AsyncStorage.removeItem(key) : AsyncStorage.setItem(key, JSON.stringify(v))
