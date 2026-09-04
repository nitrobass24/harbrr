import { useQuery } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import { keys } from "@/lib/query"

export function useDefinitions() {
  return useQuery({
    queryKey: keys.definitions.all,
    queryFn: () => unwrap(api.http.GET("/api/definitions")),
    staleTime: 5 * 60_000, // the catalog changes only on a vendor refresh
  })
}

export function useDefinition(id: string | null) {
  return useQuery({
    queryKey: keys.definitions.detail(id),
    queryFn: () => unwrap(api.http.GET("/api/definitions/{id}", { params: { path: { id: id as string } } })),
    enabled: id !== null,
  })
}
