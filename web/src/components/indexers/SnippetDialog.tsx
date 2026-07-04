import { useQuery } from "@tanstack/react-query"
import { Copy } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { api } from "@/lib/api"
import { copyText } from "@/lib/clipboard"

// cross-seed v6 has no indexer API: this dialog surfaces the copy-paste
// config.js torznab entry the server mints (freeleech-bypass /full feed).
export function SnippetDialog({ slug, onClose }: { slug: string | null, onClose: () => void }) {
  const snippet = useQuery({
    queryKey: ["indexers", slug, "crossseed-snippet"],
    queryFn: () => api.getCrossseedSnippet(slug as string),
    enabled: slug !== null,
  })

  return (
    <Dialog open={slug !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>cross-seed snippet — {slug}</DialogTitle>
          <DialogDescription>
            Paste into the <code>torznab</code> array of cross-seed&apos;s config.js, replace the
            apikey placeholder, and restart cross-seed.
          </DialogDescription>
        </DialogHeader>
        <pre className="max-h-72 overflow-auto rounded-md border border-border bg-muted p-3 font-mono text-[12px] whitespace-pre-wrap">
          {snippet.data?.configJs ?? (snippet.isError ? "Failed to load the snippet." : "Loading…")}
        </pre>
        <DialogFooter>
          <Button
            disabled={!snippet.data}
            onClick={() => {
              void copyText(snippet.data?.configJs ?? "", "Snippet copied")
            }}
          >
            <Copy className="h-4 w-4" /> Copy
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
