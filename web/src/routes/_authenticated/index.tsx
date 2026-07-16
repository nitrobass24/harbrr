import { createFileRoute, Link } from "@tanstack/react-router"
import { Plus, Search as SearchIcon } from "lucide-react"
import { PageHeader } from "@/components/layout/PageHeader"
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
      <PageHeader title="Dashboard" subtitle="harbrr · single source of truth for your indexers">
        <Button variant="outline" size="sm" asChild>
          <Link to="/search"><SearchIcon className="h-3.5 w-3.5" /> Search</Link>
        </Button>
        <Button size="sm" asChild>
          <Link to="/indexers"><Plus className="h-3.5 w-3.5" /> Add indexer</Link>
        </Button>
      </PageHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-auto px-4 md:px-7 py-6">
        <DashboardTiles />
        <HealthStrip />
      </div>
    </div>
  )
}
