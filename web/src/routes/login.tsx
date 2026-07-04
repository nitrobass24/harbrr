import { useState } from "react"
import { createFileRoute, Navigate, useNavigate } from "@tanstack/react-router"
import { AuthCard } from "@/components/auth/AuthCard"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/hooks/useAuth"
import { APIError } from "@/lib/api"

export const Route = createFileRoute("/login")({
  component: Login,
})

function Login() {
  const navigate = useNavigate()
  const { isAuthenticated, setupComplete, login } = useAuth()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  if (isAuthenticated) return <Navigate to="/" />
  if (setupComplete === false) return <Navigate to="/setup" />

  const error = login.error
  const message = error instanceof APIError && error.code === "invalid_credentials" ? "Wrong username or password." : error ? "Login failed — is the server reachable?" : null

  return (
    <AuthCard title="Sign in" description="Use the admin account created at first run.">
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          login.mutate({ username, password }, { onSuccess: () => void navigate({ to: "/" }) })
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
    </AuthCard>
  )
}
