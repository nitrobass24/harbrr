import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { notifyError } from "@/lib/notify"
import { keys } from "@/lib/query"
import type { UpdateApp } from "@/lib/api"

export function useApps() {
  return useQuery({ queryKey: keys.apps.all, queryFn: () => api.listApps() })
}

// An App's identity/credential (ADR 0004) is read by app-sync, announce, and download
// surfaces alike, so any successful mutation here also invalidates those three surface
// queries — not just the apps list — since their enriched baseUrl/host/harbrrUrl comes
// from the App.
function useInvalidateApps() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: keys.apps.all })
    void qc.invalidateQueries({ queryKey: keys.appConnections.all })
    void qc.invalidateQueries({ queryKey: keys.announceConnections.all })
    void qc.invalidateQueries({ queryKey: keys.downloadClients.all })
  }
}

export function useUpdateApp() {
  const invalidate = useInvalidateApps()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateApp }) => api.updateApp(id, body),
    onSettled: invalidate,
  })
}

// Deletion is blocked (409) while any surface still references the app; the server's
// message names which ones (internal/apps.Service.Delete), so it rides straight into
// the toast rather than a generic "failed" string.
export function useDeleteApp() {
  const invalidate = useInvalidateApps()
  return useMutation({
    mutationFn: (id: number) => api.deleteApp(id),
    onError: (err: Error) => notifyError(`Deleting the app failed: ${err.message}`, err),
    onSettled: invalidate,
  })
}

export function useQuiInstances(appId: number | null) {
  return useQuery({
    queryKey: keys.apps.quiInstances(appId),
    queryFn: () => api.getQuiInstances(appId as number),
    enabled: appId !== null,
  })
}
