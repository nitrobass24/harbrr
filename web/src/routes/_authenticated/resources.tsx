import { createFileRoute } from "@tanstack/react-router"
import { ProxiesSection } from "@/components/resources/ProxiesSection"
import { SolversSection } from "@/components/resources/SolversSection"

export const Route = createFileRoute("/_authenticated/resources")({
  component: ResourcesPage,
})

function ResourcesPage() {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-7">
        <div className="flex flex-col">
          <h1 className="text-[15px] font-semibold leading-tight tracking-tight">Proxies &amp; Solvers</h1>
          <p className="text-[12px] text-faint">Shared proxy and FlareSolverr endpoints any indexer can use</p>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-7 py-6">
        <ProxiesSection />
        <SolversSection />
      </div>
    </div>
  )
}
