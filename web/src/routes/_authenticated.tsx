import { useState } from "react"
import { createFileRoute, Navigate, useRouter } from "@tanstack/react-router"
import { ProbeError } from "@/components/auth/ProbeError"
import { useAuth } from "@/hooks/useAuth"
import { AppLayout } from "@/layouts/AppLayout"

// Pathless layout guarding every in-app screen: loading renders nothing (the
// me-probe answers in milliseconds), unauthenticated visitors route to setup
// (first run) or login, everyone else gets the shell.
export const Route = createFileRoute("/_authenticated")({
  component: Guard,
})

function Guard() {
  const { isLoading, isError, retry, isAuthenticated, setupComplete, setupLoading } = useAuth()
  const router = useRouter()
  // Snapshot the attempted location once, at mount, so login can return the visitor
  // there. location.pathname is basepath-relative (TanStack strips the router
  // basepath), so this is already an app-internal path — no basepath to add or
  // double-count. Reading router.state here (rather than subscribing via
  // useLocation) is deliberate: a live subscription would re-fire the <Navigate>
  // below on every transition commit and spin. The auth screens are top-level
  // routes (never under this guard), but stay defensive: never seed a redirect back
  // to /login or /setup, which would loop.
  const [redirect] = useState<string | undefined>(() => {
    const { pathname, searchStr } = router.state.location
    if (pathname === "/login" || pathname === "/setup") return undefined
    return pathname + searchStr
  })

  if (isLoading) return null
  // A transient non-401 me-probe failure must NOT be read as "logged out" — that
  // would redirect an authenticated user to /login and lose their page. Offer a
  // retry instead (the probe is retry:false, so it needs a manual nudge). Gate on
  // !isAuthenticated so a future background refetch failure (React Query keeps
  // isError=true even with cached data) can never yank an active session to this
  // screen — only a first probe with no session data lands here.
  if (isError && !isAuthenticated) {
    return <ProbeError message="Couldn't reach harbrr to confirm your session. Check that the server is running, then retry." onRetry={retry} />
  }
  if (isAuthenticated) return <AppLayout />
  if (setupLoading) return null
  if (setupComplete === false) return <Navigate to="/setup" />
  return <Navigate to="/login" search={{ redirect }} />
}
