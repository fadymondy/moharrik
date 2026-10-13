import { fileURLToPath } from "node:url"
import { defineConfig, type Plugin } from "vite"
import react from "@vitejs/plugin-react"

// The renderer makes no network requests of its own (the main process does), so the production CSP
// allows nothing but its own files, plus https images (avatars). Dev relaxes script/connect for
// Vite's HMR client. The same split as Mahaam's desktop (managy apps/desktop/vite.config.mts).
const PROD_CSP =
  "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: https:; connect-src 'none'; base-uri 'none'; form-action 'none'; object-src 'none'"
const DEV_CSP =
  "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: https: http:; connect-src 'self' ws://localhost:5185; object-src 'none'"

function csp(): Plugin {
  let dev = true
  return {
    name: "moharrik-csp",
    configResolved(c) {
      dev = c.command === "serve"
    },
    transformIndexHtml(html) {
      return html.replace("<!--CSP-->", `<meta http-equiv="Content-Security-Policy" content="${dev ? DEV_CSP : PROD_CSP}" />`)
    },
  }
}

export default defineConfig({
  root: "src/renderer",
  base: "./",
  plugins: [react(), csp()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src/renderer", import.meta.url)) } },
  server: { port: 5185, strictPort: true },
  build: {
    chunkSizeWarningLimit: 4000,
    outDir: "../../dist/renderer",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    target: "chrome140",
  },
})
