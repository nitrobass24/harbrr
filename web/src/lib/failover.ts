import { hostname } from "@/lib/format"
import type { InstanceDetail } from "@/lib/api"

// failoverHosts reports the base-URL promotion standing of an indexer
// (autobrr/harbrr#375), or null when there is none. The gate is `failoverBaseUrl` —
// the API sets it ONLY while a promotion is in effect — rather than "effectiveBaseUrl
// differs from baseUrl", which is also true for every indexer that has no base-URL
// override at all and simply follows the definition's first link.
export function failoverHosts(detail: InstanceDetail | undefined): { inUse: string, configured: string } | null {
  if (!detail?.failoverBaseUrl) return null
  const inUse = hostname(detail.effectiveBaseUrl || detail.failoverBaseUrl)
  const configured = hostname(detail.baseUrl)
  if (inUse === "" || inUse === configured) return null
  return { inUse, configured }
}
