import { Redirect } from "expo-router"

import { useSession } from "@/lib/session"

export default function Index() {
  const { status, workspace } = useSession()
  if (status !== "signedIn") return <Redirect href="/sign-in" />
  return <Redirect href={workspace ? "/inbox" : "/workspaces"} />
}
