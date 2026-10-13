// moharrik://auth/callback when the OS routes it here rather than to the open auth session
// (a cold start from the browser). The session moves on once it holds a token.
import { router } from "expo-router"
import { useEffect } from "react"

import { Loading, Screen } from "@/components/ui"

export default function Callback() {
  useEffect(() => {
    const t = setTimeout(() => router.replace("/"), 1500)
    return () => clearTimeout(t)
  }, [])
  return (
    <Screen>
      <Loading />
    </Screen>
  )
}
