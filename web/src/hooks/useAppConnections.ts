import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import { notifyError } from "@/lib/notify"
import { keys } from "@/lib/query"
import type {
  AppConnection,
  CreateAnnounceConnection,
  CreateConnection,
  CreateSyncProfile,
  UpdateAnnounceConnection,
  UpdateConnection,
  UpdateSyncProfile
} from "@/lib/api"

export function useAppConnections() {
  return useQuery({
    queryKey: keys.appConnections.all,
    queryFn: () => unwrap(api.http.GET("/api/app-connections")),
  })
}

// useServerInfo reflects harbrr's live listen port, used to flag app-sync
// connections whose stored harbrrUrl port has drifted stale.
export function useServerInfo() {
  return useQuery({
    queryKey: keys.serverInfo.all,
    queryFn: () => unwrap(api.http.GET("/api/server-info")),
  })
}

export function useConnectionStatus(id: number | null) {
  return useQuery({
    queryKey: keys.appConnections.status(id),
    queryFn: () => unwrap(api.http.GET("/api/app-connections/{id}/status", { params: { path: { id: id as number } } })),
    enabled: id !== null,
  })
}

function useInvalidateConnections() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: keys.appConnections.all })
}

export function useCreateConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (body: CreateConnection) => unwrap(api.http.POST("/api/app-connections", { body: body })),
    onSettled: invalidate,
  })
}

// The id travels with each mutate() call (mirroring useSetConnectionEnabled),
// so one hook serves both the edit dialog and per-row actions like the
// stale-port fix.
export function useUpdateConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateConnection }) => unwrap(api.http.PATCH("/api/app-connections/{id}", { params: { path: { id } }, body })),
    onSettled: invalidate,
  })
}

export function useDeleteConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.http.DELETE("/api/app-connections/{id}", { params: { path: { id } } })),
    onSettled: invalidate,
  })
}

// Optimistic switch flip, mirroring the indexers pattern.
export function useSetConnectionEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setConnectionEnabled(id, enabled),
    onMutate: async ({ id, enabled }) => {
      await qc.cancelQueries({ queryKey: keys.appConnections.all })
      const previous = qc.getQueryData<AppConnection[]>(keys.appConnections.all)
      qc.setQueryData<AppConnection[]>(keys.appConnections.all, (list) =>
        list?.map((c) => (c.id === id ? { ...c, enabled } : c)))
      return { previous }
    },
    onError: (err, vars, context) => {
      if (context?.previous) qc.setQueryData(keys.appConnections.all, context.previous)
      notifyError(`${vars.enabled ? "Enabling" : "Disabling"} the connection failed`, err)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.appConnections.all }),
  })
}

export function useTestConnection() {
  return useMutation({ mutationFn: (id: number) => unwrap(api.http.POST("/api/app-connections/{id}/test", { params: { path: { id } } })) })
}

export function useSyncConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.http.POST("/api/app-connections/{id}/sync", { params: { path: { id } } })),
    onSettled: invalidate,
  })
}

export function useSyncAll() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: () => unwrap(api.http.POST("/api/app-connections/sync")),
    onSettled: invalidate,
  })
}

// --- sync profiles ---

export function useSyncProfiles() {
  return useQuery({
    queryKey: keys.syncProfiles.all,
    queryFn: () => unwrap(api.http.GET("/api/sync-profiles")),
  })
}

export function useSyncProfileMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.syncProfiles.all })
  return {
    create: useMutation({ mutationFn: (body: CreateSyncProfile) => unwrap(api.http.POST("/api/sync-profiles", { body: body })), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateSyncProfile }) => unwrap(api.http.PATCH("/api/sync-profiles/{id}", { params: { path: { id } }, body })),
      onSettled: invalidate,
    }),
    // The delete is refused (409) while any connection still references the profile
    // (the service-level guard), but refresh the connection list too in case one did
    // just get detached right before this call.
    remove: useMutation({
      mutationFn: (id: number) => unwrap(api.http.DELETE("/api/sync-profiles/{id}", { params: { path: { id } } })),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: keys.appConnections.all })
      },
    }),
  }
}

// --- announce targets ---

export function useAnnounceConnections() {
  return useQuery({
    queryKey: keys.announceConnections.all,
    queryFn: () => unwrap(api.http.GET("/api/announce-connections")),
  })
}

export function useCreateAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateAnnounceConnection) => unwrap(api.http.POST("/api/announce-connections", { body: body })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

// The id travels with each mutate() call (mirroring useUpdateConnection), so the one
// hook serves the edit dialog directly.
export function useUpdateAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateAnnounceConnection }) => unwrap(api.http.PATCH("/api/announce-connections/{id}", { params: { path: { id } }, body })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

export function useDeleteAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.http.DELETE("/api/announce-connections/{id}", { params: { path: { id } } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

export function useTestAnnounce() {
  return useMutation({ mutationFn: (id: number) => unwrap(api.http.POST("/api/announce-connections/{id}/test", { params: { path: { id } } })) })
}

export function useSetAnnounceEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setAnnounceEnabled(id, enabled),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}
