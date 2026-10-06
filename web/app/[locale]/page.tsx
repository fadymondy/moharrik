import { OpenLastWorkspace } from "@/components/workspace/open-last-workspace"

// A signed-in visitor lands here (proxy.ts sends a signed-out one to login) and is taken to the
// dashboard of their last workspace; the list at /workspaces is only for picking another (MH-1108).
export default function LocaleIndex() {
  return <OpenLastWorkspace />
}
