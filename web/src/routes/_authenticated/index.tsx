import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_authenticated/")({
  component: Dashboard,
})

// Placeholder: the real Dashboard (stat tiles + health strip) is its own leaf
// (docs/webui-scope.md §8).
function Dashboard() {
  return (
    <div className="px-7 py-6">
      <h1 className="text-[15px] font-semibold tracking-tight">Dashboard</h1>
      <p className="mt-1 text-[13px] text-muted-foreground">
        harbrr web UI · {window.__HARBRR_VERSION__ ?? "dev"}
      </p>
    </div>
  )
}
