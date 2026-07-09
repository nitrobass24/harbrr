import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Navigate, useNavigate } from "@tanstack/react-router"
import { AuthCard } from "@/components/auth/AuthCard"
import { ProbeError } from "@/components/auth/ProbeError"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/hooks/useAuth"
import { api, APIError, type SetupState } from "@/lib/api"

export const Route = createFileRoute("/setup")({
  component: Setup,
})

// setupErrorMessage maps a setup error to a friendly message (matching login.tsx),
// never surfacing the raw API message.
function setupErrorMessage(error: unknown): string | null {
  if (error instanceof APIError) {
    if (error.code === "invalid") return "Enter a username and a password of at least 8 characters."
    if (error.code === "already_setup") return "Setup is already complete — sign in instead."
    return "Setup failed — is the server reachable?"
  }
  return error ? "Setup failed — is the server reachable?" : null
}

// First-run wizard: create the single admin account, then sign in.
function Setup() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { isLoading, isAuthenticated, setupComplete, setupError, retrySetup } = useAuth()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")

  const create = useMutation({
    mutationFn: () => api.setup({ username, password }),
    onSuccess: () => {
      // Seed the setup-status cache synchronously so the /login guard reads the
      // fresh {setupComplete:true} on arrival. Without this it reads the stale
      // {setupComplete:false} and bounces back to /setup (a visible flash, or —
      // when the cache is still fresh so no refetch fires — a stuck empty form).
      queryClient.setQueryData<SetupState>(["auth", "setup"], { setupComplete: true })
      void navigate({ to: "/login" })
    },
  })

  if (isLoading) return null
  if (isAuthenticated) return <Navigate to="/" />
  if (setupComplete === true) return <Navigate to="/login" />
  if (setupError) {
    return <ProbeError message="Couldn't reach harbrr to check whether setup is needed. Check that the server is running, then retry." onRetry={retrySetup} />
  }
  // Only render the create-admin form once the probe confirms setup is NOT done.
  // While it is still in flight setupComplete is undefined — return nothing rather
  // than flash a form on a configured instance (or one whose probe is mid-request).
  if (setupComplete !== false) return null

  // Map the error code to a friendly message (matching login.tsx) rather than
  // surfacing the raw API message.
  const message = setupErrorMessage(create.error)

  const mismatch = confirm !== "" && confirm !== password

  return (
    <AuthCard title="Create the admin account" description="First run — this account manages every indexer and app connection.">
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          create.mutate()
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
          <Input id="password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="confirm">Confirm password</Label>
          <Input id="confirm" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          {mismatch && <p className="text-[12px] text-bad">Passwords do not match.</p>}
        </div>
        <Button type="submit" disabled={create.isPending || username === "" || password === "" || mismatch || confirm === ""}>
          {create.isPending ? "Creating…" : "Create account"}
        </Button>
      </form>
    </AuthCard>
  )
}
