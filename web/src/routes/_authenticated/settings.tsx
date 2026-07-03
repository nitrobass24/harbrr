import { createFileRoute } from "@tanstack/react-router"
import { ApiKeysSection } from "@/components/settings/ApiKeysSection"
import { CacheSection } from "@/components/settings/CacheSection"
import { NotificationsSection } from "@/components/settings/NotificationsSection"
import { SystemSection } from "@/components/settings/SystemSection"

export const Route = createFileRoute("/_authenticated/settings")({
  component: SettingsPage,
})

function SettingsPage() {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-7">
        <div className="flex flex-col">
          <h1 className="text-[15px] font-semibold leading-tight tracking-tight">Settings</h1>
          <p className="text-[12px] text-faint">Cache, API keys, notifications, logging, account</p>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-7 py-6">
        <CacheSection />
        <ApiKeysSection />
        <NotificationsSection />
        <SystemSection />
      </div>
    </div>
  )
}
