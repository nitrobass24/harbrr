import { createFileRoute, Navigate } from "@tanstack/react-router"
import { useAuth } from "@/hooks/useAuth"
import { AppLayout } from "@/layouts/AppLayout"

// Pathless layout guarding every in-app screen: loading renders nothing (the
// me-probe answers in milliseconds), unauthenticated visitors route to setup
// (first run) or login, everyone else gets the shell.
export const Route = createFileRoute("/_authenticated")({
  component: Guard,
})

function Guard() {
  const { isLoading, isAuthenticated, setupComplete, setupLoading } = useAuth()

  if (isLoading) return null
  if (isAuthenticated) return <AppLayout />
  if (setupLoading) return null
  if (setupComplete === false) return <Navigate to="/setup" />
  return <Navigate to="/login" />
}
