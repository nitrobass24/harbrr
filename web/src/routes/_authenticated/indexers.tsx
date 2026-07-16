import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { FlaskConical, Plus, Search as SearchIcon } from "lucide-react"
import { DeleteIndexerDialog } from "@/components/indexers/DeleteIndexerDialog"
import { IndexerCardsMobile } from "@/components/indexers/IndexerCardsMobile"
import { IndexerDetailsSheet } from "@/components/indexers/IndexerDetailsSheet"
import { IndexersTable, type IndexerRowActions, type IndexerRowData } from "@/components/indexers/IndexersTable"
import { SnippetDialog } from "@/components/indexers/SnippetDialog"
import { IndexerSheet, type IndexerSheetState } from "@/components/indexers/form/AddIndexerSheet"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { LoadError, LoadingBlock } from "@/components/ui/load-error"
import { useDefinitions } from "@/hooks/useDefinitions"
import { useIsMobile } from "@/hooks/useMediaQuery"
import {
  useDeleteIndexer,
  useIndexerCapabilitiesMany,
  useIndexers,
  useIndexerStatuses,
  useSetIndexerEnabled,
  useTestAllIndexers,
  useTestIndexer
} from "@/hooks/useIndexers"
import { PageHeader } from "@/components/layout/PageHeader"
import { APIError } from "@/lib/api"
import type { Capabilities } from "@/lib/api"
import { getBaseUrl } from "@/lib/base-url"
import { copyText } from "@/lib/clipboard"
import { notifyError, notifySuccess, notifyWarn } from "@/lib/notify"

// A 401/403 on a test means the session/CSRF is the problem, not the tracker — so it
// reads as a re-login prompt rather than "the indexer failed" (the #56 confusion).
const AUTH_FAILED_MSG = "Not authorized — your session may have expired. Reload the page and sign in again."
const isAuthStatus = (status?: number) => status === 401 || status === 403

export const Route = createFileRoute("/_authenticated/indexers")({
  component: IndexersPage,
})

function parentCategories(caps?: Capabilities): string {
  if (!caps?.categories) return ""
  const names = caps.categories.filter((c) => c.isParent || !c.parent).map((c) => c.name)
  return [...new Set(names)].join(", ")
}

function IndexersPage() {
  const isMobile = useIsMobile()
  const indexers = useIndexers()
  const definitions = useDefinitions()
  const slugs = (indexers.data ?? []).map((ix) => ix.slug)
  const statuses = useIndexerStatuses(slugs)
  const capabilities = useIndexerCapabilitiesMany(slugs)
  const toggle = useSetIndexerEnabled()
  const test = useTestIndexer()
  const testAll = useTestAllIndexers()
  const remove = useDeleteIndexer()

  const [filter, setFilter] = useState("")
  const [sheet, setSheet] = useState<IndexerSheetState>({ open: false })
  const [deleting, setDeleting] = useState<string | null>(null)
  const [snippetFor, setSnippetFor] = useState<string | null>(null)
  const [detailsFor, setDetailsFor] = useState<string | null>(null)

  const defTypes = new Map((definitions.data ?? []).map((d) => [d.id, d.type]))
  const needle = filter.toLowerCase()

  const rows: IndexerRowData[] = (indexers.data ?? [])
    .map((instance, i) => ({
      instance,
      type: defTypes.get(instance.definitionId),
      categories: parentCategories(capabilities[i]?.data),
      status: statuses[i]?.data,
      testing: test.isPending && test.variables === instance.slug,
    }))
    .filter((row) => row.instance.name.toLowerCase().includes(needle) ||
      row.instance.slug.includes(needle) ||
      (row.instance.baseUrl ?? "").includes(needle))

  const healthy = statuses.filter((s) => s.data?.status === "healthy").length
  const total = indexers.data?.length ?? 0

  const rowActions: IndexerRowActions = {
    onToggle: (slug, enabled) => toggle.mutate({ slug, enabled }),
    onTest: (slug) => test.mutate(slug, {
      onSuccess: (r) => r.ok ? notifySuccess(`${slug}: test passed`) : notifyError(`${slug}: test failed — ${r.error ?? "unknown error"}`),
      onError: (err) => notifyError(err instanceof APIError && isAuthStatus(err.status) ? AUTH_FAILED_MSG : `${slug}: test request failed`, err),
    }),
    onEdit: (slug) => setSheet({ open: true, mode: "edit", slug }),
    onDelete: setDeleting,
    onSnippet: setSnippetFor,
    onCopyFeedUrl: (slug) => {
      const url = `${window.location.origin}${getBaseUrl()}/api/indexers/${encodeURIComponent(slug)}/results/torznab`
      void copyText(url, "Feed URL copied (apps still need an API key)")
    },
    onDetails: setDetailsFor,
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Indexers" subtitle={`${total} configured · ${healthy} healthy`}>
        <div className="relative">
          <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
          <Input
            className="h-9 w-56 pl-8"
            placeholder="Filter indexers"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button
          variant="outline"
          disabled={testAll.isPending || total === 0}
          onClick={() => testAll.mutate(slugs, {
            onSuccess: (results) => {
              const passed = results.filter((r) => r.ok).length
              const failed = results.length - passed
              if (results.some((r) => isAuthStatus(r.status))) {
                notifyError(AUTH_FAILED_MSG)
              } else if (failed === 0) {
                notifySuccess(`All ${results.length} indexers passed`)
              } else {
                notifyWarn(`${passed} passed, ${failed} failed`)
              }
            },
            onError: (err) => notifyError("Test all failed", err),
          })}
        >
          <FlaskConical className="h-4 w-4" /> {testAll.isPending ? "Testing…" : "Test all"}
        </Button>
        <Button onClick={() => setSheet({ open: true, mode: "create" })}>
          <Plus className="h-4 w-4" /> Add indexer
        </Button>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-auto px-4 md:px-7 py-6">
        {indexers.isError && <LoadError what="indexers" />}
        {indexers.isLoading && <LoadingBlock />}
        {total === 0 && indexers.isSuccess ? (
          <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
            <div>
              <p className="text-[14px] font-medium">No indexers yet</p>
              <p className="mt-1 text-[13px] text-muted-foreground">Add your first tracker to start searching.</p>
              <Button className="mt-4" onClick={() => setSheet({ open: true, mode: "create" })}>
                <Plus className="h-4 w-4" /> Add indexer
              </Button>
            </div>
          </div>
        ) : indexers.isSuccess ? (
          <>
            {isMobile ? <IndexerCardsMobile rows={rows} actions={rowActions} /> : <IndexersTable rows={rows} actions={rowActions} />}
            <p className="mt-3 px-1 text-[12px] text-faint">Showing {rows.length} of {total} indexers</p>
          </>
        ) : null}
      </div>

      <IndexerSheet state={sheet} onClose={() => setSheet({ open: false })} />
      <IndexerDetailsSheet slug={detailsFor} onClose={() => setDetailsFor(null)} />
      <SnippetDialog slug={snippetFor} onClose={() => setSnippetFor(null)} />
      <DeleteIndexerDialog
        slug={deleting}
        pending={remove.isPending}
        onClose={() => setDeleting(null)}
        onConfirm={(slug) => remove.mutate(slug, {
          onSuccess: () => {
            notifySuccess(`${slug} deleted`)
            setDeleting(null)
          },
          onError: (err) => notifyError(`Deleting ${slug} failed`, err),
        })}
      />
    </div>
  )
}
