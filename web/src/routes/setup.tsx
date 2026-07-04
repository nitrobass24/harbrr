import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { createFileRoute, Navigate, useNavigate } from "@tanstack/react-router"
import { AuthCard } from "@/components/auth/AuthCard"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/hooks/useAuth"
import { api, APIError } from "@/lib/api"

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
  const { isAuthenticated, setupComplete } = useAuth()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")

  const create = useMutation({
    mutationFn: () => api.setup({ username, password }),
    onSuccess: () => void navigate({ to: "/login" }),
  })

  if (isAuthenticated) return <Navigate to="/" />
  if (setupComplete === true) return <Navigate to="/login" />

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
