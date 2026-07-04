import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"

export function DeleteIndexerDialog({ slug, onConfirm, onClose, pending }: {
  slug: string | null
  onConfirm: (slug: string) => void
  onClose: () => void
  pending: boolean
}) {
  return (
    <Dialog open={slug !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {slug}?</DialogTitle>
          <DialogDescription>
            Removes the indexer, its stored credentials, and its cached results. Apps
            synced to this indexer will lose it on their next sync.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" disabled={pending} onClick={() => slug && onConfirm(slug)}>
            {pending ? "Deleting…" : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
