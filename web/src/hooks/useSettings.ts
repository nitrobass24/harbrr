import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import type { CacheConfigUpdate, CreateNotification, LogLevel, UpdateNotification } from "@/lib/api"
import { keys } from "@/lib/query"

export function useHealth() {
  return useQuery({ queryKey: keys.health.all, queryFn: () => unwrap(api.http.GET("/healthz")) })
}

export function useCacheStats() {
  return useQuery({
    queryKey: keys.cache.stats(),
    queryFn: () => unwrap(api.http.GET("/api/cache/stats")),
    refetchInterval: 30_000,
  })
}

export function useCacheConfig() {
  return useQuery({ queryKey: keys.cache.config(), queryFn: () => unwrap(api.http.GET("/api/cache/config")) })
}

export function useUpdateCacheConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CacheConfigUpdate) => unwrap(api.http.PUT("/api/cache/config", { body: body })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.all }),
  })
}

export function useFlushCache() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.http.POST("/api/cache/flush")),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.stats() }),
  })
}

// Resetting the STATISTICS is a separate action from flushing the cached RESULTS
// (see CacheView) — it destroys history that cannot be recovered.
export function useResetCacheStats() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.http.POST("/api/cache/stats/reset")),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cache.stats() }),
  })
}

export function useLogLevel() {
  return useQuery({ queryKey: keys.config.logLevel(), queryFn: () => unwrap(api.http.GET("/api/config/log-level")) })
}

export function useSetLogLevel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (level: LogLevel) => unwrap(api.http.PUT("/api/config/log-level", { body: { level } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.config.logLevel() }),
  })
}

export function useAdultCategories() {
  return useQuery({ queryKey: keys.config.adultCategories(), queryFn: () => unwrap(api.http.GET("/api/config/adult-categories")) })
}

// useSetAdultCategories toggles the global hide-adult-categories setting. The
// category lists themselves are filtered server-side, so every cached
// capabilities/definition response is stale the moment the setting flips —
// invalidate those roots too or a picker keeps showing the old taxonomy until
// its next natural refetch.
export function useSetAdultCategories() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (hidden: boolean) => unwrap(api.http.PUT("/api/config/adult-categories", { body: { hidden } })),
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
  return useQuery({ queryKey: keys.config.expiryThresholds(), queryFn: () => unwrap(api.http.GET("/api/config/expiry-thresholds")) })
}

export function useSetExpiryThresholds() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (days: number[]) => unwrap(api.http.PUT("/api/config/expiry-thresholds", { body: { days } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.config.expiryThresholds() }),
  })
}

export function useApiKeys() {
  return useQuery({ queryKey: keys.apiKeys.all, queryFn: () => unwrap(api.http.GET("/api/apikeys")) })
}

export function useMintApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => unwrap(api.http.POST("/api/apikeys", { body: { name } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.apiKeys.all }),
  })
}

export function useRevokeApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.http.DELETE("/api/apikeys/{id}", { params: { path: { id } } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.apiKeys.all }),
  })
}

export function useNotifications() {
  return useQuery({ queryKey: keys.notifications.all, queryFn: () => unwrap(api.http.GET("/api/notifications")) })
}

export function useNotificationMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.notifications.all })
  return {
    create: useMutation({ mutationFn: (body: CreateNotification) => unwrap(api.http.POST("/api/notifications", { body: body })), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateNotification }) => unwrap(api.http.PATCH("/api/notifications/{id}", { params: { path: { id } }, body })),
      onSettled: invalidate,
    }),
    remove: useMutation({ mutationFn: (id: number) => unwrap(api.http.DELETE("/api/notifications/{id}", { params: { path: { id } } })), onSettled: invalidate }),
    toggle: useMutation({
      mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setNotificationEnabled(id, enabled),
      onSettled: invalidate,
    }),
    test: useMutation({ mutationFn: (id: number) => unwrap(api.http.POST("/api/notifications/{id}/test", { params: { path: { id } } })) }),
  }
}

export function useChangePassword() {
  return useMutation({
    mutationFn: ({ current, next }: { current: string, next: string }) => unwrap(api.http.POST("/api/auth/change-password", { body: { currentPassword: current, newPassword: next } })),
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
      unwrap(api.http.POST("/api/import", { body: { payload, passphrase, force } })),
  })
}

// Aggregate per-indexer stats. Keyed under its own ["indexer-stats"] root rather
// than ["indexers", "stats"] so an indexer whose slug is "stats" can never share
// a cache entry with the per-indexer detail key ["indexers", slug]. Add/delete of
// an indexer change the stat set, so those mutations invalidate this key
// explicitly (they no longer refresh it via an ["indexers"] prefix match).
export function useAllIndexerStats() {
  return useQuery({ queryKey: keys.indexerStats.all, queryFn: () => unwrap(api.http.GET("/api/indexers/stats")) })
}
