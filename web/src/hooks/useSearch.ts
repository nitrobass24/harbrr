import { useQueries } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { SearchParams } from "@/lib/api"
import { keys } from "@/lib/query"

// Fan-out: one query per selected indexer, merged by the page. Keys carry the
// full param set so paging/re-querying caches independently. Queries only run
// once a search is submitted (params !== null).
export function useSearchFanout(slugs: string[], params: SearchParams | null) {
  return useQueries({
    queries: slugs.map((slug) => ({
      queryKey: keys.search.fanout(slug, params),
      queryFn: () => api.searchIndexer(slug, params as SearchParams),
      enabled: params !== null,
      retry: false,
      staleTime: 60_000, // the server-side cache is authoritative; avoid re-fetch churn
    })),
  })
}
