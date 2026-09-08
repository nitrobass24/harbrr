import { QueryClient } from "@tanstack/react-query"
import type { SearchParams } from "@/lib/api"

// QueryClient defaults, per docs/autobrr-app-template.md "Frontend" (QueryClient
// defaults live in web/src/lib/query.ts). Only harbrr's own two departures from
// TanStack Query's defaults are set here (docs/webui-scope.md §6); everything else
// — gcTime, retry/retryDelay, refetch on mount/reconnect — is the library default
// and is left to the library rather than restated.
export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Short staleness; individual queries opt into a longer staleTime (e.g.
        // definitions, capabilities) or a refetchInterval (e.g. cache stats,
        // indexer status) where it matters.
        staleTime: 5_000,
        // No refetch-on-focus churn.
        refetchOnWindowFocus: false,
      },
    },
  })
}

// Typed query-key registry (docs/autobrr-app-template.md "Frontend": query keys
// come from a typed registry; components do not inline key arrays). Each domain
// exposes an `all` root for broad (prefix-matching) invalidation plus finer keys
// that spread from it. Key VALUES match what was already in use — this file only
// centralizes and types them, it does not change what gets cached under what key.
//
// indexers vs indexerStats are deliberately separate top-level domains (not
// indexers.stats(slug) nested under "indexers") so an indexer whose slug is
// literally "stats" can never share a cache entry with the aggregate stats query.
// That collision existed once (fixed 2026-07-09, see
// web/src/hooks/useAllIndexerStats.test.tsx) — this split makes it structurally
// impossible to reintroduce through the registry.
export const keys = {
  auth: {
    all: ["auth"] as const,
    me: () => ["auth", "me"] as const,
    setup: () => ["auth", "setup"] as const,
  },
  health: {
    all: ["health"] as const,
  },
  cache: {
    all: ["cache"] as const,
    stats: () => ["cache", "stats"] as const,
    config: () => ["cache", "config"] as const,
  },
  config: {
    logLevel: () => ["config", "log-level"] as const,
    adultCategories: () => ["config", "adult-categories"] as const,
    expiryThresholds: () => ["config", "expiry-thresholds"] as const,
  },
  apiKeys: {
    all: ["apikeys"] as const,
  },
  notifications: {
    all: ["notifications"] as const,
  },
  definitions: {
    all: ["definitions"] as const,
    detail: (id: string | null) => ["definitions", id] as const,
  },
  indexers: {
    all: ["indexers"] as const,
    list: () => ["indexers"] as const,
    detail: (slug: string) => ["indexers", slug] as const,
    status: (slug: string) => ["indexers", slug, "status"] as const,
    diagnostics: (slug: string) => ["indexers", slug, "diagnostics"] as const,
    capabilities: (slug: string) => ["indexers", slug, "capabilities"] as const,
    stats: (slug: string) => ["indexers", slug, "stats"] as const,
    crossseedSnippet: (slug: string | null) => ["indexers", slug, "crossseed-snippet"] as const,
  },
  // Aggregate per-indexer stats. Kept under its own root rather than nested under
  // indexers.* — see the note above the registry.
  indexerStats: {
    all: ["indexer-stats"] as const,
  },
  search: {
    aggregate: (slugs: string[], params: SearchParams | null) => ["search", slugs, params] as const,
  },
  proxies: {
    all: ["proxies"] as const,
  },
  solvers: {
    all: ["solvers"] as const,
  },
  downloadClients: {
    all: ["download-clients"] as const,
  },
  appConnections: {
    all: ["app-connections"] as const,
    status: (id: number | null) => ["app-connections", id, "status"] as const,
  },
  apps: {
    all: ["apps"] as const,
    quiInstances: (id: number | null) => ["apps", id, "qui-instances"] as const,
  },
  serverInfo: {
    all: ["server-info"] as const,
  },
  syncProfiles: {
    all: ["sync-profiles"] as const,
  },
  announceConnections: {
    all: ["announce-connections"] as const,
  },
  oidcConfig: {
    all: ["oidc-config"] as const,
  },
} as const
