import { useEffect, useState } from "react"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent } from "@/components/ui/dialog"
import { LoadError, LoadingBlock } from "@/components/ui/load-error"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { ReactNode } from "react"
import type { UseQueryResult } from "@tanstack/react-query"

// Save-dialog callbacks the shell hands to each section's form: close on success,
// toast on failure. Sections whose toast text predates the shell (Notifications'
// "Adding failed", Apps' inline error display) use onSuccess and supply their own
// error handling, keeping their user-visible text byte-identical.
export interface MutationDoneHandlers {
  onSuccess: () => void
  onError: (err: Error) => void
}

// `null` = closed; `"new"` = the add dialog; a `T` = editing that item.
type Target<T> = T | "new" | null

// ResourceSection is the one shell behind the list + add button + row-with-actions +
// edit/add dialog sections (proxies, solvers, sync profiles, notifications, apps,
// download clients). It owns what those sections used to each re-implement: the
// header, the card container, pending/error/empty states (a failed list query shows
// LoadError instead of masquerading as an empty card — autobrr/harbrr#556), dialog
// open/close with a remount-per-target key, and the save/delete toasts via lib/notify.
// Row content and the form stay with each section as render props.
export function ResourceSection<T extends { id: number | string, name?: string }>({
  id,
  title,
  subtitle,
  addLabel,
  query,
  empty,
  footnote,
  row,
  form,
  onDelete,
  canEdit = true,
  deleteLabel = (item) => item.name ?? "item",
  addRequest,
  onDialogClose,
}: {
  id?: string
  title: string
  // Rendered under the title (Apps); a section with a subtitle has no add button row.
  subtitle?: string
  // Absent → no add button (Apps: one is created the first time a surface connects).
  addLabel?: string
  query: UseQueryResult<T[]>
  empty: string
  // Extra line rendered between the card and the dialog (SyncProfiles' delete note).
  footnote?: ReactNode
  // Row content only — the shell owns the row container div and its key.
  // `remove` is absent when the section keeps its own delete handling (no onDelete).
  row: (item: T, actions: { edit: () => void, remove?: () => void }) => ReactNode
  // target === null → the add dialog. The form remounts per target.
  form: (target: T | null, done: MutationDoneHandlers) => ReactNode
  // Deleting `item`; the shell toasts "`label` deleted" / "Deleting `label` failed".
  onDelete?: (item: T) => Promise<unknown>
  canEdit?: boolean
  deleteLabel?: (item: T) => string
  // Deep-link support (DownloadClients' "Use as…"): opens the add dialog whenever
  // this prop shows up — the same effect the section used to own.
  addRequest?: unknown
  // Fires on every dialog close (save, escape, outside click) so a section can
  // clear add-only state like a deep-link pre-pick.
  onDialogClose?: () => void
}) {
  const [target, setTarget] = useState<Target<T>>(null)

  useEffect(() => {
    if (addRequest !== undefined) setTarget("new")
  }, [addRequest])

  const close = () => {
    setTarget(null)
    onDialogClose?.()
  }
  const done: MutationDoneHandlers = {
    onSuccess: close,
    onError: (err) => notifyError(`Save failed: ${err.message}`, err),
  }

  return (
    <section id={id} className="flex flex-col gap-3">
      <div className={addLabel ? "flex items-center gap-3" : "flex flex-col"}>
        <h2 className="text-[14px] font-semibold tracking-tight">{title}</h2>
        {subtitle && <p className="text-[12px] text-faint">{subtitle}</p>}
        {addLabel && (
          <Button variant="outline" size="sm" className="ml-auto" onClick={() => setTarget("new")}>
            <Plus className="h-3.5 w-3.5" /> {addLabel}
          </Button>
        )}
      </div>

      {query.isPending ? <LoadingBlock /> : query.isError ? <LoadError what={title.toLowerCase()} /> : (
        <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
          {query.data.length === 0 ? <p className="py-3 text-muted-foreground">{empty}</p> : query.data.map((item) => (
            <div key={item.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
              {row(item, {
                edit: () => { if (canEdit) setTarget(item) },
                remove: onDelete && (() => {
                  const label = deleteLabel(item)
                  onDelete(item).then(
                    () => notifySuccess(`${label} deleted`),
                    (err: unknown) => notifyError(`Deleting ${label} failed`, err)
                  )
                }),
              })}
            </div>
          ))}
        </div>
      )}
      {footnote}

      <Dialog open={target !== null} onOpenChange={(open) => { if (!open) close() }}>
        {target !== null && (
          // Remount (fresh form state seeded from props) per target.
          <DialogContent key={target === "new" ? "new" : target.id}>
            {form(target === "new" ? null : target, done)}
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}
