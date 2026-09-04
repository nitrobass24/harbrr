import { useState } from "react"
import { Pencil, Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ResourceSection } from "@/components/ui/resource-section"
import { useSolvers, useSolverMutations } from "@/hooks/useResources"
import type { Solver } from "@/lib/api"

// Global FlareSolverr resources indexers reference by id. Enter the base URL
// (harbrr appends /v1); the manual-cookie solver stays per-tracker, so it is not here.
export function SolversSection() {
  const solvers = useSolvers()
  const { create, update, remove } = useSolverMutations()

  return (
    <ResourceSection<Solver>
      title="FlareSolverr"
      addLabel="Add FlareSolverr"
      query={solvers}
      empty="No FlareSolverr endpoints. Add one to solve anti-bot challenges."
      row={(s, actions) => (
        <>
          <span className="font-medium">{s.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{s.type}</Badge>
          {s.maxTimeout > 0 && <span className="text-[12px] text-faint">{s.maxTimeout}s</span>}
          <span className="ml-auto flex items-center gap-1">
            <Button variant="ghost" size="icon" aria-label={`Edit ${s.name}`} onClick={actions.edit}>
              <Pencil className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Delete ${s.name}`} onClick={actions.remove}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </span>
        </>
      )}
      onDelete={(s) => remove.mutateAsync(s.id)}
      form={(target, done) => (
        <SolverForm
          solver={target}
          pending={create.isPending || update.isPending}
          onSubmit={(id, body) => {
            if (id === null) create.mutate({ name: body.name, url: body.url ?? "", maxTimeout: body.maxTimeout }, done)
            else update.mutate({ id, body }, done)
          }}
        />
      )}
    />
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
