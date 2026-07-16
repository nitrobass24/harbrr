import { createFileRoute } from "@tanstack/react-router"
import { CacheView } from "@/components/cache/CacheView"
import { PageHeader } from "@/components/layout/PageHeader"

export const Route = createFileRoute("/_authenticated/cache")({
  component: CachePage,
})

function CachePage() {
  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Cache" subtitle="Search-cache stats and live-tunable knobs" />
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-4 md:px-7 py-6">
        <CacheView />
      </div>
    </div>
  )
}
