import { failoverHosts } from "@/lib/failover"
import type { Instance } from "@/lib/api"

// Beside the indexer's name when the automatic base-URL failover moved it to
// another of the definition's hosts (autobrr/harbrr#375). Styled after the other
// row pills; the hosts are in the tooltip so the row stays one line.
export function FailoverPill({ instance }: { instance?: Instance }) {
  const hosts = failoverHosts(instance)
  if (!hosts) return null

  return (
    <span
      title={`Talking to ${hosts.inUse} — failover from ${hosts.configured || "the definition's default host"}`}
      className="inline-flex items-center rounded-full border border-warn/40 bg-warn/10 px-2 py-0.5 text-[11px] font-medium text-warn"
    >
      Failover
    </span>
  )
}
