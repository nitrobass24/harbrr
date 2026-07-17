import { createFileRoute } from "@tanstack/react-router"
import { DownloadClientsSection } from "@/components/download-clients/DownloadClientsSection"
import { PageHeader } from "@/components/layout/PageHeader"

export const Route = createFileRoute("/_authenticated/download-clients")({
  component: DownloadClientsPage,
})

function DownloadClientsPage() {
  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Download Clients" subtitle="Send grabbed releases straight to a configured download client" />
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-4 md:px-7 py-6">
        <DownloadClientsSection />
      </div>
    </div>
  )
}
