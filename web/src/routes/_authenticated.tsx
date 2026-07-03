import { createFileRoute } from "@tanstack/react-router"
import { AppLayout } from "@/layouts/AppLayout"

// Pathless layout wrapping every in-app screen with the shell. The auth guard
// (redirect to /login when unauthenticated) lands with the auth leaf
// (docs/webui-scope.md §8).
export const Route = createFileRoute("/_authenticated")({
  component: AppLayout,
})
