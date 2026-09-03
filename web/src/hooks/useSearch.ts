import { skipToken, useQuery } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import type { SearchParams } from "@/lib/api"
import { keys } from "@/lib/query"

// setParams drops undefined/empty-string values so an unset search field is omitted
// from the querystring entirely rather than sent as an empty filter.
function setParams(params: SearchParams): SearchParams {
  const query: SearchParams = {}
  for (const [key, value] of Object.entries(params) as [keyof SearchParams, unknown][]) {
    if (value !== undefined && value !== "") (query as Record<string, unknown>)[key] = value
  }
  return query
}

// ONE request for the whole subset: the server merges, sorts and windows the results
// and hands back the per-member ledger (autobrr/harbrr#372). There is deliberately no
// client-side fan-out — a second merge implementation is the thing the page and the
// aggregate feed must never disagree over. The query runs once a search is submitted
// (params !== null); the key carries the subset so changing it re-queries.
export function useSearchAggregate(slugs: string[], params: SearchParams | null) {
  return useQuery({
    queryKey: keys.search.aggregate(slugs, params),
    // Narrowed, not asserted: `enabled` already guarantees params is non-null, and a
    // cast would keep compiling if that guard ever changed.
    queryFn: params === null
      ? skipToken
      : () => unwrap(api.http.GET("/api/search", { params: { query: { ...setParams(params), indexers: slugs.join(",") } } })),
    enabled: params !== null && slugs.length > 0,
    retry: false,
    staleTime: 60_000, // the server-side cache is authoritative; avoid re-fetch churn
  })
}
