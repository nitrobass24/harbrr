import { useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
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
import { Switch } from "@/components/ui/switch"
import { useSyncProfileMutations, useSyncProfiles } from "@/hooks/useAppConnections"
import type { CreateSyncProfile, SyncProfile } from "@/lib/api"

const NEWZNAB_PARENTS = [
  { id: 2000, label: "Movies" },
  { id: 3000, label: "Audio" },
  { id: 4000, label: "PC" },
  { id: 5000, label: "TV" },
  { id: 6000, label: "XXX" },
  { id: 7000, label: "Books" },
  { id: 8000, label: "Other" },
]
const PARENT_IDS = new Set(NEWZNAB_PARENTS.map((p) => p.id))

// `null` = closed; `{ profile: null }` = add; `{ profile }` = edit that profile.
type Editing = { profile: SyncProfile | null } | null

function summarize(p: SyncProfile): string {
  const parts = [p.categories.length > 0 ? `${p.categories.length} categories` : "all categories"]
  if (p.minSeeders > 0) parts.push(`min ${p.minSeeders} seeders`)
  const off = [
    !p.enableRss && "RSS off",
    !p.enableAutomaticSearch && "automatic search off",
    !p.enableInteractiveSearch && "interactive search off",
  ].filter((s) => s !== false)
  if (off.length > 0) parts.push(off.join(", "))
  return parts.join(" · ")
}

// Sync profiles narrow which categories/toggles apply to a connection, on top
// of the app's own content type — a profile never extends beyond that type,
// and never applies to kind "qui". Deleting one FK-nulls its connections'
// references, reverting them to default sync behavior.
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
                  onSuccess: () => toast.success(`${p.name} deleted`),
                  onError: () => toast.error(`Deleting ${p.name} failed`),
                })}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {profiles.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No sync profiles. Add one to narrow which categories sync into an app.</p>
        )}
      </div>
      <p className="text-[12px] text-faint">Deleting a profile reverts its connections to default sync behavior.</p>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <ProfileForm
              // Remount (fresh state seeded from props) per target.
              key={editing.profile?.id ?? "new"}
              profile={editing.profile}
              pending={create.isPending || update.isPending}
              onSubmit={(id, body) => {
                const done = { onSuccess: () => setEditing(null), onError: (err: Error) => toast.error(`Save failed: ${err.message}`) }
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
  const [name, setName] = useState(profile?.name ?? "")
  const [checkedParents, setCheckedParents] = useState<Set<number>>(
    new Set((profile?.categories ?? []).filter((c) => PARENT_IDS.has(c))))
  const [extras, setExtras] = useState((profile?.categories ?? []).filter((c) => !PARENT_IDS.has(c)).join(", "))
  const [minSeeders, setMinSeeders] = useState(String(profile?.minSeeders ?? 0))
  const [enableRss, setEnableRss] = useState(profile?.enableRss ?? true)
  const [enableAutomaticSearch, setEnableAutomaticSearch] = useState(profile?.enableAutomaticSearch ?? true)
  const [enableInteractiveSearch, setEnableInteractiveSearch] = useState(profile?.enableInteractiveSearch ?? true)

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        // Positive integers only — a stray decimal or sign would otherwise ride into
        // the []int JSON body and fail the whole request with an opaque decode error.
        const extraIds = extras.split(",").map((s) => s.trim()).filter((s) => s !== "").map((s) => Number(s)).filter((n) => Number.isInteger(n) && n > 0)
        const categories = [...new Set([...checkedParents, ...extraIds])].sort((a, b) => a - b)
        const seeders = Number(minSeeders)
        onSubmit(profile?.id ?? null, {
          name,
          categories,
          minSeeders: Number.isNaN(seeders) ? 0 : Math.max(0, seeders),
          enableRss,
          enableAutomaticSearch,
          enableInteractiveSearch,
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit sync profile" : "Add sync profile"}</DialogTitle>
        <DialogDescription>Attach it to a connection to narrow what syncs into that app.</DialogDescription>
      </DialogHeader>

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="profile-name">Name</Label>
        <Input id="profile-name" value={name} onChange={(e) => setName(e.target.value)} />
      </span>

      <span className="flex flex-col gap-1.5">
        <Label>Categories</Label>
        <div className="grid grid-cols-4 gap-2">
          {NEWZNAB_PARENTS.map((c) => (
            <span key={c.id} className="flex items-center gap-2">
              <Checkbox
                id={`profile-cat-${c.id}`}
                checked={checkedParents.has(c.id)}
                onCheckedChange={(checked) => {
                  const next = new Set(checkedParents)
                  if (checked === true) next.add(c.id)
                  else next.delete(c.id)
                  setCheckedParents(next)
                }}
              />
              <Label htmlFor={`profile-cat-${c.id}`} className="font-normal">{c.label}</Label>
            </span>
          ))}
        </div>
        <Input placeholder="Extra category IDs, e.g. 3030" value={extras} onChange={(e) => setExtras(e.target.value)} />
        <p className="text-[12px] text-faint">
          Leave empty to sync all of the app&apos;s content categories. A profile only narrows within
          the app&apos;s content type.
        </p>
      </span>

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="profile-min-seeders">Minimum seeders</Label>
        <Input id="profile-min-seeders" type="number" min={0} value={minSeeders} onChange={(e) => setMinSeeders(e.target.value)} />
      </span>

      <span className="flex items-center justify-between">
        <Label htmlFor="profile-rss" className="font-normal">RSS</Label>
        <Switch id="profile-rss" checked={enableRss} onCheckedChange={setEnableRss} />
      </span>
      <span className="flex items-center justify-between">
        <Label htmlFor="profile-auto" className="font-normal">Automatic search</Label>
        <Switch id="profile-auto" checked={enableAutomaticSearch} onCheckedChange={setEnableAutomaticSearch} />
      </span>
      <span className="flex items-center justify-between">
        <Label htmlFor="profile-interactive" className="font-normal">Interactive search</Label>
        <Switch id="profile-interactive" checked={enableInteractiveSearch} onCheckedChange={setEnableInteractiveSearch} />
      </span>

      <DialogFooter>
        <Button type="submit" disabled={pending || !name}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add profile"}
        </Button>
      </DialogFooter>
    </form>
  )
}
