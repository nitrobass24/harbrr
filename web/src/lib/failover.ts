import { hostname } from "@/lib/format"
import type { Instance } from "@/lib/api"

// failoverHosts reports the base-URL promotion standing of an indexer
// (autobrr/harbrr#375), or null when there is none. The gate is `failoverBaseUrl` —
// the API sets it ONLY while a promotion is in effect — rather than "effectiveBaseUrl
// differs from baseUrl", which is also true for every indexer that has no base-URL
// override at all and simply follows the definition's first link. It takes the list
// row (Instance), which carries both failover fields since autobrr/harbrr#684, so the
// table needs no per-slug detail fetch.
export function failoverHosts(instance: Instance | undefined): { inUse: string, configured: string } | null {
  if (!instance?.failoverBaseUrl) return null
  // While a promotion stands, failoverBaseUrl IS the effective host: the API reports it
  // only when the promotion is the reason for the host in use.
  const inUse = hostname(instance.failoverBaseUrl)
  const configured = hostname(instance.baseUrl)
  if (inUse === "" || inUse === configured) return null
  return { inUse, configured }
}
