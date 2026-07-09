import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { CacheConfigUpdate, CreateNotification, LogLevel, UpdateNotification } from "@/types/api"

export function useHealth() {
  return useQuery({ queryKey: ["health"], queryFn: () => api.getHealth() })
}

export function useCacheStats() {
  return useQuery({
    queryKey: ["cache", "stats"],
    queryFn: () => api.getCacheStats(),
    refetchInterval: 30_000,
  })
}

export function useCacheConfig() {
  return useQuery({ queryKey: ["cache", "config"], queryFn: () => api.getCacheConfig() })
}

export function useUpdateCacheConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CacheConfigUpdate) => api.updateCacheConfig(body),
    onSettled: () => qc.invalidateQueries({ queryKey: ["cache"] }),
  })
}

export function useFlushCache() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.flushCache(),
    onSettled: () => qc.invalidateQueries({ queryKey: ["cache", "stats"] }),
  })
}

export function useLogLevel() {
  return useQuery({ queryKey: ["config", "log-level"], queryFn: () => api.getLogLevel() })
}

export function useSetLogLevel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (level: LogLevel) => api.setLogLevel(level),
    onSettled: () => qc.invalidateQueries({ queryKey: ["config", "log-level"] }),
  })
}

export function useApiKeys() {
  return useQuery({ queryKey: ["apikeys"], queryFn: () => api.listApiKeys() })
}

export function useMintApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.mintApiKey(name),
    onSettled: () => qc.invalidateQueries({ queryKey: ["apikeys"] }),
  })
}

export function useRevokeApiKey() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.revokeApiKey(id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["apikeys"] }),
  })
}

export function useNotifications() {
  return useQuery({ queryKey: ["notifications"], queryFn: () => api.listNotifications() })
}

export function useNotificationMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: ["notifications"] })
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

// Aggregate per-indexer stats. Keyed under its own ["indexer-stats"] root rather
// than ["indexers", "stats"] so an indexer whose slug is "stats" can never share
// a cache entry with the per-indexer detail key ["indexers", slug]. Add/delete of
// an indexer change the stat set, so those mutations invalidate this key
// explicitly (they no longer refresh it via an ["indexers"] prefix match).
export function useAllIndexerStats() {
  return useQuery({ queryKey: ["indexer-stats"], queryFn: () => api.listAllIndexerStats() })
}
