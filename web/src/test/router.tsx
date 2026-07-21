import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router"
import type { ReactNode } from "react"

// Minimal memory-router harness for components that render a router `Link` outside
// the real app routeTree (which drags in the auth guard): one route at "/" hosts the
// component under test, one at "/settings" is the target some links point at.
export function withMemoryRouter(ui: ReactNode) {
  const rootRoute = createRootRoute()
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => ui })
  const settingsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: () => null })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, settingsRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  return <RouterProvider router={router as never} />
}
