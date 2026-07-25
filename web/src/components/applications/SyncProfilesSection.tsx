import { useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useSyncProfileMutations, useSyncProfiles } from "@/hooks/useAppConnections"
import { useIndexers } from "@/hooks/useIndexers"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { CreateSyncProfile, SyncProfile } from "@/lib/api"

// `null` = closed; `{ profile: null }` = add; `{ profile }` = edit that profile.
type Editing = { profile: SyncProfile | null } | null

function summarize(p: SyncProfile): string {
  return p.indexerIds.length > 0 ? `${p.indexerIds.length} indexer${p.indexerIds.length === 1 ? "" : "s"}` : "all indexers"
}

// A sync profile is a pure indexer ROUTING SET (#365): name + a selected set of indexer
// instances. A connection with no profile, or a profile with an empty selection, syncs
// every compatible indexer — all sync behavior (categories, search toggles, min seeders)
// now lives per-indexer (see IndexerForm's Advanced section). Deleting a profile is
// refused while any connection still references it.
export function SyncProfilesSection() {
  const profiles = useSyncProfiles()
  const { create, update, remove } = useSyncProfileMutations()
  const [editing, setEditing] = useState<Editing>(null)

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-[14px] font-semibold tracking-tight">Sync profiles</h2>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setEditing({ profile: null })}>
          <Plus className="h-3.5 w-3.5" /> Add profile
        </Button>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(profiles.data ?? []).map((p) => (
          <div key={p.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="flex flex-col">
              <span className="font-medium">{p.name}</span>
              <span className="text-[12px] text-faint">{summarize(p)}</span>
            </span>
            <span className="ml-auto flex items-center gap-1">
              <Button variant="ghost" size="icon" aria-label={`Edit ${p.name}`} onClick={() => setEditing({ profile: p })}>
                <Pencil className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${p.name}`}
                onClick={() => remove.mutate(p.id, {
                  onSuccess: () => notifySuccess(`${p.name} deleted`),
                  onError: (err) => notifyError(`Deleting ${p.name} failed`, err),
                })}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {profiles.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No sync profiles. Add one to route a connection to only some indexers.</p>
        )}
      </div>
      <p className="text-[12px] text-faint">Deleting a profile is refused while any connection still references it.</p>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <ProfileForm
              // Remount (fresh state seeded from props) per target.
              key={editing.profile?.id ?? "new"}
              profile={editing.profile}
              pending={create.isPending || update.isPending}
              onSubmit={(id, body) => {
                const done = { onSuccess: () => setEditing(null), onError: (err: Error) => notifyError(`Save failed: ${err.message}`, err) }
                if (id === null) create.mutate(body, done)
                else update.mutate({ id, body }, done)
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}

function ProfileForm({ profile, pending, onSubmit }: {
  profile: SyncProfile | null
  pending: boolean
  onSubmit: (id: number | null, body: CreateSyncProfile) => void
}) {
  const isEdit = profile !== null
  const indexers = useIndexers()
  const [name, setName] = useState(profile?.name ?? "")
  const [selected, setSelected] = useState<Set<number>>(new Set(profile?.indexerIds ?? []))

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit(profile?.id ?? null, { name, indexerIds: [...selected].sort((a, b) => a - b) })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit sync profile" : "Add sync profile"}</DialogTitle>
        <DialogDescription>Attach it to a connection to route it to only the checked indexers.</DialogDescription>
      </DialogHeader>

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="profile-name">Name</Label>
        <Input id="profile-name" value={name} onChange={(e) => setName(e.target.value)} />
      </span>

      <span className="flex flex-col gap-1.5">
        <Label>Indexers</Label>
        <div className="flex max-h-72 flex-col gap-2 overflow-auto py-1">
          {(indexers.data ?? []).map((ix) => (
            <span key={ix.id} className="flex items-center gap-2">
              <Checkbox
                id={`profile-ix-${ix.id}`}
                checked={selected.has(ix.id)}
                onCheckedChange={(checked) => {
                  const next = new Set(selected)
                  if (checked === true) next.add(ix.id)
                  else next.delete(ix.id)
                  setSelected(next)
                }}
              />
              <Label htmlFor={`profile-ix-${ix.id}`} className="font-normal">{ix.name}</Label>
            </span>
          ))}
        </div>
        <p className="text-[12px] text-faint">No selection = all indexers.</p>
      </span>

      <DialogFooter>
        <Button type="submit" disabled={pending || !name}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add profile"}
        </Button>
      </DialogFooter>
    </form>
  )
}
