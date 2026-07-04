// Distinguishes "the server is unreachable / errored" from a genuine empty
// list, so a stopped harbrr never masquerades as an empty state.
export function LoadError({ what }: { what: string }) {
  return (
    <p role="alert" className="rounded-xl border border-bad/40 bg-bad/10 px-5 py-4 text-[13px] text-bad">
      Loading {what} failed — is harbrr running? The page retries automatically.
    </p>
  )
}

// A quiet pulsing placeholder while a list query is in flight.
export function LoadingBlock() {
  return <div className="h-36 animate-pulse rounded-xl border border-border bg-card" />
}
