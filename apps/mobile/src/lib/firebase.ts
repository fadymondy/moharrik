// The one door to @react-native-firebase (Zekra's src/lib/firebase.ts). Its JS modules look their
// native TurboModule up the moment they are imported, so a static import kills the app at launch
// in a build without Firebase linked — which is every build without the config files in secrets/
// (react-native.config.js). So nothing imports Firebase at module level: these loaders check the
// native module is registered first, then require() on demand. Types only are imported statically.
import { Platform, TurboModuleRegistry } from "react-native"

type AppModule = typeof import("@react-native-firebase/app")
type MessagingModule = typeof import("@react-native-firebase/messaging")

let linked: boolean | undefined

export function firebaseLinked(): boolean {
  if (linked === undefined) {
    try {
      linked = Platform.OS !== "web" && TurboModuleRegistry.get("NativeRNFBTurboApp") != null
    } catch {
      linked = false
    }
  }
  return linked
}

function loader<T>(load: () => T): () => T | null {
  let mod: T | null | undefined
  return () => {
    if (mod === undefined) {
      try {
        mod = firebaseLinked() ? load() : null
      } catch {
        mod = null
      }
    }
    return mod
  }
}

export const rnfbApp = loader<AppModule>(() => require("@react-native-firebase/app"))
export const rnfbMessaging = loader<MessagingModule>(() => require("@react-native-firebase/messaging"))

let ready: boolean | undefined

/** True when this build has a configured native Firebase app. */
export function firebaseReady(): boolean {
  if (ready === undefined) {
    try {
      const app = rnfbApp()
      ready = !!app && app.getApps().length > 0
    } catch {
      ready = false
    }
  }
  return ready
}
