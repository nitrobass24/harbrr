import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, APIError } from "@/lib/api"
import type { AddIndexer, Instance, TestResult, UpdateIndexer } from "@/types/api"

export function useIndexers() {
  return useQuery({
    queryKey: ["indexers"],
    queryFn: () => api.listIndexers(),
  })
}

export function useIndexer(slug: string, enabled = true) {
  return useQuery({
    queryKey: ["indexers", slug],
    queryFn: () => api.getIndexer(slug),
    enabled,
  })
}

// Health polling per slug, shared between the Indexers table and the Dashboard
// health strip via the query key (docs/webui-scope.md §2).
export function useIndexerStatuses(slugs: string[]) {
  return useQueries({
    queries: slugs.map((slug) => ({
      queryKey: ["indexers", slug, "status"],
      queryFn: () => api.getIndexerStatus(slug),
      refetchInterval: 30_000,
    })),
  })
}

export function useIndexerCapabilities(slug: string) {
  return useQuery({
    queryKey: ["indexers", slug, "capabilities"],
    queryFn: () => api.getIndexerCapabilities(slug),
    staleTime: 5 * 60_000, // caps only change on definition refresh
  })
}

// Capabilities for every listed indexer (drives the Categories column).
export function useIndexerCapabilitiesMany(slugs: string[]) {
  return useQueries({
    queries: slugs.map((slug) => ({
      queryKey: ["indexers", slug, "capabilities"],
      queryFn: () => api.getIndexerCapabilities(slug),
      staleTime: 5 * 60_000,
    })),
  })
}

export function useIndexerStats(slug: string, enabled = true) {
  return useQuery({
    queryKey: ["indexers", slug, "stats"],
    queryFn: () => api.getIndexerStats(slug),
    enabled,
  })
}

export function useAddIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: AddIndexer) => api.addIndexer(body),
    // Adding an indexer grows the aggregate stat set (useAllIndexerStats), which
    // is now keyed off ["indexer-stats"] and no longer caught by an ["indexers"]
    // prefix invalidation, so refresh it explicitly.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["indexers"] })
      void qc.invalidateQueries({ queryKey: ["indexer-stats"] })
    },
  })
}

export function useUpdateIndexer(slug: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: UpdateIndexer) => api.updateIndexer(slug, body),
    onSettled: () => qc.invalidateQueries({ queryKey: ["indexers"] }),
  })
}

export function useDeleteIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => api.deleteIndexer(slug),
    // Deleting an indexer shrinks the aggregate stat set (see useAddIndexer note).
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["indexers"] })
      void qc.invalidateQueries({ queryKey: ["indexer-stats"] })
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
      await qc.cancelQueries({ queryKey: ["indexers"] })
      const previous = qc.getQueryData<Instance[]>(["indexers"])
      qc.setQueryData<Instance[]>(["indexers"], (list) =>
        list?.map((ix) => (ix.slug === slug ? { ...ix, enabled } : ix)))
      return { previous }
    },
    onError: (_err, vars, context) => {
      if (context?.previous) qc.setQueryData(["indexers"], context.previous)
      toast.error(`${vars.enabled ? "Enabling" : "Disabling"} ${vars.slug} failed`)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["indexers"] }),
  })
}

function toastTestResult(result: TestResult, slug: string) {
  if (result.ok) toast.success(`${slug}: test passed`)
  else toast.error(`${slug}: test failed — ${result.error ?? "unknown error"}`)
}

function toastTestError(_err: unknown, slug: string) {
  toast.error(`${slug}: test request failed`)
}

// toastResult opts into hook-level pass/fail toasts. These are attached to the
// mutation itself (not a mutate()-call callback), so they still fire even if
// the component that triggered the test has since unmounted — e.g. the add/edit
// sheet's save-and-test flow, which closes immediately after calling mutate().
// Callers that stay mounted for the mutation's lifetime (the Indexers table's
// per-row test / test-all) toast at the call site instead, so leave this off
// there to avoid a double toast.
export function useTestIndexer(options?: { toastResult?: boolean }) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => api.testIndexer(slug),
    onSuccess: options?.toastResult ? toastTestResult : undefined,
    onError: options?.toastResult ? toastTestError : undefined,
    onSettled: (_res, _err, slug) =>
      qc.invalidateQueries({ queryKey: ["indexers", slug, "status"] }),
  })
}

// status carries the HTTP status when the test request itself failed (threw), so a
// caller can tell an auth/session failure (401/403) from a genuine tracker failure.
export type TestAllResult = { slug: string, ok: boolean, error?: string, status?: number }

// Test every configured indexer in parallel, resolving each result (never
// rejecting) so one failing tracker cannot mask the rest. Statuses are
// refreshed once the whole batch settles.
export function useTestAllIndexers() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slugs: string[]): Promise<TestAllResult[]> =>
      Promise.all(slugs.map(async (slug) => {
        try {
          const res = await api.testIndexer(slug)
          return { slug, ok: res.ok, error: res.error }
        } catch (err) {
          if (err instanceof APIError) return { slug, ok: false, error: err.message, status: err.status }
          return { slug, ok: false, error: "test request failed" }
        }
      })),
    onSettled: () => qc.invalidateQueries({ queryKey: ["indexers"] }),
  })
}
