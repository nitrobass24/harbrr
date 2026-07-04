import { createFileRoute } from "@tanstack/react-router"
import { CacheView } from "@/components/cache/CacheView"

export const Route = createFileRoute("/_authenticated/cache")({
  component: CachePage,
})

function CachePage() {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-7">
        <div className="flex flex-col">
          <h1 className="text-[15px] font-semibold leading-tight tracking-tight">Cache</h1>
          <p className="text-[12px] text-faint">Search-cache stats and live-tunable knobs</p>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-7 py-6">
        <CacheView />
      </div>
    </div>
  )
}
