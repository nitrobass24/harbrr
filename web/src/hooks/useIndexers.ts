import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { AddIndexer, Instance, UpdateIndexer } from "@/types/api"

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
    onSettled: () => qc.invalidateQueries({ queryKey: ["indexers"] }),
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
    onSettled: () => qc.invalidateQueries({ queryKey: ["indexers"] }),
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

export function useTestIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => api.testIndexer(slug),
    onSettled: (_res, _err, slug) =>
      qc.invalidateQueries({ queryKey: ["indexers", slug, "status"] }),
  })
}
