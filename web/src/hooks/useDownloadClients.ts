import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { CreateDownloadClient, UpdateDownloadClient } from "@/lib/api"
import { keys } from "@/lib/query"

export function useDownloadClients() {
  return useQuery({ queryKey: keys.downloadClients.all, queryFn: () => api.listDownloadClients() })
}

function useInvalidateDownloadClients() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: keys.downloadClients.all })
}

export function useCreateDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({ mutationFn: (body: CreateDownloadClient) => api.createDownloadClient(body), onSettled: invalidate })
}

export function useUpdateDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateDownloadClient }) => api.updateDownloadClient(id, body),
    onSettled: invalidate,
  })
}

export function useDeleteDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({ mutationFn: (id: number) => api.deleteDownloadClient(id), onSettled: invalidate })
}

export function useSetDownloadClientEnabled() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setDownloadClientEnabled(id, enabled),
    onSettled: invalidate,
  })
}

export function useTestDownloadClient() {
  return useMutation({ mutationFn: (id: number) => api.testDownloadClient(id) })
}
