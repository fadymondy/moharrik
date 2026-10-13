// Where the app connects. The server comes from the build (package.json "moharrik.serverUrl", or
// MOHARRIK_URL when building — scripts/build-main.mjs), MOHARRIK_URL at run time overrides it,
// and the sign-in screen can change it. Never a URL written into the code.
declare const __MOHARRIK_URL__: string

export const defaultServer = (): string => (process.env.MOHARRIK_URL || __MOHARRIK_URL__).replace(/\/+$/, "")

/** A scripted run against the local demo stack (scripts/shots.mjs): no system browser. */
export const e2e = process.env.MOHARRIK_E2E === "1"

/** MOHARRIK_SHOTS=<dir>: walk the screens, save a PNG of each, then quit (main/shots.ts). */
export const shotsDir = process.env.MOHARRIK_SHOTS || ""
