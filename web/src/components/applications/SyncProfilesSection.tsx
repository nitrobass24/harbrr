import { useState } from "react"
import { Pencil, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ResourceSection } from "@/components/ui/resource-section"
import { useSyncProfileMutations, useSyncProfiles } from "@/hooks/useAppConnections"
import { useIndexers } from "@/hooks/useIndexers"
import type { CreateSyncProfile, SyncProfile } from "@/lib/api"

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

  return (
    <ResourceSection<SyncProfile>
      title="Sync profiles"
      addLabel="Add profile"
      query={profiles}
      empty="No sync profiles. Add one to route a connection to only some indexers."
      footnote={<p className="text-[12px] text-faint">Deleting a profile is refused while any connection still references it.</p>}
      row={(p, actions) => (
        <>
          <span className="flex flex-col">
            <span className="font-medium">{p.name}</span>
            <span className="text-[12px] text-faint">{summarize(p)}</span>
          </span>
          <span className="ml-auto flex items-center gap-1">
            <Button variant="ghost" size="icon" aria-label={`Edit ${p.name}`} onClick={actions.edit}>
              <Pencil className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Delete ${p.name}`} onClick={actions.remove}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </span>
        </>
      )}
      onDelete={(p) => remove.mutateAsync(p.id)}
      form={(target, done) => (
        <ProfileForm
          profile={target}
          pending={create.isPending || update.isPending}
          onSubmit={(id, body) => {
            if (id === null) create.mutate(body, done)
            else update.mutate({ id, body }, done)
          }}
        />
      )}
    />
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
