import { createFileRoute, Navigate } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
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

  if (isLoading) return null
  // A transient non-401 me-probe failure must NOT be read as "logged out" — that
  // would redirect an authenticated user to /login and lose their page. Offer a
  // retry instead (the probe is retry:false, so it needs a manual nudge). Gate on
  // !isAuthenticated so a future background refetch failure (React Query keeps
  // isError=true even with cached data) can never yank an active session to this
  // screen — only a first probe with no session data lands here.
  if (isError && !isAuthenticated) return <AuthProbeError onRetry={retry} />
  if (isAuthenticated) return <AppLayout />
  if (setupLoading) return null
  if (setupComplete === false) return <Navigate to="/setup" />
  return <Navigate to="/login" />
}

// AuthProbeError is shown when the session probe errors (not a 401) so the visitor
// can retry rather than being bounced to the login screen.
function AuthProbeError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="flex max-w-sm flex-col items-center gap-4 text-center">
        <p role="alert" className="rounded-xl border border-bad/40 bg-bad/10 px-5 py-4 text-[13px] text-bad">
          Couldn&apos;t reach harbrr to confirm your session. Check that the server is running, then retry.
        </p>
        <Button onClick={onRetry}>Retry</Button>
      </div>
    </div>
  )
}
