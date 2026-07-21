import { createFileRoute } from "@tanstack/react-router"
import { AppsSection } from "@/components/applications/AppsSection"
import { PageHeader } from "@/components/layout/PageHeader"
import { ApiKeysSection } from "@/components/settings/ApiKeysSection"
import { BackupSection } from "@/components/settings/BackupSection"
import { NotificationsSection } from "@/components/settings/NotificationsSection"
import { SystemSection } from "@/components/settings/SystemSection"

export const Route = createFileRoute("/_authenticated/settings")({
  component: SettingsPage,
})

function SettingsPage() {
  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Settings" subtitle="API keys, apps, notifications, logging, account" />
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-4 md:px-7 py-6">
        <ApiKeysSection />
        <AppsSection />
        <NotificationsSection />
        <BackupSection />
        <SystemSection />
      </div>
    </div>
  )
}
