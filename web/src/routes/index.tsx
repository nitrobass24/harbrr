import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/")({
  component: Index,
})

// Walking-skeleton stub: replaced by the Dashboard screen (docs/webui-scope.md §2).
function Index() {
  return (
    <main className="grid min-h-screen place-items-center">
      <div className="text-center">
        <h1 className="text-2xl font-semibold tracking-tight">harbrr</h1>
        <p className="text-sm text-muted-foreground">
          Web UI walking skeleton · {window.__HARBRR_VERSION__ ?? "dev"}
        </p>
      </div>
    </main>
  )
}
