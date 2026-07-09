import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type {
  AppConnection,
  CreateAnnounceConnection,
  CreateConnection,
  CreateSyncProfile,
  UpdateConnection,
  UpdateSyncProfile
} from "@/types/api"

export function useAppConnections() {
  return useQuery({
    queryKey: ["app-connections"],
    queryFn: () => api.listConnections(),
  })
}

// useServerInfo reflects harbrr's live listen port, used to flag app-sync
// connections whose stored harbrrUrl port has drifted stale.
export function useServerInfo() {
  return useQuery({
    queryKey: ["server-info"],
    queryFn: () => api.getServerInfo(),
  })
}

export function useConnectionStatus(id: number | null) {
  return useQuery({
    queryKey: ["app-connections", id, "status"],
    queryFn: () => api.getConnectionStatus(id as number),
    enabled: id !== null,
  })
}

function useInvalidateConnections() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: ["app-connections"] })
}

export function useCreateConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (body: CreateConnection) => api.createConnection(body),
    onSettled: invalidate,
  })
}

// The id travels with each mutate() call (mirroring useSetConnectionEnabled),
// so one hook serves both the edit dialog and per-row actions like the
// stale-port fix.
export function useUpdateConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateConnection }) => api.updateConnection(id, body),
    onSettled: invalidate,
  })
}

export function useDeleteConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (id: number) => api.deleteConnection(id),
    onSettled: invalidate,
  })
}

// Optimistic switch flip, mirroring the indexers pattern.
export function useSetConnectionEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setConnectionEnabled(id, enabled),
    onMutate: async ({ id, enabled }) => {
      await qc.cancelQueries({ queryKey: ["app-connections"] })
      const previous = qc.getQueryData<AppConnection[]>(["app-connections"])
      qc.setQueryData<AppConnection[]>(["app-connections"], (list) =>
        list?.map((c) => (c.id === id ? { ...c, enabled } : c)))
      return { previous }
    },
    onError: (_err, vars, context) => {
      if (context?.previous) qc.setQueryData(["app-connections"], context.previous)
      toast.error(`${vars.enabled ? "Enabling" : "Disabling"} the connection failed`)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["app-connections"] }),
  })
}

export function useTestConnection() {
  return useMutation({ mutationFn: (id: number) => api.testConnection(id) })
}

export function useSyncConnection() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: (id: number) => api.syncConnection(id),
    onSettled: invalidate,
  })
}

export function useSyncAll() {
  const invalidate = useInvalidateConnections()
  return useMutation({
    mutationFn: () => api.syncAllConnections(),
    onSettled: invalidate,
  })
}

export function useSetSelectedIndexers(id: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (instanceIds: number[]) => api.setSelectedIndexers(id, instanceIds),
    onSettled: () => qc.invalidateQueries({ queryKey: ["app-connections", id, "status"] }),
  })
}

// --- sync profiles ---

export function useSyncProfiles() {
  return useQuery({
    queryKey: ["sync-profiles"],
    queryFn: () => api.listSyncProfiles(),
  })
}

export function useSyncProfileMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: ["sync-profiles"] })
  return {
    create: useMutation({ mutationFn: (body: CreateSyncProfile) => api.createSyncProfile(body), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateSyncProfile }) => api.updateSyncProfile(id, body),
      onSettled: invalidate,
    }),
    // Deleting a profile nulls any connection's reference (ON DELETE SET NULL), so
    // refresh the connection list too.
    remove: useMutation({
      mutationFn: (id: number) => api.deleteSyncProfile(id),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: ["app-connections"] })
      },
    }),
  }
}

// --- announce targets ---

export function useAnnounceConnections() {
  return useQuery({
    queryKey: ["announce-connections"],
    queryFn: () => api.listAnnounceConnections(),
  })
}

export function useCreateAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateAnnounceConnection) => api.createAnnounceConnection(body),
    onSettled: () => qc.invalidateQueries({ queryKey: ["announce-connections"] }),
  })
}

export function useDeleteAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.deleteAnnounceConnection(id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["announce-connections"] }),
  })
}

export function useSetAnnounceEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setAnnounceEnabled(id, enabled),
    onSettled: () => qc.invalidateQueries({ queryKey: ["announce-connections"] }),
  })
}
