import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import type { Prefs, Route, SessionOut } from "../shared/types"
import { App } from "./app"
import { CallError, invoke } from "./bridge"
import { PrefsProvider } from "./prefs"
import "./styles.css"

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 20_000,
      // A 4xx will not get better by asking again.
      retry: (n, e) => !(e instanceof CallError && e.status >= 400 && e.status < 500) && n < 2,
      refetchOnWindowFocus: true,
    },
  },
})

const [prefs, start] = await Promise.all([
  invoke<Prefs>("app:prefs"),
  invoke<{ session: SessionOut | null; server: string; route: Route | null }>("session:get"),
])

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <PrefsProvider initial={prefs}>
        <App initial={start} />
      </PrefsProvider>
    </QueryClientProvider>
  </StrictMode>,
)
