import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { notifyError, notifySuccess } from "@/lib/notify"
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
    queryFn: () => api.listConnections(),
  })
}

// useServerInfo reflects harbrr's live listen port, used to flag app-sync
// connections whose stored harbrrUrl port has drifted stale.
export function useServerInfo() {
  return useQuery({
    queryKey: keys.serverInfo.all,
    queryFn: () => api.getServerInfo(),
  })
}

export function useConnectionStatus(id: number | null) {
  return useQuery({
    queryKey: keys.appConnections.status(id),
    queryFn: () => api.getConnectionStatus(id as number),
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
    onSettled: () => qc.invalidateQueries({ queryKey: keys.appConnections.status(id) }),
  })
}

// Seeds a qui announce target from a qui app-connection (#72), reusing its stored
// credentials — no re-entry. Invalidates the announce-connections query so
// AnnounceSection (and the ConnectionCard's own duplicate check) see the new target.
export function useCreateAnnounceTargetFromAppConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.createAnnounceTargetFromAppConnection(id),
    onSuccess: () => notifySuccess("Announce target created"),
    onError: (err) => notifyError("Creating the announce target failed", err),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

// --- sync profiles ---

export function useSyncProfiles() {
  return useQuery({
    queryKey: keys.syncProfiles.all,
    queryFn: () => api.listSyncProfiles(),
  })
}

export function useSyncProfileMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.syncProfiles.all })
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
        void qc.invalidateQueries({ queryKey: keys.appConnections.all })
      },
    }),
  }
}

// --- announce targets ---

export function useAnnounceConnections() {
  return useQuery({
    queryKey: keys.announceConnections.all,
    queryFn: () => api.listAnnounceConnections(),
  })
}

export function useCreateAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateAnnounceConnection) => api.createAnnounceConnection(body),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

// The id travels with each mutate() call (mirroring useUpdateConnection), so the one
// hook serves the edit dialog directly.
export function useUpdateAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateAnnounceConnection }) => api.updateAnnounceConnection(id, body),
    onSettled: () => qc.invalidateQueries({ queryKey: ["announce-connections"] }),
  })
}

export function useDeleteAnnounce() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.deleteAnnounceConnection(id),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}

export function useTestAnnounce() {
  return useMutation({ mutationFn: (id: number) => api.testAnnounceConnection(id) })
}

export function useSetAnnounceEnabled() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setAnnounceEnabled(id, enabled),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.announceConnections.all }),
  })
}
