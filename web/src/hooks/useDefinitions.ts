import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { keys } from "@/lib/query"

export function useDefinitions() {
  return useQuery({
    queryKey: keys.definitions.all,
    queryFn: () => api.listDefinitions(),
    staleTime: 5 * 60_000, // the catalog changes only on a vendor refresh
  })
}

export function useDefinition(id: string | null) {
  return useQuery({
    queryKey: keys.definitions.detail(id),
    queryFn: () => api.getDefinition(id as string),
    enabled: id !== null,
  })
}
