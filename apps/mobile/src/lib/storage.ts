// Two stores: the device token in the OS keystore (Keychain / Android Keystore through
// expo-secure-store), everything else (workspace, language, appearance, server) in AsyncStorage.
import AsyncStorage from "@react-native-async-storage/async-storage"
import * as SecureStore from "expo-secure-store"
import { Platform } from "react-native"

import type { TokenStore } from "@moharrik/client"

const TOKEN = "moharrik.token"

export const tokenStore: TokenStore =
  Platform.OS === "web"
    ? {
        // Web is a development preview only; it has no keystore.
        get: async () => globalThis.sessionStorage?.getItem(TOKEN) ?? null,
        set: async (v) => (v ? globalThis.sessionStorage?.setItem(TOKEN, v) : globalThis.sessionStorage?.removeItem(TOKEN)),
      }
    : {
        get: () => SecureStore.getItemAsync(TOKEN),
        set: async (v) => {
          if (v) await SecureStore.setItemAsync(TOKEN, v, { keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK })
          else await SecureStore.deleteItemAsync(TOKEN)
        },
      }

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
