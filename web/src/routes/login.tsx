import { useState } from "react"
import { createFileRoute, Navigate, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { AuthCard } from "@/components/auth/AuthCard"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/hooks/useAuth"
import { api, APIError } from "@/lib/api"
import { safeRedirectPath } from "@/lib/safe-redirect"

// The `redirect` search param carries the page a logged-out visitor was bounced
// from (set by the _authenticated guard and the 401 handler). Stored raw here and
// only validated at consumption via safeRedirectPath — the param is attacker-
// controllable, so it is never trusted as-is.
export const Route = createFileRoute("/login")({
  component: Login,
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
})

function Login() {
  const navigate = useNavigate()
  const { redirect } = Route.useSearch()
  const { isAuthenticated, setupComplete, login } = useAuth()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  // OIDC/SSO posture (autobrr/harbrr#9): the endpoint always answers 200 with a
  // disabled default, so no retry/error state is needed here — staleTime:
  // Infinity because it never changes without a restart.
  const { data: oidc } = useQuery({
    queryKey: ["oidc-config"],
    queryFn: () => api.getOIDCConfig(),
    staleTime: Infinity,
    retry: false,
  })
  const showSSO = oidc?.enabled ?? false
  const showBuiltInLogin = !oidc?.enabled || !oidc.disableBuiltInLogin

  // An already-authenticated visitor (or one who just signed in, while the me-probe
  // refetch is in flight) honours the validated redirect too, so a deep-link →
  // login → land-on-target flow isn't short-circuited to the dashboard by the race.
  const destination = safeRedirectPath(redirect)

  if (isAuthenticated) return <Navigate to={destination} />
  if (setupComplete === false) return <Navigate to="/setup" />

  const error = login.error
  const message = error instanceof APIError && error.code === "invalid_credentials" ? "Wrong username or password." : error ? "Login failed — is the server reachable?" : null

  return (
    <AuthCard title="Sign in" description="Use the admin account created at first run.">
      <div className="flex flex-col gap-4">
        {showBuiltInLogin && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              login.mutate({ username, password }, { onSuccess: () => void navigate({ to: destination }) })
            }}
          >
            {message && (
              <p role="alert" className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{message}</p>
            )}
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="username">Username</Label>
              <Input id="username" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="password">Password</Label>
              <Input id="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            <Button type="submit" disabled={login.isPending || username === "" || password === ""}>
              {login.isPending ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        )}
        {showBuiltInLogin && showSSO && (
          <div className="flex items-center gap-3 text-[12px] uppercase text-muted-foreground">
            <div className="h-px flex-1 bg-border" />
            or continue with
            <div className="h-px flex-1 bg-border" />
          </div>
        )}
        {showSSO && (
          <Button
            type="button"
            variant="outline"
            // Full-page navigation (not a fetch): the browser needs to actually
            // leave the app for the IdP's own login page.
            onClick={() => { window.location.href = oidc!.authorizationUrl }}
          >
            Sign in with SSO
          </Button>
        )}
      </div>
    </AuthCard>
  )
}
