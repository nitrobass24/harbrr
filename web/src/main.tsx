import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { getBaseUrl } from "@/lib/base-url"
import { routeTree } from "./routeTree.gen"
import "./index.css"

const router = createRouter({ routeTree, basepath: getBaseUrl() })

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}

// Conventions from docs/webui-scope.md §6: short staleness, no focus refetch;
// health/status polling opts in per-query via refetchInterval.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: false,
    },
  },
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
)
