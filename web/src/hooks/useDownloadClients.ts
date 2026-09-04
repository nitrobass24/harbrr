import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import type { CreateDownloadClient, UpdateDownloadClient } from "@/lib/api"
import { keys } from "@/lib/query"

export function useDownloadClients() {
  return useQuery({ queryKey: keys.downloadClients.all, queryFn: () => unwrap(api.http.GET("/api/download-clients")) })
}

function useInvalidateDownloadClients() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: keys.downloadClients.all })
}

export function useCreateDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({ mutationFn: (body: CreateDownloadClient) => unwrap(api.http.POST("/api/download-clients", { body: body })), onSettled: invalidate })
}

export function useUpdateDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({
    mutationFn: ({ id, body }: { id: number, body: UpdateDownloadClient }) => unwrap(api.http.PATCH("/api/download-clients/{id}", { params: { path: { id } }, body })),
    onSettled: invalidate,
  })
}

export function useDeleteDownloadClient() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({ mutationFn: (id: number) => unwrap(api.http.DELETE("/api/download-clients/{id}", { params: { path: { id } } })), onSettled: invalidate })
}

export function useSetDownloadClientEnabled() {
  const invalidate = useInvalidateDownloadClients()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number, enabled: boolean }) => api.setDownloadClientEnabled(id, enabled),
    onSettled: invalidate,
  })
}

export function useTestDownloadClient() {
  return useMutation({ mutationFn: (id: number) => unwrap(api.http.POST("/api/download-clients/{id}/test", { params: { path: { id } } })) })
}
