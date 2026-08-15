import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { AddIndexer, IndexerStatus, Instance, TestAllResults, UpdateIndexer } from "@/lib/api"
import { notifyError } from "@/lib/notify"
import { keys } from "@/lib/query"

export function useIndexers() {
  return useQuery({
    queryKey: keys.indexers.list(),
    queryFn: () => api.listIndexers(),
  })
}

export function useIndexer(slug: string, enabled = true) {
  return useQuery({
    queryKey: keys.indexers.detail(slug),
    queryFn: () => api.getIndexer(slug),
    enabled,
  })
}

// Health polling per slug, shared between the Indexers table and the Dashboard
// health strip via the query key (docs/webui-scope.md §2).
export function useIndexerStatuses(slugs: string[]) {
  return useQueries({
    queries: slugs.map((slug) => ({
      queryKey: keys.indexers.status(slug),
      queryFn: () => api.getIndexerStatus(slug),
      refetchInterval: 30_000,
    })),
  })
}

// Recent captured failed fetches for one indexer (autobrr/harbrr#390). Memory-only
// server-side, so it is refetched on the same cadence as the status it explains.
export function useIndexerDiagnostics(slug: string) {
  return useQuery({
    queryKey: keys.indexers.diagnostics(slug),
    queryFn: () => api.getIndexerDiagnostics(slug),
    refetchInterval: 30_000,
  })
}

export function useIndexerCapabilities(slug: string) {
  return useQuery({
    queryKey: keys.indexers.capabilities(slug),
    queryFn: () => api.getIndexerCapabilities(slug),
    staleTime: 5 * 60_000, // caps only change on definition refresh
  })
}

// Capabilities for every listed indexer (drives the Categories column).
export function useIndexerCapabilitiesMany(slugs: string[]) {
  return useQueries({
    queries: slugs.map((slug) => ({
      queryKey: keys.indexers.capabilities(slug),
      queryFn: () => api.getIndexerCapabilities(slug),
      staleTime: 5 * 60_000,
    })),
  })
}

export function useIndexerStats(slug: string, enabled = true) {
  return useQuery({
    queryKey: keys.indexers.stats(slug),
    queryFn: () => api.getIndexerStats(slug),
    enabled,
  })
}

export function useAddIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: AddIndexer) => api.addIndexer(body),
    // Adding an indexer grows the aggregate stat set (useAllIndexerStats), which
    // lives under keys.indexerStats (its own root) and so is no longer caught by
    // an indexers.all prefix invalidation — refresh it explicitly.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.indexers.all })
      void qc.invalidateQueries({ queryKey: keys.indexerStats.all })
    },
  })
}

export function useUpdateIndexer(slug: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: UpdateIndexer) => api.updateIndexer(slug, body),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.indexers.all }),
  })
}

export function useDeleteIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => api.deleteIndexer(slug),
    // Deleting an indexer shrinks the aggregate stat set (see useAddIndexer note).
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.indexers.all })
      void qc.invalidateQueries({ queryKey: keys.indexerStats.all })
    },
  })
}

// Optimistic enable/disable: flip the switch instantly, roll back on error
// (qui's useInstances pattern, per docs/webui-scope.md §6).
export function useSetIndexerEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ slug, enabled }: { slug: string, enabled: boolean }) => api.setIndexerEnabled(slug, enabled),
    onMutate: async ({ slug, enabled }) => {
      await qc.cancelQueries({ queryKey: keys.indexers.all })
      const previous = qc.getQueryData<Instance[]>(keys.indexers.list())
      qc.setQueryData<Instance[]>(keys.indexers.list(), (list) =>
        list?.map((ix) => (ix.slug === slug ? { ...ix, enabled } : ix)))
      return { previous }
    },
    onError: (err, vars, context) => {
      if (context?.previous) qc.setQueryData(keys.indexers.list(), context.previous)
      notifyError(`${vars.enabled ? "Enabling" : "Disabling"} ${vars.slug} failed`, err)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.indexers.all }),
  })
}

// probePollMs / probeWaitMs bound the wait for a save's background probe: poll twice a
// second, give up after 30s. The queue normally turns a probe around in well under a
// second; past the ceiling the row's health badge is the honest place to look, and a
// saturated queue may have dropped the probe entirely.
const probePollMs = 500
const probeWaitMs = 30_000

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

// useProbeVerdict reports the outcome of the background credential probe the SERVER runs
// after a create, or after an update that changed a credential or the base URL
// (autobrr/harbrr#484). The sheet no longer fires its own follow-up test — that meant two
// logins back-to-back on a brand-new indexer, and it only ever worked for the browser
// (the API and a future importer got no health evidence at all) — so this waits out the
// one probe the save already triggered and surfaces its verdict.
//
// `probing` on the status payload is what makes that possible: without it, an edit whose
// probe has not started yet is indistinguishable from one whose probe passed and left the
// indexer exactly as healthy as it was.
//
// Only a FAILING verdict toasts. The save itself already said "saved", a passing probe
// simply leaves the badge green, and an edit that changed nothing the login depends on
// runs no probe at all — so silence here always means "nothing new is wrong".
export function useProbeVerdict() {
  const qc = useQueryClient()
  return async (slug: string) => {
    try {
      const deadline = Date.now() + probeWaitMs
      let status = await api.getIndexerStatus(slug)
      while (status.probing && Date.now() < deadline) {
        await sleep(probePollMs)
        status = await api.getIndexerStatus(slug)
      }
      // The badge reads the same query key, so seeding it here is also what makes the
      // row turn red at the same moment as the toast.
      qc.setQueryData(keys.indexers.status(slug), status)
      if (status.status === "failing") notifyError(`${slug}: ${probeFailureReason(status)}`)
    } catch (err) {
      // Reporting the verdict is best-effort: the badge still carries it.
      notifyError(`${slug}: reading the health check failed`, err)
    }
  }
}

// probeFailureReason is the operator-facing "why": the newest health event's detail (an
// auth failure quotes the tracker's own refusal), else its kind, else the honest generic
// — a failure nothing classified records no event at all, it is simply "queried, never
// succeeded".
function probeFailureReason(status: IndexerStatus): string {
  const event = status.events[0]
  return event?.detail ?? event?.kind ?? "health check failed"
}

// The explicit per-row test. Its only caller (the Indexers table) stays mounted for
// the mutation's whole lifetime, so it toasts at the call site; the add/edit sheet no
// longer tests at all, because the server now probes an indexer itself when it is
// created or its credentials change (autobrr/harbrr#484).
export function useTestIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => api.testIndexer(slug),
    onSettled: (_res, _err, slug) =>
      qc.invalidateQueries({ queryKey: keys.indexers.status(slug) }),
  })
}

export type TestAllResult = TestAllResults["results"][number]

// Test every configured indexer in ONE request. The server owns the fan-out now
// (autobrr/harbrr#485): it runs the batch through the same bounded probe queue the
// boot/create health probes use, so the burst is capped server-side instead of the
// browser opening one unbounded request per indexer. It also means the probes finish
// even if this page unmounts mid-run — only the reporting is lost, not the work.
//
// The server still resolves each indexer individually, so one failing tracker cannot
// mask the rest; a rejection here is the request itself failing (auth/session), which
// the call site distinguishes in onError.
export function useTestAllIndexers() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (slugs: string[]): Promise<TestAllResult[]> =>
      (await api.testAllIndexers(slugs)).results,
    onSettled: () => qc.invalidateQueries({ queryKey: keys.indexers.all }),
  })
}
