import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { CacheConfigUpdate, CreateNotification, LogLevel, UpdateNotification } from "@/lib/api"
import { keys } from "@/lib/query"

export function useHealth() {
  return useQuery({ queryKey: keys.health.all, queryFn: () => api.getHealth() })
}

export function useCacheStats() {
  return useQuery({
    queryKey: keys.cache.stats(),
    queryFn: () => api.getCacheStats(),
    refetchInterval: 30_000,
  })
}

export function useCacheConfig() {
  return useQuery({ queryKey: keys.cache.config(), queryFn: () => api.getCacheConfig() })
}

export function useUpdateCacheConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CacheConfigUpdate) => api.updateCacheConfig(body),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.all }),
  })
}

export function useFlushCache() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.flushCache(),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.stats() }),
  })
}

// Resetting the STATISTICS is a separate action from flushing the cached RESULTS
// (see CacheView) — it destroys history that cannot be recovered.
export function useResetCacheStats() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.resetCacheStats(),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.stats() }),
  })
}

export function useLogLevel() {
  return useQuery({ queryKey: keys.config.logLevel(), queryFn: () => api.getLogLevel() })
}

export function useSetLogLevel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (level: LogLevel) => api.setLogLevel(level),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.config.logLevel() }),
  })
}

export function useAdultCategories() {
  return useQuery({ queryKey: keys.config.adultCategories(), queryFn: () => api.getAdultCategories() })
}

// useSetAdultCategories toggles the global hide-adult-categories setting. The
// category lists themselves are filtered server-side, so every cached
// capabilities/definition response is stale the moment the setting flips —
// invalidate those roots too or a picker keeps showing the old taxonomy until
// its next natural refetch.
export function useSetAdultCategories() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (hidden: boolean) => api.setAdultCategories(hidden),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.config.adultCategories() })
      void qc.invalidateQueries({ queryKey: keys.indexers.all })
      void qc.invalidateQueries({ queryKey: keys.definitions.all })
    },
  })
}

// The expiry lead-time dial (#399). Nothing else caches on it, so a plain
// invalidate of its own key is the whole story.
export function useExpiryThresholds() {
  return useQuery({ queryKey: keys.config.expiryThresholds(), queryFn: () => api.getExpiryThresholds() })
}

export function useSetExpiryThresholds() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (days: number[]) => api.setExpiryThresholds(days),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.config.expiryThresholds() }),
  })
}

export function useApiKeys() {
  return useQuery({ queryKey: keys.apiKeys.all, queryFn: () => api.listApiKeys() })
}

export function useMintApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.mintApiKey(name),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.apiKeys.all }),
  })
}

export function useRevokeApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.revokeApiKey(id),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.apiKeys.all }),
  })
}

export function useNotifications() {
  return useQuery({ queryKey: keys.notifications.all, queryFn: () => api.listNotifications() })
}

export function useNotificationMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.notifications.all })
  return {
    create: useMutation({ mutationFn: (body: CreateNotification) => api.createNotification(body), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateNotification }) => api.updateNotification(id, body),
      onSettled: invalidate,
    }),
    remove: useMutation({ mutationFn: (id: number) => api.deleteNotification(id), onSettled: invalidate }),
    toggle: useMutation({
      mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setNotificationEnabled(id, enabled),
      onSettled: invalidate,
    }),
    test: useMutation({ mutationFn: (id: number) => api.testNotification(id) }),
  }
}

export function useChangePassword() {
  return useMutation({
    mutationFn: ({ current, next }: { current: string, next: string }) => api.changePassword(current, next),
  })
}

export function useExportBackup() {
  return useMutation({
    mutationFn: (passphrase: string) => api.exportBackup(passphrase),
  })
}

// useImportBackup restores a bundle (wipe-and-load) — no query invalidation on
// success, since a restore replaces API keys and possibly the admin account, so
// the caller hard-reloads the page instead of patching cached queries.
export function useImportBackup() {
  return useMutation({
    mutationFn: ({ payload, passphrase, force }: { payload: string, passphrase: string, force?: boolean }) =>
      api.importBackup(payload, passphrase, force),
  })
}

// Aggregate per-indexer stats. Keyed under its own ["indexer-stats"] root rather
// than ["indexers", "stats"] so an indexer whose slug is "stats" can never share
// a cache entry with the per-indexer detail key ["indexers", slug]. Add/delete of
// an indexer change the stat set, so those mutations invalidate this key
// explicitly (they no longer refresh it via an ["indexers"] prefix match).
export function useAllIndexerStats() {
  return useQuery({ queryKey: keys.indexerStats.all, queryFn: () => api.listAllIndexerStats() })
}
