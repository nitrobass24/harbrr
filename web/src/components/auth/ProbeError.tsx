import { Button } from "@/components/ui/button"

// ProbeError is the full-screen retry state shown when a bootstrap probe (the
// session or setup-status check) errors, so the visitor can retry rather than
// being bounced or left on a form that can't submit. The probes are retry:false,
// so the retry is a manual nudge.
export function ProbeError({ message, onRetry }: { message: string, onRetry: () => void }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="flex max-w-sm flex-col items-center gap-4 text-center">
        <p role="alert" className="rounded-xl border border-bad/40 bg-bad/10 px-5 py-4 text-[13px] text-bad">
          {message}
        </p>
        <Button onClick={onRetry}>Retry</Button>
      </div>
    </div>
  )
}
