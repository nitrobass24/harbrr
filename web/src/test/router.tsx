import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider, useSearch } from "@tanstack/react-router"
import type { ReactNode } from "react"

// Renders whatever search params the current route landed with (inline, not a named
// component — the file has one meaningful export and react-refresh's "only export
// components" check is happier that way) so a test can assert on the result of a
// `navigate({ to, search })` call without needing the real page behind that route.
// `strict: false` reads the nearest match's search generically.
function echo(testId: string) {
  return () => <p data-testid={testId}>{JSON.stringify(useSearch({ strict: false }))}</p>
}

// Minimal memory-router harness for components that render a router `Link`/`navigate`
// outside the real app routeTree (which drags in the auth guard): one route at "/"
// hosts the component under test; "/settings" is a plain target some links point at;
// "/applications" and "/download-clients" echo their search params back as text, for
// asserting on a "Use as…" navigate() call's destination + params.
export function withMemoryRouter(ui: ReactNode) {
  const rootRoute = createRootRoute()
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => ui })
  const settingsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: () => null })
  const applicationsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/applications",
    validateSearch: (search: Record<string, unknown>) => search,
    component: echo("echo-applications"),
  })
  const downloadClientsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/download-clients",
    validateSearch: (search: Record<string, unknown>) => search,
    component: echo("echo-download-clients"),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, settingsRoute, applicationsRoute, downloadClientsRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  return <RouterProvider router={router as never} />
}
