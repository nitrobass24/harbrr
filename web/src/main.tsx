import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClientProvider } from "@tanstack/react-query"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { ThemeProvider } from "@/components/themes/theme-provider"
import { getBaseUrl } from "@/lib/base-url"
import { createQueryClient } from "@/lib/query"
import { routeTree } from "./routeTree.gen"
import "./index.css"

const router = createRouter({ routeTree, basepath: getBaseUrl() })

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}

// health/status polling opts in per-query via refetchInterval; defaults live in
// lib/query.ts.
const queryClient = createQueryClient()

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>
)
