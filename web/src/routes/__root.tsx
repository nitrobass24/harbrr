import { createRootRoute, Outlet } from "@tanstack/react-router"

export const Route = createRootRoute({
  component: () => <Outlet />,
  notFoundComponent: NotFound,
})

function NotFound() {
  return (
    <main className="grid min-h-screen place-items-center">
      <div className="text-center">
        <h1 className="text-2xl font-semibold tracking-tight">404</h1>
        <p className="text-sm text-muted-foreground">This page does not exist.</p>
      </div>
    </main>
  )
}
