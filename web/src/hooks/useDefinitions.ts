import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useDefinitions() {
  return useQuery({
    queryKey: ["definitions"],
    queryFn: () => api.listDefinitions(),
    staleTime: 5 * 60_000, // the catalog changes only on a vendor refresh
  })
}

export function useDefinition(id: string | null) {
  return useQuery({
    queryKey: ["definitions", id],
    queryFn: () => api.getDefinition(id as string),
    enabled: id !== null,
  })
}
