import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { SearchParams } from "@/lib/api"
import { keys } from "@/lib/query"

// ONE request for the whole subset: the server merges, sorts and windows the results
// and hands back the per-member ledger (autobrr/harbrr#372). There is deliberately no
// client-side fan-out — a second merge implementation is the thing the page and the
// aggregate feed must never disagree over. The query runs once a search is submitted
// (params !== null); the key carries the subset so changing it re-queries.
export function useSearchAggregate(slugs: string[], params: SearchParams | null) {
  return useQuery({
    queryKey: keys.search.aggregate(slugs, params),
    queryFn: () => api.searchAggregate(slugs, params as SearchParams),
    enabled: params !== null && slugs.length > 0,
    retry: false,
    staleTime: 60_000, // the server-side cache is authoritative; avoid re-fetch churn
  })
}
