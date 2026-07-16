import { createFileRoute } from "@tanstack/react-router"
import { PageHeader } from "@/components/layout/PageHeader"
import { ProxiesSection } from "@/components/resources/ProxiesSection"
import { SolversSection } from "@/components/resources/SolversSection"

export const Route = createFileRoute("/_authenticated/resources")({
  component: ResourcesPage,
})

function ResourcesPage() {
  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Proxies & Solvers" subtitle="Shared proxy and FlareSolverr endpoints any indexer can use" />
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-4 md:px-7 py-6">
        <ProxiesSection />
        <SolversSection />
      </div>
    </div>
  )
}
