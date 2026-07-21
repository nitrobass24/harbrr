import { useEffect, useRef } from "react"

import type { App } from "@/lib/api"

// useInitialAppPick applies a "Use as…" deep-link pre-pick (autobrr/harbrr#300)
// exactly once, the first time the target App appears in `apps` (already true at
// mount when the list is cached from the page the deep-link came from). A lazy
// initializer can't do this — it silently no-ops on a cold cache — and an ungated
// effect would clobber manual edits on a later refetch. The per-form apply body
// (which fields to set) stays at the call site; only the once-when-present gating
// is shared.
export function useInitialAppPick(
  initialAppId: number | undefined,
  apps: App[] | undefined,
  apply: (app: App) => void
) {
  const applied = useRef(false)
  useEffect(() => {
    if (initialAppId === undefined || applied.current) return
    const app = apps?.find((a) => a.id === initialAppId)
    if (!app) return
    applied.current = true
    apply(app)
  }, [initialAppId, apps, apply])
}
