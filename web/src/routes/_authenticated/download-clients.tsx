import { useEffect } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { DownloadClientsSection } from "@/components/download-clients/DownloadClientsSection"
import { PageHeader } from "@/components/layout/PageHeader"

export const Route = createFileRoute("/_authenticated/download-clients")({
  component: DownloadClientsPage,
  // "Use as…" deep-link (autobrr/harbrr#300): AppsSection lands here with the App
  // pre-picked for a new download client. An invalid/missing appId degrades to
  // undefined — the page just opens with nothing pre-picked, never crashes.
  validateSearch: (search: Record<string, unknown>): { appId?: number } => ({
    appId: typeof search.appId === "number" ? search.appId : undefined,
  }),
})

function DownloadClientsPage() {
  const navigate = useNavigate()
  const { appId } = Route.useSearch()

  // Open the add-dialog pre-picked, then clear the search param (replace: true) so a
  // close-and-reopen or the back button doesn't re-trigger it.
  useEffect(() => {
    if (appId === undefined) return
    void navigate({ to: "/download-clients", search: {}, replace: true })
  }, [appId, navigate])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Download Clients" subtitle="Send grabbed releases straight to a configured download client" />
      <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-auto px-4 md:px-7 py-6">
        <DownloadClientsSection initialCreate={appId !== undefined ? { appId } : undefined} />
      </div>
    </div>
  )
}
