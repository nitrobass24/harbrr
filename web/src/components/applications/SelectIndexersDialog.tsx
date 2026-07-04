import { useState } from "react"
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
import { Label } from "@/components/ui/label"
import { useConnectionStatus } from "@/hooks/useAppConnections"
import { useIndexers } from "@/hooks/useIndexers"
import type { AppConnection } from "@/types/api"

// Which harbrr indexers a scope=selected connection mirrors. PUT replaces the
// whole selection (instanceIds), so the dialog stages a full set.
export function SelectIndexersDialog({ conn, pending, onClose, onSave }: {
  conn: AppConnection | null
  pending: boolean
  onClose: () => void
  onSave: (id: number, instanceIds: number[]) => void
}) {
  return (
    <Dialog open={conn !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        {conn && <Picker key={conn.id} conn={conn} pending={pending} onSave={onSave} />}
      </DialogContent>
    </Dialog>
  )
}

function Picker({ conn, pending, onSave }: {
  conn: AppConnection
  pending: boolean
  onSave: (id: number, instanceIds: number[]) => void
}) {
  const indexers = useIndexers()
  const status = useConnectionStatus(conn.id)
  const [staged, setStaged] = useState<Set<number> | null>(null)

  const selected = staged ?? new Set(
    (status.data?.indexers ?? []).filter((r) => r.selected).map((r) => r.instanceId))

  return (
    <>
      <DialogHeader>
        <DialogTitle>Select indexers — {conn.name}</DialogTitle>
        <DialogDescription>Only the checked indexers are pushed to this app.</DialogDescription>
      </DialogHeader>
      <div className="flex max-h-72 flex-col gap-2 overflow-auto py-1">
        {(indexers.data ?? []).map((ix) => (
          <span key={ix.id} className="flex items-center gap-2">
            <Checkbox
              id={`sel-${ix.id}`}
              checked={selected.has(ix.id)}
              onCheckedChange={(checked) => {
                const next = new Set(selected)
                if (checked === true) next.add(ix.id)
                else next.delete(ix.id)
                setStaged(next)
              }}
            />
            <Label htmlFor={`sel-${ix.id}`} className="font-normal">{ix.name}</Label>
          </span>
        ))}
      </div>
      <DialogFooter>
        <Button disabled={pending || status.isLoading} onClick={() => onSave(conn.id, [...selected])}>
          {pending ? "Saving…" : "Save selection"}
        </Button>
      </DialogFooter>
    </>
  )
}
