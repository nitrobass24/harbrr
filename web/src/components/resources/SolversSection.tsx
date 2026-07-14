import { useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import { useSolvers, useSolverMutations } from "@/hooks/useResources"
import type { Solver } from "@/lib/api"

// `null` = closed; `{ solver: null }` = add; `{ solver }` = edit that solver.
type Editing = { solver: Solver | null } | null

// Global FlareSolverr resources indexers reference by id. Enter the base URL
// (harbrr appends /v1); the manual-cookie solver stays per-tracker, so it is not here.
export function SolversSection() {
  const solvers = useSolvers()
  const { create, update, remove } = useSolverMutations()
  const [editing, setEditing] = useState<Editing>(null)

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-[14px] font-semibold tracking-tight">FlareSolverr</h2>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setEditing({ solver: null })}>
          <Plus className="h-3.5 w-3.5" /> Add FlareSolverr
        </Button>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(solvers.data ?? []).map((s) => (
          <div key={s.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="font-medium">{s.name}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{s.type}</Badge>
            {s.maxTimeout > 0 && <span className="text-[12px] text-faint">{s.maxTimeout}s</span>}
            <span className="ml-auto flex items-center gap-1">
              <Button variant="ghost" size="icon" aria-label={`Edit ${s.name}`} onClick={() => setEditing({ solver: s })}>
                <Pencil className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${s.name}`}
                onClick={() => remove.mutate(s.id, {
                  onSuccess: () => toast.success(`${s.name} deleted`),
                  onError: () => toast.error(`Deleting ${s.name} failed`),
                })}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {solvers.data?.length === 0 && <p className="py-3 text-muted-foreground">No FlareSolverr endpoints. Add one to solve anti-bot challenges.</p>}
      </div>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <SolverForm
              key={editing.solver?.id ?? "new"}
              solver={editing.solver}
              pending={create.isPending || update.isPending}
              onSubmit={(id, body) => {
                const done = { onSuccess: () => setEditing(null), onError: (err: Error) => toast.error(`Save failed: ${err.message}`) }
                if (id === null) create.mutate({ name: body.name, url: body.url ?? "", maxTimeout: body.maxTimeout }, done)
                else update.mutate({ id, body }, done)
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}

function SolverForm({ solver, pending, onSubmit }: {
  solver: Solver | null
  pending: boolean
  onSubmit: (id: number | null, body: { name: string, url?: string, maxTimeout: number }) => void
}) {
  const isEdit = solver !== null
  const [name, setName] = useState(solver?.name ?? "")
  const [url, setUrl] = useState("")
  const [maxTimeout, setMaxTimeout] = useState(String(solver?.maxTimeout ?? 0))

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        const mt = Number(maxTimeout)
        onSubmit(solver?.id ?? null, {
          name,
          url: isEdit ? (url || undefined) : url,
          maxTimeout: Number.isNaN(mt) ? 0 : mt,
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit FlareSolverr" : "Add FlareSolverr"}</DialogTitle>
        <DialogDescription>The endpoint URL is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="solver-name">Name</Label>
          <Input id="solver-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="solver-timeout">Max timeout (seconds, 0 = default)</Label>
          <Input id="solver-timeout" type="number" min={0} value={maxTimeout} onChange={(e) => setMaxTimeout(e.target.value)} />
        </span>
      </div>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="solver-url">URL {isEdit ? <span className="text-faint">(leave blank to keep)</span> : <span className="text-faint">(base, no /v1)</span>}</Label>
        <Input
          id="solver-url"
          type="password"
          autoComplete="off"
          placeholder="http://flaresolverr:8191"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </span>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || (!isEdit && !url)}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add FlareSolverr"}
        </Button>
      </DialogFooter>
    </form>
  )
}
