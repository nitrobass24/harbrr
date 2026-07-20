import { Badge } from "@/components/ui/badge"
import { hostname, kindLabel } from "@/lib/format"
import type { App } from "@/lib/api"

// ConfiguredAppsBlock is the reuse-first entry point of a create dialog (autobrr/harbrr#296):
// a row per surface-compatible App, above the create form's own fields. Picking one collapses
// the form to its surface-specific fields instead of re-entering an identity that already
// exists. Renders nothing when the caller has no compatible Apps to offer.
export function ConfiguredAppsBlock({ apps, isUsed, onPick }: {
  apps: App[] // caller-filtered to surface-compatible kinds
  isUsed?: (a: App) => boolean // row disabled with "already added" when true
  onPick: (a: App) => void
}) {
  if (apps.length === 0) return null
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-col">
        <span className="text-[13px] font-medium">Already configured</span>
        <span className="text-[12px] text-faint">
          Reuse an app you&apos;ve set up before — its URL and credential come along.
        </span>
      </div>
      <div className="flex flex-col rounded-lg border border-border/60">
        {apps.map((a) => {
          const used = isUsed?.(a) ?? false
          return (
            // type="button" — this renders inside the dialog's <form>; the default type submits it.
            <button
              key={a.id}
              type="button"
              disabled={used}
              onClick={() => onPick(a)}
              className="flex items-center gap-2 border-b border-border/60 px-3 py-2 text-left last:border-b-0 enabled:hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60"
            >
              <span className="text-[13px] font-medium">{a.name}</span>
              <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{kindLabel(a.kind)}</Badge>
              <span className="text-[12px] text-faint">{hostname(a.baseUrl)}</span>
              <span className="ml-auto text-[12px] text-faint">{used ? "already added" : "configured"}</span>
            </button>
          )
        })}
      </div>
      <p className="text-[12px] text-faint">…or set up something new below.</p>
    </div>
  )
}

// ReusingAppHint is the create-side sibling of ManagedByAppHint: it names the App whose
// identity a fresh row is about to inherit, before the row exists to link to.
export function ReusingAppHint({ app, tail = "nothing else to enter" }: {
  app: App | undefined
  tail?: string
}) {
  if (!app) return null
  return (
    <p className="text-[12px] text-faint">
      Reusing <span className="font-medium text-foreground">{app.name}</span>&apos;s base URL and API key — {tail}.
    </p>
  )
}
