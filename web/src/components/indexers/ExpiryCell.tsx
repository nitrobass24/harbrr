import { expiryState } from "@/lib/expiry"
import { cn } from "@/lib/utils"
import type { Instance } from "@/lib/api"

// The expiry column cell (autobrr/harbrr#399): a short label with the full sentence
// on hover. The derivation lives in lib/expiry so the row ordering reads the same
// numbers this renders.
export function ExpiryCell({ instance, now }: { instance: Instance, now?: Date }) {
  const state = expiryState(instance, now)
  return (
    <span className={cn("whitespace-nowrap text-[13px]", state.tone)} title={state.detail}>
      {state.label}
    </span>
  )
}
