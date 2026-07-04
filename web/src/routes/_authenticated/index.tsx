import { createFileRoute, Link } from "@tanstack/react-router"
import { Plus, Search as SearchIcon } from "lucide-react"
import { DashboardTiles } from "@/components/dashboard/DashboardTiles"
import { HealthStrip } from "@/components/dashboard/HealthStrip"
import { Button } from "@/components/ui/button"

export const Route = createFileRoute("/_authenticated/")({
  component: Dashboard,
})

// At-a-glance value + health (docs/webui-scope.md §2): is harbrr healthy and
// how much tracker traffic is it saving. Reuses the Indexers screen's query
// keys, so navigating between the two never double-fetches.
function Dashboard() {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-7">
        <div className="flex flex-col">
          <h1 className="text-[15px] font-semibold leading-tight tracking-tight">Dashboard</h1>
          <p className="text-[12px] text-faint">harbrr · single source of truth for your indexers</p>
        </div>
        <div className="ml-auto flex items-center gap-2.5">
          <Button variant="outline" size="sm" asChild>
            <Link to="/search"><SearchIcon className="h-3.5 w-3.5" /> Search</Link>
          </Button>
          <Button size="sm" asChild>
            <Link to="/indexers"><Plus className="h-3.5 w-3.5" /> Add indexer</Link>
          </Button>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-auto px-7 py-6">
        <DashboardTiles />
        <HealthStrip />
      </div>
    </div>
  )
}
